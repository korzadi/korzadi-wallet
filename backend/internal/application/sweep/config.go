package sweep

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// Config holds every operational knob of the sweep service. Everything
// fails CLOSED, mirroring blockchainwithdrawal.Config: with no
// configuration, nothing is swept.
type Config struct {
	// Enabled is the master switch (SWEEPS_ENABLED, default false).
	Enabled bool
	// MainnetEnabled must be explicitly true before any network whose
	// chain ID is a known MAINNET chain is allowed to sweep
	// (SWEEPS_MAINNET_ENABLED, default false) — same fail-closed posture
	// WITHDRAWALS_MAINNET_ENABLED already has for Bloque 6B, applied here
	// so a fresh deployment never moves real funds until an operator
	// explicitly opts in twice (once for withdrawals, once for sweeps).
	MainnetEnabled bool

	// DustThresholds: minimum on-chain balance (in the asset's own human
	// units) worth sweeping, keyed by upper-case asset code
	// (SWEEP_DUST_<ASSET>, e.g. SWEEP_DUST_USDT=5). An asset with NO
	// threshold configured is simply never swept — fail closed, exactly
	// the same posture blockchainwithdrawal.Config takes for an asset
	// missing MAX/DAILY limits — rather than guessing a "reasonable"
	// default dust value nobody configured.
	DustThresholds map[string]decimal.Decimal

	PollInterval    time.Duration // worker tick
	LeaseDuration   time.Duration // how long one worker owns a sweep
	BatchSize       int           // sweeps advanced per tick
	CandidateScan   int           // candidates evaluated per tick
	MaxPrepareTries int           // re-price/re-sign attempts before failing a rejected tx
	// BroadcastGrace: how long a transaction may be neither found nor
	// re-broadcastable before it is escalated to manual review — same
	// meaning as blockchainwithdrawal.Config.BroadcastGrace.
	BroadcastGrace time.Duration
}

const (
	envEnabled        = "SWEEPS_ENABLED"
	envMainnetEnabled = "SWEEPS_MAINNET_ENABLED"
	envPollInterval   = "SWEEPS_POLL_INTERVAL"
	envBroadcastGrace = "SWEEPS_BROADCAST_GRACE"
	envDustPrefix     = "SWEEP_DUST_"
)

// DefaultConfig returns the built-in operational defaults (all disabled).
func DefaultConfig() Config {
	return Config{
		DustThresholds:  map[string]decimal.Decimal{},
		PollInterval:    10 * time.Second,
		LeaseDuration:   5 * time.Minute,
		BatchSize:       10,
		CandidateScan:   25,
		MaxPrepareTries: 5,
		BroadcastGrace:  15 * time.Minute,
	}
}

// LoadConfigFromEnv reads the configuration.
//
//	SWEEP_DUST_<ASSET>  optional per asset (e.g. SWEEP_DUST_USDT=5) — an
//	                    asset with none configured is never swept.
func LoadConfigFromEnv() (Config, error) {
	cfg := DefaultConfig()

	var err error
	if cfg.Enabled, err = envBool(envEnabled, false); err != nil {
		return Config{}, err
	}
	if cfg.MainnetEnabled, err = envBool(envMainnetEnabled, false); err != nil {
		return Config{}, err
	}
	if v := os.Getenv(envPollInterval); v != "" {
		d, perr := time.ParseDuration(v)
		if perr != nil || d < time.Second {
			return Config{}, fmt.Errorf("sweeps: %s inválido (mínimo 1s): %q", envPollInterval, v)
		}
		cfg.PollInterval = d
	}
	if v := os.Getenv(envBroadcastGrace); v != "" {
		d, perr := time.ParseDuration(v)
		if perr != nil || d < time.Minute {
			return Config{}, fmt.Errorf("sweeps: %s inválido (mínimo 1m): %q", envBroadcastGrace, v)
		}
		cfg.BroadcastGrace = d
	}

	for _, kv := range os.Environ() {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		key, value := kv[:eq], kv[eq+1:]
		if !strings.HasPrefix(key, envDustPrefix) {
			continue
		}
		code := strings.ToUpper(strings.TrimPrefix(key, envDustPrefix))
		d, derr := decimal.NewFromString(strings.TrimSpace(value))
		if derr != nil || d.IsNegative() {
			return Config{}, fmt.Errorf("sweeps: %s debe ser un decimal >= 0: %q", key, value)
		}
		cfg.DustThresholds[code] = d
	}
	return cfg, nil
}

// DustThresholdFor returns the configured dust threshold for assetCode and
// whether one is configured at all — an asset with ok=false must never be
// swept (see package doc on fail-closed defaults).
func (c Config) DustThresholdFor(assetCode string) (decimal.Decimal, bool) {
	d, ok := c.DustThresholds[strings.ToUpper(assetCode)]
	return d, ok
}

func envBool(key string, def bool) (bool, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("sweeps: %s inválido: %q", key, v)
	}
	return b, nil
}
