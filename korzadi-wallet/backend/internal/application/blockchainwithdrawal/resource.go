package blockchainwithdrawal

// TRON (and any chain priced in RESOURCES rather than gas) support for the
// withdrawal flow. The state machine — statuses, ledger reservations, limits,
// persistence of the signed tx BEFORE broadcasting, confirm/complete/settle,
// reorg handling — is the SAME code EVM uses; only the chain-specific pieces
// live here:
//
//   - no nonce: uniqueness comes from the persisted signed tx, and the nonce
//     column stores the TaPoS reference block number instead;
//   - a signed tx EXPIRES: once the chain time passes its expiration (plus a
//     safety margin) and the node does not know it, it can NEVER be mined, so
//     re-signing with a fresh reference block is safe. That replaces the EVM
//     "nonce still free" guard;
//   - cost = Energy + Bandwidth (staked, or TRX burned) instead of gas × price.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/big"
	"time"

	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/cryptutil"
	"korzadi-wallet/backend/internal/infrastructure/tronwallet"
)

// ErrTxExpired: a signed transaction expired without ever being mined.
var ErrTxExpired = errors.New("withdrawals: la transacción expiró sin ser minada")

// ResourceChain is the TRON counterpart of EVMChain (see tronclient.WithdrawalChain).
type ResourceChain interface {
	Network() domain.Network
	ConfirmationsRequired() uint64
	ExpirationWindow() time.Duration
	ExpirySafetyMargin() time.Duration
	TokenBalanceRaw(ctx context.Context, contract, address string) (*big.Int, error)
	NativeBalanceSun(ctx context.Context, address string) (*big.Int, error)
	Quote(ctx context.Context, from, contract, to string, amountRaw *big.Int) (*domain.ResourceQuote, error)
	RefBlock(ctx context.Context) (domain.ChainRef, error)
	HeadTimeMs(ctx context.Context) (int64, error)
	Broadcast(ctx context.Context, signedRawTx []byte) (string, error)
	Receipt(ctx context.Context, txHash string) (*domain.TransactionReceipt, error)
	TxKnown(ctx context.Context, txHash string) (bool, error)
}

// TronSignParams are the TRON-only inputs of a SignRequest.
type TronSignParams struct {
	RefBlockID   []byte // 32-byte blockID of the TaPoS reference block
	TimestampMs  int64  // chain time
	ExpirationMs int64
	FeeLimitSun  int64
}

// observer is the part of a chain the broadcast/confirm steps share.
type observer interface {
	Receipt(ctx context.Context, txHash string) (*domain.TransactionReceipt, error)
	TxKnown(ctx context.Context, txHash string) (bool, error)
	Broadcast(ctx context.Context, signedRawTx []byte) (string, error)
	ConfirmationsRequired() uint64
}

func (rt NetworkRuntime) configured() bool { return rt.Chain != nil || rt.Resource != nil }

func (rt NetworkRuntime) observer() observer {
	if rt.Resource != nil {
		return rt.Resource
	}
	return rt.Chain
}

// nonceTracked: only nonce-based (EVM) networks use the evm_nonces lease.
func (s *Service) nonceTracked(n domain.Network) bool { return s.Networks[n].Resource == nil }

func sunToTRX(sun int64) decimal.Decimal { return decimal.New(sun, -6) }

// ---- signing ----

// TronSigner signs TRC-20 transfers with the custody key (HOT or user), exactly
// like EVMSigner does for EVM: the key is resolved, decrypted (with its AAD),
// used and dropped INSIDE this call, and the result is verified field by field.
type TronSigner struct {
	keys KeyResolver
	dec  Decryptor
}

func NewTronSigner(keys KeyResolver, dec Decryptor) *TronSigner {
	return &TronSigner{keys: keys, dec: dec}
}

func (s *TronSigner) Sign(ctx context.Context, req SignRequest) (*SignedTx, error) {
	if err := validateTronSignRequest(req); err != nil {
		return nil, err
	}
	var (
		encrypted string
		aad       cryptutil.AAD
		err       error
	)
	if ks, ok := s.keys.(KeySourceResolver); ok {
		encrypted, aad, err = ks.FindEncryptedKeyWithAAD(ctx, req.Network, req.FromAddress, req.UserID)
	} else {
		encrypted, err = s.keys.FindEncryptedPrivateKey(ctx, req.Network, req.FromAddress, req.UserID)
		aad = cryptutil.ForCryptoAddress(req.FromAddress)
	}
	if err != nil {
		return nil, fmt.Errorf("signer: resolver llave de custodia: %w", err)
	}
	keyHex, err := s.dec.Decrypt(ctx, encrypted, aad)
	if err != nil {
		return nil, fmt.Errorf("signer: descifrar llave de custodia")
	}
	defer func() { keyHex = "" }()

	raw, err := tronwallet.BuildTRC20TransferRaw(tronwallet.TRC20TransferParams{
		Owner: req.FromAddress, Contract: req.ContractAddress, To: req.ToAddress, AmountRaw: req.AmountRaw,
		RefBlockID: req.Tron.RefBlockID, TimestampMs: req.Tron.TimestampMs,
		ExpirationMs: req.Tron.ExpirationMs, FeeLimitSun: req.Tron.FeeLimitSun,
	})
	if err != nil {
		return nil, fmt.Errorf("signer: construir tx TRON: %w", err)
	}
	txID, signed, err := tronwallet.SignRaw(keyHex, raw)
	if err != nil {
		return nil, fmt.Errorf("signer: firmar transacción: %w", err)
	}
	if err := verifySignedTron(req, signed); err != nil {
		return nil, err
	}
	return &SignedTx{Raw: signed, TxHash: txID}, nil
}

func validateTronSignRequest(req SignRequest) error {
	if req.Network.Family() != domain.FamilyTron {
		return fmt.Errorf("signer: red no TRON: %s", req.Network)
	}
	if req.AssetKind != domain.AssetKindToken {
		return fmt.Errorf("signer: TRON solo soporta retiros de tokens TRC-20")
	}
	if req.AmountRaw == nil || req.AmountRaw.Sign() <= 0 {
		return fmt.Errorf("signer: monto inválido")
	}
	if req.Tron == nil {
		return fmt.Errorf("signer: faltan los parámetros TRON (bloque de referencia/expiración)")
	}
	for _, a := range []string{req.FromAddress, req.ToAddress, req.ContractAddress} {
		if tronwallet.ValidateAddress(a) != nil {
			return fmt.Errorf("signer: dirección TRON inválida")
		}
	}
	return nil
}

// verifySignedTron decodes what we just signed and checks it matches the
// request exactly — including that the recovered signer IS the source address.
func verifySignedTron(req SignRequest, signed []byte) error {
	p, err := tronwallet.ParseSignedTx(signed)
	if err != nil {
		return fmt.Errorf("signer: tx firmada ilegible: %w", err)
	}
	if !p.IsTransfer || p.CallValue != 0 {
		return fmt.Errorf("signer: la tx firmada no es un transfer TRC-20 puro")
	}
	if p.Owner != req.FromAddress || p.Contract != req.ContractAddress || p.To != req.ToAddress ||
		p.AmountRaw == nil || p.AmountRaw.Cmp(req.AmountRaw) != 0 {
		return fmt.Errorf("signer: la tx firmada no coincide con lo solicitado")
	}
	if p.ExpirationMs != req.Tron.ExpirationMs || p.TimestampMs != req.Tron.TimestampMs || p.FeeLimitSun != req.Tron.FeeLimitSun {
		return fmt.Errorf("signer: la tx firmada no coincide (tiempos/fee_limit)")
	}
	signer, err := p.RecoverSigner()
	if err != nil || signer != req.FromAddress {
		return fmt.Errorf("signer: el firmante de la tx no es la dirección de origen")
	}
	return nil
}

// FamilySigner routes a SignRequest to the signer of its network family.
type FamilySigner struct {
	EVM  Signer
	Tron Signer
}

func (f FamilySigner) Sign(ctx context.Context, req SignRequest) (*SignedTx, error) {
	switch req.Network.Family() {
	case domain.FamilyEVM:
		if f.EVM != nil {
			return f.EVM.Sign(ctx, req)
		}
	case domain.FamilyTron:
		if f.Tron != nil {
			return f.Tron.Sign(ctx, req)
		}
	}
	return nil, fmt.Errorf("%w: %s", ErrNoSigner, req.Network)
}

// ---- prepare (replaces the gas/nonce half of prepareAndSign) ----

func (s *Service) prepareResourceTx(ctx context.Context, rt NetworkRuntime, w *Withdrawal,
	netAsset domain.NetworkAsset, raw *big.Int, contract string, committedRaw *big.Int, from string) (*Withdrawal, error) {

	rc := rt.Resource
	if netAsset.Kind != domain.AssetKindToken {
		return s.failWithRelease(ctx, w, "asset_disabled", ErrAssetNotEnabled, false)
	}

	tokenBalance, err := rc.TokenBalanceRaw(ctx, contract, from)
	if err != nil {
		return w, fmt.Errorf("balance del token: %w", err)
	}
	if new(big.Int).Sub(tokenBalance, committedRaw).Cmp(raw) < 0 {
		return s.failWithRelease(ctx, w, "insufficient_onchain_funds", ErrInsufficientOnChain, false)
	}

	quote, err := rc.Quote(ctx, from, contract, w.ToAddress, raw)
	if err != nil {
		if errors.Is(err, domain.ErrResourceFeeLimitExceeded) {
			return s.failWithRelease(ctx, w, "network_fee_overrun", ErrNetworkFeeOverrun, false)
		}
		if isRevertError(err) {
			return s.failWithRelease(ctx, w, "estimate_reverted", err, false)
		}
		return w, fmt.Errorf("estimar recursos: %w", err)
	}

	// Do not silently absorb a price spike beyond what the user was quoted.
	if w.EstimatedNetworkFeeNative.IsPositive() {
		factor := decimal.NewFromInt(10000 + s.Config.MaxNetworkFeeOverrunBps)
		ceiling := w.EstimatedNetworkFeeNative.Mul(factor).Div(decimal.NewFromInt(10000))
		if sunToTRX(quote.WorstCaseSun).GreaterThan(ceiling) {
			return s.failWithRelease(ctx, w, "network_fee_overrun", ErrNetworkFeeOverrun, false)
		}
	}

	// Energy/Bandwidth: whatever the HOT's staked resources do not cover is
	// BURNED from its TRX balance. If it lacks TRX, the gas station tops it up
	// (within the GAS wallet's policy limits) — never more than the shortfall.
	trx, err := rc.NativeBalanceSun(ctx, from)
	if err != nil {
		return w, fmt.Errorf("balance TRX: %w", err)
	}
	if trx.Cmp(big.NewInt(quote.BurnSun)) < 0 {
		if rt.Gas == nil {
			return s.failWithRelease(ctx, w, "gas_unavailable", ErrGasUnavailable, false)
		}
		if err := rt.Gas.EnsureGasFor(ctx, gasOwner(w.ID), from, big.NewInt(quote.BurnSun)); err != nil {
			return w, fmt.Errorf("gas station: %w", err)
		}
	}

	ref, err := rc.RefBlock(ctx)
	if err != nil {
		return w, fmt.Errorf("bloque de referencia: %w", err)
	}
	expiration := ref.HeadTimestampMs + rc.ExpirationWindow().Milliseconds()

	signed, err := s.Signer.Sign(ctx, SignRequest{
		Network: w.Network, UserID: w.UserID, FromAddress: from, ToAddress: w.ToAddress,
		AssetKind: netAsset.Kind, ContractAddress: contract, AmountRaw: raw,
		Tron: &TronSignParams{RefBlockID: ref.ID, TimestampMs: ref.HeadTimestampMs, ExpirationMs: expiration, FeeLimitSun: quote.FeeLimitSun},
	})
	if err != nil {
		return w, fmt.Errorf("firmar: %w", err)
	}

	refNum, energy := int64(ref.Number), quote.EnergyRequired
	energyPrice := decimal.NewFromInt(quote.EnergyPriceSun)
	hash := signed.TxHash
	applied, err := s.Store.Apply(ctx, w.ID, StatusProcessing, StatusProcessing,
		Update{
			Nonce: &refNum, GasLimit: &energy, GasPriceWei: &energyPrice,
			SignedRawTx: signed.Raw, TxHash: &hash, MarkSigned: true, RequireUnsigned: true,
		},
		Event{Type: "signed", Detail: map[string]any{
			"tx_hash": hash, "ref_block": ref.Number, "expiration_ms": expiration,
			"energy": quote.EnergyRequired, "burn_sun": quote.BurnSun, "fee_limit_sun": quote.FeeLimitSun,
		}})
	if err != nil {
		return w, fmt.Errorf("persistir tx firmada: %w", err)
	}
	if !applied {
		return s.refresh(ctx, w)
	}
	log.Printf("withdrawal: firmado (TRON) id=%s asset=%s ref_block=%d tx=%s", w.ID, w.AssetCode, ref.Number, hash)
	return s.refresh(ctx, w)
}

// ---- broadcast / missing-receipt (replace the nonce guards) ----

// resourceTxExpired: chain time is past the tx expiration + safety margin.
func (s *Service) resourceTxExpired(ctx context.Context, rt NetworkRuntime, w *Withdrawal) (bool, error) {
	parsed, err := tronwallet.ParseSignedTx(w.SignedRawTx)
	if err != nil {
		return false, fmt.Errorf("tx firmada ilegible: %w", err)
	}
	head, err := rt.Resource.HeadTimeMs(ctx)
	if err != nil {
		return false, fmt.Errorf("hora de la cadena: %w", err)
	}
	return head > parsed.ExpirationMs+rt.Resource.ExpirySafetyMargin().Milliseconds(), nil
}

// resignExpired discards a signed tx that can never be mined and goes back to
// signing a fresh one (new reference block => new txid). Callers MUST have
// verified, in this tick, that there is no receipt and the node does not know it.
func (s *Service) resignExpired(ctx context.Context, w *Withdrawal, why string) (time.Duration, error) {
	if w.Attempts+1 >= s.Config.MaxPrepareTries {
		_, ferr := s.failWithRelease(ctx, w, "tx_expired", ErrTxExpired, false)
		return 0, ferr
	}
	applied, err := s.Store.Apply(ctx, w.ID, w.Status, StatusProcessing, Update{ClearSigned: true},
		Event{Type: "expired_resign", Detail: map[string]any{"old_tx_hash": w.TxHash, "reason": why}})
	if err != nil {
		return 0, err
	}
	if applied {
		log.Printf("withdrawal: tx TRON expirada sin minar, se firmará de nuevo id=%s old_tx=%s", w.ID, w.TxHash)
	}
	return 0, nil
}

func (s *Service) broadcastResourceStep(ctx context.Context, rt NetworkRuntime, w *Withdrawal) (time.Duration, error) {
	rc := rt.Resource
	if w.TxHash == "" || len(w.SignedRawTx) == 0 {
		if _, err := s.Store.Apply(ctx, w.ID, StatusProcessing, StatusProcessing, Update{ClearSigned: true}, Event{Type: "reset_signed_state"}); err != nil {
			return 0, err
		}
		return 0, nil
	}
	receipt, err := rc.Receipt(ctx, w.TxHash)
	if err != nil {
		return 0, fmt.Errorf("consultar receipt: %w", err)
	}
	if receipt != nil {
		return s.markBroadcasted(ctx, w, "found_on_chain")
	}
	known, err := rc.TxKnown(ctx, w.TxHash)
	if err != nil {
		return 0, fmt.Errorf("consultar tx en la red: %w", err)
	}
	if known {
		return s.markBroadcasted(ctx, w, "known_by_node")
	}
	expired, err := s.resourceTxExpired(ctx, rt, w)
	if err != nil {
		return 0, err
	}
	if expired {
		return s.resignExpired(ctx, w, "expired_before_broadcast")
	}
	sentHash, err := rc.Broadcast(ctx, w.SignedRawTx)
	return s.finishBroadcast(ctx, w, sentHash, err)
}

// resourceMissing handles a broadcasted tx whose receipt is not visible.
func (s *Service) resourceMissing(ctx context.Context, rt NetworkRuntime, w *Withdrawal) (time.Duration, error) {
	if len(w.SignedRawTx) == 0 {
		return confirmPollDelay, nil
	}
	expired, err := s.resourceTxExpired(ctx, rt, w)
	if err != nil {
		return 0, err
	}
	if expired {
		return s.resignExpired(ctx, w, "expired_unseen")
	}
	// Not expired yet and unknown: re-send the SAME bytes (idempotent: same txid).
	_ = s.Store.AddEvent(ctx, w.ID, "rebroadcast", w.Status, w.Status, map[string]any{"tx_hash": w.TxHash})
	if _, berr := rt.Resource.Broadcast(ctx, w.SignedRawTx); berr != nil && classifyBroadcastError(berr) != broadcastAlreadyKnown {
		return 0, fmt.Errorf("rebroadcast: %w", berr)
	}
	return confirmPollDelay, nil
}
