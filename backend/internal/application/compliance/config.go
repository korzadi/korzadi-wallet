package compliance

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// Config holds every Bloque 9 setting. Everything is OFF / fail-closed by
// default: nothing here activates a provider that does not exist.
type Config struct {
	KYCEnabled            bool
	KYCProvider           string
	KYCWebhookSecret      string // never logged
	WebhookTolerance      time.Duration
	AMLEnabled            bool
	SanctionsEnabled      bool
	SanctionsProvider     string
	WorkersEnabled        bool
	WorkerInterval        time.Duration
	KYCRenewalWindow      time.Duration   // 0 = renewal only after EXPIRED/REJECTED
	KYCDefaultValidity    time.Duration   // 0 = no expiry unless provider sends one
	LimitedTransferMax    decimal.Decimal // KYC_LIMITED_TRANSFER_MAX_AMOUNT; 0 = no limited tier (deny)
	WithdrawalRequiresKYC bool
	AutoHoldOnCritical    bool
	SanctionsAutoHold     bool
	ScreeningMaxAttempts  int
	ScreeningRetryBackoff time.Duration
	Retention             RetentionPolicy
}

// RetentionPolicy: 0 means "retain indefinitely / not yet decided". The real
// periods are a LEGAL decision per jurisdiction and are intentionally not
// defaulted here (see docs/KYC_AML.md).
type RetentionPolicy struct {
	KYCHistoryDays      int
	WebhookEventDays    int
	ScreeningResultDays int
	ClosedFlagDays      int
	AnonymizeAfterDays  int
}

// LoadConfig reads configuration through getenv (os.Getenv in production)
// and validates it. It never prints secret values.
func LoadConfig(getenv func(string) string) (Config, error) {
	c := Config{
		WebhookTolerance:      5 * time.Minute,
		WorkerInterval:        time.Minute,
		WithdrawalRequiresKYC: true,
		AutoHoldOnCritical:    true,
		SanctionsAutoHold:     true,
		ScreeningMaxAttempts:  5,
		ScreeningRetryBackoff: time.Minute,
	}
	b := func(k string, def bool) (bool, error) {
		v := strings.TrimSpace(getenv(k))
		if v == "" {
			return def, nil
		}
		p, err := strconv.ParseBool(v)
		if err != nil {
			return false, fmt.Errorf("compliance: %s inválido", k)
		}
		return p, nil
	}
	i := func(k string, def int) (int, error) {
		v := strings.TrimSpace(getenv(k))
		if v == "" {
			return def, nil
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("compliance: %s inválido", k)
		}
		return n, nil
	}
	var err error
	if c.KYCEnabled, err = b("KYC_ENABLED", false); err != nil {
		return c, err
	}
	if c.AMLEnabled, err = b("AML_ENABLED", false); err != nil {
		return c, err
	}
	if c.SanctionsEnabled, err = b("SANCTIONS_SCREENING_ENABLED", false); err != nil {
		return c, err
	}
	if c.WorkersEnabled, err = b("COMPLIANCE_WORKERS_ENABLED", false); err != nil {
		return c, err
	}
	if c.WithdrawalRequiresKYC, err = b("KYC_WITHDRAWAL_REQUIRES_VERIFIED", true); err != nil {
		return c, err
	}
	if c.AutoHoldOnCritical, err = b("AML_AUTO_HOLD_ON_CRITICAL", true); err != nil {
		return c, err
	}
	if c.SanctionsAutoHold, err = b("SANCTIONS_AUTO_HOLD", true); err != nil {
		return c, err
	}
	c.KYCProvider = strings.TrimSpace(getenv("KYC_PROVIDER"))
	c.KYCWebhookSecret = getenv("KYC_WEBHOOK_SECRET")
	c.SanctionsProvider = strings.TrimSpace(getenv("SANCTIONS_PROVIDER"))

	var n int
	if n, err = i("KYC_WEBHOOK_TOLERANCE_SECONDS", 300); err != nil {
		return c, err
	}
	c.WebhookTolerance = time.Duration(n) * time.Second
	if n, err = i("COMPLIANCE_WORKER_INTERVAL_SECONDS", 60); err != nil {
		return c, err
	}
	c.WorkerInterval = time.Duration(n) * time.Second
	if n, err = i("KYC_RENEWAL_WINDOW_DAYS", 0); err != nil {
		return c, err
	}
	c.KYCRenewalWindow = time.Duration(n) * 24 * time.Hour
	if n, err = i("KYC_DEFAULT_VALIDITY_DAYS", 0); err != nil {
		return c, err
	}
	c.KYCDefaultValidity = time.Duration(n) * 24 * time.Hour
	if c.ScreeningMaxAttempts, err = i("SCREENING_MAX_ATTEMPTS", 5); err != nil {
		return c, err
	}
	if n, err = i("SCREENING_RETRY_BACKOFF_SECONDS", 60); err != nil {
		return c, err
	}
	c.ScreeningRetryBackoff = time.Duration(n) * time.Second

	if v := strings.TrimSpace(getenv("KYC_LIMITED_TRANSFER_MAX_AMOUNT")); v != "" {
		d, derr := decimal.NewFromString(v)
		if derr != nil || d.IsNegative() {
			return c, fmt.Errorf("compliance: KYC_LIMITED_TRANSFER_MAX_AMOUNT inválido")
		}
		c.LimitedTransferMax = d
	}
	if c.Retention.KYCHistoryDays, err = i("RETENTION_KYC_HISTORY_DAYS", 0); err != nil {
		return c, err
	}
	if c.Retention.WebhookEventDays, err = i("RETENTION_WEBHOOK_EVENT_DAYS", 0); err != nil {
		return c, err
	}
	if c.Retention.ScreeningResultDays, err = i("RETENTION_SCREENING_RESULT_DAYS", 0); err != nil {
		return c, err
	}
	if c.Retention.ClosedFlagDays, err = i("RETENTION_CLOSED_FLAG_DAYS", 0); err != nil {
		return c, err
	}
	if c.Retention.AnonymizeAfterDays, err = i("RETENTION_ANONYMIZE_AFTER_DAYS", 0); err != nil {
		return c, err
	}
	return c, c.Validate()
}

// Validate enforces fail-safe rules: enabling KYC requires a named provider
// and a webhook secret; enabling screening requires a named provider.
func (c Config) Validate() error {
	if c.KYCEnabled {
		if c.KYCProvider == "" {
			return fmt.Errorf("compliance: KYC_ENABLED=true requiere KYC_PROVIDER")
		}
		if len(c.KYCWebhookSecret) < 16 {
			return fmt.Errorf("compliance: KYC_ENABLED=true requiere KYC_WEBHOOK_SECRET (>=16 caracteres)")
		}
	}
	if c.SanctionsEnabled && c.SanctionsProvider == "" {
		return fmt.Errorf("compliance: SANCTIONS_SCREENING_ENABLED=true requiere SANCTIONS_PROVIDER")
	}
	if c.WebhookTolerance <= 0 {
		return fmt.Errorf("compliance: KYC_WEBHOOK_TOLERANCE_SECONDS debe ser > 0")
	}
	return nil
}
