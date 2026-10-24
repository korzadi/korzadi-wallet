package fee

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// BinancePriceProvider is a secondary USDT market source. Its 24-hour ticker
// includes closeTime, which is used as the quote timestamp for staleness checks.
type BinancePriceProvider struct {
	baseURL string
	client  *http.Client
	symbols map[string]string
}

func NewBinancePriceProvider(baseURL string, client *http.Client) (*BinancePriceProvider, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, fmt.Errorf("%w: Binance base URL vacío", ErrInvalidConfig)
	}
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	return &BinancePriceProvider{baseURL: strings.TrimRight(baseURL, "/"), client: client, symbols: map[string]string{
		"BNB": "BNBUSDT", "ETH": "ETHUSDT", "BTC": "BTCUSDT", "POL": "POLUSDT", "TRX": "TRXUSDT",
	}}, nil
}

func (p *BinancePriceProvider) GetPrice(ctx context.Context, baseAsset, quoteAsset string) (Price, error) {
	baseAsset, quoteAsset = strings.ToUpper(strings.TrimSpace(baseAsset)), strings.ToUpper(strings.TrimSpace(quoteAsset))
	if baseAsset == quoteAsset {
		return Price{BaseAsset: baseAsset, QuoteAsset: quoteAsset, Value: decimal.NewFromInt(1), Source: "binance", AsOf: time.Now().UTC()}, nil
	}
	if quoteAsset != "USDT" {
		return Price{}, fmt.Errorf("%w: Binance solo soporta USDT", ErrPriceUnavailable)
	}
	symbol, ok := p.symbols[baseAsset]
	if !ok {
		return Price{}, fmt.Errorf("%w: asset no soportado por Binance: %s", ErrPriceUnavailable, baseAsset)
	}
	endpoint, err := url.Parse(p.baseURL + "/ticker/24hr")
	if err != nil {
		return Price{}, fmt.Errorf("fee: URL Binance inválida: %w", err)
	}
	query := endpoint.Query()
	query.Set("symbol", symbol)
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return Price{}, fmt.Errorf("fee: crear request Binance: %w", err)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return Price{}, fmt.Errorf("fee: Binance request falló: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Price{}, fmt.Errorf("fee: Binance respondió HTTP %d", resp.StatusCode)
	}
	var body struct {
		LastPrice string `json:"lastPrice"`
		CloseTime int64  `json:"closeTime"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return Price{}, fmt.Errorf("fee: respuesta Binance inválida: %w", err)
	}
	value, err := decimal.NewFromString(body.LastPrice)
	if err != nil || !value.IsPositive() || body.CloseTime <= 0 {
		return Price{}, fmt.Errorf("%w: Binance devolvió precio inválido o sin timestamp", ErrPriceUnavailable)
	}
	return Price{BaseAsset: baseAsset, QuoteAsset: quoteAsset, Value: value, Source: "binance", AsOf: time.UnixMilli(body.CloseTime).UTC()}, nil
}
