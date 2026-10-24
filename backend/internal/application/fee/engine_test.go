package fee

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
)

// ---- fakes ----

type fakeNetworkFeeEstimator struct {
	network domain.Network
	amount  decimal.Decimal
	asset   string
	err     error
}

func (f *fakeNetworkFeeEstimator) SupportsNetwork(network domain.Network) bool {
	return network == f.network
}
func (f *fakeNetworkFeeEstimator) EstimateNetworkFee(ctx context.Context, network domain.Network) (NetworkFeeEstimate, error) {
	if f.err != nil {
		return NetworkFeeEstimate{}, f.err
	}
	return NetworkFeeEstimate{NativeAmount: f.amount, NativeAssetCode: f.asset}, nil
}

type fakePriceProvider struct {
	price Price
	err   error
}

func (f *fakePriceProvider) GetPrice(ctx context.Context, base, quote string) (Price, error) {
	if f.err != nil {
		return Price{}, f.err
	}
	return f.price, nil
}

type fakeAssetChecker struct {
	assets map[string]domain.NetworkAsset // "network/code" -> asset
}

func newFakeAssetChecker() *fakeAssetChecker {
	return &fakeAssetChecker{assets: map[string]domain.NetworkAsset{}}
}

func (f *fakeAssetChecker) register(network domain.Network, code string, enabled bool) {
	f.assets[string(network)+"/"+code] = domain.NetworkAsset{Network: network, AssetCode: code, Enabled: enabled}
}

func (f *fakeAssetChecker) Get(ctx context.Context, network domain.Network, code string) (domain.NetworkAsset, bool, error) {
	a, ok := f.assets[string(network)+"/"+code]
	return a, ok, nil
}

type fakeQuoteStore struct {
	mu    sync.Mutex
	byID  map[uuid.UUID]*FeeQuote
	saves int
}

func newFakeQuoteStore() *fakeQuoteStore { return &fakeQuoteStore{byID: map[uuid.UUID]*FeeQuote{}} }

func (f *fakeQuoteStore) Save(ctx context.Context, q *FeeQuote) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saves++
	cp := *q
	f.byID[q.ID] = &cp
	return nil
}

func (f *fakeQuoteStore) FindByID(ctx context.Context, id uuid.UUID) (*FeeQuote, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	q, ok := f.byID[id]
	if !ok {
		return nil, nil
	}
	cp := *q
	return &cp, nil
}

func (f *fakeQuoteStore) SetReservationID(ctx context.Context, quoteID, reservationID uuid.UUID) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	q, ok := f.byID[quoteID]
	if !ok {
		return false, errors.New("quote no existe")
	}
	if q.ReservationID != nil {
		return false, nil
	}
	q.ReservationID = &reservationID
	return true, nil
}

func (f *fakeQuoteStore) UpdateStatus(ctx context.Context, quoteID uuid.UUID, status QuoteStatus) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	q, ok := f.byID[quoteID]
	if !ok {
		return errors.New("quote no existe")
	}
	q.Status = status
	return nil
}

const testFeeDecimals = int32(6)

func baseConfig() Config {
	return Config{
		ServiceFeeFlat: decimal.NewFromInt(1), ServiceFeeMin: decimal.NewFromFloat(0.5), ServiceFeeMax: decimal.NewFromInt(50),
		NetworkFeeMin: decimal.NewFromFloat(0.01), NetworkFeeMax: decimal.NewFromInt(100),
		TotalFeeMin: decimal.NewFromFloat(0.5), TotalFeeMax: decimal.NewFromInt(150),
		VolatilityBufferBps: 1500, MaxPriceAge: 5 * time.Minute, QuoteTTL: 2 * time.Minute,
	}
}

func newTestEngine(cfg Config, estimator NetworkFeeEstimator, price PriceProvider, assets AssetSupportChecker, quotes QuoteStore) *Engine {
	return NewEngine(cfg, NewServiceFeePolicy(cfg), []NetworkFeeEstimator{estimator}, price, assets, quotes, "USDT", testFeeDecimals)
}

func freshPrice(value decimal.Decimal) Price {
	return Price{BaseAsset: "ETH", QuoteAsset: "USDT", Value: value, Source: "test", AsOf: time.Now().UTC()}
}

// ---- tests ----

func TestEstimateFee_ConversionExample(t *testing.T) {
	// spec's own worked example: 0.00021 ETH network cost, ETH/USDT = 3000
	// -> 0.63 USDT raw, then the volatility buffer on top.
	assets := newFakeAssetChecker()
	assets.register(domain.NetworkETHEREUM, "ETH", true)
	quotes := newFakeQuoteStore()
	cfg := baseConfig()
	cfg.VolatilityBufferBps = 0 // isolate the conversion math from the buffer for this test

	estimator := &fakeNetworkFeeEstimator{network: domain.NetworkETHEREUM, amount: decimal.NewFromFloat(0.00021), asset: "ETH"}
	engine := newTestEngine(cfg, estimator, &fakePriceProvider{price: freshPrice(decimal.NewFromInt(3000))}, assets, quotes)

	quote, err := engine.EstimateFee(context.Background(), EstimateFeeInput{
		UserID: uuid.New(), OperationType: OperationWithdrawal, Network: domain.NetworkETHEREUM, OperationAssetCode: "ETH",
	})
	if err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}
	if !quote.NetworkFee.Equal(decimal.NewFromFloat(0.63)) {
		t.Errorf("network fee = %s, esperaba 0.63 (0.00021 * 3000)", quote.NetworkFee)
	}
}

func TestEstimateFee_ServiceAndNetworkFeeStaySeparateButSumExactly(t *testing.T) {
	assets := newFakeAssetChecker()
	assets.register(domain.NetworkETHEREUM, "ETH", true)
	quotes := newFakeQuoteStore()
	cfg := baseConfig()

	estimator := &fakeNetworkFeeEstimator{network: domain.NetworkETHEREUM, amount: decimal.NewFromFloat(0.0002), asset: "ETH"}
	engine := newTestEngine(cfg, estimator, &fakePriceProvider{price: freshPrice(decimal.NewFromInt(3000))}, assets, quotes)

	quote, err := engine.EstimateFee(context.Background(), EstimateFeeInput{
		UserID: uuid.New(), OperationType: OperationWithdrawal, Network: domain.NetworkETHEREUM, OperationAssetCode: "ETH",
	})
	if err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}
	if !quote.TotalFee.Equal(quote.ServiceFee.Add(quote.NetworkFee)) {
		t.Errorf("total (%s) != service (%s) + network (%s)", quote.TotalFee, quote.ServiceFee, quote.NetworkFee)
	}
	if quote.ServiceFee.IsZero() || quote.NetworkFee.IsZero() {
		t.Error("ambos componentes deben permanecer visibles y distintos de cero por separado (spec §1, §23)")
	}
}

func TestEstimateFee_VolatilityBufferApplied(t *testing.T) {
	assets := newFakeAssetChecker()
	assets.register(domain.NetworkETHEREUM, "ETH", true)

	cfgNoBuffer := baseConfig()
	cfgNoBuffer.VolatilityBufferBps = 0
	cfgBuffer := baseConfig()
	cfgBuffer.VolatilityBufferBps = 5000 // 50%

	estimator := &fakeNetworkFeeEstimator{network: domain.NetworkETHEREUM, amount: decimal.NewFromFloat(0.001), asset: "ETH"}
	price := &fakePriceProvider{price: freshPrice(decimal.NewFromInt(1000))}

	engineNoBuffer := newTestEngine(cfgNoBuffer, estimator, price, assets, newFakeQuoteStore())
	engineBuffer := newTestEngine(cfgBuffer, estimator, price, assets, newFakeQuoteStore())

	in := EstimateFeeInput{UserID: uuid.New(), OperationType: OperationWithdrawal, Network: domain.NetworkETHEREUM, OperationAssetCode: "ETH"}

	quoteA, err := engineNoBuffer.EstimateFee(context.Background(), in)
	if err != nil {
		t.Fatalf("sin buffer: %v", err)
	}
	quoteB, err := engineBuffer.EstimateFee(context.Background(), in)
	if err != nil {
		t.Fatalf("con buffer: %v", err)
	}
	if !quoteB.NetworkFee.GreaterThan(quoteA.NetworkFee) {
		t.Errorf("network fee con buffer (%s) debía ser mayor que sin buffer (%s)", quoteB.NetworkFee, quoteA.NetworkFee)
	}
	// raw = 0.001 * 1000 = 1.00 USDT; with 50% buffer = 1.50 USDT
	if !quoteB.NetworkFee.Equal(decimal.NewFromFloat(1.50)) {
		t.Errorf("network fee con 50%% buffer = %s, esperaba 1.50", quoteB.NetworkFee)
	}
}

func TestEstimateFee_StalePrice_Rejected(t *testing.T) {
	assets := newFakeAssetChecker()
	assets.register(domain.NetworkETHEREUM, "ETH", true)
	cfg := baseConfig()
	cfg.MaxPriceAge = 1 * time.Minute

	staleprice := Price{Value: decimal.NewFromInt(3000), Source: "test", AsOf: time.Now().UTC().Add(-10 * time.Minute)}
	estimator := &fakeNetworkFeeEstimator{network: domain.NetworkETHEREUM, amount: decimal.NewFromFloat(0.0002), asset: "ETH"}
	engine := newTestEngine(cfg, estimator, &fakePriceProvider{price: staleprice}, assets, newFakeQuoteStore())

	_, err := engine.EstimateFee(context.Background(), EstimateFeeInput{
		UserID: uuid.New(), OperationType: OperationWithdrawal, Network: domain.NetworkETHEREUM, OperationAssetCode: "ETH",
	})
	if !errors.Is(err, ErrPriceStale) {
		t.Fatalf("esperaba ErrPriceStale, obtuve: %v", err)
	}
}

func TestEstimateFee_PriceUnavailable_Rejected(t *testing.T) {
	assets := newFakeAssetChecker()
	assets.register(domain.NetworkETHEREUM, "ETH", true)
	estimator := &fakeNetworkFeeEstimator{network: domain.NetworkETHEREUM, amount: decimal.NewFromFloat(0.0002), asset: "ETH"}
	engine := newTestEngine(baseConfig(), estimator, &fakePriceProvider{err: ErrPriceUnavailable}, assets, newFakeQuoteStore())

	_, err := engine.EstimateFee(context.Background(), EstimateFeeInput{
		UserID: uuid.New(), OperationType: OperationWithdrawal, Network: domain.NetworkETHEREUM, OperationAssetCode: "ETH",
	})
	if !errors.Is(err, ErrPriceUnavailable) {
		t.Fatalf("esperaba ErrPriceUnavailable, obtuve: %v", err)
	}
}

func TestEstimateFee_NetworkFeeAboveMaximum_Rejected(t *testing.T) {
	assets := newFakeAssetChecker()
	assets.register(domain.NetworkETHEREUM, "ETH", true)
	cfg := baseConfig()
	cfg.NetworkFeeMax = decimal.NewFromInt(10)

	// A wildly abnormal network cost — spec §15's own example scenario.
	estimator := &fakeNetworkFeeEstimator{network: domain.NetworkETHEREUM, amount: decimal.NewFromInt(1000), asset: "ETH"}
	engine := newTestEngine(cfg, estimator, &fakePriceProvider{price: freshPrice(decimal.NewFromInt(3000))}, assets, newFakeQuoteStore())

	_, err := engine.EstimateFee(context.Background(), EstimateFeeInput{
		UserID: uuid.New(), OperationType: OperationWithdrawal, Network: domain.NetworkETHEREUM, OperationAssetCode: "ETH",
	})
	if !errors.Is(err, ErrFeeTooHigh) {
		t.Fatalf("esperaba ErrFeeTooHigh, obtuve: %v", err)
	}
}

func TestEstimateFee_NetworkFeeBelowMinimum_IsNotClamped(t *testing.T) {
	assets := newFakeAssetChecker()
	assets.register(domain.NetworkETHEREUM, "ETH", true)

	cfg := baseConfig()
	cfg.NetworkFeeMin = decimal.NewFromFloat(0.05)
	cfg.VolatilityBufferBps = 0

	estimator := &fakeNetworkFeeEstimator{
		network: domain.NetworkETHEREUM,
		amount:  decimal.NewFromFloat(0.00000001),
		asset:   "ETH",
	}

	engine := newTestEngine(
		cfg,
		estimator,
		&fakePriceProvider{price: freshPrice(decimal.NewFromInt(3000))},
		assets,
		newFakeQuoteStore(),
	)

	quote, err := engine.EstimateFee(context.Background(), EstimateFeeInput{
		UserID:             uuid.New(),
		OperationType:      OperationWithdrawal,
		Network:            domain.NetworkETHEREUM,
		OperationAssetCode: "ETH",
	})
	if err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}

	expected := decimal.NewFromFloat(0.00003)
	if !quote.NetworkFee.Equal(expected) {
		t.Errorf("network fee = %s, esperaba %s", quote.NetworkFee, expected)
	}
}

func TestEstimateFee_TotalBelowMinimum_ServiceFeeToppedUp_InvariantHolds(t *testing.T) {
	assets := newFakeAssetChecker()
	assets.register(domain.NetworkETHEREUM, "ETH", true)
	cfg := baseConfig()
	cfg.ServiceFeeFlat = decimal.Zero
	cfg.ServiceFeeMin = decimal.Zero
	cfg.NetworkFeeMin = decimal.Zero
	cfg.TotalFeeMin = decimal.NewFromFloat(2.00)
	cfg.VolatilityBufferBps = 0

	estimator := &fakeNetworkFeeEstimator{network: domain.NetworkETHEREUM, amount: decimal.NewFromFloat(0.0001), asset: "ETH"}
	engine := newTestEngine(cfg, estimator, &fakePriceProvider{price: freshPrice(decimal.NewFromInt(1))}, assets, newFakeQuoteStore()) // network fee ~= 0.0001 USDT, way under 2.00 minimum

	quote, err := engine.EstimateFee(context.Background(), EstimateFeeInput{
		UserID: uuid.New(), OperationType: OperationWithdrawal, Network: domain.NetworkETHEREUM, OperationAssetCode: "ETH",
	})
	if err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}
	if !quote.TotalFee.Equal(decimal.NewFromFloat(2.00)) {
		t.Errorf("total fee = %s, esperaba el mínimo 2.00", quote.TotalFee)
	}
	if !quote.TotalFee.Equal(quote.ServiceFee.Add(quote.NetworkFee)) {
		t.Errorf("invariante rota tras el ajuste al mínimo: total(%s) != service(%s)+network(%s)", quote.TotalFee, quote.ServiceFee, quote.NetworkFee)
	}
}

func TestEstimateFee_UnsupportedNetwork_Rejected(t *testing.T) {
	assets := newFakeAssetChecker()
	assets.register(domain.NetworkBASE, "ETH", true) // asset is fine; no estimator supports this network below
	estimator := &fakeNetworkFeeEstimator{network: domain.NetworkETHEREUM, amount: decimal.NewFromFloat(0.0002), asset: "ETH"}
	engine := newTestEngine(baseConfig(), estimator, &fakePriceProvider{price: freshPrice(decimal.NewFromInt(3000))}, assets, newFakeQuoteStore())

	_, err := engine.EstimateFee(context.Background(), EstimateFeeInput{
		UserID: uuid.New(), OperationType: OperationWithdrawal, Network: domain.NetworkBASE, OperationAssetCode: "ETH",
	})
	if !errors.Is(err, ErrUnsupportedNetwork) {
		t.Fatalf("esperaba ErrUnsupportedNetwork, obtuve: %v", err)
	}
}

func TestEstimateFee_UnregisteredAsset_Rejected(t *testing.T) {
	assets := newFakeAssetChecker() // nothing registered
	estimator := &fakeNetworkFeeEstimator{network: domain.NetworkETHEREUM, amount: decimal.NewFromFloat(0.0002), asset: "ETH"}
	engine := newTestEngine(baseConfig(), estimator, &fakePriceProvider{price: freshPrice(decimal.NewFromInt(3000))}, assets, newFakeQuoteStore())

	_, err := engine.EstimateFee(context.Background(), EstimateFeeInput{
		UserID: uuid.New(), OperationType: OperationWithdrawal, Network: domain.NetworkETHEREUM, OperationAssetCode: "FAKECOIN",
	})
	if !errors.Is(err, ErrUnsupportedAsset) {
		t.Fatalf("esperaba ErrUnsupportedAsset, obtuve: %v", err)
	}
}

func TestEstimateFee_DisabledAsset_Rejected(t *testing.T) {
	assets := newFakeAssetChecker()
	assets.register(domain.NetworkETHEREUM, "ETH", false) // registered but disabled
	estimator := &fakeNetworkFeeEstimator{network: domain.NetworkETHEREUM, amount: decimal.NewFromFloat(0.0002), asset: "ETH"}
	engine := newTestEngine(baseConfig(), estimator, &fakePriceProvider{price: freshPrice(decimal.NewFromInt(3000))}, assets, newFakeQuoteStore())

	_, err := engine.EstimateFee(context.Background(), EstimateFeeInput{
		UserID: uuid.New(), OperationType: OperationWithdrawal, Network: domain.NetworkETHEREUM, OperationAssetCode: "ETH",
	})
	if !errors.Is(err, ErrUnsupportedAsset) {
		t.Fatalf("esperaba ErrUnsupportedAsset para un asset deshabilitado, obtuve: %v", err)
	}
}

func TestEstimateFee_NegativeAmount_Rejected(t *testing.T) {
	assets := newFakeAssetChecker()
	assets.register(domain.NetworkETHEREUM, "ETH", true)
	estimator := &fakeNetworkFeeEstimator{network: domain.NetworkETHEREUM, amount: decimal.NewFromFloat(0.0002), asset: "ETH"}
	engine := newTestEngine(baseConfig(), estimator, &fakePriceProvider{price: freshPrice(decimal.NewFromInt(3000))}, assets, newFakeQuoteStore())

	_, err := engine.EstimateFee(context.Background(), EstimateFeeInput{
		UserID: uuid.New(), OperationType: OperationWithdrawal, Network: domain.NetworkETHEREUM, OperationAssetCode: "ETH",
		OperationAmount: decimal.NewFromInt(-5),
	})
	if !errors.Is(err, ErrInvalidAmount) {
		t.Fatalf("esperaba ErrInvalidAmount, obtuve: %v", err)
	}
}

func TestEstimateFee_RoundingIsDeterministic(t *testing.T) {
	assets := newFakeAssetChecker()
	assets.register(domain.NetworkETHEREUM, "ETH", true)
	estimator := &fakeNetworkFeeEstimator{network: domain.NetworkETHEREUM, amount: decimal.NewFromFloat(0.00021), asset: "ETH"}
	price := &fakePriceProvider{price: freshPrice(decimal.NewFromInt(3000))}

	var results []decimal.Decimal
	for i := 0; i < 5; i++ {
		engine := newTestEngine(baseConfig(), estimator, price, assets, newFakeQuoteStore())
		q, err := engine.EstimateFee(context.Background(), EstimateFeeInput{
			UserID: uuid.New(), OperationType: OperationWithdrawal, Network: domain.NetworkETHEREUM, OperationAssetCode: "ETH",
		})
		if err != nil {
			t.Fatalf("intento %d: %v", i, err)
		}
		results = append(results, q.TotalFee)
	}
	for i := 1; i < len(results); i++ {
		if !results[i].Equal(results[0]) {
			t.Errorf("resultado %d (%s) difiere del primero (%s) — el redondeo debe ser determinista", i, results[i], results[0])
		}
	}
}

func TestQuote_IsExpired(t *testing.T) {
	q := &FeeQuote{ExpiresAt: time.Now().Add(-1 * time.Minute)}
	if !q.IsExpired(time.Now()) {
		t.Error("una cotización con expires_at en el pasado debe reportarse expirada")
	}
	q2 := &FeeQuote{ExpiresAt: time.Now().Add(1 * time.Minute)}
	if q2.IsExpired(time.Now()) {
		t.Error("una cotización con expires_at en el futuro no debe reportarse expirada")
	}
}

func TestValidateQuote_RejectsExpired(t *testing.T) {
	engine := newTestEngine(baseConfig(), &fakeNetworkFeeEstimator{}, &fakePriceProvider{}, newFakeAssetChecker(), newFakeQuoteStore())
	quote := &FeeQuote{Status: QuoteStatusActive, ExpiresAt: time.Now().Add(-1 * time.Minute)}
	if err := engine.ValidateQuote(quote, time.Now()); !errors.Is(err, ErrQuoteExpired) {
		t.Fatalf("esperaba ErrQuoteExpired, obtuve: %v", err)
	}
}

func TestValidateQuote_RejectsConsumed(t *testing.T) {
	engine := newTestEngine(baseConfig(), &fakeNetworkFeeEstimator{}, &fakePriceProvider{}, newFakeAssetChecker(), newFakeQuoteStore())
	quote := &FeeQuote{Status: QuoteStatusConsumed, ExpiresAt: time.Now().Add(1 * time.Minute)}
	if err := engine.ValidateQuote(quote, time.Now()); !errors.Is(err, ErrQuoteNotActive) {
		t.Fatalf("esperaba ErrQuoteNotActive, obtuve: %v", err)
	}
}

func TestGetQuote_OwnershipEnforced(t *testing.T) {
	assets := newFakeAssetChecker()
	assets.register(domain.NetworkETHEREUM, "ETH", true)
	quotes := newFakeQuoteStore()
	estimator := &fakeNetworkFeeEstimator{network: domain.NetworkETHEREUM, amount: decimal.NewFromFloat(0.0002), asset: "ETH"}
	engine := newTestEngine(baseConfig(), estimator, &fakePriceProvider{price: freshPrice(decimal.NewFromInt(3000))}, assets, quotes)

	owner := uuid.New()
	quote, err := engine.EstimateFee(context.Background(), EstimateFeeInput{
		UserID: owner, OperationType: OperationWithdrawal, Network: domain.NetworkETHEREUM, OperationAssetCode: "ETH",
	})
	if err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}

	attacker := uuid.New()
	if _, err := engine.GetQuote(context.Background(), quote.ID, attacker); !errors.Is(err, ErrQuoteNotFound) {
		t.Fatalf("esperaba ErrQuoteNotFound para un usuario ajeno, obtuve: %v", err)
	}
	if _, err := engine.GetQuote(context.Background(), quote.ID, owner); err != nil {
		t.Errorf("el dueño debía poder leer su propia cotización: %v", err)
	}
}
