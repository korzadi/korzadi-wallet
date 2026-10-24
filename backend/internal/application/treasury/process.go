package treasury

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/big"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/cryptutil"
	"korzadi-wallet/backend/internal/infrastructure/evmwallet"
)

const (
	confirmPollDelay  = 10 * time.Second
	retryDelay        = 15 * time.Second
	maxBackoff        = 5 * time.Minute
	preSignRetryLimit = 50 // same tolerance sweep/blockchainwithdrawal apply before failing an unsigned row closed
)

// Backoff mirrors sweep.Backoff / blockchainwithdrawal's exponential
// backoff exactly, so an operator sees the same retry cadence across every
// Bloque 7 subsystem.
func Backoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	d := 5 * time.Second
	for i := 1; i < attempts && d < maxBackoff; i++ {
		d *= 2
	}
	if d > maxBackoff {
		d = maxBackoff
	}
	return d
}

// Advance performs the next step of ONE rebalance. The caller must hold
// the row's lease (see Worker). Every step persists its result before the
// next one, so a crash at any point resumes here on the next tick — same
// discipline as sweep.Service.Advance:
//
//	pending    → processing                                (startProcessing)
//	processing → nonce, sign, PERSIST signed tx, broadcast  (processingStep)
//	broadcasted/confirming → follow the chain, then complete (confirmStep)
func (s *Service) Advance(ctx context.Context, r *Rebalance) (time.Duration, error) {
	if !s.deps.Config.Enabled {
		return 0, ErrRebalancingDisabled
	}
	switch r.Status {
	case StatusPending:
		updated, err := s.startProcessing(ctx, r)
		if err != nil || updated == nil || updated.Status != StatusProcessing {
			return 0, err
		}
		return s.processingStep(ctx, updated)

	case StatusProcessing:
		return s.processingStep(ctx, r)

	case StatusBroadcasted, StatusConfirming:
		return s.confirmStep(ctx, r)

	default:
		return 0, nil
	}
}

func (s *Service) startProcessing(ctx context.Context, r *Rebalance) (*Rebalance, error) {
	if _, err := s.deps.Rebalances.Apply(ctx, r.ID, StatusPending, StatusProcessing, Update{}, Event{Type: "processing_started"}); err != nil {
		return r, err
	}
	return s.refresh(ctx, r)
}

func (s *Service) processingStep(ctx context.Context, r *Rebalance) (time.Duration, error) {
	rt, ok := s.runtimeFor(r.Network)
	if !ok {
		return 0, fmt.Errorf("%w: %s", ErrNetworkNotConfigured, r.Network)
	}
	if len(r.SignedRawTx) == 0 {
		updated, err := s.prepareAndSign(ctx, rt, r)
		if err != nil {
			return 0, err
		}
		if updated == nil || updated.Status != StatusProcessing || len(updated.SignedRawTx) == 0 {
			return 0, nil // failed/finished by prepareAndSign, or claimed by another worker
		}
		r = updated
	}
	return s.broadcastStep(ctx, rt, r)
}

// prepareAndSign re-validates the asset against the SAME registry
// gasstation/sweep use, re-checks the amount against a FRESH balance read
// of the SOURCE custody wallet (never trusting the value decided at
// evaluation time alone — spec 7.4.2's "gas" and balance caveats), tops up
// native gas through the SAME Gas Station port sweep/blockchainwithdrawal
// use when the source lacks it for a TOKEN transfer, allocates the nonce
// through the SAME nonce store, and signs by decrypting the SOURCE custody
// wallet's OWN key directly — the same pattern gasstation.Service already
// uses for a custody-wallet-to-address transfer (NOT
// blockchainwithdrawal.Signer/KeyResolver, which is scoped to USER keys in
// crypto_addresses and does not apply to custody_wallets). The signed
// transaction is PERSISTED before anything is broadcast, so a crash
// between signing and sending can never cause a spend: the next run
// re-broadcasts the identical bytes rather than building a new one.
func (s *Service) prepareAndSign(ctx context.Context, rt NetworkRuntime, r *Rebalance) (*Rebalance, error) {
	if r.Attempts >= preSignRetryLimit {
		return s.failClosed(ctx, r, "prepare_retries_exhausted", errors.New("demasiados intentos antes de firmar"))
	}

	netAsset, found, err := s.deps.Registry.Get(ctx, r.Network, r.AssetCode)
	if err != nil {
		return r, fmt.Errorf("registro de assets: %w", err)
	}
	if !found || !netAsset.Enabled {
		return s.failClosed(ctx, r, "asset_disabled", ErrAssetNotEnabled)
	}

	sourceWallet, err := s.deps.Wallets.Get(ctx, r.FromCustodyWalletID)
	if err != nil {
		return r, fmt.Errorf("resolver custody wallet origen: %w", err)
	}
	if sourceWallet == nil {
		return s.failClosed(ctx, r, "source_wallet_missing", ErrWalletNotFound)
	}
	if sourceWallet.Status != "active" {
		return s.failClosed(ctx, r, "source_wallet_inactive", ErrWalletNotActive)
	}

	chain := rt.Chain
	from := r.FromAddress

	gasPrice, err := chain.SuggestGasPrice(ctx)
	if err != nil {
		return r, fmt.Errorf("gas price: %w", err)
	}
	if gasPrice == nil || gasPrice.Sign() <= 0 {
		return r, errors.New("gas price inválido devuelto por la red")
	}
	nativeBalance, err := chain.NativeBalanceWei(ctx, from)
	if err != nil {
		return r, fmt.Errorf("balance nativo: %w", err)
	}

	amountRaw, err := toRawUnits(r.Amount, netAsset.Decimals)
	if err != nil {
		return r, fmt.Errorf("monto de rebalanceo inválido: %w", err)
	}
	if amountRaw.Sign() <= 0 {
		return s.failClosed(ctx, r, "invalid_amount", errors.New("monto de rebalanceo no positivo"))
	}

	contract := ""
	var gasLimit uint64

	switch netAsset.Kind {
	case domain.AssetKindNative:
		// The amount is a DELIBERATE policy decision (deficit/excess
		// already capped by max_rebalance_amount) — unlike a sweep, it is
		// NEVER auto-shrunk to fit whatever balance happens to be
		// available. If the source cannot cover amount + gas, this fails
		// closed rather than silently sending less than the policy asked
		// for.
		gasLimit, err = chain.EstimateGasNative(ctx, from, r.ToAddress, amountRaw)
		if err != nil {
			break
		}
		gasLimit += gasLimit / 5 // same 20% headroom convention as sweep/blockchainwithdrawal
		gasCost := new(big.Int).Mul(new(big.Int).SetUint64(gasLimit), gasPrice)
		need := new(big.Int).Add(amountRaw, gasCost)
		if nativeBalance.Cmp(need) < 0 {
			return s.failClosed(ctx, r, insufficientCode(r.Reason), insufficientErr(r.Reason))
		}
	case domain.AssetKindToken:
		contract = netAsset.ContractAddress
		if contract == "" {
			return s.failClosed(ctx, r, "asset_disabled", ErrAssetNotEnabled)
		}
		tokenBalance, terr := chain.TokenBalanceRaw(ctx, contract, from)
		if terr != nil {
			return r, fmt.Errorf("balance del token: %w", terr)
		}
		if tokenBalance.Cmp(amountRaw) < 0 {
			return s.failClosed(ctx, r, insufficientCode(r.Reason), insufficientErr(r.Reason))
		}
		gasLimit, err = chain.EstimateGasToken(ctx, from, contract, r.ToAddress, amountRaw)
		if err == nil {
			gasLimit += gasLimit / 5
		}
	default:
		return s.failClosed(ctx, r, "asset_disabled", ErrAssetNotEnabled)
	}
	if err != nil {
		if isRevertError(err) {
			return s.failClosed(ctx, r, "estimate_reverted", err)
		}
		return r, fmt.Errorf("estimar gas: %w", err)
	}

	// Gas for a TOKEN rebalance: the native balance must cover gas
	// separately from the token amount — top it up through the same Gas
	// Station port sweep already uses. A NATIVE rebalance already carved
	// gas out of the required balance check above, so no top-up is
	// requested (asking Gas Station to fund a rebalance of its own native
	// coin would be a refill loop) — identical reasoning to
	// sweep.prepareAndSign.
	if netAsset.Kind == domain.AssetKindToken {
		gasCost := new(big.Int).Mul(new(big.Int).SetUint64(gasLimit), gasPrice)
		if nativeBalance.Cmp(gasCost) < 0 {
			if rt.Gas == nil {
				return s.failClosed(ctx, r, "gas_unavailable", ErrGasUnavailable)
			}
			if err := rt.Gas.EnsureGasFor(ctx, gasOwner(r.ID), from, gasCost); err != nil {
				return r, fmt.Errorf("gas station: %w", err)
			}
			return r, fmt.Errorf("%w: esperando confirmación del top-up de gas", ErrGasUnavailable)
		}
	}

	pending, err := chain.PendingNonce(ctx, from)
	if err != nil {
		return r, fmt.Errorf("nonce pendiente: %w", err)
	}
	nonce, err := s.deps.Nonces.Allocate(ctx, r.Network, from, nonceOwner(r.ID), pending)
	if err != nil {
		return r, fmt.Errorf("asignar nonce: %w", err)
	}

	keyHex, err := s.deps.Decryptor.Decrypt(ctx, sourceWallet.EncryptedPrivateKey, cryptutil.ForCustodyWallet(sourceWallet.Address))
	if err != nil {
		return r, fmt.Errorf("descifrar llave de la custody wallet origen")
	}
	defer func() { keyHex = "" }() // best-effort: see blockchainwithdrawal.EVMSigner's identical comment on Go string immutability

	var signed *evmwallet.SignedTransaction
	switch netAsset.Kind {
	case domain.AssetKindNative:
		signed, err = evmwallet.BuildAndSignNativeTransfer(keyHex, chain.ChainID(), nonce, r.ToAddress, amountRaw, gasLimit, gasPrice)
	case domain.AssetKindToken:
		signed, err = evmwallet.BuildAndSignERC20Transfer(keyHex, chain.ChainID(), nonce, contract, r.ToAddress, amountRaw, gasLimit, gasPrice)
	}
	if err != nil {
		return r, fmt.Errorf("firmar: %w", err)
	}

	// AUDIT FIX: the decrypted key must belong to the source custody wallet.
	if err := evmwallet.VerifySender(signed.RawTx, chain.ChainID(), from); err != nil {
		return r, fmt.Errorf("verificar remitente de la tx firmada: %w", err)
	}

	nonceI, gasLimitI := int64(nonce), int64(gasLimit)
	gasPriceDec := decimal.NewFromBigInt(gasPrice, 0)
	hash := signed.TxHash
	applied, err := s.deps.Rebalances.Apply(ctx, r.ID, StatusProcessing, StatusProcessing,
		Update{
			Nonce: &nonceI, GasLimit: &gasLimitI, GasPriceWei: &gasPriceDec,
			SignedRawTx: signed.RawTx, TxHash: &hash, MarkSigned: true, RequireUnsigned: true,
		},
		Event{Type: "signed", Detail: map[string]any{
			"tx_hash": hash, "nonce": nonce, "gas_limit": gasLimit, "gas_price_wei": gasPrice.String(), "amount": r.Amount.String(),
		}})
	if err != nil {
		return r, fmt.Errorf("persistir tx firmada: %w", err)
	}
	if !applied {
		return s.refresh(ctx, r) // someone else already persisted a signed tx (or the state moved)
	}
	if err := s.deps.Nonces.MarkSigned(ctx, r.Network, from, nonceOwner(r.ID), hash); err != nil {
		log.Printf("treasury: no se pudo marcar el nonce como firmado id=%s: %v (se reintentará al transmitir)", r.ID, err)
	}
	log.Printf("treasury: rebalanceo firmado id=%s network=%s asset=%s from=%s nonce=%d tx=%s", r.ID, r.Network, r.AssetCode, from, nonce, hash)
	return s.refresh(ctx, r)
}

func insufficientCode(reason Reason) string {
	if reason == ReasonDeficit {
		return "insufficient_treasury"
	}
	return "insufficient_hot"
}
func insufficientErr(reason Reason) error {
	if reason == ReasonDeficit {
		return ErrInsufficientTreasury
	}
	return ErrInsufficientHot
}

// broadcastStep sends the already-persisted signed transaction, never
// blindly (spec 7.6/7.4: "Antes de volver a transmitir: receipt,
// mempool/estado, nonce, evidencia de que no fue transmitida"). Re-sending
// the SAME signed bytes is idempotent (same hash, same nonce); an
// ambiguous error never assumes "not sent" — identical discipline to
// sweep.broadcastStep / blockchainwithdrawal.broadcastStep.
func (s *Service) broadcastStep(ctx context.Context, rt NetworkRuntime, r *Rebalance) (time.Duration, error) {
	chain := rt.Chain
	if r.TxHash == "" || len(r.SignedRawTx) == 0 || r.Nonce == nil {
		if _, err := s.deps.Rebalances.Apply(ctx, r.ID, StatusProcessing, StatusProcessing, Update{ClearSigned: true}, Event{Type: "reset_signed_state"}); err != nil {
			return 0, err
		}
		return 0, nil
	}
	if err := s.deps.Nonces.MarkSigned(ctx, r.Network, r.FromAddress, nonceOwner(r.ID), r.TxHash); err != nil {
		log.Printf("treasury: no se pudo marcar el nonce como firmado id=%s: %v", r.ID, err)
	}

	receipt, err := chain.Receipt(ctx, r.TxHash)
	if err != nil {
		return 0, fmt.Errorf("consultar receipt: %w", err)
	}
	if receipt != nil {
		return s.markBroadcasted(ctx, r, "found_on_chain")
	}
	known, err := chain.TxKnown(ctx, r.TxHash)
	if err != nil {
		return 0, fmt.Errorf("consultar tx en la red: %w", err)
	}
	if known {
		return s.markBroadcasted(ctx, r, "known_by_node")
	}

	pending, err := chain.PendingNonce(ctx, r.FromAddress)
	if err != nil {
		return 0, fmt.Errorf("nonce pendiente: %w", err)
	}
	if pending > uint64(*r.Nonce) {
		if s.graceExceeded(r.SignedAt) {
			_, ferr := s.failNeedsReview(ctx, r, "nonce_stuck", ErrNonceStuck)
			return 0, ferr
		}
		log.Printf("treasury: nonce %d ya usado en la red pero tx %s desconocida; esperando id=%s", *r.Nonce, r.TxHash, r.ID)
		return retryDelay, nil
	}

	sentHash, err := chain.Broadcast(ctx, r.SignedRawTx)
	if err == nil {
		if !strings.EqualFold(sentHash, r.TxHash) {
			log.Printf("treasury: ADVERTENCIA hash devuelto por la red (%s) distinto del firmado (%s) id=%s", sentHash, r.TxHash, r.ID)
		}
		return s.markBroadcasted(ctx, r, "accepted")
	}

	switch classifyBroadcastError(err) {
	case broadcastAlreadyKnown:
		return s.markBroadcasted(ctx, r, "already_known")

	case broadcastReprice:
		if r.Attempts+1 >= preSignRetryLimit {
			_, ferr := s.failClosed(ctx, r, "broadcast_rejected", err)
			return 0, ferr
		}
		if _, aerr := s.deps.Rebalances.Apply(ctx, r.ID, StatusProcessing, StatusProcessing, Update{ClearSigned: true},
			Event{Type: "reprice", Detail: map[string]any{"reason": sanitizeCause(err)}}); aerr != nil {
			return 0, aerr
		}
		return 0, fmt.Errorf("%w: %v", ErrBroadcastRejected, sanitizeCause(err))

	case broadcastRejected:
		_, ferr := s.failClosed(ctx, r, "broadcast_rejected", err)
		return 0, ferr

	default:
		// AMBIGUOUS: never assumed as failure, never retried
		// automatically — the next tick re-checks receipt/mempool/nonce
		// before any further send (spec 7.6.1: "Nunca hacer blind
		// resend").
		log.Printf("treasury: broadcast ambiguo id=%s tx=%s: %v", r.ID, r.TxHash, sanitizeCause(err))
		_ = s.deps.Rebalances.AddEvent(ctx, r.ID, "broadcast_ambiguous", r.Status, r.Status, map[string]any{"error": sanitizeCause(err)})
		return 0, fmt.Errorf("%w: %v", ErrBroadcastAmbiguous, sanitizeCause(err))
	}
}

func (s *Service) markBroadcasted(ctx context.Context, r *Rebalance, how string) (time.Duration, error) {
	applied, err := s.deps.Rebalances.Apply(ctx, r.ID, StatusProcessing, StatusBroadcasted,
		Update{MarkBroadcasted: true},
		Event{Type: "broadcasted", Detail: map[string]any{"tx_hash": r.TxHash, "how": how}})
	if err != nil {
		return 0, err
	}
	if applied {
		s.auditRebalance(ctx, r, "treasury_rebalance_broadcast", map[string]any{"tx_hash": r.TxHash, "how": how})
		log.Printf("treasury: rebalanceo transmitido id=%s network=%s asset=%s direction=%s tx=%s (%s)", r.ID, r.Network, r.AssetCode, r.Direction, r.TxHash, how)
	}
	return confirmPollDelay, nil
}

// ---- broadcasted / confirming ----

func (s *Service) confirmStep(ctx context.Context, r *Rebalance) (time.Duration, error) {
	rt, ok := s.runtimeFor(r.Network)
	if !ok {
		return 0, fmt.Errorf("%w: %s", ErrNetworkNotConfigured, r.Network)
	}
	if r.TxHash == "" {
		return 0, errors.New("rebalanceo transmitido sin tx hash")
	}
	chain := rt.Chain

	receipt, err := chain.Receipt(ctx, r.TxHash)
	if err != nil {
		return 0, fmt.Errorf("consultar receipt: %w", err)
	}
	if receipt == nil {
		return s.handleMissingReceipt(ctx, rt, r)
	}
	if !receipt.Status {
		return s.handleReverted(ctx, r)
	}

	required := uint64(1)
	if r.RequiredConfirmations != nil && *r.RequiredConfirmations > 0 {
		required = uint64(*r.RequiredConfirmations)
	} else if c := chain.ConfirmationsRequired(); c > 0 {
		required = c
	}

	reorged := r.BlockHash != "" && !strings.EqualFold(r.BlockHash, receipt.BlockHash)
	blockNumber := int64(receipt.BlockNumber)
	confirmations := int64(receipt.Confirmations)
	blockHash := receipt.BlockHash

	var ev Event
	switch {
	case reorged:
		log.Printf("treasury: REORG detectado id=%s tx=%s bloque %s → %s", r.ID, r.TxHash, r.BlockHash, blockHash)
		ev = Event{Type: "reorg_detected", Detail: map[string]any{"old_block_hash": r.BlockHash, "new_block_hash": blockHash, "new_block": blockNumber}}
	case r.Status == StatusBroadcasted:
		ev = Event{Type: "mined", Detail: map[string]any{"block": blockNumber, "block_hash": blockHash, "confirmations": confirmations}}
	}
	applied, err := s.deps.Rebalances.Apply(ctx, r.ID, r.Status, StatusConfirming,
		Update{BlockNumber: &blockNumber, BlockHash: &blockHash, Confirmations: &confirmations}, ev)
	if err != nil {
		return 0, err
	}
	if !applied {
		return 0, nil
	}

	if reorged || uint64(confirmations) < required {
		return confirmPollDelay, nil
	}

	current, err := s.refresh(ctx, r)
	if err != nil {
		return 0, err
	}
	if current.Status != StatusConfirming {
		return 0, nil
	}
	return s.complete(ctx, current)
}

func (s *Service) handleMissingReceipt(ctx context.Context, rt NetworkRuntime, r *Rebalance) (time.Duration, error) {
	chain := rt.Chain

	if r.Status == StatusConfirming {
		zero := int64(0)
		if _, err := s.deps.Rebalances.Apply(ctx, r.ID, StatusConfirming, StatusBroadcasted,
			Update{ClearBlock: true, Confirmations: &zero},
			Event{Type: "reorg_dropped", Detail: map[string]any{"old_block_hash": r.BlockHash}}); err != nil {
			return 0, err
		}
		log.Printf("treasury: receipt desaparecido tras reorg id=%s tx=%s", r.ID, r.TxHash)
		return confirmPollDelay, nil
	}

	known, err := chain.TxKnown(ctx, r.TxHash)
	if err != nil {
		return 0, fmt.Errorf("consultar tx en la red: %w", err)
	}
	if known {
		return confirmPollDelay, nil
	}

	if r.Nonce != nil && len(r.SignedRawTx) > 0 {
		pending, err := chain.PendingNonce(ctx, r.FromAddress)
		if err != nil {
			return 0, fmt.Errorf("nonce pendiente: %w", err)
		}
		if pending <= uint64(*r.Nonce) {
			_ = s.deps.Rebalances.AddEvent(ctx, r.ID, "rebroadcast", r.Status, r.Status, map[string]any{"tx_hash": r.TxHash})
			if _, berr := chain.Broadcast(ctx, r.SignedRawTx); berr != nil && classifyBroadcastError(berr) != broadcastAlreadyKnown {
				return 0, fmt.Errorf("rebroadcast: %w", berr)
			}
			return confirmPollDelay, nil
		}
	}

	if s.graceExceeded(r.BroadcastedAt) {
		_, ferr := s.failNeedsReview(ctx, r, "tx_lost", ErrNonceStuck)
		return 0, ferr
	}
	return confirmPollDelay, nil
}

// handleReverted: the rebalance transaction was mined but reverted — no
// funds moved between HOT and TREASURY. Always needs a human: a reverted
// plain transfer/ERC-20 transfer between two custody wallets Korzadi
// itself controls should never happen under normal operation.
func (s *Service) handleReverted(ctx context.Context, r *Rebalance) (time.Duration, error) {
	if err := s.deps.Nonces.MarkConfirmed(ctx, r.Network, r.FromAddress, nonceOwner(r.ID)); err != nil {
		return 0, fmt.Errorf("marcar nonce consumido: %w", err)
	}
	_, err := s.failNeedsReview(ctx, r, "tx_reverted", errors.New("la transacción de rebalanceo fue revertida on-chain"))
	return 0, err
}

// complete marks the rebalance completed and writes the custody audit
// trail once the configured confirmations were reached. This block never
// touches the Ledger (spec: a rebalance moves Korzadi's own infrastructure
// funds, never a user's balance).
func (s *Service) complete(ctx context.Context, r *Rebalance) (time.Duration, error) {
	applied, err := s.deps.Rebalances.Apply(ctx, r.ID, StatusConfirming, StatusCompleted,
		Update{MarkCompleted: true},
		Event{Type: "completed", Detail: map[string]any{"tx_hash": r.TxHash, "amount": r.Amount.String()}})
	if err != nil {
		return 0, err
	}
	if !applied {
		return 0, nil
	}
	if err := s.deps.Nonces.MarkConfirmed(ctx, r.Network, r.FromAddress, nonceOwner(r.ID)); err != nil {
		log.Printf("treasury: no se pudo marcar el nonce confirmado id=%s: %v", r.ID, err)
	}
	s.auditRebalance(ctx, r, "treasury_rebalance_completed", map[string]any{"tx_hash": r.TxHash})
	log.Printf("treasury: rebalanceo COMPLETADO id=%s network=%s asset=%s direction=%s amount=%s tx=%s", r.ID, r.Network, r.AssetCode, r.Direction, r.Amount.String(), r.TxHash)
	return 0, nil
}

// ---- broadcast error classification ----
//
// Deliberately duplicated (not imported) from sweep/blockchainwithdrawal —
// pure, stateless string classification, kept byte-for-byte identical so
// every Bloque 7 subsystem treats the same provider error the same way.
// Re-exporting sweep's unexported classifier purely so 7.4 could import it
// would be a change to 7.3 "sin necesidad".

type broadcastClass int

const (
	broadcastAmbiguous broadcastClass = iota
	broadcastAlreadyKnown
	broadcastReprice
	broadcastRejected
)

func classifyBroadcastError(err error) broadcastClass {
	if err == nil {
		return broadcastAmbiguous
	}
	msg := strings.ToLower(err.Error())
	has := func(subs ...string) bool {
		for _, sub := range subs {
			if strings.Contains(msg, sub) {
				return true
			}
		}
		return false
	}
	switch {
	case has("already known", "known transaction", "same hash was already imported", "already in the mempool"):
		return broadcastAlreadyKnown
	case has("replacement transaction underpriced"):
		return broadcastAmbiguous
	case has("transaction underpriced", "max fee per gas less than block base fee", "fee cap less than block base fee", "insufficient funds"):
		return broadcastReprice
	case has("intrinsic gas too low", "exceeds block gas limit", "gas limit reached", "invalid sender", "invalid chain id", "oversized data"):
		return broadcastRejected
	default:
		return broadcastAmbiguous
	}
}

func isRevertError(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "execution reverted") || strings.Contains(msg, "revert")
}

func (s *Service) graceExceeded(since *time.Time) bool {
	return since != nil && s.now().Sub(*since) > 15*time.Minute
}
