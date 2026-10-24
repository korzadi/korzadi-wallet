package gasstation

// TronService is the TRON counterpart of Service: the Gas Station funds a HOT
// wallet with TRX so it can BURN what its staked Energy/Bandwidth do not cover
// (a TRC-20 transfer costs Energy + Bandwidth, paid in TRX when not staked).
//
// It REUSES everything chain-agnostic: the GAS wallet registry, the refill
// policy (minimum/target/max_refill — amounts in TRX, 6 decimals), planRefill
// (a refill never exceeds max_refill nor pushes past target), the
// gas_station_refills table (one in-flight refill per target, idempotent by
// owner), toWei and the audit trail. Only the chain parts differ: no nonce,
// the transfer is a native TRX TransferContract, and a tx EXPIRES.
//
// Amounts passed to EnsureGasFor, and stored in refills.amount_wei, are in SUN
// (TRON's base unit); the column name is historical.
//
// NOT implemented (BLOQUEADO / decisión de política + nodo real): delegating
// staked Energy/Bandwidth (DelegateResourceContract) from the GAS wallet instead of
// sending TRX to burn.

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/application/custody"
	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/cryptutil"
	"korzadi-wallet/backend/internal/infrastructure/postgres"
	"korzadi-wallet/backend/internal/infrastructure/tronwallet"
)

// TronChain is the slice of tronclient.WithdrawalChain this service needs.
type TronChain interface {
	NativeBalanceSun(ctx context.Context, address string) (*big.Int, error)
	RefBlock(ctx context.Context) (domain.ChainRef, error)
	ExpirationWindow() time.Duration
	ExpirySafetyMargin() time.Duration
	Broadcast(ctx context.Context, signedRawTx []byte) (string, error)
	NativeReceipt(ctx context.Context, txHash string) (*domain.TransactionReceipt, error)
	TxKnown(ctx context.Context, txHash string) (bool, error)
}

type TronDeps struct {
	Network   domain.Network
	Wallets   WalletRegistry
	Policies  PolicyStore
	Refills   RefillStore
	Assets    domain.AssetRegistry
	Chain     TronChain
	Decryptor Decryptor
	Audit     AuditLogger

	ConfirmPollEvery time.Duration // default 3s
	ConfirmMaxWait   time.Duration // default 90s
	Now              func() time.Time
	Sleep            func(ctx context.Context, d time.Duration) error
}

type TronService struct{ TronDeps }

func NewTronService(d TronDeps) (*TronService, error) {
	if d.Wallets == nil || d.Policies == nil || d.Refills == nil || d.Assets == nil || d.Chain == nil || d.Decryptor == nil {
		return nil, errors.New("gasstation: dependencias TRON incompletas")
	}
	if d.Network.Family() != domain.FamilyTron {
		return nil, fmt.Errorf("gasstation: %s no es una red TRON", d.Network)
	}
	if d.ConfirmPollEvery <= 0 {
		d.ConfirmPollEvery = 3 * time.Second
	}
	if d.ConfirmMaxWait <= 0 {
		d.ConfirmMaxWait = 90 * time.Second
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Sleep == nil {
		d.Sleep = func(ctx context.Context, dur time.Duration) error {
			select {
			case <-time.After(dur):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return &TronService{TronDeps: d}, nil
}

// EnsureGasFor satisfies blockchainwithdrawal.GasTopUp. requiredSun is the TRX
// (in sun) the target must hold to burn for its transaction.
func (s *TronService) EnsureGasFor(ctx context.Context, owner, targetAddress string, requiredSun *big.Int) error {
	if strings.TrimSpace(owner) == "" {
		return fmt.Errorf("%w: owner requerido", ErrInvalidRequest)
	}
	if requiredSun == nil || requiredSun.Sign() <= 0 {
		return fmt.Errorf("%w: monto requerido inválido", ErrInvalidRequest)
	}
	if tronwallet.ValidateAddress(targetAddress) != nil {
		return fmt.Errorf("%w: dirección destino inválida", ErrInvalidRequest)
	}
	target := targetAddress

	nativeAsset, err := s.nativeAsset(ctx)
	if err != nil {
		return err
	}
	wallet, err := s.Wallets.FindActive(ctx, string(custody.WalletTypeGas), string(s.Network), nativeAsset.AssetCode)
	if err != nil {
		return fmt.Errorf("gasstation: resolver wallet GAS: %w", err)
	}
	if wallet == nil {
		return ErrNoGasWallet
	}
	policy, err := s.Policies.Get(ctx, wallet.ID)
	if err != nil {
		return fmt.Errorf("gasstation: leer política: %w", err)
	}
	if policy == nil || !policy.Enabled {
		return ErrDisabled
	}

	current, err := s.Chain.NativeBalanceSun(ctx, target)
	if err != nil {
		return fmt.Errorf("gasstation: consultar balance TRX de %s: %w", target, err)
	}
	minimum, err := toWei(policy.MinimumBalance, nativeAsset.Decimals)
	if err != nil {
		return fmt.Errorf("gasstation: minimum_balance de política inválida: %w", err)
	}
	if current.Cmp(minimum) >= 0 && current.Cmp(requiredSun) >= 0 {
		return nil // genuinely not needed
	}

	owned, err := s.Refills.FindOwned(ctx, string(s.Network), target, owner)
	if err != nil {
		return fmt.Errorf("gasstation: buscar refill propio: %w", err)
	}
	if owned != nil {
		switch postgres.RefillStatus(owned.Status) {
		case postgres.RefillStatusConfirmed:
			return nil
		case postgres.RefillStatusBroadcasted:
			return s.resume(ctx, wallet, owned)
		case postgres.RefillStatusPending:
			return s.signAndSend(ctx, wallet, target, owned)
		}
	}

	targetBal, err := toWei(policy.TargetBalance, nativeAsset.Decimals)
	if err != nil {
		return fmt.Errorf("gasstation: target_balance de política inválida: %w", err)
	}
	maxRefill, err := toWei(policy.MaxRefill, nativeAsset.Decimals)
	if err != nil {
		return fmt.Errorf("gasstation: max_refill de política inválida: %w", err)
	}
	plan, err := planRefill(current, requiredSun, targetBal, maxRefill)
	if err != nil {
		s.audit(ctx, &wallet.ID, "gas_station_refill_rejected", map[string]any{"network": s.Network, "target": target, "reason": err.Error()})
		return err
	}
	created, err := s.Refills.Create(ctx, postgres.GasStationRefill{
		CustodyWalletID: wallet.ID, Network: string(s.Network), TargetAddress: target, OwnerRef: owner, AmountWei: plan.amountWei,
	})
	if err != nil {
		if errors.Is(err, postgres.ErrRefillInFlight) {
			return s.observeInFlight(ctx, target, requiredSun)
		}
		return fmt.Errorf("gasstation: crear refill: %w", err)
	}
	return s.signAndSend(ctx, wallet, target, created)
}

func (s *TronService) signAndSend(ctx context.Context, wallet *postgres.CustodyWallet, target string, row *postgres.GasStationRefill) error {
	if row.AmountWei == nil || !row.AmountWei.IsInt64() || row.AmountWei.Sign() <= 0 {
		return fmt.Errorf("%w: monto de refill fuera de rango", ErrInvalidRequest)
	}
	ref, err := s.Chain.RefBlock(ctx)
	if err != nil {
		return fmt.Errorf("gasstation: bloque de referencia: %w", err)
	}
	raw, err := tronwallet.BuildTRXTransferRaw(tronwallet.TRXTransferParams{
		Owner: wallet.Address, To: target, AmountSun: row.AmountWei.Int64(), RefBlockID: ref.ID,
		TimestampMs: ref.HeadTimestampMs, ExpirationMs: ref.HeadTimestampMs + s.Chain.ExpirationWindow().Milliseconds(),
	})
	if err != nil {
		return fmt.Errorf("gasstation: construir top-up: %w", err)
	}
	keyHex, err := s.Decryptor.Decrypt(ctx, wallet.EncryptedPrivateKey, cryptutil.ForCustodyWallet(wallet.Address))
	if err != nil {
		return fmt.Errorf("gasstation: descifrar llave de la wallet GAS")
	}
	defer func() { keyHex = "" }()
	txID, signed, err := tronwallet.SignRaw(keyHex, raw)
	if err != nil {
		return fmt.Errorf("gasstation: firmar top-up: %w", err)
	}
	// The decrypted key MUST belong to the GAS wallet address, and the tx must
	// be exactly the top-up we planned.
	parsed, err := tronwallet.ParseSignedTx(signed)
	if err != nil || !parsed.IsTRXTransfer || parsed.To != target || parsed.TRXAmountSun != row.AmountWei.Int64() {
		return errors.New("gasstation: la tx firmada no coincide con el top-up planeado")
	}
	if signer, serr := parsed.RecoverSigner(); serr != nil || signer != wallet.Address {
		return errors.New("gasstation: el firmante de la tx no es la wallet GAS")
	}

	// Persist the hash BEFORE sending, so an ambiguous broadcast is reconciled
	// (never blindly re-sent).
	if err := s.Refills.MarkBroadcasted(ctx, row.ID, txID); err != nil {
		return fmt.Errorf("gasstation: registrar refill firmado: %w", err)
	}
	row.TxHash = txID
	if _, err := s.Chain.Broadcast(ctx, signed); err != nil {
		switch classifyTronBroadcast(err) {
		case tronKnown:
			// already in the node: continue to confirmation
		case tronRejected:
			_ = s.Refills.MarkFailed(ctx, row.ID, "rechazado por el nodo")
			s.audit(ctx, &wallet.ID, "gas_station_refill_failed", map[string]any{"network": s.Network, "target": target, "amount_sun": row.AmountWei.String()})
			return fmt.Errorf("gasstation: broadcast rechazado: %w", err)
		default:
			s.audit(ctx, &wallet.ID, "gas_station_refill_ambiguous", map[string]any{"network": s.Network, "target": target, "tx_hash": txID})
			return fmt.Errorf("%w: %v", ErrBroadcastAmbiguous, err)
		}
	}
	return s.waitAndClose(ctx, wallet, row)
}

// resume reconciles a refill a previous call already signed/broadcast. A TRON
// tx that is unknown AND past its expiration can never be mined, so — unlike EVM —
// it can be closed as failed and a fresh refill planned on the next call.
func (s *TronService) resume(ctx context.Context, wallet *postgres.CustodyWallet, row *postgres.GasStationRefill) error {
	receipt, err := s.Chain.NativeReceipt(ctx, row.TxHash)
	if err != nil {
		return fmt.Errorf("gasstation: consultar receipt del refill en curso: %w", err)
	}
	if receipt != nil {
		if receipt.Status {
			_ = s.Refills.MarkConfirmed(ctx, row.ID)
			return nil
		}
		_ = s.Refills.MarkFailed(ctx, row.ID, "transacción fallida on-chain")
		return fmt.Errorf("gasstation: el refill anterior (tx %s) falló on-chain", row.TxHash)
	}
	known, err := s.Chain.TxKnown(ctx, row.TxHash)
	if err != nil {
		return fmt.Errorf("gasstation: verificar si el nodo conoce la tx: %w", err)
	}
	if known {
		return s.waitAndClose(ctx, wallet, row)
	}
	// A zero UpdatedAt means "age unknown": stay conservative (never declare it dead).
	if !row.UpdatedAt.IsZero() && s.Now().Sub(row.UpdatedAt) > s.Chain.ExpirationWindow()+s.Chain.ExpirySafetyMargin() {
		_ = s.Refills.MarkFailed(ctx, row.ID, "expirada sin minar")
		return fmt.Errorf("gasstation: el top-up previo expiró sin minarse; se planeará uno nuevo (reintenta)")
	}
	return fmt.Errorf("%w: la tx previa %s aún no es conocida por el nodo", ErrBroadcastAmbiguous, row.TxHash)
}

func (s *TronService) waitAndClose(ctx context.Context, wallet *postgres.CustodyWallet, row *postgres.GasStationRefill) error {
	deadline := s.Now().Add(s.ConfirmMaxWait)
	for s.Now().Before(deadline) {
		receipt, err := s.Chain.NativeReceipt(ctx, row.TxHash)
		if err == nil && receipt != nil {
			if receipt.Status {
				_ = s.Refills.MarkConfirmed(ctx, row.ID)
				s.audit(ctx, &wallet.ID, "gas_station_refill_confirmed", map[string]any{
					"network": s.Network, "target": row.TargetAddress, "amount_sun": row.AmountWei.String(), "tx_hash": row.TxHash})
				return nil
			}
			_ = s.Refills.MarkFailed(ctx, row.ID, "transacción fallida on-chain")
			return fmt.Errorf("gasstation: el top-up (tx %s) falló on-chain", row.TxHash)
		}
		if err := s.Sleep(ctx, s.ConfirmPollEvery); err != nil {
			return fmt.Errorf("gasstation: esperando confirmación del top-up: %w", err)
		}
	}
	return fmt.Errorf("%w: tiempo de espera agotado tras %s para tx %s", ErrBroadcastAmbiguous, s.ConfirmMaxWait, row.TxHash)
}

func (s *TronService) observeInFlight(ctx context.Context, target string, required *big.Int) error {
	deadline := s.Now().Add(s.ConfirmMaxWait)
	for s.Now().Before(deadline) {
		inFlight, err := s.Refills.FindInFlight(ctx, string(s.Network), target)
		if err != nil {
			return fmt.Errorf("gasstation: observar refill en curso: %w", err)
		}
		if inFlight == nil {
			if bal, berr := s.Chain.NativeBalanceSun(ctx, target); berr == nil && bal.Cmp(required) >= 0 {
				return nil
			}
			return ErrRefillInProgress
		}
		if err := s.Sleep(ctx, s.ConfirmPollEvery); err != nil {
			return fmt.Errorf("gasstation: esperando refill concurrente: %w", err)
		}
	}
	return ErrRefillInProgress
}

func (s *TronService) nativeAsset(ctx context.Context) (domain.NetworkAsset, error) {
	assets, err := s.Assets.ListEnabled(ctx, s.Network)
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

func (s *TronService) audit(ctx context.Context, walletID *uuid.UUID, action string, md map[string]any) {
	if s.Audit == nil {
		return
	}
	_ = s.Audit.LogCustodyWallet(ctx, walletID, action, "system:gasstation", md)
}

type tronBroadcastClass int

const (
	tronAmbiguous tronBroadcastClass = iota
	tronKnown
	tronRejected
)

// classifyTronBroadcast: ONLY answers that prove the node refused the tx are
// definitive; timeouts and anything unrecognized stay ambiguous.
func classifyTronBroadcast(err error) tronBroadcastClass {
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
	case has("dup_transaction_error"):
		return tronKnown
	case has("sigerror", "contract_validate_error", "bandwith_error", "tapos_error", "transaction_expiration_error", "too_big_transaction_error"):
		return tronRejected
	default:
		return tronAmbiguous
	}
}
