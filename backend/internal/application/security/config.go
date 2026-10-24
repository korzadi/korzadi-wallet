package security

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// RateLimitRule is one endpoint/operation's configurable rate limit
// (spec §8): at most Limit requests per Window, scoped per IP and/or per
// user depending on how the middleware is attached.
type RateLimitRule struct {
	Limit  int
	Window time.Duration
}

// Config is Bloque 8's own configuration, loaded from environment
// variables at startup. It never stores or logs secret VALUES — only
// booleans, numbers and durations.
type Config struct {
	// RateLimitEnabled is the master switch for the security rate
	// limiting layer (SECURITY_RATE_LIMIT_ENABLED, default true — fails
	// closed toward "protected", per spec §8 example).
	RateLimitEnabled bool

	RateLimits map[string]RateLimitRule // keyed by operation name: "login","2fa","withdrawal","transfer","deposit","admin","security"

	// TransfersEnabled is the kill switch for internal transfers (spec
	// §20 example, SECURITY_TRANSFERS_ENABLED) — transfers had no
	// equivalent to WITHDRAWALS_ENABLED / SWEEPS_ENABLED before Bloque 8.
	// Defaults to true: Bloque 8 must not silently disable a feature
	// that worked before it existed.
	TransfersEnabled bool
	// AdminOperationsEnabled: SECURITY_ADMIN_OPERATIONS_ENABLED, default true.
	AdminOperationsEnabled bool

	// VelocityThresholds configure the "many X in little time" counters
	// of spec §14. Each entry is (max events, window).
	Velocity map[string]RateLimitRule

	// NewDestinationStepUp: a withdrawal to a destination never used
	// before by this user requires step-up (spec §15 + §12).
	NewDestinationStepUp bool
}

const (
	envRateLimitEnabled     = "SECURITY_RATE_LIMIT_ENABLED"
	envTransfersEnabled     = "SECURITY_TRANSFERS_ENABLED"
	envAdminOpsEnabled      = "SECURITY_ADMIN_OPERATIONS_ENABLED"
	envNewDestinationStepUp = "SECURITY_NEW_DESTINATION_STEP_UP"
)

// defaultRateLimits are used whenever the matching SECURITY_RATE_LIMIT_*
// environment variable is not set — chosen conservatively, never so tight
// that normal usage would be blocked (spec §8: "no bloquear
// indiscriminadamente").
func defaultRateLimits() map[string]RateLimitRule {
	return map[string]RateLimitRule{
		"login":      {Limit: 10, Window: time.Minute},
		"2fa":        {Limit: 10, Window: time.Minute},
		"withdrawal": {Limit: 10, Window: time.Minute},
		"transfer":   {Limit: 20, Window: time.Minute},
		"deposit":    {Limit: 20, Window: time.Minute},
		"admin":      {Limit: 60, Window: time.Minute},
		"security":   {Limit: 20, Window: time.Minute},
	}
}

func defaultVelocity() map[string]RateLimitRule {
	return map[string]RateLimitRule{
		"withdrawals_per_user":      {Limit: 5, Window: 10 * time.Minute},
		"transfers_per_user":        {Limit: 15, Window: 10 * time.Minute},
		"failed_logins_per_user":    {Limit: 5, Window: 15 * time.Minute},
		"failed_2fa_per_user":       {Limit: 5, Window: 15 * time.Minute},
		"security_setting_changes":  {Limit: 5, Window: time.Hour},
		"new_destinations_per_user": {Limit: 3, Window: 24 * time.Hour},
	}
}

// DefaultConfig returns Bloque 8's built-in defaults: everything that
// already worked keeps working (fail open on the NEW kill switches this
// block introduces), while rate limiting and step-up-on-new-destination
// are on by default (fail closed on genuinely new protections).
func DefaultConfig() Config {
	return Config{
		RateLimitEnabled:       true,
		RateLimits:             defaultRateLimits(),
		TransfersEnabled:       true,
		AdminOperationsEnabled: true,
		Velocity:               defaultVelocity(),
		NewDestinationStepUp:   true,
	}
}

// LoadConfigFromEnv reads Bloque 8 configuration from the environment.
// Every SECURITY_RATE_LIMIT_<OP>=<limit>/<window> variable (e.g.
// SECURITY_RATE_LIMIT_WITHDRAWAL=5/1m) overrides one entry of RateLimits;
// an invalid value is a startup error (fail closed on misconfiguration,
// never silently ignored — spec §19).
func LoadConfigFromEnv() (Config, error) {
	cfg := DefaultConfig()

	var err error
	if cfg.RateLimitEnabled, err = envBool(envRateLimitEnabled, true); err != nil {
		return Config{}, err
	}
	if cfg.TransfersEnabled, err = envBool(envTransfersEnabled, true); err != nil {
		return Config{}, err
	}
	if cfg.AdminOperationsEnabled, err = envBool(envAdminOpsEnabled, true); err != nil {
		return Config{}, err
	}
	if cfg.NewDestinationStepUp, err = envBool(envNewDestinationStepUp, true); err != nil {
		return Config{}, err
	}

	for _, op := range []string{"LOGIN", "2FA", "WITHDRAWAL", "TRANSFER", "DEPOSIT", "ADMIN", "SECURITY"} {
		key := "SECURITY_RATE_LIMIT_" + op
		v := os.Getenv(key)
		if v == "" {
			continue
		}
		rule, perr := parseRateLimitRule(key, v)
		if perr != nil {
			return Config{}, perr
		}
		cfg.RateLimits[strings.ToLower(op)] = rule
	}

	return cfg, nil
}

// parseRateLimitRule parses "<limit>/<window>" such as "10/1m" or "5/30s".
func parseRateLimitRule(key, value string) (RateLimitRule, error) {
	parts := strings.SplitN(value, "/", 2)
	if len(parts) != 2 {
		return RateLimitRule{}, fmt.Errorf("security: %s inválido, formato esperado <limite>/<ventana> (ej. 10/1m): %q", key, value)
	}
	limit, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || limit <= 0 {
		return RateLimitRule{}, fmt.Errorf("security: %s tiene un límite inválido: %q", key, value)
	}
	window, err := time.ParseDuration(strings.TrimSpace(parts[1]))
	if err != nil || window <= 0 {
		return RateLimitRule{}, fmt.Errorf("security: %s tiene una ventana inválida: %q", key, value)
	}
	return RateLimitRule{Limit: limit, Window: window}, nil
}

func envBool(key string, def bool) (bool, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("security: %s inválido: %q", key, v)
	}
	return b, nil
}

// ---------------------------------------------------------------------
// Secret / configuration validation (spec §19)
// ---------------------------------------------------------------------

// RequiredSecret names one configuration value that must be present (and,
// optionally, of a minimum length) before the process is allowed to serve
// traffic. Value is passed in-memory only for the length/format check —
// ValidateSecrets never returns it, logs it, or includes it in an error.
type RequiredSecret struct {
	// Name is the environment variable name, used ONLY in error messages
	// (never the value).
	Name string
	// Value is the actual secret already read by the caller (e.g. via
	// os.Getenv or mustEnv) — kept out of this struct's own zero-value
	// error paths.
	Value string
	// MinLength: 0 means "just needs to be non-empty".
	MinLength int
}

// ValidateSecrets checks that every required secret is present and meets
// its minimum length, WITHOUT ever printing or wrapping the value itself.
// Errors only ever say "missing", "invalid" or "misconfigured" plus the
// variable NAME — never the secret (spec §19: "Los errores de
// configuración deben indicar únicamente: missing, invalid,
// misconfigured. Nunca el secreto.").
func ValidateSecrets(secrets ...RequiredSecret) error {
	var problems []string
	for _, s := range secrets {
		if strings.TrimSpace(s.Value) == "" {
			problems = append(problems, fmt.Sprintf("%s: missing", s.Name))
			continue
		}
		if s.MinLength > 0 && len(s.Value) < s.MinLength {
			problems = append(problems, fmt.Sprintf("%s: invalid (too short)", s.Name))
			continue
		}
		if looksLikePlaceholder(s.Value) {
			problems = append(problems, fmt.Sprintf("%s: misconfigured (placeholder value)", s.Name))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%w: %s", ErrConfigMissingSecret, strings.Join(problems, "; "))
	}
	return nil
}

// looksLikePlaceholder catches the handful of values people paste from
// .env.example and forget to change — never a substitute for real secret
// strength checking, just a cheap safety net.
func looksLikePlaceholder(v string) bool {
	lower := strings.ToLower(strings.TrimSpace(v))
	switch lower {
	case "changeme", "change_me", "changeit", "secret", "password", "test", "example", "todo", "xxxx":
		return true
	}
	return false
}
