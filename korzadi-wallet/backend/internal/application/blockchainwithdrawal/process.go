package blockchainwithdrawal

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	appfee "korzadi-wallet/backend/internal/application/fee"
	"korzadi-wallet/backend/internal/domain"
)

const (
	confirmPollDelay  = 10 * time.Second
	retryDelay        = 15 * time.Second
	maxBackoff        = 5 * time.Minute
	preSignRetryLimit = 50 // transient failures tolerated BEFORE signing; then the (still unsigned) withdrawal is failed and released
)

func backoff(attempts int) time.Duration {
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

// Advance performs the next step of ONE withdrawal. The caller must hold the
// row's lease. Every step is idempotent and persists its result before the
// next one, so a crash at any point resumes here on the next tick:
//
//	pending    → reserve amount + fee                    (reserveStep)
//	reserved   → processing                              (startProcessing)
//	processing → nonce, sign, PERSIST signed tx, broadcast (processingStep)
//	broadcasted/confirming → follow the chain, then settle (confirmStep)
//
// It returns how long to wait before the next attempt; a non-nil error means
// "transient problem, retry with backoff" (the row's state is unchanged or
// safely advanced).
func (s *Service) Advance(ctx context.Context, w *Withdrawal) (time.Duration, error) {
	switch w.Status {
	case StatusPending:
		_, err := s.reserveStep(ctx, w)
		if err != nil && !isFinalFailure(err) {
			return 0, err
		}
		return 0, nil

	case StatusReserved:
		if d, held := s.securityHold(ctx, w); held {
			return d, nil
		}
		updated, err := s.startProcessing(ctx, w)
		if err != nil || updated == nil || updated.Status != StatusProcessing {
			return 0, err
		}
		return s.processingStep(ctx, updated)

	case StatusProcessing:
		if d, held := s.securityHold(ctx, w); held {
			return d, nil
		}
		return s.processingStep(ctx, w)

	case StatusBroadcasted, StatusConfirming:
		return s.confirmStep(ctx, w)

	default:
		return 0, nil
	}
}

// ---- reserved → processing ----

func (s *Service) startProcessing(ctx context.Context, w *Withdrawal) (*Withdrawal, error) {
	if err := s.verifyReservations(ctx, w); err != nil {
		if errors.Is(err, ErrReservationLost) {
			return s.failWithRelease(ctx, w, "reservation_lost", err, false)
		}
		return w, err
	}
	if _, err := s.Store.Apply(ctx, w.ID, StatusReserved, StatusProcessing, Update{}, Event{Type: "processing_started"}); err != nil {
		return w, err
	}
	return s.refresh(ctx, w)
}

// verifyReservations proves BOTH reservations still protect the funds. It is
// checked before signing so a transaction is never built for a withdrawal
// whose funds are no longer locked.
func (s *Service) verifyReservations(ctx context.Context, w *Withdrawal) error {
	for _, id := range []*uuid.UUID{w.AmountReservationID, w.FeeReservationID} {
		if id == nil {
			return fmt.Errorf("%w: falta una reserva", ErrReservationLost)
		}
		res, err := s.Ledger.FindReservation(ctx, *id)
		if err != nil {
			if errors.Is(err, domain.ErrReservationNotFound) {
				return fmt.Errorf("%w: reserva inexistente", ErrReservationLost)
			}
			return err
		}
		if res == nil || res.Status != domain.ReservationActive {
			return fmt.Errorf("%w: reserva %s no activa", ErrReservationLost, id)
		}
	}
	return nil
}

// ---- processing: prepare, sign, persist, broadcast ----

func (s *Service) processingStep(ctx context.Context, w *Withdrawal) (time.Duration, error) {
	rt, ok := s.Networks[w.Network]
	if !ok || !rt.configured() {
		return 0, fmt.Errorf("%w: %s", ErrNetworkNotConfigured, w.Network)
	}

	if len(w.SignedRawTx) == 0 {
		if err := s.verifyReservations(ctx, w); err != nil {
			if errors.Is(err, ErrReservationLost) {
				_, ferr := s.failWithRelease(ctx, w, "reservation_lost", err, false)
				return 0, ferr
			}
			return 0, err
		}
		updated, err := s.prepareAndSign(ctx, rt, w)
		if err != nil {
			return 0, err
		}
		if updated == nil || updated.Status != StatusProcessing || len(updated.SignedRawTx) == 0 {
			return 0, nil // failed/finished by prepareAndSign, or picked up by another worker
		}
		w = updated
	}
	return s.broadcastStep(ctx, rt, w)
}

// prepareAndSign resolves everything from the registries, checks funds and
// gas, allocates the nonce (PostgreSQL), signs through the Signer and
// PERSISTS the signed transaction and its hash BEFORE anything is sent.
// Nothing is broadcast here, so a crash after Sign but before persistence
// can never cause a spend: the next run simply signs again.
//
// Returns (nil-or-terminal row, nil) when the withdrawal was failed here.
func (s *Service) prepareAndSign(ctx context.Context, rt NetworkRuntime, w *Withdrawal) (*Withdrawal, error) {
	if w.Attempts >= preSignRetryLimit {
		return s.failWithRelease(ctx, w, "prepare_retries_exhausted", errors.New("demasiados intentos antes de firmar"), false)
	}
	if s.Signer == nil {
		return w, ErrNoSigner
	}

	netAsset, found, err := s.Registry.Get(ctx, w.Network, w.AssetCode)
	if err != nil {
		return w, fmt.Errorf("registro de assets: %w", err)
	}
	if !found || !netAsset.Enabled {
		return s.failWithRelease(ctx, w, "asset_disabled", ErrAssetNotEnabled, false)
	}
	raw, err := ToRawUnits(w.Amount, netAsset.Decimals)
	if err != nil {
		return s.failWithRelease(ctx, w, "invalid_amount", err, false)
	}
	// The contract ALWAYS comes from the registry (never from the request).
	contract := ""
	if netAsset.Kind == domain.AssetKindToken {
		contract = netAsset.ContractAddress
		if contract == "" {
			return s.failWithRelease(ctx, w, "asset_disabled", ErrAssetNotEnabled, false)
		}
	}

	chain := rt.Chain
	from := w.FromAddress

	// Camino B: every withdrawal of a network shares the HOT wallet, so (1) the
	// check-and-sign below is serialised per HOT address (in this process), and
	// (2) funds already committed to signed-but-unfinished withdrawals are
	// subtracted from the on-chain balance. Without this, two concurrent
	// withdrawals would both see the same balance and both be approved.
	committedRaw := new(big.Int)
	if s.HotWallets != nil {
		defer s.lockHot(w.Network, from)()
		reader, ok := s.Store.(InFlightReader)
		if !ok {
			return w, errors.New("store sin soporte de retiros en vuelo")
		}
		committed, cerr := reader.SumInFlight(ctx, w.Network, from, w.AssetCode, w.ID)
		if cerr != nil {
			return w, fmt.Errorf("retiros en vuelo: %w", cerr)
		}
		if committed.IsPositive() {
			committedRaw, err = ToRawUnits(committed, netAsset.Decimals)
			if err != nil {
				return w, fmt.Errorf("convertir retiros en vuelo: %w", err)
			}
		}
	}

	if rt.Resource != nil {
		return s.prepareResourceTx(ctx, rt, w, netAsset, raw, contract, committedRaw, from)
	}

	gasPrice, err := chain.SuggestGasPrice(ctx)
	if err != nil {
		return w, fmt.Errorf("gas price: %w", err)
	}
	if gasPrice == nil || gasPrice.Sign() <= 0 {
		return w, errors.New("gas price inválido devuelto por la red")
	}
	nativeBalance, err := chain.NativeBalanceWei(ctx, from)
	if err != nil {
		return w, fmt.Errorf("balance nativo: %w", err)
	}
	// nativeAvail: native balance not already committed to in-flight native
	// withdrawals from this same source (equal to nativeBalance otherwise).
	nativeAvail := nativeBalance
	if netAsset.Kind == domain.AssetKindNative {
		nativeAvail = new(big.Int).Sub(nativeBalance, committedRaw)
	}

	var gasLimit uint64
	switch netAsset.Kind {
	case domain.AssetKindNative:
		if nativeAvail.Cmp(raw) < 0 {
			return s.failWithRelease(ctx, w, "insufficient_onchain_funds", ErrInsufficientOnChain, false)
		}
		gasLimit, err = chain.EstimateGasNative(ctx, from, w.ToAddress, raw)
	case domain.AssetKindToken:
		tokenBalance, terr := chain.TokenBalanceRaw(ctx, contract, from)
		if terr != nil {
			return w, fmt.Errorf("balance del token: %w", terr)
		}
		if new(big.Int).Sub(tokenBalance, committedRaw).Cmp(raw) < 0 {
			return s.failWithRelease(ctx, w, "insufficient_onchain_funds", ErrInsufficientOnChain, false)
		}
		gasLimit, err = chain.EstimateGasToken(ctx, from, contract, w.ToAddress, raw)
	default:
		return s.failWithRelease(ctx, w, "asset_disabled", ErrAssetNotEnabled, false)
	}
	if err != nil {
		if isRevertError(err) {
			return s.failWithRelease(ctx, w, "estimate_reverted", err, false)
		}
		return w, fmt.Errorf("estimar gas: %w", err)
	}
	gasLimit += gasLimit / 5 // 20% headroom against state shifting before inclusion
	gasCost := new(big.Int).Mul(new(big.Int).SetUint64(gasLimit), gasPrice)

	// Do not silently absorb a gas spike beyond what the user was quoted.
	if w.EstimatedNetworkFeeNative.IsPositive() {
		factor := decimal.NewFromInt(10000 + s.Config.MaxNetworkFeeOverrunBps)
		ceiling := w.EstimatedNetworkFeeNative.Mul(factor).Div(decimal.NewFromInt(10000))
		if WeiToDecimal(gasCost).GreaterThan(ceiling) {
			return s.failWithRelease(ctx, w, "network_fee_overrun", ErrNetworkFeeOverrun, false)
		}
	}

	// Gas: reuse the existing gas station. For a NATIVE withdrawal the
	// address must already hold the amount itself (checked above) — the
	// station only ever covers the gas shortfall, never the user's amount.
	required := gasCost
	if netAsset.Kind == domain.AssetKindNative {
		required = new(big.Int).Add(raw, gasCost)
	}
	topUpTarget := required
	if netAsset.Kind == domain.AssetKindNative {
		topUpTarget = new(big.Int).Add(required, committedRaw)
	}
	if nativeAvail.Cmp(required) < 0 {
		if rt.Gas == nil {
			return s.failWithRelease(ctx, w, "gas_unavailable", ErrGasUnavailable, false)
		}
		if err := rt.Gas.EnsureGasFor(ctx, gasOwner(w.ID), from, topUpTarget); err != nil {
			return w, fmt.Errorf("gas station: %w", err)
		}
	}

	pending, err := chain.PendingNonce(ctx, from)
	if err != nil {
		return w, fmt.Errorf("nonce pendiente: %w", err)
	}
	nonce, err := s.Nonces.Allocate(ctx, w.Network, from, nonceOwner(w.ID), pending)
	if err != nil {
		return w, fmt.Errorf("asignar nonce: %w", err)
	}

	signed, err := s.Signer.Sign(ctx, SignRequest{
		Network:         w.Network,
		UserID:          w.UserID,
		FromAddress:     from,
		ToAddress:       w.ToAddress,
		AssetKind:       netAsset.Kind,
		ContractAddress: contract,
		AmountRaw:       raw,
		ChainID:         chain.ChainID(),
		Nonce:           nonce,
		GasLimit:        gasLimit,
		GasPriceWei:     gasPrice,
	})
	if err != nil {
		return w, fmt.Errorf("firmar: %w", err)
	}

	nonceI, gasLimitI := int64(nonce), int64(gasLimit)
	gasPriceDec := decimal.NewFromBigInt(gasPrice, 0)
	hash := signed.TxHash
	applied, err := s.Store.Apply(ctx, w.ID, StatusProcessing, StatusProcessing,
		Update{
			Nonce: &nonceI, GasLimit: &gasLimitI, GasPriceWei: &gasPriceDec,
			SignedRawTx: signed.Raw, TxHash: &hash, MarkSigned: true, RequireUnsigned: true,
		},
		Event{Type: "signed", Detail: map[string]any{
			"tx_hash": hash, "nonce": nonce, "gas_limit": gasLimit, "gas_price_wei": gasPrice.String(),
		}})
	if err != nil {
		return w, fmt.Errorf("persistir tx firmada: %w", err)
	}
	if !applied {
		return s.refresh(ctx, w) // someone else already persisted a signed tx (or the state moved)
	}
	if err := s.Nonces.MarkSigned(ctx, w.Network, from, nonceOwner(w.ID), hash); err != nil {
		log.Printf("withdrawal: no se pudo marcar el nonce como firmado id=%s: %v (se reintentará al transmitir)", w.ID, err)
	}
	log.Printf("withdrawal: firmado id=%s network=%s asset=%s nonce=%d tx=%s", w.ID, w.Network, w.AssetCode, nonce, hash)
	return s.refresh(ctx, w)
}

// broadcastStep sends the already-persisted signed transaction, but NEVER
// blindly: every attempt first checks whether the network already knows the
// transaction (receipt, then mempool) and whether the nonce is still free.
// Re-sending the SAME signed bytes is idempotent (same hash, same nonce), so
// even a repeated send cannot spend twice; an ambiguous error (timeout, EOF)
// never assumes "not sent".
func (s *Service) broadcastStep(ctx context.Context, rt NetworkRuntime, w *Withdrawal) (time.Duration, error) {
	if rt.Resource != nil {
		return s.broadcastResourceStep(ctx, rt, w)
	}
	chain := rt.Chain
	if w.TxHash == "" || len(w.SignedRawTx) == 0 || w.Nonce == nil {
		// Inconsistent row: treat as unsigned and re-prepare.
		if _, err := s.Store.Apply(ctx, w.ID, StatusProcessing, StatusProcessing, Update{ClearSigned: true}, Event{Type: "reset_signed_state"}); err != nil {
			return 0, err
		}
		return 0, nil
	}
	if err := s.Nonces.MarkSigned(ctx, w.Network, w.FromAddress, nonceOwner(w.ID), w.TxHash); err != nil {
		log.Printf("withdrawal: no se pudo marcar el nonce como firmado id=%s: %v", w.ID, err)
	}

	receipt, err := chain.Receipt(ctx, w.TxHash)
	if err != nil {
		return 0, fmt.Errorf("consultar receipt: %w", err)
	}
	if receipt != nil {
		return s.markBroadcasted(ctx, w, "found_on_chain")
	}
	known, err := chain.TxKnown(ctx, w.TxHash)
	if err != nil {
		return 0, fmt.Errorf("consultar tx en la red: %w", err)
	}
	if known {
		return s.markBroadcasted(ctx, w, "known_by_node")
	}

	pending, err := chain.PendingNonce(ctx, w.FromAddress)
	if err != nil {
		return 0, fmt.Errorf("nonce pendiente: %w", err)
	}
	if pending > uint64(*w.Nonce) {
		// The nonce is taken but the network does not know OUR hash: either
		// a lagging node index or a different transaction owns the nonce.
		if s.graceExceeded(w.SignedAt) {
			_, ferr := s.failNeedsReview(ctx, w, "nonce_stuck", ErrNonceStuck)
			return 0, ferr
		}
		log.Printf("withdrawal: nonce %d ya usado en la red pero tx %s desconocida; esperando id=%s", *w.Nonce, w.TxHash, w.ID)
		return retryDelay, nil
	}

	sentHash, err := chain.Broadcast(ctx, w.SignedRawTx)
	return s.finishBroadcast(ctx, w, sentHash, err)
}

// finishBroadcast interprets the outcome of a broadcast attempt. Shared by EVM
// and TRON: ONLY errors that prove the node did not accept the tx are
// definitive; everything else is ambiguous and is reconciled, never assumed.
func (s *Service) finishBroadcast(ctx context.Context, w *Withdrawal, sentHash string, err error) (time.Duration, error) {
	if err == nil {
		if !strings.EqualFold(sentHash, w.TxHash) {
			log.Printf("withdrawal: ADVERTENCIA hash devuelto por la red (%s) distinto del firmado (%s) id=%s", sentHash, w.TxHash, w.ID)
		}
		return s.markBroadcasted(ctx, w, "accepted")
	}

	switch classifyBroadcastError(err) {
	case broadcastAlreadyKnown:
		return s.markBroadcasted(ctx, w, "already_known")

	case broadcastReprice:
		// The node definitively refused this exact transaction (fee too low
		// / balance moved): it is NOT in its pool, so discard it and sign
		// again with fresh gas — same nonce, so at most one can ever execute.
		if w.Attempts+1 >= s.Config.MaxPrepareTries {
			_, ferr := s.failWithRelease(ctx, w, "broadcast_rejected", err, false)
			return 0, ferr
		}
		if _, aerr := s.Store.Apply(ctx, w.ID, StatusProcessing, StatusProcessing, Update{ClearSigned: true},
			Event{Type: "reprice", Detail: map[string]any{"reason": sanitizeCause(err)}}); aerr != nil {
			return 0, aerr
		}
		return 0, fmt.Errorf("%w: %v", ErrBroadcastRejected, sanitizeCause(err))

	case broadcastRejected:
		_, ferr := s.failWithRelease(ctx, w, "broadcast_rejected", err, false)
		return 0, ferr

	default:
		// AMBIGUOUS (timeout, EOF, unknown error, "nonce too low"...): the
		// transaction may or may not have reached the network. Do NOT
		// assume either; the next tick re-checks receipt/mempool/nonce
		// before any further send.
		log.Printf("withdrawal: broadcast ambiguo id=%s tx=%s: %v", w.ID, w.TxHash, sanitizeCause(err))
		_ = s.Store.AddEvent(ctx, w.ID, "broadcast_ambiguous", w.Status, w.Status, map[string]any{"error": sanitizeCause(err)})
		return 0, fmt.Errorf("broadcast ambiguo: %w", err)
	}
}

func (s *Service) markBroadcasted(ctx context.Context, w *Withdrawal, how string) (time.Duration, error) {
	applied, err := s.Store.Apply(ctx, w.ID, StatusProcessing, StatusBroadcasted,
		Update{MarkBroadcasted: true},
		Event{Type: "broadcasted", Detail: map[string]any{"tx_hash": w.TxHash, "how": how}})
	if err != nil {
		return 0, err
	}
	if applied {
		log.Printf("withdrawal: transmitido id=%s network=%s asset=%s tx=%s (%s)", w.ID, w.Network, w.AssetCode, w.TxHash, how)
	}
	return confirmPollDelay, nil
}

// ---- broadcasted / confirming ----

func (s *Service) confirmStep(ctx context.Context, w *Withdrawal) (time.Duration, error) {
	rt, ok := s.Networks[w.Network]
	if !ok || !rt.configured() {
		return 0, fmt.Errorf("%w: %s", ErrNetworkNotConfigured, w.Network)
	}
	if w.TxHash == "" {
		return 0, errors.New("retiro transmitido sin tx hash")
	}
	chain := rt.observer()

	// Never trust local state alone: ask the chain every time.
	receipt, err := chain.Receipt(ctx, w.TxHash)
	if err != nil {
		return 0, fmt.Errorf("consultar receipt: %w", err)
	}
	if receipt == nil {
		return s.handleMissingReceipt(ctx, rt, w)
	}
	if !receipt.Status {
		return s.handleReverted(ctx, w, receipt)
	}

	required := uint64(1)
	if w.RequiredConfirmations != nil && *w.RequiredConfirmations > 0 {
		required = uint64(*w.RequiredConfirmations)
	} else if c := chain.ConfirmationsRequired(); c > 0 {
		required = c
	}

	reorged := w.BlockHash != "" && !strings.EqualFold(w.BlockHash, receipt.BlockHash)
	blockNumber := int64(receipt.BlockNumber)
	confirmations := int64(receipt.Confirmations)
	blockHash := receipt.BlockHash

	var ev Event
	switch {
	case reorged:
		log.Printf("withdrawal: REORG detectado id=%s tx=%s bloque %s → %s", w.ID, w.TxHash, w.BlockHash, blockHash)
		ev = Event{Type: "reorg_detected", Detail: map[string]any{"old_block_hash": w.BlockHash, "new_block_hash": blockHash, "new_block": blockNumber}}
	case w.Status == StatusBroadcasted:
		ev = Event{Type: "mined", Detail: map[string]any{"block": blockNumber, "block_hash": blockHash, "confirmations": confirmations}}
	}
	applied, err := s.Store.Apply(ctx, w.ID, w.Status, StatusConfirming,
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

	current, err := s.refresh(ctx, w)
	if err != nil {
		return 0, err
	}
	if current.Status != StatusConfirming {
		return 0, nil
	}
	return s.complete(ctx, current, receipt)
}

func (s *Service) handleMissingReceipt(ctx context.Context, rt NetworkRuntime, w *Withdrawal) (time.Duration, error) {
	chain := rt.observer()

	if w.Status == StatusConfirming {
		// It had a receipt before and now it is gone: a reorg dropped it.
		// Keep the transaction and history; go back to "broadcasted" so it
		// is followed (and, if needed, re-sent) again. NEVER completed.
		zero := int64(0)
		if _, err := s.Store.Apply(ctx, w.ID, StatusConfirming, StatusBroadcasted,
			Update{ClearBlock: true, Confirmations: &zero},
			Event{Type: "reorg_dropped", Detail: map[string]any{"old_block_hash": w.BlockHash}}); err != nil {
			return 0, err
		}
		log.Printf("withdrawal: receipt desaparecido tras reorg id=%s tx=%s", w.ID, w.TxHash)
		return confirmPollDelay, nil
	}

	known, err := chain.TxKnown(ctx, w.TxHash)
	if err != nil {
		return 0, fmt.Errorf("consultar tx en la red: %w", err)
	}
	if known {
		return confirmPollDelay, nil // still in the mempool
	}

	if rt.Resource != nil {
		// TRON: no nonce — a tx that expired unseen is re-signed; otherwise re-sent.
		return s.resourceMissing(ctx, rt, w)
	}
	if w.Nonce != nil && len(w.SignedRawTx) > 0 {
		pending, err := rt.Chain.PendingNonce(ctx, w.FromAddress)
		if err != nil {
			return 0, fmt.Errorf("nonce pendiente: %w", err)
		}
		if pending <= uint64(*w.Nonce) {
			// Dropped from the mempool and the nonce is still free: re-send
			// the SAME signed bytes (idempotent — same hash, same nonce).
			_ = s.Store.AddEvent(ctx, w.ID, "rebroadcast", w.Status, w.Status, map[string]any{"tx_hash": w.TxHash})
			if _, berr := chain.Broadcast(ctx, w.SignedRawTx); berr != nil && classifyBroadcastError(berr) != broadcastAlreadyKnown {
				return 0, fmt.Errorf("rebroadcast: %w", berr)
			}
			return confirmPollDelay, nil
		}
	}

	// Nonce consumed but our hash unknown: lagging index or a foreign tx.
	if s.graceExceeded(w.BroadcastedAt) {
		_, ferr := s.failNeedsReview(ctx, w, "tx_lost", ErrNonceStuck)
		return 0, ferr
	}
	return confirmPollDelay, nil
}

// handleReverted: the transaction was mined but reverted — the transfer did
// NOT happen. The nonce is consumed (never released); the user's funds are
// released; the gas already burned is recorded as the actual network fee
// (Korzadi absorbs it — no negative balances, no silent adjustments).
func (s *Service) handleReverted(ctx context.Context, w *Withdrawal, receipt *domain.TransactionReceipt) (time.Duration, error) {
	if s.nonceTracked(w.Network) {
		if err := s.Nonces.MarkConfirmed(ctx, w.Network, w.FromAddress, nonceOwner(w.ID)); err != nil {
			return 0, fmt.Errorf("marcar nonce consumido: %w", err)
		}
	}
	native, usdt := s.actualFees(ctx, w, receipt)
	if _, err := s.Store.Apply(ctx, w.ID, w.Status, w.Status,
		Update{ActualNetworkFeeNative: native, ActualNetworkFeeUSDT: usdt}, Event{}); err != nil {
		return 0, err
	}
	_, err := s.failWithRelease(ctx, w, "tx_reverted", errors.New("la transacción fue revertida on-chain"), false)
	return 0, err
}

// ---- completion / settlement ----

// complete runs ONLY after the configured confirmations were reached and the
// receipt block is the canonical one. It settles the ledger atomically
// (reservations consumed + debit posted in ONE SQL transaction) and then
// marks the withdrawal completed. Both steps are idempotent, so a crash
// between them just replays the settlement (same idempotency key) and
// finishes.
func (s *Service) complete(ctx context.Context, w *Withdrawal, receipt *domain.TransactionReceipt) (time.Duration, error) {
	ledgerTx, err := s.settle(ctx, w)
	if err != nil {
		if errors.Is(err, domain.ErrReservationNotActive) || errors.Is(err, domain.ErrReservationNotFound) ||
			errors.Is(err, domain.ErrSettlementMismatch) || errors.Is(err, domain.ErrInsufficientFunds) ||
			errors.Is(err, ErrReservationLost) {
			// The funds LEFT the chain but the ledger cannot settle them:
			// never auto-release. An operator must reconcile.
			_, ferr := s.failNeedsReview(ctx, w, "settlement_failed", err)
			return 0, ferr
		}
		return 0, fmt.Errorf("liquidar en el ledger: %w", err)
	}

	native, usdt := s.actualFees(ctx, w, receipt)
	applied, err := s.Store.Apply(ctx, w.ID, StatusConfirming, StatusCompleted,
		Update{LedgerTransactionID: &ledgerTx.ID, ActualNetworkFeeNative: native, ActualNetworkFeeUSDT: usdt, MarkCompleted: true},
		Event{Type: "completed", Detail: map[string]any{
			"ledger_transaction_id": ledgerTx.ID.String(), "tx_hash": w.TxHash,
			"service_fee_usdt": w.ServiceFeeUSDT.String(), "estimated_network_fee_usdt": w.EstimatedNetworkFeeUSDT.String(),
			"total_fee_usdt": w.TotalFeeUSDT.String(),
		}})
	if err != nil {
		return 0, err
	}
	if !applied {
		return 0, nil
	}
	s.finishLimit(ctx, w, true)
	s.recordDestinationUse(ctx, w)

	if s.nonceTracked(w.Network) {
		if err := s.Nonces.MarkConfirmed(ctx, w.Network, w.FromAddress, nonceOwner(w.ID)); err != nil {
			log.Printf("withdrawal: no se pudo marcar el nonce confirmado id=%s: %v", w.ID, err)
		}
	}
	if w.FeeQuoteID != nil {
		if err := s.Quotes.UpdateStatus(ctx, *w.FeeQuoteID, appfee.QuoteStatusConsumed); err != nil {
			log.Printf("withdrawal: no se pudo marcar la cotización consumida id=%s: %v", w.ID, err)
		}
	}
	log.Printf("withdrawal: COMPLETADO id=%s network=%s asset=%s amount=%s tx=%s", w.ID, w.Network, w.AssetCode, w.Amount.String(), w.TxHash)
	return 0, nil
}

// settle posts the accounting for a completed withdrawal through the Ledger
// (never ledger_entries directly). One atomic settlement consumes BOTH
// reservations and books, as separate entries so each component stays
// visible:
//
//	user   −Amount      (withdrawn asset)   / clearing +Amount
//	user   −ServiceFee  (USDT)              / clearing +ServiceFee
//	user   −NetworkFee  (USDT, estimated)   / clearing +NetworkFee
//
// The user pays exactly what was reserved (the quoted total); the real gas
// cost is only recorded on the withdrawal for reconciliation.
func (s *Service) settle(ctx context.Context, w *Withdrawal) (*domain.LedgerTransaction, error) {
	if w.AmountReservationID == nil || w.FeeReservationID == nil {
		return nil, fmt.Errorf("%w: faltan reservas al liquidar", ErrReservationLost)
	}
	userAsset, err := s.UserAccounts.FindOrCreateUserAccount(ctx, w.UserID, w.AssetCode)
	if err != nil {
		return nil, fmt.Errorf("cuenta de usuario %s: %w", w.AssetCode, err)
	}
	userFee, err := s.UserAccounts.FindOrCreateUserAccount(ctx, w.UserID, FeeAssetCode)
	if err != nil {
		return nil, fmt.Errorf("cuenta de usuario %s: %w", FeeAssetCode, err)
	}
	clearingAsset, err := s.Clearing.FindClearingAccount(ctx, w.Network, w.AssetCode)
	if err != nil {
		return nil, fmt.Errorf("cuenta de clearing %s: %w", w.AssetCode, err)
	}
	clearingFee, err := s.Clearing.FindClearingAccount(ctx, w.Network, FeeAssetCode)
	if err != nil {
		return nil, fmt.Errorf("cuenta de clearing %s: %w", FeeAssetCode, err)
	}

	entries := []domain.LedgerEntryInput{
		{WalletAccountID: userAsset, AssetCode: w.AssetCode, Amount: w.Amount.Neg()},
		{WalletAccountID: clearingAsset, AssetCode: w.AssetCode, Amount: w.Amount},
	}
	if w.ServiceFeeUSDT.IsPositive() {
		entries = append(entries,
			domain.LedgerEntryInput{WalletAccountID: userFee, AssetCode: FeeAssetCode, Amount: w.ServiceFeeUSDT.Neg()},
			domain.LedgerEntryInput{WalletAccountID: clearingFee, AssetCode: FeeAssetCode, Amount: w.ServiceFeeUSDT})
	}
	if w.EstimatedNetworkFeeUSDT.IsPositive() {
		entries = append(entries,
			domain.LedgerEntryInput{WalletAccountID: userFee, AssetCode: FeeAssetCode, Amount: w.EstimatedNetworkFeeUSDT.Neg()},
			domain.LedgerEntryInput{WalletAccountID: clearingFee, AssetCode: FeeAssetCode, Amount: w.EstimatedNetworkFeeUSDT})
	}

	ref := w.ID.String()
	userID := w.UserID
	return s.Ledger.SettleReservations(ctx, domain.ReservationSettlementInput{
		ReservationIDs: []uuid.UUID{*w.AmountReservationID, *w.FeeReservationID},
		TxType:         domain.TxTypeWithdrawal,
		IdempotencyKey: "withdrawal-settle:" + w.ID.String(),
		ReferenceID:    &ref,
		Description: fmt.Sprintf("Retiro %s %s a %s en %s (tx %s): monto + service fee %s + network fee %s USDT",
			w.Amount.String(), w.AssetCode, w.ToAddress, w.Network, w.TxHash,
			w.ServiceFeeUSDT.String(), w.EstimatedNetworkFeeUSDT.String()),
		CreatedBy: &userID,
		Entries:   entries,
	})
}

// actualFees computes the real network fee from the receipt (gas used ×
// effective gas price) and converts it to USDT with the SAME exchange rate
// the user was quoted. Returns nil pointers when it cannot be computed.
func (s *Service) actualFees(ctx context.Context, w *Withdrawal, receipt *domain.TransactionReceipt) (native, usdt *decimal.Decimal) {
	var nativeFee decimal.Decimal
	switch {
	case receipt == nil:
		return nil, nil
	case receipt.NativeFee != nil: // TRON reports the real fee directly (TRX)
		nativeFee = *receipt.NativeFee
	case receipt.EffectiveGasPriceWei != nil && receipt.GasUsed != 0:
		nativeFee = WeiToDecimal(new(big.Int).Mul(new(big.Int).SetUint64(receipt.GasUsed), receipt.EffectiveGasPriceWei))
	default:
		return nil, nil
	}
	native = &nativeFee
	if w.FeeQuoteID != nil {
		if q, err := s.Quotes.FindByID(ctx, *w.FeeQuoteID); err == nil && q != nil && q.ExchangeRate.IsPositive() {
			converted := nativeFee.Mul(q.ExchangeRate).Round(18)
			usdt = &converted
		}
	}
	return native, usdt
}

func (s *Service) graceExceeded(since *time.Time) bool {
	return since != nil && s.Now().Sub(*since) > s.Config.BroadcastGrace
}

// ---- broadcast error classification ----

type broadcastClass int

const (
	broadcastAmbiguous broadcastClass = iota
	broadcastAlreadyKnown
	broadcastReprice
	broadcastRejected
)

// classifyBroadcastError inspects an RPC error message. ONLY errors that
// prove the node did not accept the transaction are "definitive"; anything
// unrecognized — including timeouts and "nonce too low" (our own tx may
// already be mined) — is AMBIGUOUS and must be reconciled, never assumed.
func classifyBroadcastError(err error) broadcastClass {
	if err == nil {
		return broadcastAmbiguous
	}
	msg := strings.ToLower(err.Error())
	has := func(subs ...string) bool {
		for _, s := range subs {
			if strings.Contains(msg, s) {
				return true
			}
		}
		return false
	}
	switch {
	case has("already known", "known transaction", "same hash was already imported", "already in the mempool",
		"dup_transaction_error"):
		return broadcastAlreadyKnown
	case has("replacement transaction underpriced"):
		// Another tx with this nonce sits in the pool — possibly ours from
		// an earlier ambiguous send. Reconcile, do not reprice blindly.
		return broadcastAmbiguous
	case has("transaction underpriced", "max fee per gas less than block base fee", "fee cap less than block base fee", "insufficient funds",
		// TRON: this exact tx cannot be included (expired / bad TaPoS / no bandwidth) → sign a new one
		"transaction_expiration_error", "tapos_error", "bandwith_error", "account resource insufficient"):
		return broadcastReprice
	case has("intrinsic gas too low", "exceeds block gas limit", "gas limit reached", "invalid sender", "invalid chain id", "oversized data",
		"sigerror", "contract_validate_error", "too_big_transaction_error"):
		return broadcastRejected
	default:
		return broadcastAmbiguous
	}
}

func isRevertError(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "execution reverted") || strings.Contains(msg, "revert")
}
