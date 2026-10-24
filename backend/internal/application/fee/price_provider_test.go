package fee

import (
	"context"
	"os"
	"testing"

	"github.com/shopspring/decimal"
)

func TestEnvPriceProvider_ReturnsConfiguredPrice(t *testing.T) {
	os.Setenv("FEE_PRICE_ETH_USDT", "3000.50")
	defer os.Unsetenv("FEE_PRICE_ETH_USDT")

	p, err := NewEnvPriceProvider([][2]string{{"ETH", "USDT"}})
	if err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}
	price, err := p.GetPrice(context.Background(), "ETH", "USDT")
	if err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}
	if !price.Value.Equal(decimal.NewFromFloat(3000.50)) {
		t.Errorf("price = %s, esperaba 3000.50", price.Value)
	}
	if price.Source == "" {
		t.Error("price.Source no debe estar vacío (spec §12: cada cotización debe tener source)")
	}
	if price.AsOf.IsZero() {
		t.Error("price.AsOf no debe estar vacío")
	}
}

func TestEnvPriceProvider_UnconfiguredPair_ReturnsError(t *testing.T) {
	os.Unsetenv("FEE_PRICE_BNB_USDT")
	p, err := NewEnvPriceProvider([][2]string{{"ETH", "USDT"}}) // BNB deliberately not in the pair list
	if err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}
	_, err = p.GetPrice(context.Background(), "BNB", "USDT")
	if err == nil {
		t.Fatal("esperaba error para un par sin precio configurado — nunca debe inventarse un precio (spec §13)")
	}
}

func TestEnvPriceProvider_IdentityPair_ReturnsOne(t *testing.T) {
	p, err := NewEnvPriceProvider(nil)
	if err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}
	price, err := p.GetPrice(context.Background(), "USDT", "USDT")
	if err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}
	if !price.Value.Equal(decimal.NewFromInt(1)) {
		t.Errorf("USDT/USDT = %s, esperaba 1", price.Value)
	}
}

func TestNewEnvPriceProvider_RejectsInvalidValue(t *testing.T) {
	os.Setenv("FEE_PRICE_ETH_USDT", "not-a-number")
	defer os.Unsetenv("FEE_PRICE_ETH_USDT")

	_, err := NewEnvPriceProvider([][2]string{{"ETH", "USDT"}})
	if err == nil {
		t.Fatal("esperaba error por valor de precio no numérico")
	}
}

func TestNewEnvPriceProvider_RejectsNonPositiveValue(t *testing.T) {
	os.Setenv("FEE_PRICE_ETH_USDT", "0")
	defer os.Unsetenv("FEE_PRICE_ETH_USDT")

	_, err := NewEnvPriceProvider([][2]string{{"ETH", "USDT"}})
	if err == nil {
		t.Fatal("esperaba error: un precio de 0 nunca es válido (spec §13: nunca usar 0)")
	}
}
