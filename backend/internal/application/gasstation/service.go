package gasstation

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	bw "korzadi-wallet/backend/internal/application/blockchainwithdrawal"
	"korzadi-wallet/backend/internal/application/custody"
	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/cryptutil"
	"korzadi-wallet/backend/internal/infrastructure/evmwallet"
	"korzadi-wallet/backend/internal/infrastructure/postgres"
)

// Deps wires one Service — one instance per EVM network, exactly like the
// old bscwithdrawal.GasStation was one instance bound to BSC. main.go
// builds one per active entry of evmProviders that has an active GAS
// custody wallet + enabled policy.
type Deps struct {
	Network domain.Network

	Wallets  WalletRegistry
	Policies PolicyStore
	Refills  RefillStore
	Nonces   NonceStore
	Chain    Chain
	Assets   domain.AssetRegistry

	Decryptor Decryptor
	Audit     AuditLogger // optional: nil disables audit logging (best-effort everywhere)

	// TopUpGasLimit is the gas limit for the native top-up transfer itself.
	// 21000 (the fixed cost of a plain value transfer with no calldata) is
	// correct for every EVM network today and is the default when zero.
	TopUpGasLimit uint64

	// ConfirmPollEvery / ConfirmMaxWait govern how long EnsureGasFor waits
	// for ITS OWN just-broadcast top-up to be mined before returning
	// success — same synchronous-wait contract the old bscwithdrawal
	// gas station already had (its caller, blockchainwithdrawal.process.go,
	// calls EnsureGasFor synchronously and expects the target to actually
	// have gas by the time it returns nil). Defaults: 3s / 90s.
	ConfirmPollEvery time.Duration
	ConfirmMaxWait   time.Duration

	// Now and Sleep are overridable for tests; production defaults are
	// time.Now and a context-aware real sleep.
	Now   func() time.Time
	Sleep func(ctx context.Context, d time.Duration) error
}

type Service struct {
	network domain.Network

	wallets  WalletRegistry
	policies PolicyStore
	refills  RefillStore
	nonces   NonceStore
	chain    Chain
	assets   domain.AssetRegistry

	decryptor Decryptor
	audit     AuditLogger

	topUpGasLimit    uint64
	confirmPollEvery time.Duration
	confirmMaxWait   time.Duration

	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) error
}

func NewService(d Deps) (*Service, error) {
	if d.Network == "" {
		return nil, fmt.Errorf("gasstation: Network requerida")
	}
	if !d.Network.IsEVM() {
		return nil, fmt.Errorf("gasstation: red no EVM: %s (este bloque es exclusivamente EVM, ver doc del paquete)", d.Network)
	}
	if d.Wallets == nil || d.Policies == nil || d.Refills == nil || d.Nonces == nil || d.Chain == nil || d.Assets == nil || d.Decryptor == nil {
		return nil, fmt.Errorf("gasstation: dependencias requeridas faltantes")
	}
	s := &Service{
		network: d.Network, wallets: d.Wallets, policies: d.Policies, refills: d.Refills,
		nonces: d.Nonces, chain: d.Chain, assets: d.Assets, decryptor: d.Decryptor, audit: d.Audit,
		topUpGasLimit:    d.TopUpGasLimit,
		confirmPollEvery: d.ConfirmPollEvery,
		confirmMaxWait:   d.ConfirmMaxWait,
		now:              d.Now,
		sleep:            d.Sleep,
	}
	if s.topUpGasLimit == 0 {
		s.topUpGasLimit = 21000
	}
	if s.confirmPollEvery == 0 {
		s.confirmPollEvery = 3 * time.Second
	}
	if s.confirmMaxWait == 0 {
		s.confirmMaxWait = 90 * time.Second
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.sleep == nil {
		s.sleep = func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		}
	}
	return s, nil
}

// EnsureGasFor satisfies blockchainwithdrawal.GasTopUp / the same shape the
// old bscwithdrawal.GasStation exposed. targetAddress is the address that
// needs native gas; requiredWei is what the CALLER's own operation needs —
// the Gas Station may fund MORE than that (up to target_balance) but never
// less, and never more than max_refill in one call.
//
// owner identifies the calling operation for BOTH nonce bookkeeping (evm_nonces,
// unchanged convention: e.g. "gas:<withdrawal id>") and refill idempotency
// (gas_station_refills) — a retry with the same owner after a crash resumes
// the same refill instead of sending a second transaction.
func (s *Service) EnsureGasFor(ctx context.Context, owner, targetAddress string, requiredWei *big.Int) error {
	if strings.TrimSpace(owner) == "" {
		return fmt.Errorf("%w: owner requerido", ErrInvalidRequest)
	}
	if requiredWei == nil || requiredWei.Sign() <= 0 {
		return fmt.Errorf("%w: requiredWei inválido", ErrInvalidRequest)
	}
	if !common.IsHexAddress(targetAddress) {
		return fmt.Errorf("%w: dirección destino inválida", ErrInvalidRequest)
	}
	target := common.HexToAddress(targetAddress).Hex()

	nativeAsset, err := s.resolveNativeAsset(ctx)
	if err != nil {
		return err
	}

	wallet, err := s.wallets.FindActive(ctx, string(custody.WalletTypeGas), string(s.network), nativeAsset.AssetCode)
	if err != nil {
		return fmt.Errorf("gasstation: resolver wallet GAS: %w", err)
	}
	if wallet == nil {
		return ErrNoGasWallet
	}

	policy, err := s.policies.Get(ctx, wallet.ID)
	if err != nil {
		return fmt.Errorf("gasstation: leer política: %w", err)
	}
	if policy == nil || !policy.Enabled {
		return ErrDisabled
	}

	currentBalance, err := s.chain.NativeBalanceWei(ctx, target)
	if err != nil {
		return fmt.Errorf("gasstation: consultar balance nativo de %s: %w", target, err)
	}

	minimumWei, err := toWei(policy.MinimumBalance, nativeAsset.Decimals)
	if err != nil {
		return fmt.Errorf("gasstation: minimum_balance de política inválida: %w", err)
	}

	// ---- Threshold: no refill unless genuinely needed ----
	needsRefill := currentBalance.Cmp(minimumWei) < 0 || currentBalance.Cmp(requiredWei) < 0
	if !needsRefill {
		return nil
	}

	// ---- Idempotency: resume our own in-flight refill, if any ----
	owned, err := s.refills.FindOwned(ctx, string(s.network), target, owner)
	if err != nil {
		return fmt.Errorf("gasstation: buscar refill propio: %w", err)
	}
	if owned != nil {
		switch postgres.RefillStatus(owned.Status) {
		case postgres.RefillStatusConfirmed:
			return nil
		case postgres.RefillStatusBroadcasted:
			return s.resumeBroadcasted(ctx, wallet, owner, owned)
		case postgres.RefillStatusPending:
			// Signed but never persisted as broadcasted (crash between
			// planning and signing) — fall through and (re)plan+sign+send
			// using the SAME row (Create is not called again).
			return s.signAndSend(ctx, wallet, owner, target, owned)
		}
	}

	// ---- Plan a new refill ----
	targetWei, err := toWei(policy.TargetBalance, nativeAsset.Decimals)
	if err != nil {
		return fmt.Errorf("gasstation: target_balance de política inválida: %w", err)
	}
	maxRefillWei, err := toWei(policy.MaxRefill, nativeAsset.Decimals)
	if err != nil {
		return fmt.Errorf("gasstation: max_refill de política inválida: %w", err)
	}
	plan, err := planRefill(currentBalance, requiredWei, targetWei, maxRefillWei)
	if err != nil {
		s.auditEvent(ctx, &wallet.ID, "gas_station_refill_rejected", map[string]any{
			"network": s.network, "target": target, "reason": err.Error(),
		})
		return err
	}

	created, err := s.refills.Create(ctx, postgres.GasStationRefill{
		CustodyWalletID: wallet.ID,
		Network:         string(s.network),
		TargetAddress:   target,
		OwnerRef:        owner,
		AmountWei:       plan.amountWei,
	})
	if err != nil {
		if errors.Is(err, postgres.ErrRefillInFlight) {
			return s.observeInFlight(ctx, target, requiredWei)
		}
		return fmt.Errorf("gasstation: crear refill: %w", err)
	}

	return s.signAndSend(ctx, wallet, owner, target, created)
}

// planRefill decides exactly how much to send, capped by policy — it NEVER
// exceeds max_refill and NEVER pushes the wallet past target_balance
// (spec: "refill nunca supera target/max_refill"), and refuses outright
// (ErrPolicyExceeded) rather than silently sending less than the caller's
// own operation needs.
func planRefill(currentBalance, requiredWei, targetWei, maxRefillWei *big.Int) (refillPlan, error) {
	effectiveTarget := targetWei
	if requiredWei.Cmp(effectiveTarget) > 0 {
		effectiveTarget = requiredWei
	}
	desired := new(big.Int).Sub(effectiveTarget, currentBalance)
	if desired.Sign() <= 0 {
		// Should not happen (caller already established needsRefill), but
		// fail safe rather than send a zero/negative amount.
		return refillPlan{}, fmt.Errorf("%w: monto de refill no positivo", ErrInvalidRequest)
	}
	amount := desired
	if amount.Cmp(maxRefillWei) > 0 {
		amount = new(big.Int).Set(maxRefillWei)
	}
	newBalance := new(big.Int).Add(currentBalance, amount)
	if newBalance.Cmp(requiredWei) < 0 {
		return refillPlan{}, ErrPolicyExceeded
	}
	return refillPlan{amountWei: amount}, nil
}

// signAndSend signs and broadcasts refillRow.AmountWei to target from the
// GAS wallet, then waits (bounded) for confirmation. It is used both for a
// fresh 'pending' row and for resuming a 'pending' row left over from a
// crash between planning and signing — either way, the amount ALREADY
// decided (refillRow.AmountWei) is what gets sent, never re-derived.
func (s *Service) signAndSend(ctx context.Context, wallet *postgres.CustodyWallet, owner, target string, refillRow *postgres.GasStationRefill) error {

	pendingNonce, err := s.chain.PendingNonce(ctx, wallet.Address)
	if err != nil {
		return fmt.Errorf("gasstation: obtener nonce pendiente de la wallet GAS: %w", err)
	}
	nonce, err := s.nonces.Allocate(ctx, s.network, wallet.Address, owner, pendingNonce)
	if err != nil {
		return fmt.Errorf("gasstation: asignar nonce: %w", err)
	}

	keyHex, err := s.decryptor.Decrypt(ctx, wallet.EncryptedPrivateKey, cryptutil.ForCustodyWallet(wallet.Address))
	if err != nil {
		return fmt.Errorf("gasstation: descifrar llave de la wallet GAS")
	}
	defer func() { keyHex = "" }() // best-effort: see blockchainwithdrawal.EVMSigner's identical comment on Go string immutability

	gasFees, err := bw.ResolveGasFeeParams(ctx, s.chain)
	if err != nil {
		return fmt.Errorf("gasstation: obtener gas fee quote: %w", err)
	}

	var signed *evmwallet.SignedTransaction
	if gasFees.MaxFeePerGasWei != nil {
		signed, err = evmwallet.BuildAndSignNativeTransfer1559(keyHex, s.chain.ChainID(), nonce, target, refillRow.AmountWei, s.topUpGasLimit, gasFees.MaxFeePerGasWei, gasFees.MaxPriorityFeePerGasWei)
	} else {
		signed, err = evmwallet.BuildAndSignNativeTransfer(keyHex, s.chain.ChainID(), nonce, target, refillRow.AmountWei, s.topUpGasLimit, gasFees.GasPriceWei)
	}
	if err != nil {
		return fmt.Errorf("gasstation: firmar top-up: %w", err)
	}

	// AUDIT FIX: the decrypted key must belong to the GAS wallet address.
	if err := evmwallet.VerifySender(signed.RawTx, s.chain.ChainID(), wallet.Address); err != nil {
		return fmt.Errorf("gasstation: verificar remitente de la tx firmada: %w", err)
	}

	if err := s.nonces.MarkSigned(ctx, s.network, wallet.Address, owner, signed.TxHash); err != nil {
		return fmt.Errorf("gasstation: registrar nonce firmado: %w", err)
	}
	if err := s.refills.MarkBroadcasted(ctx, refillRow.ID, signed.TxHash); err != nil {
		return fmt.Errorf("gasstation: registrar refill firmado: %w", err)
	}
	refillRow.TxHash = signed.TxHash

	txHash, err := s.chain.Broadcast(ctx, signed.RawTx)
	if err != nil {
		switch classifyBroadcastError(err) {
		case broadcastAlreadyKnown:
			// The network already has this exact tx (e.g. a retry after an
			// earlier ambiguous send) — proceed to confirmation exactly as
			// if Broadcast had succeeded.
		case broadcastDefinitiveRejection:
			_ = s.refills.MarkFailed(ctx, refillRow.ID, err.Error())
			_ = s.nonces.Release(ctx, s.network, wallet.Address, owner)
			s.auditEvent(ctx, &wallet.ID, "gas_station_refill_failed", map[string]any{
				"network": s.network, "target": target, "amount_wei": refillRow.AmountWei.String(), "error": err.Error(),
			})
			return fmt.Errorf("gasstation: broadcast rechazado: %w", err)
		default:
			// AMBIGUOUS: never assumed as failure, never retried
			// automatically — same rule Bloque 6B applies to withdrawal
			// broadcasts. The row stays 'broadcasted' with tx_hash set so
			// the NEXT call with the same owner (or a future
			// reconciliation pass) resolves it instead of re-sending.
			s.auditEvent(ctx, &wallet.ID, "gas_station_refill_ambiguous", map[string]any{
				"network": s.network, "target": target, "tx_hash": signed.TxHash, "error": err.Error(),
			})
			return fmt.Errorf("%w: %v", ErrBroadcastAmbiguous, err)
		}
	} else {
		refillRow.TxHash = txHash
	}

	return s.waitForConfirmationAndClose(ctx, wallet, owner, refillRow)
}

// resumeBroadcasted handles a refill row that a PREVIOUS call already
// signed and broadcast (or attempted to) — it never re-signs or
// re-broadcasts; it only checks what actually happened on-chain.
func (s *Service) resumeBroadcasted(ctx context.Context, wallet *postgres.CustodyWallet, owner string, refillRow *postgres.GasStationRefill) error {
	receipt, err := s.chain.Receipt(ctx, refillRow.TxHash)
	if err != nil {
		return fmt.Errorf("gasstation: consultar receipt del refill en curso: %w", err)
	}
	if receipt != nil {
		if receipt.Status {
			_ = s.refills.MarkConfirmed(ctx, refillRow.ID)
			_ = s.nonces.MarkConfirmed(ctx, s.network, wallet.Address, owner)
			return nil
		}
		_ = s.refills.MarkFailed(ctx, refillRow.ID, "transacción revertida on-chain")
		s.auditEvent(ctx, &wallet.ID, "gas_station_refill_failed", map[string]any{
			"network": s.network, "target": refillRow.TargetAddress, "tx_hash": refillRow.TxHash, "reason": "reverted",
		})
		return fmt.Errorf("gasstation: el refill anterior (tx %s) revirtió on-chain", refillRow.TxHash)
	}
	known, err := s.chain.TxKnown(ctx, refillRow.TxHash)
	if err != nil {
		return fmt.Errorf("gasstation: verificar si el nodo conoce la tx: %w", err)
	}
	if known {
		// Still pending inclusion — wait the same bounded window a fresh
		// send would, rather than giving up immediately.
		return s.waitForConfirmationAndClose(ctx, wallet, owner, refillRow)
	}
	// Genuinely unknown to the node (dropped from mempool, or it never
	// actually reached it despite the ambiguous broadcast). Per spec, this
	// is NOT auto-retried: leave it for a human/reconciliation pass.
	return fmt.Errorf("%w: la tx previa %s ya no es conocida por el nodo", ErrBroadcastAmbiguous, refillRow.TxHash)
}

// waitForConfirmationAndClose polls for a receipt up to confirmMaxWait —
// EnsureGasFor's caller (blockchainwithdrawal.process.go) calls it
// synchronously and expects the target to actually be funded by the time
// it returns nil, exactly the contract the old bscwithdrawal.GasStation
// already had.
func (s *Service) waitForConfirmationAndClose(ctx context.Context, wallet *postgres.CustodyWallet, owner string, refillRow *postgres.GasStationRefill) error {
	deadline := s.now().Add(s.confirmMaxWait)
	for s.now().Before(deadline) {
		receipt, err := s.chain.Receipt(ctx, refillRow.TxHash)
		if err == nil && receipt != nil {
			if receipt.Status {
				_ = s.refills.MarkConfirmed(ctx, refillRow.ID)
				_ = s.nonces.MarkConfirmed(ctx, s.network, wallet.Address, owner)
				s.auditEvent(ctx, &wallet.ID, "gas_station_refill_confirmed", map[string]any{
					"network": s.network, "target": refillRow.TargetAddress,
					"amount_wei": refillRow.AmountWei.String(), "tx_hash": refillRow.TxHash,
				})
				return nil
			}
			_ = s.refills.MarkFailed(ctx, refillRow.ID, "transacción revertida on-chain")
			return fmt.Errorf("gasstation: el top-up (tx %s) revirtió on-chain", refillRow.TxHash)
		}
		if err := s.sleep(ctx, s.confirmPollEvery); err != nil {
			return fmt.Errorf("gasstation: esperando confirmación del top-up: %w", err)
		}
	}
	// Timed out waiting — leave the row 'broadcasted' (NOT failed: it may
	// still land) and report ambiguous, same posture as a broadcast error.
	return fmt.Errorf("%w: tiempo de espera agotado tras %s para tx %s", ErrBroadcastAmbiguous, s.confirmMaxWait, refillRow.TxHash)
}

// observeInFlight is reached when Create() reports that another concurrent
// call already owns a 'pending'/'broadcasted' refill for this exact
// target. It never sends anything itself — it only waits briefly to see
// whether the other refill lands, so a caller that can tolerate a short
// wait gets a clean "no action needed" instead of an error.
func (s *Service) observeInFlight(ctx context.Context, target string, requiredWei *big.Int) error {
	deadline := s.now().Add(s.confirmMaxWait)
	for s.now().Before(deadline) {
		inFlight, err := s.refills.FindInFlight(ctx, string(s.network), target)
		if err != nil {
			return fmt.Errorf("gasstation: observar refill en curso: %w", err)
		}
		if inFlight == nil {
			// It resolved (confirmed or failed) between our Create() and
			// now. Re-check the target's balance: if it's now sufficient,
			// we're done; otherwise the other attempt failed and we
			// should let the caller retry (which will Create() a fresh row).
			balance, berr := s.chain.NativeBalanceWei(ctx, target)
			if berr == nil && balance.Cmp(requiredWei) >= 0 {
				return nil
			}
			return ErrRefillInProgress
		}
		if err := s.sleep(ctx, s.confirmPollEvery); err != nil {
			return fmt.Errorf("gasstation: esperando refill concurrente: %w", err)
		}
	}
	return ErrRefillInProgress
}

func (s *Service) resolveNativeAsset(ctx context.Context) (domain.NetworkAsset, error) {
	assets, err := s.assets.ListEnabled(ctx, s.network)
	if err != nil {
		return domain.NetworkAsset{}, fmt.Errorf("gasstation: leer asset registry: %w", err)
	}
	for _, a := range assets {
		if a.Kind == domain.AssetKindNative {
			return a, nil
		}
	}
	return domain.NetworkAsset{}, ErrNoNativeAsset
}

func (s *Service) auditEvent(ctx context.Context, custodyWalletID *uuid.UUID, action string, metadata map[string]any) {
	if s.audit == nil {
		return
	}
	// Best-effort: never blocks or fails the refill (same posture
	// custody.Service already takes for its own audit writes). Never
	// includes private key material — only network/address/amount/tx_hash
	// style fields, matching spec's audit field list.
	_ = s.audit.LogCustodyWallet(ctx, custodyWalletID, action, "system:gasstation", metadata)
}

// toWei converts a human-readable policy amount to base units, treating an
// exact zero specially since blockchainwithdrawal.ToRawUnits (reused here
// for the actual conversion) rejects non-positive amounts by design (a
// withdrawal amount can never legitimately be zero) — a policy's
// minimum_balance, unlike a withdrawal amount, may legitimately be 0.
func toWei(amount decimal.Decimal, decimals int32) (*big.Int, error) {
	if amount.IsZero() {
		return big.NewInt(0), nil
	}
	return bw.ToRawUnits(amount, decimals)
}
