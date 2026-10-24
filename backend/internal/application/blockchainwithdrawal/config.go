package blockchainwithdrawal

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// AssetLimits are the operational safety limits for ONE asset (in units of
// that asset, not USDT). No KYC/tier system — only what operations needs.
type AssetLimits struct {
	Min      decimal.Decimal // optional; zero means "only > 0"
	Max      decimal.Decimal // per withdrawal — REQUIRED for the asset to be enabled
	DailyMax decimal.Decimal // per user per rolling 24h — REQUIRED for the asset to be enabled
}

// Config holds every operational knob of the withdrawal service. Everything
// fails CLOSED: with no configuration, nothing can be withdrawn.
type Config struct {
	// Enabled is the master switch (WITHDRAWALS_ENABLED, default false).
	Enabled bool
	// MainnetEnabled must be explicitly true before any network whose
	// chain ID is a known MAINNET chain is allowed to withdraw
	// (WITHDRAWALS_MAINNET_ENABLED, default false).
	MainnetEnabled bool

	Limits map[string]AssetLimits // keyed by upper-case asset code

	// MaxNetworkFeeOverrunBps: if, right before signing, the real gas cost
	// exceeds the quoted native network fee by more than this many basis
	// points, the withdrawal fails (funds released) instead of Korzadi
	// silently absorbing a gas spike. Default 5000 (=50%).
	MaxNetworkFeeOverrunBps int64

	PollInterval    time.Duration // worker tick
	LeaseDuration   time.Duration // how long one worker owns a withdrawal
	BatchSize       int
	MaxPrepareTries int // re-price/re-sign attempts before failing a rejected tx
	MaxReserveTries int // attempts to reserve funds before failing
	// BroadcastGrace: how long a transaction may be neither found nor
	// re-broadcastable before it is escalated to manual review.
	BroadcastGrace time.Duration

	// NewDestinationCooldown: espera de seguridad antes de poder retirar a una
	// dirección que el usuario nunca había usado (WITHDRAWAL_NEW_DESTINATION_COOLDOWN,
	// default 24h; "0" la desactiva). Se avisa al titular al registrarla.
	NewDestinationCooldown time.Duration
}

const (
	envEnabled         = "WITHDRAWALS_ENABLED"
	envNewDestCooldown = "WITHDRAWAL_NEW_DESTINATION_COOLDOWN"
	envMainnetEnabled  = "WITHDRAWALS_MAINNET_ENABLED"
	envOverrunBps      = "WITHDRAWALS_NETWORK_FEE_OVERRUN_BPS"
	envPollInterval    = "WITHDRAWALS_POLL_INTERVAL"
	envBroadcastGrace  = "WITHDRAWALS_BROADCAST_GRACE"
	envLimitMaxPrefix  = "WITHDRAWAL_LIMIT_MAX_"
	envLimitMinPrefix  = "WITHDRAWAL_LIMIT_MIN_"
	envLimitDailyPrefx = "WITHDRAWAL_LIMIT_DAILY_"
)

// DefaultConfig returns the built-in operational defaults (all disabled).
func DefaultConfig() Config {
	return Config{
		Limits:                  map[string]AssetLimits{},
		MaxNetworkFeeOverrunBps: 5000,
		PollInterval:            5 * time.Second,
		LeaseDuration:           5 * time.Minute,
		BatchSize:               10,
		MaxPrepareTries:         5,
		MaxReserveTries:         10,
		BroadcastGrace:          15 * time.Minute,

		NewDestinationCooldown: 24 * time.Hour,
	}
}

// LoadConfigFromEnv reads the configuration. Limits are per asset:
//
//	WITHDRAWAL_LIMIT_MAX_<ASSET>    required  (e.g. WITHDRAWAL_LIMIT_MAX_USDT=5000)
//	WITHDRAWAL_LIMIT_DAILY_<ASSET>  required
//	WITHDRAWAL_LIMIT_MIN_<ASSET>    optional
//
// An asset with MAX or DAILY missing is simply not withdrawable (fail
// closed) — it is not an error, but it is reported by DisabledAssets.
func LoadConfigFromEnv() (Config, error) {
	cfg := DefaultConfig()

	var err error
	if cfg.Enabled, err = envBool(envEnabled, false); err != nil {
		return Config{}, err
	}
	if cfg.MainnetEnabled, err = envBool(envMainnetEnabled, false); err != nil {
		return Config{}, err
	}
	if v := os.Getenv(envOverrunBps); v != "" {
		bps, perr := strconv.ParseInt(v, 10, 64)
		if perr != nil || bps < 0 {
			return Config{}, fmt.Errorf("withdrawals: %s inválido: %q", envOverrunBps, v)
		}
		cfg.MaxNetworkFeeOverrunBps = bps
	}
	if v := os.Getenv(envPollInterval); v != "" {
		d, perr := time.ParseDuration(v)
		if perr != nil || d < time.Second {
			return Config{}, fmt.Errorf("withdrawals: %s inválido (mínimo 1s): %q", envPollInterval, v)
		}
		cfg.PollInterval = d
	}
	if v := os.Getenv(envNewDestCooldown); v != "" {
		d, perr := time.ParseDuration(v)
		if perr != nil || d < 0 || d > 30*24*time.Hour {
			return Config{}, fmt.Errorf("withdrawals: %s inválido (0 a 720h): %q", envNewDestCooldown, v)
		}
		cfg.NewDestinationCooldown = d
	}
	if v := os.Getenv(envBroadcastGrace); v != "" {
		d, perr := time.ParseDuration(v)
		if perr != nil || d < time.Minute {
			return Config{}, fmt.Errorf("withdrawals: %s inválido (mínimo 1m): %q", envBroadcastGrace, v)
		}
		cfg.BroadcastGrace = d
	}

	maxes := map[string]decimal.Decimal{}
	mins := map[string]decimal.Decimal{}
	dailies := map[string]decimal.Decimal{}

	for _, kv := range os.Environ() {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		key, value := kv[:eq], kv[eq+1:]
		switch {
		case strings.HasPrefix(key, envLimitMaxPrefix):
			code := strings.ToUpper(strings.TrimPrefix(key, envLimitMaxPrefix))
			d, perr := parsePositiveDecimal(key, value)
			if perr != nil {
				return Config{}, perr
			}
			maxes[code] = d
		case strings.HasPrefix(key, envLimitDailyPrefx):
			code := strings.ToUpper(strings.TrimPrefix(key, envLimitDailyPrefx))
			d, perr := parsePositiveDecimal(key, value)
			if perr != nil {
				return Config{}, perr
			}
			dailies[code] = d
		case strings.HasPrefix(key, envLimitMinPrefix):
			code := strings.ToUpper(strings.TrimPrefix(key, envLimitMinPrefix))
			d, perr := parsePositiveDecimal(key, value)
			if perr != nil {
				return Config{}, perr
			}
			mins[code] = d
		}
	}

	for code, max := range maxes {
		daily, ok := dailies[code]
		if !ok {
			continue // no daily limit configured → asset stays disabled
		}
		limits := AssetLimits{Min: mins[code], Max: max, DailyMax: daily}
		if err := limits.validate(code); err != nil {
			return Config{}, err
		}
		cfg.Limits[code] = limits
	}
	return cfg, nil
}

func (l AssetLimits) validate(code string) error {
	if !l.Max.IsPositive() || !l.DailyMax.IsPositive() {
		return fmt.Errorf("withdrawals: límites de %s deben ser positivos", code)
	}
	if l.Min.IsNegative() || l.Min.GreaterThan(l.Max) {
		return fmt.Errorf("withdrawals: el mínimo de %s no puede exceder el máximo", code)
	}
	if l.Max.GreaterThan(l.DailyMax) {
		return fmt.Errorf("withdrawals: el máximo por retiro de %s no puede exceder el límite diario", code)
	}
	return nil
}

// DisabledAssets lists assets that have a MAX or DAILY limit env var but not
// both — useful for a startup warning.
func DisabledAssets() []string {
	maxes := map[string]bool{}
	dailies := map[string]bool{}
	for _, kv := range os.Environ() {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		key := kv[:eq]
		if strings.HasPrefix(key, envLimitMaxPrefix) {
			maxes[strings.ToUpper(strings.TrimPrefix(key, envLimitMaxPrefix))] = true
		}
		if strings.HasPrefix(key, envLimitDailyPrefx) {
			dailies[strings.ToUpper(strings.TrimPrefix(key, envLimitDailyPrefx))] = true
		}
	}
	var out []string
	for code := range maxes {
		if !dailies[code] {
			out = append(out, code)
		}
	}
	for code := range dailies {
		if !maxes[code] {
			out = append(out, code)
		}
	}
	return out
}

func envBool(key string, def bool) (bool, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("withdrawals: %s inválido: %q", key, v)
	}
	return b, nil
}

func parsePositiveDecimal(key, value string) (decimal.Decimal, error) {
	d, err := decimal.NewFromString(strings.TrimSpace(value))
	if err != nil || !d.IsPositive() {
		return decimal.Zero, fmt.Errorf("withdrawals: %s debe ser un decimal positivo: %q", key, value)
	}
	return d, nil
}
