package fee

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

type priceProviderFunc func(context.Context, string, string) (Price, error)

func (f priceProviderFunc) GetPrice(ctx context.Context, base, quote string) (Price, error) {
	return f(ctx, base, quote)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCoinGeckoPriceProvider_APIKeyHeaderAndTimestamp(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if got := r.Header.Get("x-cg-demo-api-key"); got != "test-api-key" {
			t.Fatalf("api key header = %q", got)
		}
		if r.URL.Query().Get("include_last_updated_at") != "true" {
			t.Fatal("request did not ask for source timestamp")
		}
		body := `{"ethereum":{"usd":2000,"last_updated_at":1780000000},"tether":{"usd":1,"last_updated_at":1780000001}}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	p, err := NewCoinGeckoPriceProviderWithAPIKey("https://prices.invalid", client, "test-api-key")
	if err != nil {
		t.Fatal(err)
	}
	price, err := p.GetPrice(context.Background(), "ETH", "USDT")
	if err != nil {
		t.Fatal(err)
	}
	if price.AsOf.Unix() != 1780000000 {
		t.Fatalf("AsOf = %v", price.AsOf)
	}
}

func TestBinancePriceProvider_QuoteAndTimestamp(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(r.URL.Path, "/ticker/24hr") || r.URL.Query().Get("symbol") != "ETHUSDT" {
			t.Fatalf("unexpected Binance request: %s", r.URL.String())
		}
		body := `{"lastPrice":"2500.25","closeTime":1780000000000}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	p, err := NewBinancePriceProvider("https://prices.invalid/api/v3", client)
	if err != nil {
		t.Fatal(err)
	}
	price, err := p.GetPrice(context.Background(), "eth", "usdt")
	if err != nil {
		t.Fatal(err)
	}
	if !price.Value.Equal(decimal.RequireFromString("2500.25")) || price.Source != "binance" || price.AsOf.UnixMilli() != 1780000000000 {
		t.Fatalf("unexpected quote: %+v", price)
	}
}

func TestCachedFallbackPriceProvider_CacheFallbackAndFailClosed(t *testing.T) {
	now := time.Now().UTC()
	calls := 0
	primary := priceProviderFunc(func(context.Context, string, string) (Price, error) {
		calls++
		return Price{BaseAsset: "ETH", QuoteAsset: "USDT", Value: decimal.NewFromInt(2000), Source: "primary", AsOf: now}, nil
	})
	secondaryCalls := 0
	secondary := priceProviderFunc(func(context.Context, string, string) (Price, error) {
		secondaryCalls++
		return Price{}, errors.New("secondary unavailable")
	})
	p, err := NewCachedFallbackPriceProvider(primary, secondary, time.Minute, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	p.now = func() time.Time { return now }
	if _, err := p.GetPrice(context.Background(), "ETH", "USDT"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.GetPrice(context.Background(), "ETH", "USDT"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("primary calls in TTL = %d, want 1", calls)
	}

	now = now.Add(2 * time.Minute)
	primary = priceProviderFunc(func(context.Context, string, string) (Price, error) {
		return Price{}, errors.New("primary unavailable")
	})
	p.primary = primary
	if _, err := p.GetPrice(context.Background(), "ETH", "USDT"); err != nil {
		t.Fatalf("valid bounded stale cache should serve after provider failure: %v", err)
	}

	now = now.Add(2 * time.Hour)
	if _, err := p.GetPrice(context.Background(), "ETH", "USDT"); !errors.Is(err, ErrPriceUnavailable) {
		t.Fatalf("stale quote must fail closed, got %v", err)
	}
	if secondaryCalls == 0 {
		t.Fatal("secondary provider was not tried")
	}
}

func TestCachedFallbackPriceProvider_RejectsStaleProviderQuotes(t *testing.T) {
	now := time.Now().UTC()
	stale := priceProviderFunc(func(context.Context, string, string) (Price, error) {
		return Price{BaseAsset: "ETH", QuoteAsset: "USDT", Value: decimal.NewFromInt(2000), AsOf: now.Add(-2 * time.Hour)}, nil
	})
	p, err := NewCachedFallbackPriceProvider(stale, stale, time.Minute, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	p.now = func() time.Time { return now }
	if _, err := p.GetPrice(context.Background(), "ETH", "USDT"); !errors.Is(err, ErrPriceUnavailable) {
		t.Fatalf("stale providers must fail closed, got %v", err)
	}
}

func TestCachedFallbackPriceProvider_UsesSecondaryWhenPrimaryFails(t *testing.T) {
	now := time.Now().UTC()
	failed := priceProviderFunc(func(context.Context, string, string) (Price, error) {
		return Price{}, errors.New("primary unavailable")
	})
	backup := priceProviderFunc(func(context.Context, string, string) (Price, error) {
		return Price{BaseAsset: "ETH", QuoteAsset: "USDT", Value: decimal.NewFromInt(1900), Source: "backup", AsOf: now}, nil
	})
	p, err := NewCachedFallbackPriceProvider(failed, backup, time.Minute, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	p.now = func() time.Time { return now }
	price, err := p.GetPrice(context.Background(), "ETH", "USDT")
	if err != nil {
		t.Fatal(err)
	}
	if price.Source != "backup" {
		t.Fatalf("source = %q, want backup", price.Source)
	}
}
