package fee

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
)

// QuoteStore is the persistence port for FeeQuote — implemented by
// postgres.FeeQuoteRepository. A quote must survive across requests (the
// future withdrawal service looks it up again to validate and reserve
// against it — spec §17-18), so it lives in Postgres, not in memory.
type QuoteStore interface {
	Save(ctx context.Context, q *FeeQuote) error
	FindByID(ctx context.Context, id uuid.UUID) (*FeeQuote, error)
	// SetReservationID atomically claims a reservation for a quote —
	// succeeds (claimed=true) only if the quote had no reservation yet.
	// This is the sole idempotency guard for ReserveFee (spec §22): two
	// concurrent reservation attempts for the same quote_id can only ever
	// have one winner here.
	SetReservationID(ctx context.Context, quoteID, reservationID uuid.UUID) (claimed bool, err error)
	UpdateStatus(ctx context.Context, quoteID uuid.UUID, status QuoteStatus) error
}

// AssetSupportChecker lets the engine confirm the operation asset is real
// and enabled before spending any effort estimating a fee for it — spec
// §5 by way of Bloque 1's asset registry (network_assets), the same
// source of truth the blockchain scanners already use. Kept as a narrow
// interface so this package doesn't import the concrete
// postgres.NetworkAssetRepository type directly.
type AssetSupportChecker interface {
	Get(ctx context.Context, network domain.Network, assetCode string) (asset domain.NetworkAsset, found bool, err error)
}

// Engine is the Universal Fee Engine's single entrypoint (spec §25).
type Engine struct {
	cfg              Config
	serviceFee       ServiceFeePolicy
	networkFees      []NetworkFeeEstimator // tried in order; first that SupportsNetwork wins
	prices           PriceProvider
	assets           AssetSupportChecker
	quotes           QuoteStore
	feeAssetCode     string
	feeAssetDecimals int32
}

func NewEngine(
	cfg Config,
	serviceFee ServiceFeePolicy,
	networkFees []NetworkFeeEstimator,
	prices PriceProvider,
	assets AssetSupportChecker,
	quotes QuoteStore,
	feeAssetCode string,
	feeAssetDecimals int32,
) *Engine {
	return &Engine{
		cfg: cfg, serviceFee: serviceFee, networkFees: networkFees, prices: prices,
		assets: assets, quotes: quotes, feeAssetCode: feeAssetCode, feeAssetDecimals: feeAssetDecimals,
	}
}

// EstimateFeeInput is everything a caller supplies to request a quote —
// spec §25: "operation, network, asset, amount". UserID always comes from
// the authenticated caller (the HTTP handler), never from this struct's
// own construction by request-body binding directly.
type EstimateFeeInput struct {
	UserID             uuid.UUID
	OperationType      OperationType
	Network            domain.Network
	OperationAssetCode string
	// OperationAmount is optional — needed only if ServiceFeePolicy has a
	// percentage component. A zero value means "not supplied".
	OperationAmount decimal.Decimal
}

// EstimateFee computes and persists a new FeeQuote (spec §25).
func (e *Engine) EstimateFee(ctx context.Context, in EstimateFeeInput) (*FeeQuote, error) {
	if in.UserID == uuid.Nil {
		return nil, fmt.Errorf("fee: usuario requerido")
	}
	if in.Network == "" {
		return nil, fmt.Errorf("%w: red requerida", ErrUnsupportedNetwork)
	}
	if in.OperationAssetCode == "" {
		return nil, fmt.Errorf("%w: asset requerido", ErrUnsupportedAsset)
	}
	if !in.OperationAmount.IsZero() && in.OperationAmount.IsNegative() {
		return nil, fmt.Errorf("%w: el monto de la operación no puede ser negativo", ErrInvalidAmount)
	}

	// Validate the OPERATION asset against the real registry (spec §5) —
	// never assume a client-supplied symbol exists. This deliberately
	// checks Bloque 1's network_assets (network + asset identity), not
	// just a bare symbol, since spec §3 is explicit that "USDT Ethereum"
	// and "USDT BSC" are never interchangeable.
	if e.assets != nil {
		asset, found, err := e.assets.Get(ctx, in.Network, in.OperationAssetCode)
		if err != nil {
			return nil, fmt.Errorf("validar asset de la operación: %w", err)
		}
		if !found || !asset.Enabled {
			return nil, fmt.Errorf("%w: %s en %s", ErrUnsupportedAsset, in.OperationAssetCode, in.Network)
		}
	}

	estimator := e.estimatorFor(in.Network)
	if estimator == nil {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedNetwork, in.Network)
	}

	networkEstimate, err := estimator.EstimateNetworkFee(ctx, in.Network)
	if err != nil {
		return nil, err
	}

	price, err := e.prices.GetPrice(ctx, networkEstimate.NativeAssetCode, e.feeAssetCode)
	if err != nil {
		return nil, err
	}
	age := time.Since(price.AsOf)
	if age > e.cfg.MaxPriceAge {
		return nil, fmt.Errorf("%w: precio de %s tiene %s de antigüedad (máximo permitido %s)",
			ErrPriceStale, networkEstimate.NativeAssetCode, age.Round(time.Second), e.cfg.MaxPriceAge)
	}

	// native amount -> USDT, then the volatility buffer on top (spec §11):
	// protected = raw × (1 + buffer). Applied BEFORE limits/rounding so a
	// buffered-but-still-too-high fee is still correctly rejected by the
	// max check below, and a buffered-but-still-too-low fee is still
	// correctly raised to the configured minimum.
	rawNetworkFeeUSDT := networkEstimate.NativeAmount.Mul(price.Value)
	bufferMultiplier := decimal.NewFromInt(10000 + e.cfg.VolatilityBufferBps).Div(decimal.NewFromInt(10000))
	protectedNetworkFeeUSDT := rawNetworkFeeUSDT.Mul(bufferMultiplier)

	networkFeeUSDT := protectedNetworkFeeUSDT
	if networkFeeUSDT.GreaterThan(e.cfg.NetworkFeeMax) {
		// A network fee this high is a sign the RPC/provider returned
		// something abnormal (spec §15) — rejected outright rather than
		// silently capped, since capping here would mean Korzadi absorbs
		// an unbounded real cost while telling the user a bounded, wrong
		// number.
		return nil, fmt.Errorf("%w: %s %s excede el máximo de %s %s", ErrFeeTooHigh,
			networkFeeUSDT.String(), e.feeAssetCode, e.cfg.NetworkFeeMax.String(), e.feeAssetCode)
	}

	serviceFeeUSDT := e.serviceFee.ComputeServiceFee(in.OperationAmount, in.OperationAssetCode)

	// Round UP, never down (spec §16: "nunca redondear de forma que
	// pueda provocar cobrar menos que el costo protegido"), and round
	// EACH component independently before summing — not the total after
	// summing — so TotalFee always reconstructs exactly as
	// ServiceFee + NetworkFee (spec §1, §23) even once each is stored in
	// a fixed-scale NUMERIC column.
	serviceFeeUSDT = serviceFeeUSDT.RoundUp(e.feeAssetDecimals)
	networkFeeUSDT = networkFeeUSDT.RoundUp(e.feeAssetDecimals)
	totalFeeUSDT := serviceFeeUSDT.Add(networkFeeUSDT)

	if totalFeeUSDT.GreaterThan(e.cfg.TotalFeeMax) {
		return nil, fmt.Errorf("%w: total %s %s excede el máximo de %s %s", ErrFeeTooHigh,
			totalFeeUSDT.String(), e.feeAssetCode, e.cfg.TotalFeeMax.String(), e.feeAssetCode)
	}
	if totalFeeUSDT.LessThan(e.cfg.TotalFeeMin) {
		// Raise ServiceFee by the shortfall rather than overwriting
		// TotalFee directly — NetworkFee reflects a real, already-measured
		// blockchain cost and must never be inflated to make up a minimum;
		// any top-up belongs to Korzadi's own charge.
		shortfall := e.cfg.TotalFeeMin.Sub(totalFeeUSDT)
		serviceFeeUSDT = serviceFeeUSDT.Add(shortfall).RoundUp(e.feeAssetDecimals)
		totalFeeUSDT = serviceFeeUSDT.Add(networkFeeUSDT)
	}

	now := time.Now().UTC()
	quote := &FeeQuote{
		ID:                  uuid.New(),
		UserID:              in.UserID,
		OperationType:       in.OperationType,
		Network:             in.Network,
		OperationAssetCode:  in.OperationAssetCode,
		FeeAssetCode:        e.feeAssetCode,
		ServiceFee:          serviceFeeUSDT,
		NetworkFee:          networkFeeUSDT,
		TotalFee:            totalFeeUSDT,
		NetworkFeeNative:    networkEstimate.NativeAmount,
		NativeAssetCode:     networkEstimate.NativeAssetCode,
		ExchangeRate:        price.Value,
		PriceSource:         price.Source,
		PriceAsOf:           price.AsOf,
		VolatilityBufferBps: int32(e.cfg.VolatilityBufferBps),
		Status:              QuoteStatusActive,
		CreatedAt:           now,
		ExpiresAt:           now.Add(e.cfg.QuoteTTL),
	}

	if err := e.quotes.Save(ctx, quote); err != nil {
		return nil, fmt.Errorf("guardar cotización: %w", err)
	}

	return quote, nil
}

// GetQuote fetches a previously issued quote, scoped to userID — used by
// the future withdrawal service (and by anyone auditing their own
// quotes) to re-check validity before acting on it (spec §18). Never
// returns a quote belonging to a different user (mirrors the ownership
// pattern from Bloques 4-5).
func (e *Engine) GetQuote(ctx context.Context, quoteID, userID uuid.UUID) (*FeeQuote, error) {
	q, err := e.quotes.FindByID(ctx, quoteID)
	if err != nil {
		return nil, fmt.Errorf("buscar cotización: %w", err)
	}
	if q == nil || q.UserID != userID {
		return nil, ErrQuoteNotFound
	}
	return q, nil
}

// ValidateQuote checks a quote is still active and unexpired — spec §18,
// the exact check the future withdrawal service must perform before
// reserving/consuming it.
func (e *Engine) ValidateQuote(quote *FeeQuote, now time.Time) error {
	if quote.Status != QuoteStatusActive && quote.Status != QuoteStatusReserved {
		return ErrQuoteNotActive
	}
	if quote.IsExpired(now) {
		return ErrQuoteExpired
	}
	return nil
}

func (e *Engine) estimatorFor(network domain.Network) NetworkFeeEstimator {
	for _, est := range e.networkFees {
		if est.SupportsNetwork(network) {
			return est
		}
	}
	return nil
}
