package fee

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// Price is a single exchange-rate quote — spec §12 requires every
// quote to carry enough to judge its own freshness (timestamp) and
// origin (source), never just a bare number.
type Price struct {
	BaseAsset  string // e.g. "ETH"
	QuoteAsset string // e.g. "USDT"
	Value      decimal.Decimal
	Source     string
	AsOf       time.Time
}

// PriceProvider resolves how much one unit of BaseAsset is worth in
// QuoteAsset. Deliberately not coupled to any concrete exchange/oracle
// (spec §10) — the Fee Engine only ever talks to this interface, so a
// real market-data provider can be swapped in later (Bloque 6B or beyond)
// without touching engine.go at all.
//
// Implementations must NEVER fabricate a price: if no reliable quote
// exists, return an error (spec §13) — never 0, never a stale value
// silently treated as current.
type PriceProvider interface {
	GetPrice(ctx context.Context, baseAsset, quoteAsset string) (Price, error)
}

// EnvPriceProvider is the only implementation this block ships: a
// deliberately simple, static source of prices read from the
// environment, e.g. FEE_PRICE_ETH_USDT=3000.00. It exists so the Fee
// Engine is usable and testable end-to-end today, and is explicitly a
// placeholder — spec §10 asks for an interface that can later be backed
// by "exchange / oracle / market data provider / internal pricing
// engine" without changing the engine. Swapping this out for a live feed
// later is a one-line change in main.go's wiring, nothing more.
//
// Every price this provider returns is timestamped with the moment it
// was read, not a fixed/fake time — so the engine's staleness check
// (spec §12) still applies meaningfully: an operator who hasn't updated
// FEE_PRICE_* in a Config reload will still eventually trip
// ErrPriceStale once MaxPriceAge elapses since this process started
// treating that value as current, e.g. after a process restart.
type EnvPriceProvider struct {
	prices   map[string]decimal.Decimal // "BASE/QUOTE" -> value, loaded once at construction
	loadedAt time.Time
}

// NewEnvPriceProvider reads every FEE_PRICE_{BASE}_{QUOTE} variable
// present in the environment for the given pairs. Missing pairs are
// simply absent from the map — GetPrice reports ErrPriceUnavailable for
// them, never a fabricated value.
func NewEnvPriceProvider(pairs [][2]string) (*EnvPriceProvider, error) {
	p := &EnvPriceProvider{prices: map[string]decimal.Decimal{}, loadedAt: time.Now().UTC()}
	for _, pair := range pairs {
		base, quote := pair[0], pair[1]
		key := "FEE_PRICE_" + strings.ToUpper(base) + "_" + strings.ToUpper(quote)
		v := os.Getenv(key)
		if v == "" {
			continue
		}
		d, err := decimal.NewFromString(v)
		if err != nil {
			return nil, fmt.Errorf("%w: %s inválido: %v", ErrInvalidConfig, key, err)
		}
		if !d.IsPositive() {
			return nil, fmt.Errorf("%w: %s debe ser positivo: %s", ErrInvalidConfig, key, v)
		}
		p.prices[pairKey(base, quote)] = d
	}
	return p, nil
}

func pairKey(base, quote string) string {
	return strings.ToUpper(base) + "/" + strings.ToUpper(quote)
}

func (p *EnvPriceProvider) GetPrice(ctx context.Context, baseAsset, quoteAsset string) (Price, error) {
	if strings.EqualFold(baseAsset, quoteAsset) {
		return Price{BaseAsset: baseAsset, QuoteAsset: quoteAsset, Value: decimal.NewFromInt(1), Source: "identity", AsOf: time.Now().UTC()}, nil
	}
	v, ok := p.prices[pairKey(baseAsset, quoteAsset)]
	if !ok {
		return Price{}, fmt.Errorf("%w: %s/%s", ErrPriceUnavailable, baseAsset, quoteAsset)
	}
	return Price{BaseAsset: baseAsset, QuoteAsset: quoteAsset, Value: v, Source: "env_config", AsOf: p.loadedAt}, nil
}
