package recovery

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config controls the recovery worker.
//
// Enabled gates ONLY the two write actions (Unstick / EscalateToReview) —
// detection, classification and recording an incident ALWAYS run,
// regardless of Enabled (spec 7.6.2: "No debe impedir que el sistema
// registre o detecte incidentes"). This is the one Bloque 7 kill switch
// that does NOT stop its subsystem entirely when off.
type Config struct {
	Enabled      bool
	PollInterval time.Duration

	// StaleAfter: how long an ACTIVE, unlocked row can go untouched before
	// it is considered KindStuckNoWorker.
	StaleAfter time.Duration
	// MaxAttemptsBeforeReview: an ACTIVE row with Attempts at or above this
	// is KindStuckProcessing and eligible for EscalateToReview.
	MaxAttemptsBeforeReview int

	BatchSize int
}

const (
	envEnabled      = "RECOVERY_ENABLED"
	envPollInterval = "RECOVERY_INTERVAL"
)

func DefaultConfig() Config {
	return Config{
		PollInterval:            60 * time.Second,
		StaleAfter:              20 * time.Minute,
		MaxAttemptsBeforeReview: 50, // matches sweep/treasury's own preSignRetryLimit-class ceilings
		BatchSize:               200,
	}
}

func LoadConfigFromEnv() (Config, error) {
	cfg := DefaultConfig()
	var err error
	if cfg.Enabled, err = envBool(envEnabled, false); err != nil {
		return Config{}, err
	}
	if v := os.Getenv(envPollInterval); v != "" {
		d, perr := time.ParseDuration(v)
		if perr != nil || d < time.Second {
			return Config{}, fmt.Errorf("recovery: %s inválido (mínimo 1s): %q", envPollInterval, v)
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
		return false, fmt.Errorf("recovery: %s inválido: %q", key, v)
	}
	return b, nil
}
