package sweep

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/big"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	bw "korzadi-wallet/backend/internal/application/blockchainwithdrawal"
	"korzadi-wallet/backend/internal/domain"
)

const (
	confirmPollDelay  = 10 * time.Second
	retryDelay        = 15 * time.Second
	maxBackoff        = 5 * time.Minute
	preSignRetryLimit = 50 // same tolerance as blockchainwithdrawal before failing an unsigned row closed
)

// Backoff mirrors blockchainwithdrawal's exponential backoff exactly, so an
// operator sees the same retry cadence for both subsystems.
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

// Advance performs the next step of ONE sweep. The caller must hold the
// row's lease (see Worker). Every step persists its result before the next
// one, so a crash at any point resumes here on the next tick — same
// discipline as blockchainwithdrawal.Service.Advance, minus the
// reservation/settlement steps a sweep does not have (it never touches the
// Ledger; see package doc):
//
//	pending    → processing                                (startProcessing)
//	processing → nonce, sign, PERSIST signed tx, broadcast  (processingStep)
//	broadcasted/confirming → follow the chain, then complete (confirmStep)
func (s *Service) Advance(ctx context.Context, sw *Sweep) (time.Duration, error) {
	if !s.deps.Config.Enabled {
		return 0, ErrSweepsDisabled
	}
	switch sw.Status {
	case StatusPending:
		updated, err := s.startProcessing(ctx, sw)
		if err != nil || updated == nil || updated.Status != StatusProcessing {
			return 0, err
		}
		return s.processingStep(ctx, updated)

	case StatusProcessing:
		return s.processingStep(ctx, sw)

	case StatusBroadcasted, StatusConfirming:
		return s.confirmStep(ctx, sw)

	default:
		return 0, nil
	}
}

func (s *Service) startProcessing(ctx context.Context, sw *Sweep) (*Sweep, error) {
	if _, err := s.deps.Store.Apply(ctx, sw.ID, StatusPending, StatusProcessing, Update{}, Event{Type: "processing_started"}); err != nil {
		return sw, err
	}
	return s.refresh(ctx, sw)
}

func (s *Service) processingStep(ctx context.Context, sw *Sweep) (time.Duration, error) {
	rt, ok := s.runtimeFor(sw.Network)
	if !ok {
		return 0, fmt.Errorf("%w: %s", ErrNetworkNotConfigured, sw.Network)
	}
	if len(sw.SignedRawTx) == 0 {
		updated, err := s.prepareAndSign(ctx, rt, sw)
		if err != nil {
			return 0, err
		}
		if updated == nil || updated.Status != StatusProcessing || len(updated.SignedRawTx) == 0 {
			return 0, nil // failed/finished by prepareAndSign, or claimed by another worker
		}
		sw = updated
	}
	return s.broadcastStep(ctx, rt, sw)
}

// prepareAndSign re-validates everything from the SAME registries
// blockchainwithdrawal uses, re-prices the sweep against a FRESH on-chain
// balance (the address can only have gained funds since the candidate scan
// — see candidates.go — but never lost any, since nothing else may spend
// from a deposit address except this exact flow, serialized by
// uq_sweeps_inflight_source), tops up gas through the SAME Gas Station
// port if the address itself is short, allocates the nonce through the
// SAME nonce store, and signs through the SAME signer. The signed
// transaction is PERSISTED before anything is broadcast, so a crash
// between signing and sending can never cause a spend: the next run
// re-broadcasts the identical bytes rather than building a new one.
func (s *Service) prepareAndSign(ctx context.Context, rt NetworkRuntime, sw *Sweep) (*Sweep, error) {
	if sw.Attempts >= preSignRetryLimit {
		return s.failClosed(ctx, sw, "prepare_retries_exhausted", errors.New("demasiados intentos antes de firmar"))
	}
	if s.deps.Signer == nil {
		return sw, ErrNoSigner
	}

	netAsset, found, err := s.deps.Registry.Get(ctx, sw.Network, sw.AssetCode)
	if err != nil {
		return sw, fmt.Errorf("registro de assets: %w", err)
	}
	if !found || !netAsset.Enabled {
		return s.failClosed(ctx, sw, "asset_disabled", ErrAssetNotEnabled)
	}

	chain := rt.Chain
	from := sw.FromAddress

	gasFees, err := bw.ResolveGasFeeParams(ctx, chain)
	if err != nil {
		return sw, fmt.Errorf("gas fee quote: %w", err)
	}
	gasPrice := gasFees.CapPerGasWei()
	nativeBalance, err := chain.NativeBalanceWei(ctx, from)
	if err != nil {
		return sw, fmt.Errorf("balance nativo: %w", err)
	}

	contract := ""
	var raw *big.Int
	var gasLimit uint64

	switch netAsset.Kind {
	case domain.AssetKindNative:
		// Re-derive the amount from the live balance rather than trusting
		// the value priced at candidate time: it is the authoritative
		// source and can only have grown (more deposits, or a gas
		// top-up) since then.
		gasLimit, err = chain.EstimateGasNative(ctx, from, sw.ToAddress, nativeBalance)
		if err != nil {
			break
		}
		gasLimit += gasLimit / 5 // same 20% headroom convention as blockchainwithdrawal
		gasCost := new(big.Int).Mul(new(big.Int).SetUint64(gasLimit), gasPrice)
		netWei := new(big.Int).Sub(nativeBalance, gasCost)
		if netWei.Sign() <= 0 {
			return s.failClosed(ctx, sw, "insufficient_onchain_funds", ErrInsufficientOnChain)
		}
		raw = netWei
	case domain.AssetKindToken:
		contract = netAsset.ContractAddress
		if contract == "" {
			return s.failClosed(ctx, sw, "asset_disabled", ErrAssetNotEnabled)
		}
		tokenBalance, terr := chain.TokenBalanceRaw(ctx, contract, from)
		if terr != nil {
			return sw, fmt.Errorf("balance del token: %w", terr)
		}
		if tokenBalance.Sign() <= 0 {
			return s.failClosed(ctx, sw, "insufficient_onchain_funds", ErrInsufficientOnChain)
		}
		raw = tokenBalance
		gasLimit, err = chain.EstimateGasToken(ctx, from, contract, sw.ToAddress, raw)
		if err == nil {
			gasLimit += gasLimit / 5
		}
	default:
		return s.failClosed(ctx, sw, "asset_disabled", ErrAssetNotEnabled)
	}
	if err != nil {
		if isRevertError(err) {
			return s.failClosed(ctx, sw, "estimate_reverted", err)
		}
		return sw, fmt.Errorf("estimar gas: %w", err)
	}

	// Gas: for a TOKEN sweep the native balance must cover gas separately
	// from the token amount — top it up through the exact same Gas
	// Station port blockchainwithdrawal already uses. For a NATIVE sweep
	// gas is already carved out of the balance above, so no top-up is
	// ever requested (asking Gas Station to fund a sweep of its OWN
	// native coin would be a refill loop).
	if netAsset.Kind == domain.AssetKindToken {
		gasCost := new(big.Int).Mul(new(big.Int).SetUint64(gasLimit), gasPrice)
		if nativeBalance.Cmp(gasCost) < 0 {
			if rt.Gas == nil {
				return s.failClosed(ctx, sw, "gas_unavailable", ErrGasUnavailable)
			}
			if err := rt.Gas.EnsureGasFor(ctx, gasOwner(sw.ID), from, gasCost); err != nil {
				return sw, fmt.Errorf("gas station: %w", err)
			}
			// The top-up transaction itself needs to land before this
			// sweep can spend it; come back next tick rather than racing
			// PendingNonce against a transfer we cannot yet see.
			return sw, fmt.Errorf("%w: esperando confirmación del top-up de gas", ErrGasUnavailable)
		}
	}

	pending, err := chain.PendingNonce(ctx, from)
	if err != nil {
		return sw, fmt.Errorf("nonce pendiente: %w", err)
	}
	nonce, err := s.deps.Nonces.Allocate(ctx, sw.Network, from, nonceOwner(sw.ID), pending)
	if err != nil {
		return sw, fmt.Errorf("asignar nonce: %w", err)
	}

	signed, err := s.deps.Signer.Sign(ctx, SignRequest{
		Network:                 sw.Network,
		UserID:                  sw.FromUserID,
		FromAddress:             from,
		ToAddress:               sw.ToAddress,
		AssetKind:               netAsset.Kind,
		ContractAddress:         contract,
		AmountRaw:               raw,
		ChainID:                 chain.ChainID(),
		Nonce:                   nonce,
		GasLimit:                gasLimit,
		GasPriceWei:             gasFees.GasPriceWei,
		MaxFeePerGasWei:         gasFees.MaxFeePerGasWei,
		MaxPriorityFeePerGasWei: gasFees.MaxPriorityFeePerGasWei,
	})
	if err != nil {
		return sw, fmt.Errorf("firmar: %w", err)
	}

	amount := bw.WeiToDecimal(raw)
	if netAsset.Kind == domain.AssetKindToken {
		scale := decimal.New(1, netAsset.Decimals)
		amount = decimal.NewFromBigInt(raw, 0).Div(scale)
	}
	nonceI, gasLimitI := int64(nonce), int64(gasLimit)
	gasPriceDec := decimal.NewFromBigInt(gasPrice, 0)
	hash := signed.TxHash
	applied, err := s.deps.Store.Apply(ctx, sw.ID, StatusProcessing, StatusProcessing,
		Update{
			Amount: &amount, Nonce: &nonceI, GasLimit: &gasLimitI, GasPriceWei: &gasPriceDec,
			SignedRawTx: signed.Raw, TxHash: &hash, MarkSigned: true, RequireUnsigned: true,
		},
		Event{Type: "signed", Detail: map[string]any{
			"tx_hash": hash, "nonce": nonce, "gas_limit": gasLimit, "gas_price_wei": gasPrice.String(), "amount": amount.String(),
		}})
	if err != nil {
		return sw, fmt.Errorf("persistir tx firmada: %w", err)
	}
	if !applied {
		return s.refresh(ctx, sw) // someone else already persisted a signed tx (or the state moved)
	}
	if err := s.deps.Nonces.MarkSigned(ctx, sw.Network, from, nonceOwner(sw.ID), hash); err != nil {
		log.Printf("sweep: no se pudo marcar el nonce como firmado id=%s: %v (se reintentará al transmitir)", sw.ID, err)
	}
	log.Printf("sweep: firmado id=%s network=%s asset=%s from=%s nonce=%d tx=%s", sw.ID, sw.Network, sw.AssetCode, from, nonce, hash)
	return s.refresh(ctx, sw)
}

// broadcastStep sends the already-persisted signed transaction, never
// blindly: it first checks whether the network already knows the
// transaction (receipt, then mempool) and whether the nonce is still free.
// Re-sending the SAME signed bytes is idempotent (same hash, same nonce);
// an ambiguous error (timeout, EOF) never assumes "not sent" — same
// discipline as blockchainwithdrawal.broadcastStep.
func (s *Service) broadcastStep(ctx context.Context, rt NetworkRuntime, sw *Sweep) (time.Duration, error) {
	chain := rt.Chain
	if sw.TxHash == "" || len(sw.SignedRawTx) == 0 || sw.Nonce == nil {
		if _, err := s.deps.Store.Apply(ctx, sw.ID, StatusProcessing, StatusProcessing, Update{ClearSigned: true}, Event{Type: "reset_signed_state"}); err != nil {
			return 0, err
		}
		return 0, nil
	}
	if err := s.deps.Nonces.MarkSigned(ctx, sw.Network, sw.FromAddress, nonceOwner(sw.ID), sw.TxHash); err != nil {
		log.Printf("sweep: no se pudo marcar el nonce como firmado id=%s: %v", sw.ID, err)
	}

	receipt, err := chain.Receipt(ctx, sw.TxHash)
	if err != nil {
		return 0, fmt.Errorf("consultar receipt: %w", err)
	}
	if receipt != nil {
		return s.markBroadcasted(ctx, sw, "found_on_chain")
	}
	known, err := chain.TxKnown(ctx, sw.TxHash)
	if err != nil {
		return 0, fmt.Errorf("consultar tx en la red: %w", err)
	}
	if known {
		return s.markBroadcasted(ctx, sw, "known_by_node")
	}

	pending, err := chain.PendingNonce(ctx, sw.FromAddress)
	if err != nil {
		return 0, fmt.Errorf("nonce pendiente: %w", err)
	}
	if pending > uint64(*sw.Nonce) {
		if s.graceExceeded(sw.SignedAt) {
			_, ferr := s.failNeedsReview(ctx, sw, "nonce_stuck", ErrNonceStuck)
			return 0, ferr
		}
		log.Printf("sweep: nonce %d ya usado en la red pero tx %s desconocida; esperando id=%s", *sw.Nonce, sw.TxHash, sw.ID)
		return retryDelay, nil
	}

	sentHash, err := chain.Broadcast(ctx, sw.SignedRawTx)
	if err == nil {
		if !strings.EqualFold(sentHash, sw.TxHash) {
			log.Printf("sweep: ADVERTENCIA hash devuelto por la red (%s) distinto del firmado (%s) id=%s", sentHash, sw.TxHash, sw.ID)
		}
		return s.markBroadcasted(ctx, sw, "accepted")
	}

	switch classifyBroadcastError(err) {
	case broadcastAlreadyKnown:
		return s.markBroadcasted(ctx, sw, "already_known")

	case broadcastReprice:
		// The node definitively refused this exact transaction (fee too
		// low / balance moved): it is NOT in its pool, so discard it and
		// re-prepare from scratch — same nonce, so at most one can ever
		// execute.
		if sw.Attempts+1 >= s.deps.Config.MaxPrepareTries {
			_, ferr := s.failClosed(ctx, sw, "broadcast_rejected", err)
			return 0, ferr
		}
		if _, aerr := s.deps.Store.Apply(ctx, sw.ID, StatusProcessing, StatusProcessing, Update{ClearSigned: true},
			Event{Type: "reprice", Detail: map[string]any{"reason": sanitizeCause(err)}}); aerr != nil {
			return 0, aerr
		}
		return 0, fmt.Errorf("%w: %v", ErrBroadcastRejected, sanitizeCause(err))

	case broadcastRejected:
		_, ferr := s.failClosed(ctx, sw, "broadcast_rejected", err)
		return 0, ferr

	default:
		// AMBIGUOUS: the transaction may or may not have reached the
		// network. NEVER blindly retried — the next tick re-checks
		// receipt/mempool/nonce before any further send.
		log.Printf("sweep: broadcast ambiguo id=%s tx=%s: %v", sw.ID, sw.TxHash, sanitizeCause(err))
		_ = s.deps.Store.AddEvent(ctx, sw.ID, "broadcast_ambiguous", sw.Status, sw.Status, map[string]any{"error": sanitizeCause(err)})
		return 0, fmt.Errorf("broadcast ambiguo: %w", err)
	}
}

func (s *Service) markBroadcasted(ctx context.Context, sw *Sweep, how string) (time.Duration, error) {
	applied, err := s.deps.Store.Apply(ctx, sw.ID, StatusProcessing, StatusBroadcasted,
		Update{MarkBroadcasted: true},
		Event{Type: "broadcasted", Detail: map[string]any{"tx_hash": sw.TxHash, "how": how}})
	if err != nil {
		return 0, err
	}
	if applied {
		s.auditWallet(ctx, &sw.ToCustodyWalletID, "sweep_broadcast", map[string]any{
			"sweep_id": sw.ID.String(), "network": string(sw.Network), "asset_code": sw.AssetCode,
			"from_address": sw.FromAddress, "to_address": sw.ToAddress, "tx_hash": sw.TxHash, "amount": sw.Amount.String(),
		})
		log.Printf("sweep: transmitido id=%s network=%s asset=%s from=%s tx=%s (%s)", sw.ID, sw.Network, sw.AssetCode, sw.FromAddress, sw.TxHash, how)
	}
	return confirmPollDelay, nil
}

// ---- broadcasted / confirming ----

func (s *Service) confirmStep(ctx context.Context, sw *Sweep) (time.Duration, error) {
	rt, ok := s.runtimeFor(sw.Network)
	if !ok {
		return 0, fmt.Errorf("%w: %s", ErrNetworkNotConfigured, sw.Network)
	}
	if sw.TxHash == "" {
		return 0, errors.New("sweep transmitido sin tx hash")
	}
	chain := rt.Chain

	// Never trust local state alone: ask the chain every time.
	receipt, err := chain.Receipt(ctx, sw.TxHash)
	if err != nil {
		return 0, fmt.Errorf("consultar receipt: %w", err)
	}
	if receipt == nil {
		return s.handleMissingReceipt(ctx, rt, sw)
	}
	if !receipt.Status {
		return s.handleReverted(ctx, sw)
	}

	required := uint64(1)
	if sw.RequiredConfirmations != nil && *sw.RequiredConfirmations > 0 {
		required = uint64(*sw.RequiredConfirmations)
	} else if c := chain.ConfirmationsRequired(); c > 0 {
		required = c
	}

	reorged := sw.BlockHash != "" && !strings.EqualFold(sw.BlockHash, receipt.BlockHash)
	blockNumber := int64(receipt.BlockNumber)
	confirmations := int64(receipt.Confirmations)
	blockHash := receipt.BlockHash

	var ev Event
	switch {
	case reorged:
		log.Printf("sweep: REORG detectado id=%s tx=%s bloque %s → %s", sw.ID, sw.TxHash, sw.BlockHash, blockHash)
		ev = Event{Type: "reorg_detected", Detail: map[string]any{"old_block_hash": sw.BlockHash, "new_block_hash": blockHash, "new_block": blockNumber}}
	case sw.Status == StatusBroadcasted:
		ev = Event{Type: "mined", Detail: map[string]any{"block": blockNumber, "block_hash": blockHash, "confirmations": confirmations}}
	}
	applied, err := s.deps.Store.Apply(ctx, sw.ID, sw.Status, StatusConfirming,
		Update{BlockNumber: &blockNumber, BlockHash: &blockHash, Confirmations: &confirmations}, ev)
	if err != nil {
		return 0, err
	}
	if !applied {
		return 0, nil // state moved under us; next tick re-evaluates
	}

	// A reorg resets progress: never complete in the same tick.
	if reorged || uint64(confirmations) < required {
		return confirmPollDelay, nil
	}

	current, err := s.refresh(ctx, sw)
	if err != nil {
		return 0, err
	}
	if current.Status != StatusConfirming {
		return 0, nil
	}
	return s.complete(ctx, current)
}

func (s *Service) handleMissingReceipt(ctx context.Context, rt NetworkRuntime, sw *Sweep) (time.Duration, error) {
	chain := rt.Chain

	if sw.Status == StatusConfirming {
		// Had a receipt before and now it is gone: a reorg dropped it.
		// Go back to "broadcasted" so it is followed (and, if needed,
		// re-sent) again. NEVER completed.
		zero := int64(0)
		if _, err := s.deps.Store.Apply(ctx, sw.ID, StatusConfirming, StatusBroadcasted,
			Update{ClearBlock: true, Confirmations: &zero},
			Event{Type: "reorg_dropped", Detail: map[string]any{"old_block_hash": sw.BlockHash}}); err != nil {
			return 0, err
		}
		log.Printf("sweep: receipt desaparecido tras reorg id=%s tx=%s", sw.ID, sw.TxHash)
		return confirmPollDelay, nil
	}

	known, err := chain.TxKnown(ctx, sw.TxHash)
	if err != nil {
		return 0, fmt.Errorf("consultar tx en la red: %w", err)
	}
	if known {
		return confirmPollDelay, nil // still in the mempool
	}

	if sw.Nonce != nil && len(sw.SignedRawTx) > 0 {
		pending, err := chain.PendingNonce(ctx, sw.FromAddress)
		if err != nil {
			return 0, fmt.Errorf("nonce pendiente: %w", err)
		}
		if pending <= uint64(*sw.Nonce) {
			// Dropped from the mempool and the nonce is still free:
			// re-send the SAME signed bytes (idempotent).
			_ = s.deps.Store.AddEvent(ctx, sw.ID, "rebroadcast", sw.Status, sw.Status, map[string]any{"tx_hash": sw.TxHash})
			if _, berr := chain.Broadcast(ctx, sw.SignedRawTx); berr != nil && classifyBroadcastError(berr) != broadcastAlreadyKnown {
				return 0, fmt.Errorf("rebroadcast: %w", berr)
			}
			return confirmPollDelay, nil
		}
	}

	if s.graceExceeded(sw.BroadcastedAt) {
		_, ferr := s.failNeedsReview(ctx, sw, "tx_lost", ErrNonceStuck)
		return 0, ferr
	}
	return confirmPollDelay, nil
}

// handleReverted: the sweep transaction was mined but reverted — no funds
// moved. The nonce is consumed (never released) and gas was burned from the
// deposit address for nothing; this always needs a human, because a
// reverted plain-value transfer to an EOA custody wallet should not happen
// under normal operation and may indicate the destination is not what it
// appears to be.
func (s *Service) handleReverted(ctx context.Context, sw *Sweep) (time.Duration, error) {
	if err := s.deps.Nonces.MarkConfirmed(ctx, sw.Network, sw.FromAddress, nonceOwner(sw.ID)); err != nil {
		return 0, fmt.Errorf("marcar nonce consumido: %w", err)
	}
	_, err := s.failNeedsReview(ctx, sw, "tx_reverted", errors.New("la transacción de sweep fue revertida on-chain"))
	return 0, err
}

// complete runs ONLY after the configured confirmations were reached. It
// does NOT touch the Ledger — the credit already happened at deposit
// confirmation time (see package doc) — it only marks the sweep completed
// and writes the custody audit trail.
func (s *Service) complete(ctx context.Context, sw *Sweep) (time.Duration, error) {
	applied, err := s.deps.Store.Apply(ctx, sw.ID, StatusConfirming, StatusCompleted,
		Update{MarkCompleted: true},
		Event{Type: "completed", Detail: map[string]any{"tx_hash": sw.TxHash, "amount": sw.Amount.String()}})
	if err != nil {
		return 0, err
	}
	if !applied {
		return 0, nil
	}
	if err := s.deps.Nonces.MarkConfirmed(ctx, sw.Network, sw.FromAddress, nonceOwner(sw.ID)); err != nil {
		log.Printf("sweep: no se pudo marcar el nonce confirmado id=%s: %v", sw.ID, err)
	}
	s.auditWallet(ctx, &sw.ToCustodyWalletID, "sweep_completed", map[string]any{
		"sweep_id": sw.ID.String(), "network": string(sw.Network), "asset_code": sw.AssetCode,
		"from_address": sw.FromAddress, "to_address": sw.ToAddress, "tx_hash": sw.TxHash, "amount": sw.Amount.String(),
	})
	log.Printf("sweep: COMPLETADO id=%s network=%s asset=%s from=%s amount=%s tx=%s", sw.ID, sw.Network, sw.AssetCode, sw.FromAddress, sw.Amount.String(), sw.TxHash)
	return 0, nil
}

// ---- broadcast error classification ----
//
// Deliberately duplicated (not imported) from blockchainwithdrawal: it is
// unexported there, and re-exporting a piece of Bloque 6B purely so 7.3
// can import it would be a change to 6B "without necessity" (spec:
// "No modificar 6B sin necesidad"). This is pure, stateless string
// classification — not a second signer, nonce manager or broadcaster — and
// is kept byte-for-byte identical to blockchainwithdrawal's classifier so
// the two subsystems always treat the same provider error the same way.

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
