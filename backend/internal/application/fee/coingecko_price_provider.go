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

type CoinGeckoPriceProvider struct {
	baseURL    string
	httpClient *http.Client
	apiKey     string
	ids        map[string]string
}

type coinGeckoPriceItem struct {
	USD           float64 `json:"usd"`
	LastUpdatedAt int64   `json:"last_updated_at"`
}
type coinGeckoSimplePriceResponse map[string]coinGeckoPriceItem

func NewCoinGeckoPriceProvider(baseURL string, httpClient *http.Client) (*CoinGeckoPriceProvider, error) {
	return NewCoinGeckoPriceProviderWithAPIKey(baseURL, httpClient, "")
}

func NewCoinGeckoPriceProviderWithAPIKey(baseURL string, httpClient *http.Client, apiKey string) (*CoinGeckoPriceProvider, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, fmt.Errorf("%w: CoinGecko base URL vacío", ErrInvalidConfig)
	}

	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: 10 * time.Second,
		}
	}

	return &CoinGeckoPriceProvider{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: httpClient,
		apiKey:     strings.TrimSpace(apiKey),
		ids: map[string]string{
			"BNB": "binancecoin",
			"ETH": "ethereum",
			"BTC": "bitcoin",
			"POL": "polygon-ecosystem-token",
			"TRX": "tron",
		},
	}, nil
}

func (p *CoinGeckoPriceProvider) GetPrice(
	ctx context.Context,
	baseAsset,
	quoteAsset string,
) (Price, error) {
	baseAsset = strings.ToUpper(strings.TrimSpace(baseAsset))
	quoteAsset = strings.ToUpper(strings.TrimSpace(quoteAsset))

	if baseAsset == quoteAsset {
		return Price{
			BaseAsset:  baseAsset,
			QuoteAsset: quoteAsset,
			Value:      decimal.NewFromInt(1),
			Source:     "coingecko",
			AsOf:       time.Now().UTC(),
		}, nil
	}

	if quoteAsset != "USDT" {
		return Price{}, fmt.Errorf(
			"%w: CoinGecko provider solo soporta cotización en USDT, recibido %s",
			ErrPriceUnavailable,
			quoteAsset,
		)
	}

	coinID, ok := p.ids[baseAsset]
	if !ok {
		return Price{}, fmt.Errorf(
			"%w: asset no soportado por CoinGecko: %s",
			ErrPriceUnavailable,
			baseAsset,
		)
	}

	endpoint, err := url.Parse(p.baseURL + "/simple/price")
	if err != nil {
		return Price{}, fmt.Errorf("fee: URL CoinGecko inválida: %w", err)
	}

	query := endpoint.Query()
	query.Set("ids", coinID+",tether")
	query.Set("vs_currencies", "usd")
	query.Set("include_last_updated_at", "true")
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return Price{}, fmt.Errorf("fee: no se pudo crear request CoinGecko: %w", err)
	}
	if p.apiKey != "" {
		req.Header.Set("x-cg-demo-api-key", p.apiKey)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return Price{}, fmt.Errorf("fee: CoinGecko request falló: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Price{}, fmt.Errorf(
			"fee: CoinGecko respondió HTTP %d",
			resp.StatusCode,
		)
	}

	var payload coinGeckoSimplePriceResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return Price{}, fmt.Errorf(
			"fee: respuesta CoinGecko inválida: %w",
			err,
		)
	}

	baseData, ok := payload[coinID]
	if !ok {
		return Price{}, fmt.Errorf(
			"%w: CoinGecko no devolvió datos para %s",
			ErrPriceUnavailable,
			baseAsset,
		)
	}

	tetherData, ok := payload["tether"]
	if !ok {
		return Price{}, fmt.Errorf(
			"%w: CoinGecko no devolvió precio de USDT",
			ErrPriceUnavailable,
		)
	}

	if baseData.USD <= 0 || baseData.LastUpdatedAt <= 0 {
		return Price{}, fmt.Errorf(
			"%w: precio USD inválido o sin timestamp para %s",
			ErrPriceUnavailable,
			baseAsset,
		)
	}

	if tetherData.USD <= 0 || tetherData.LastUpdatedAt <= 0 {
		return Price{}, fmt.Errorf(
			"%w: precio USD inválido o sin timestamp para USDT",
			ErrPriceUnavailable,
		)
	}

	baseUSDDecimal := decimal.NewFromFloat(baseData.USD)
	usdtUSDDecimal := decimal.NewFromFloat(tetherData.USD)

	priceUSDT := baseUSDDecimal.Div(usdtUSDDecimal)

	if !priceUSDT.IsPositive() {
		return Price{}, fmt.Errorf(
			"%w: precio calculado inválido para %s/USDT",
			ErrPriceUnavailable,
			baseAsset,
		)
	}

	return Price{
		BaseAsset:  baseAsset,
		QuoteAsset: quoteAsset,
		Value:      priceUSDT,
		Source:     "coingecko",
		AsOf:       time.Unix(minTimestamp(baseData.LastUpdatedAt, tetherData.LastUpdatedAt), 0).UTC(),
	}, nil
}

func minTimestamp(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
