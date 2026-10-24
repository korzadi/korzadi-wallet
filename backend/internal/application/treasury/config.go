package treasury

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds every operational knob of the treasury rebalancing worker.
// Everything fails CLOSED, mirroring sweep.Config / blockchainwithdrawal.Config:
// with no configuration, nothing is rebalanced.
type Config struct {
	// Enabled is the master switch (TREASURY_REBALANCE_ENABLED, default
	// false). Spec: "No activar mainnet automáticamente" — a fresh
	// deployment never rebalances until an operator explicitly opts in.
	Enabled bool

	// MainnetEnabled must be explicitly true before any network whose
	// chain ID is a known MAINNET chain is allowed to rebalance
	// (TREASURY_REBALANCE_MAINNET_ENABLED, default false) — same
	// fail-closed posture WITHDRAWALS_MAINNET_ENABLED / SWEEPS_MAINNET_ENABLED
	// already have.
	MainnetEnabled bool

	// PollInterval: worker tick (TREASURY_REBALANCE_INTERVAL, default 60s
	// per spec).
	PollInterval time.Duration

	LeaseDuration time.Duration // how long one worker owns a rebalance
	BatchSize     int           // rebalances advanced per tick
	PolicyScan    int           // policies evaluated per tick
}

const (
	envEnabled        = "TREASURY_REBALANCE_ENABLED"
	envMainnetEnabled = "TREASURY_REBALANCE_MAINNET_ENABLED"
	envPollInterval   = "TREASURY_REBALANCE_INTERVAL"
)

// DefaultConfig returns the built-in operational defaults (disabled).
func DefaultConfig() Config {
	return Config{
		PollInterval:  60 * time.Second,
		LeaseDuration: 5 * time.Minute,
		BatchSize:     10,
		PolicyScan:    50,
	}
}

// LoadConfigFromEnv reads TREASURY_REBALANCE_ENABLED / _MAINNET_ENABLED /
// _INTERVAL. Every other knob keeps its DefaultConfig value — there is
// deliberately no env var for BatchSize/LeaseDuration/PolicyScan yet
// (spec's minimum config surface is exactly these three), so operators
// tune those, if ever needed, via a code change with review, not an
// untracked env var.
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
			return Config{}, fmt.Errorf("treasury: %s inválido (mínimo 1s): %q", envPollInterval, v)
		}
		cfg.PollInterval = d
	}
	return cfg, nil
}

func envBool(key string, def bool) (bool, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("treasury: %s inválido: %q", key, v)
	}
	return b, nil
}
