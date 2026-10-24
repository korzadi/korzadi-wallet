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
	ids        map[string]string
}

type coinGeckoSimplePriceResponse map[string]map[string]float64

func NewCoinGeckoPriceProvider(baseURL string, httpClient *http.Client) (*CoinGeckoPriceProvider, error) {
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
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return Price{}, fmt.Errorf("fee: no se pudo crear request CoinGecko: %w", err)
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

	baseUSD, ok := baseData["usd"]
	if !ok || baseUSD <= 0 {
		return Price{}, fmt.Errorf(
			"%w: precio USD inválido para %s",
			ErrPriceUnavailable,
			baseAsset,
		)
	}

	usdtUSD, ok := tetherData["usd"]
	if !ok || usdtUSD <= 0 {
		return Price{}, fmt.Errorf(
			"%w: precio USD inválido para USDT",
			ErrPriceUnavailable,
		)
	}

	baseUSDDecimal := decimal.NewFromFloat(baseUSD)
	usdtUSDDecimal := decimal.NewFromFloat(usdtUSD)

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
		AsOf:       time.Now().UTC(),
	}, nil
}
