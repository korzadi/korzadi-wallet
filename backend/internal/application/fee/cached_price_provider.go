package fee

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// CachedFallbackPriceProvider caches validated quotes for ttl, tries the
// primary then the secondary provider, and only serves cache within maxAge.
// No missing, malformed, future-dated, or stale quote is converted into a price.
type CachedFallbackPriceProvider struct {
	primary, secondary PriceProvider
	ttl, maxAge        time.Duration
	now                func() time.Time
	mu                 sync.Mutex
	cache              map[string]Price
}

func NewCachedFallbackPriceProvider(primary, secondary PriceProvider, ttl, maxAge time.Duration) (*CachedFallbackPriceProvider, error) {
	if primary == nil || secondary == nil || ttl <= 0 || maxAge <= 0 {
		return nil, fmt.Errorf("%w: cache/fallback de precios incompleto", ErrInvalidConfig)
	}
	return &CachedFallbackPriceProvider{primary: primary, secondary: secondary, ttl: ttl, maxAge: maxAge, now: time.Now, cache: map[string]Price{}}, nil
}

func (p *CachedFallbackPriceProvider) GetPrice(ctx context.Context, base, quote string) (Price, error) {
	base, quote = strings.ToUpper(strings.TrimSpace(base)), strings.ToUpper(strings.TrimSpace(quote))
	key := pairKey(base, quote)
	now := p.now().UTC()
	p.mu.Lock()
	cached, hasCached := p.cache[key]
	if hasCached && now.Sub(cached.AsOf) <= p.ttl && validCachedPrice(cached, base, quote, now, p.maxAge) {
		p.mu.Unlock()
		return cached, nil
	}
	p.mu.Unlock()

	var errs []error
	for _, provider := range []PriceProvider{p.primary, p.secondary} {
		price, err := provider.GetPrice(ctx, base, quote)
		if err == nil {
			err = validatePrice(price, base, quote, now, p.maxAge)
		}
		if err == nil {
			p.mu.Lock()
			p.cache[key] = price
			p.mu.Unlock()
			return price, nil
		}
		errs = append(errs, err)
	}
	if hasCached && validCachedPrice(cached, base, quote, now, p.maxAge) {
		return cached, nil
	}
	return Price{}, fmt.Errorf("%w: proveedor primario/secundario sin cotización válida (%v)", ErrPriceUnavailable, errorsJoin(errs))
}

func validatePrice(price Price, base, quote string, now time.Time, maxAge time.Duration) error {
	if !strings.EqualFold(price.BaseAsset, base) || !strings.EqualFold(price.QuoteAsset, quote) || !price.Value.IsPositive() || price.AsOf.IsZero() {
		return fmt.Errorf("cotización inválida")
	}
	age := now.Sub(price.AsOf)
	if age < -5*time.Second || age > maxAge {
		return fmt.Errorf("cotización obsoleta o futura")
	}
	return nil
}

func validCachedPrice(price Price, base, quote string, now time.Time, maxAge time.Duration) bool {
	return validatePrice(price, base, quote, now, maxAge) == nil
}

func errorsJoin(errs []error) error {
	if len(errs) == 0 {
		return fmt.Errorf("sin proveedores")
	}
	return fmt.Errorf("primario: %v; secundario: %v", errs[0], errs[1])
}
