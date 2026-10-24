package fee

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestCoinGeckoPriceProvider_LivePrices(t *testing.T) {
	// Llama a la API publica real de CoinGecko: depende de internet y de su
	// rate limit (falla con -count>1). Solo se ejecuta si se pide explicitamente.
	if os.Getenv("RUN_LIVE_TESTS") != "1" {
		t.Skip("test en vivo: defina RUN_LIVE_TESTS=1 para ejecutarlo")
	}
	provider, err := NewCoinGeckoPriceProvider(
		"https://api.coingecko.com/api/v3",
		nil,
	)
	if err != nil {
		t.Fatalf("crear provider: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	assets := []string{"BNB", "ETH", "BTC", "POL"}

	for _, asset := range assets {
		t.Run(asset, func(t *testing.T) {
			price, err := provider.GetPrice(ctx, asset, "USDT")
			if err != nil {
				t.Fatalf("obtener precio %s/USDT: %v", asset, err)
			}

			if price.BaseAsset != asset {
				t.Fatalf("BaseAsset incorrecto: esperado=%s obtenido=%s", asset, price.BaseAsset)
			}

			if price.QuoteAsset != "USDT" {
				t.Fatalf("QuoteAsset incorrecto: esperado=USDT obtenido=%s", price.QuoteAsset)
			}

			if !price.Value.GreaterThan(decimal.Zero) {
				t.Fatalf("precio inválido: %s", price.Value)
			}

			if price.Source != "coingecko" {
				t.Fatalf("source incorrecto: %s", price.Source)
			}

			if price.AsOf.IsZero() {
				t.Fatal("AsOf no puede estar vacío")
			}
		})
	}
}

func TestCoinGeckoPriceProvider_Identity(t *testing.T) {
	provider, err := NewCoinGeckoPriceProvider("http://example.com", nil)
	if err != nil {
		t.Fatalf("crear provider: %v", err)
	}

	price, err := provider.GetPrice(context.Background(), "USDT", "USDT")
	if err != nil {
		t.Fatalf("identity: %v", err)
	}

	if !price.Value.Equal(decimal.NewFromInt(1)) {
		t.Fatalf("esperado 1, obtenido %s", price.Value)
	}

	if price.Source != "coingecko" {
		t.Fatalf("source incorrecto: %s", price.Source)
	}
}

func TestCoinGeckoPriceProvider_UnsupportedAsset(t *testing.T) {
	provider, err := NewCoinGeckoPriceProvider("http://example.com", nil)
	if err != nil {
		t.Fatalf("crear provider: %v", err)
	}

	_, err = provider.GetPrice(context.Background(), "DOGE", "USDT")
	if err == nil {
		t.Fatal("esperaba error para asset no soportado")
	}

	if !strings.Contains(err.Error(), "asset no soportado") {
		t.Fatalf("error inesperado: %v", err)
	}
}

func TestCoinGeckoPriceProvider_UnsupportedQuote(t *testing.T) {
	provider, err := NewCoinGeckoPriceProvider("http://example.com", nil)
	if err != nil {
		t.Fatalf("crear provider: %v", err)
	}

	_, err = provider.GetPrice(context.Background(), "BNB", "USD")
	if err == nil {
		t.Fatal("esperaba error para quote no soportado")
	}

	if !strings.Contains(err.Error(), "solo soporta cotización en USDT") {
		t.Fatalf("error inesperado: %v", err)
	}
}

func TestCoinGeckoPriceProvider_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer server.Close()

	provider, err := NewCoinGeckoPriceProvider(server.URL, nil)
	if err != nil {
		t.Fatalf("crear provider: %v", err)
	}

	_, err = provider.GetPrice(context.Background(), "BNB", "USDT")
	if err == nil {
		t.Fatal("esperaba error HTTP")
	}

	if !strings.Contains(err.Error(), "HTTP 429") {
		t.Fatalf("error inesperado: %v", err)
	}
}

func TestCoinGeckoPriceProvider_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"binancecoin":`))
	}))
	defer server.Close()

	provider, err := NewCoinGeckoPriceProvider(server.URL, nil)
	if err != nil {
		t.Fatalf("crear provider: %v", err)
	}

	_, err = provider.GetPrice(context.Background(), "BNB", "USDT")
	if err == nil {
		t.Fatal("esperaba error JSON")
	}

	if !strings.Contains(err.Error(), "respuesta CoinGecko inválida") {
		t.Fatalf("error inesperado: %v", err)
	}
}

func TestCoinGeckoPriceProvider_MissingTetherPrice(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"binancecoin":{"usd":765.0,"last_updated_at":1792861200}}`))
	}))
	defer server.Close()

	provider, err := NewCoinGeckoPriceProvider(server.URL, nil)
	if err != nil {
		t.Fatalf("crear provider: %v", err)
	}

	_, err = provider.GetPrice(context.Background(), "BNB", "USDT")
	if err == nil {
		t.Fatal("esperaba error por USDT ausente")
	}

	if !strings.Contains(err.Error(), "no devolvió precio de USDT") {
		t.Fatalf("error inesperado: %v", err)
	}
}

func TestCoinGeckoPriceProvider_InvalidPrice(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(
			`{"binancecoin":{"usd":0,"last_updated_at":1792861200},"tether":{"usd":1,"last_updated_at":1792861200}}`,
		))
	}))
	defer server.Close()

	provider, err := NewCoinGeckoPriceProvider(server.URL, nil)
	if err != nil {
		t.Fatalf("crear provider: %v", err)
	}

	_, err = provider.GetPrice(context.Background(), "BNB", "USDT")
	if err == nil {
		t.Fatal("esperaba error por precio inválido")
	}

	if !strings.Contains(err.Error(), "precio USD inválido") {
		t.Fatalf("error inesperado: %v", err)
	}
}
