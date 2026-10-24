package fee

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
)

// Config holds every business-sensitive number the Fee Engine needs —
// spec §27: "toda configuración sensible al negocio debe ser
// configurable... no introducir secretos en código." Every field here is
// read from the environment once at startup; nothing below is a business
// decision baked into the calculation logic itself.
type Config struct {
	// Service fee policy: a flat charge plus a percentage of the
	// operation amount (spec §4), each clamped to [ServiceFeeMin, ServiceFeeMax].
	ServiceFeeFlat       decimal.Decimal // USDT
	ServiceFeePercentBps int64           // basis points (1/100 of a percent) of OperationAmount, when known
	ServiceFeeMin        decimal.Decimal // USDT
	ServiceFeeMax        decimal.Decimal // USDT

	// Network fee sanity bounds (spec §14-15) — protect against a
	// misbehaving RPC/gas oracle returning an absurd cost.
	NetworkFeeMin decimal.Decimal // USDT
	NetworkFeeMax decimal.Decimal // USDT

	TotalFeeMin decimal.Decimal // USDT
	TotalFeeMax decimal.Decimal // USDT

	// VolatilityBufferBps is added on top of the raw converted network
	// fee before limits/rounding (spec §11) — e.g. 1500 = 15%.
	VolatilityBufferBps int64

	// MaxPriceAge is how old a price quote may be before it's rejected as
	// stale (spec §12).
	MaxPriceAge   time.Duration
	PriceCacheTTL time.Duration

	// QuoteTTL is how long a FeeQuote remains usable after creation
	// (spec §18).
	QuoteTTL time.Duration

	// GasLimitByNetwork is the assumed gas units for a withdrawal
	// transaction on each EVM network — spec §7 example (65,000). This is
	// deliberately a configured estimate, not a live simulation: Bloque
	// 6A never builds a real transaction to run eth_estimateGas against
	// (that belongs to Bloque 6B), so this is the best available proxy
	// for "roughly how much gas will this withdrawal cost".
	GasLimitByNetwork map[domain.Network]uint64

	// BitcoinTxVBytes is the assumed transaction size, in virtual bytes,
	// for a typical single-input withdrawal (spec §8). Same rationale as
	// GasLimitByNetwork: a configured estimate, not a built transaction.
	BitcoinTxVBytes uint64
}

const (
	envServiceFeeFlat       = "FEE_SERVICE_FLAT_USDT"
	envServiceFeePercentBps = "FEE_SERVICE_PERCENT_BPS"
	envServiceFeeMin        = "FEE_SERVICE_MIN_USDT"
	envServiceFeeMax        = "FEE_SERVICE_MAX_USDT"
	envNetworkFeeMin        = "FEE_NETWORK_MIN_USDT"
	envNetworkFeeMax        = "FEE_NETWORK_MAX_USDT"
	envTotalFeeMin          = "FEE_TOTAL_MIN_USDT"
	envTotalFeeMax          = "FEE_TOTAL_MAX_USDT"
	envVolatilityBufferBps  = "FEE_VOLATILITY_BUFFER_BPS"
	envMaxPriceAge          = "FEE_MAX_PRICE_AGE"
	envPriceCacheTTL        = "FEE_PRICE_CACHE_TTL"
	envQuoteTTL             = "FEE_QUOTE_TTL"
	envBitcoinTxVBytes      = "FEE_BTC_TX_VBYTES"
)

// evmGasLimitEnv mirrors evmclient's per-network env prefixes so gas
// limit config reads FEE_GAS_LIMIT_{PREFIX}, e.g. FEE_GAS_LIMIT_ETH,
// FEE_GAS_LIMIT_BSC, FEE_GAS_LIMIT_POLYGON.
var evmGasLimitEnvPrefix = map[domain.Network]string{
	domain.NetworkETHEREUM: "ETH",
	domain.NetworkBSC:      "BSC",
	domain.NetworkPOLYGON:  "POLYGON",
	domain.NetworkARBITRUM: "ARBITRUM",
	domain.NetworkOPTIMISM: "OPTIMISM",
	domain.NetworkBASE:     "BASE",
}

// LoadConfigFromEnv reads every Fee Engine policy value from the
// environment, applying safe, conservative defaults (spec §11: "no
// utilizar una protección absurda o ilimitada... deben existir valores
// seguros") for anything left unset — this is a fee-charging system, so
// unset config fails toward "charge a small, safe, bounded amount", never
// toward zero or unlimited.
func LoadConfigFromEnv() (Config, error) {
	cfg := Config{
		ServiceFeeFlat:       decimal.NewFromFloat(1.00),
		ServiceFeePercentBps: 0,
		ServiceFeeMin:        decimal.NewFromFloat(0.50),
		ServiceFeeMax:        decimal.NewFromInt(50),
		NetworkFeeMin:        decimal.NewFromFloat(0.05),
		NetworkFeeMax:        decimal.NewFromInt(100),
		TotalFeeMin:          decimal.NewFromFloat(0.55),
		TotalFeeMax:          decimal.NewFromInt(150),
		VolatilityBufferBps:  5000, // 50% sobre el costo real estimado de red
		MaxPriceAge:          5 * time.Minute,
		PriceCacheTTL:        30 * time.Second,
		QuoteTTL:             2 * time.Minute,
		BitcoinTxVBytes:      140, // typical single-input, single-output native SegWit spend
		GasLimitByNetwork:    map[domain.Network]uint64{},
	}

	if err := decimalEnv(envServiceFeeFlat, &cfg.ServiceFeeFlat); err != nil {
		return Config{}, err
	}
	if err := int64Env(envServiceFeePercentBps, &cfg.ServiceFeePercentBps); err != nil {
		return Config{}, err
	}
	if err := decimalEnv(envServiceFeeMin, &cfg.ServiceFeeMin); err != nil {
		return Config{}, err
	}
	if err := decimalEnv(envServiceFeeMax, &cfg.ServiceFeeMax); err != nil {
		return Config{}, err
	}
	if err := decimalEnv(envNetworkFeeMin, &cfg.NetworkFeeMin); err != nil {
		return Config{}, err
	}
	if err := decimalEnv(envNetworkFeeMax, &cfg.NetworkFeeMax); err != nil {
		return Config{}, err
	}
	if err := decimalEnv(envTotalFeeMin, &cfg.TotalFeeMin); err != nil {
		return Config{}, err
	}
	if err := decimalEnv(envTotalFeeMax, &cfg.TotalFeeMax); err != nil {
		return Config{}, err
	}
	if err := int64Env(envVolatilityBufferBps, &cfg.VolatilityBufferBps); err != nil {
		return Config{}, err
	}
	if err := durationEnv(envMaxPriceAge, &cfg.MaxPriceAge); err != nil {
		return Config{}, err
	}
	if err := durationEnv(envPriceCacheTTL, &cfg.PriceCacheTTL); err != nil {
		return Config{}, err
	}
	if err := durationEnv(envQuoteTTL, &cfg.QuoteTTL); err != nil {
		return Config{}, err
	}
	if v := os.Getenv(envBitcoinTxVBytes); v != "" {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return Config{}, fmt.Errorf("%w: %s inválido: %v", ErrInvalidConfig, envBitcoinTxVBytes, err)
		}
		cfg.BitcoinTxVBytes = n
	}

	for network, prefix := range evmGasLimitEnvPrefix {
		key := "FEE_GAS_LIMIT_" + prefix
		v := os.Getenv(key)
		if v == "" {
			cfg.GasLimitByNetwork[network] = 65000 // spec §7's own example value, a reasonable default for a simple native/token transfer
			continue
		}
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return Config{}, fmt.Errorf("%w: %s inválido: %v", ErrInvalidConfig, key, err)
		}
		cfg.GasLimitByNetwork[network] = n
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func (c Config) validate() error {
	if c.ServiceFeeMin.GreaterThan(c.ServiceFeeMax) {
		return fmt.Errorf("%w: %s (%s) > %s (%s)", ErrInvalidConfig, envServiceFeeMin, c.ServiceFeeMin, envServiceFeeMax, c.ServiceFeeMax)
	}
	if c.NetworkFeeMin.GreaterThan(c.NetworkFeeMax) {
		return fmt.Errorf("%w: %s (%s) > %s (%s)", ErrInvalidConfig, envNetworkFeeMin, c.NetworkFeeMin, envNetworkFeeMax, c.NetworkFeeMax)
	}
	if c.TotalFeeMin.GreaterThan(c.TotalFeeMax) {
		return fmt.Errorf("%w: %s (%s) > %s (%s)", ErrInvalidConfig, envTotalFeeMin, c.TotalFeeMin, envTotalFeeMax, c.TotalFeeMax)
	}
	if c.VolatilityBufferBps < 0 || c.VolatilityBufferBps > 10000 {
		return fmt.Errorf("%w: %s fuera de rango [0,10000]: %d", ErrInvalidConfig, envVolatilityBufferBps, c.VolatilityBufferBps)
	}
	if c.ServiceFeePercentBps < 0 || c.ServiceFeePercentBps > 10000 {
		return fmt.Errorf("%w: %s fuera de rango [0,10000]: %d", ErrInvalidConfig, envServiceFeePercentBps, c.ServiceFeePercentBps)
	}
	if c.MaxPriceAge <= 0 {
		return fmt.Errorf("%w: %s debe ser positivo", ErrInvalidConfig, envMaxPriceAge)
	}
	if c.PriceCacheTTL <= 0 || c.PriceCacheTTL > c.MaxPriceAge {
		return fmt.Errorf("%w: %s debe ser positivo y no superar FEE_MAX_PRICE_AGE", ErrInvalidConfig, envPriceCacheTTL)
	}
	if c.QuoteTTL <= 0 {
		return fmt.Errorf("%w: %s debe ser positivo", ErrInvalidConfig, envQuoteTTL)
	}
	return nil
}

func decimalEnv(key string, dst *decimal.Decimal) error {
	v := os.Getenv(key)
	if v == "" {
		return nil
	}
	d, err := decimal.NewFromString(v)
	if err != nil {
		return fmt.Errorf("%w: %s inválido: %v", ErrInvalidConfig, key, err)
	}
	if d.IsNegative() {
		return fmt.Errorf("%w: %s no puede ser negativo: %s", ErrInvalidConfig, key, v)
	}
	*dst = d
	return nil
}

func int64Env(key string, dst *int64) error {
	v := os.Getenv(key)
	if v == "" {
		return nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return fmt.Errorf("%w: %s inválido: %v", ErrInvalidConfig, key, err)
	}
	*dst = n
	return nil
}

func durationEnv(key string, dst *time.Duration) error {
	v := os.Getenv(key)
	if v == "" {
		return nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fmt.Errorf("%w: %s inválido: %v", ErrInvalidConfig, key, err)
	}
	if d <= 0 {
		return fmt.Errorf("%w: %s debe ser positivo: %s", ErrInvalidConfig, key, v)
	}
	*dst = d
	return nil
}
