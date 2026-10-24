package reconciliation

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config controls the reconciliation worker. Everything fails CLOSED: with
// no configuration nothing runs — same posture as every other Bloque 7
// worker. Being read-only, this kill switch is lower-stakes than
// sweeps/treasury's (nothing here ever moves funds), but the spec still
// asks for one explicitly ("Configurable: RECONCILIATION_ENABLED=false...
// No activar automáticamente") so it is honored exactly.
type Config struct {
	Enabled      bool
	PollInterval time.Duration

	BatchSize int // wallets/assets reconciled per pass
}

const (
	envEnabled      = "RECONCILIATION_ENABLED"
	envPollInterval = "RECONCILIATION_INTERVAL"
)

func DefaultConfig() Config {
	return Config{
		PollInterval: 300 * time.Second, // spec default: 300s
		BatchSize:    200,
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
			return Config{}, fmt.Errorf("reconciliation: %s inválido (mínimo 1s): %q", envPollInterval, v)
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
		return false, fmt.Errorf("reconciliation: %s inválido: %q", key, v)
	}
	return b, nil
}
