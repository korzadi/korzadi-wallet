package security

import (
	"context"
	"time"

	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/domain"
)

// EventType is the closed vocabulary of spec §16.
type EventType string

const (
	EventLoginFailed             EventType = "LOGIN_FAILED"
	EventLoginRateLimited        EventType = "LOGIN_RATE_LIMITED"
	EventTwoFactorFailed         EventType = "TWO_FACTOR_FAILED"
	EventWithdrawalLimitRejected EventType = "WITHDRAWAL_LIMIT_REJECTED"
	EventTransferLimitRejected   EventType = "TRANSFER_LIMIT_REJECTED"
	EventAuthorizationDenied     EventType = "AUTHORIZATION_DENIED"
	EventIDORAttempt             EventType = "IDOR_ATTEMPT"
	EventRateLimitTriggered      EventType = "RATE_LIMIT_TRIGGERED"
	EventNewDestination          EventType = "NEW_DESTINATION"
	EventHighValueOperation      EventType = "HIGH_VALUE_OPERATION"
	EventAdminAction             EventType = "ADMIN_ACTION"
	EventLedgerAdjustment        EventType = "LEDGER_ADJUSTMENT"
	EventSecuritySettingChanged  EventType = "SECURITY_SETTING_CHANGED"
	EventLimitChanged            EventType = "LIMIT_CHANGED"
	EventKillSwitchActivated     EventType = "KILL_SWITCH_ACTIVATED"
)

// secretLikeKeys is the deny-list of metadata keys that must never reach
// security_events or audit_logs, mirroring spec §16/§17's explicit list.
// This is a second line of defense — callers should never pass these in
// the first place — implemented as a filter rather than trusted
// discipline alone.
var secretLikeKeys = map[string]bool{
	"private_key": true, "privatekey": true,
	"jwt": true, "token": true, "access_token": true, "refresh_token": true,
	"password": true, "password_hash": true,
	"2fa_secret": true, "totp_secret": true, "two_factor_secret": true,
	"seed": true, "mnemonic": true,
	"encrypted_secret": true, "secret": true,
}

// Event is one row of security_events plus everything an EventRepository
// needs to persist it.
type Event struct {
	Type         EventType
	Severity     string // "info" | "warning" | "critical"; defaults to "info"
	UserID       *uuid.UUID
	ActorRole    string
	Operation    OperationType
	ResourceType string
	ResourceID   *uuid.UUID
	IP           string
	RequestID    string
	Metadata     map[string]any
	CreatedAt    time.Time
}

// EventRepository persists security_events rows.
type EventRepository interface {
	Insert(ctx context.Context, e Event) error
	// List returns the most recent events, optionally filtered by userID
	// (nil = all users) — used by the admin security dashboard.
	List(ctx context.Context, userID *uuid.UUID, limit, offset int) ([]Event, error)
}

// EventLogger records a security event to security_events AND, for
// anything that also matters for the general admin audit trail (spec
// §17), forwards it through the existing domain.AuditLogger — never a
// second, competing audit mechanism, just fan-out of the same fact.
type EventLogger struct {
	repo  EventRepository
	audit domain.AuditLogger // optional; nil is fine (mirrors transfer.Service's own audit field)
}

func NewEventLogger(repo EventRepository, audit domain.AuditLogger) *EventLogger {
	return &EventLogger{repo: repo, audit: audit}
}

// Log records e, stripping any metadata key that looks like a secret
// before it is ever written or forwarded. It never returns an error to
// avoid a logging failure blocking the sensitive operation it describes —
// callers that must react to logging failures should treat this the same
// way the rest of the codebase treats domain.AuditLogger: best-effort.
func (l *EventLogger) Log(ctx context.Context, e Event) error {
	if e.Severity == "" {
		e.Severity = "info"
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	e.Metadata = sanitizeMetadata(e.Metadata)

	var firstErr error
	if l.repo != nil {
		if err := l.repo.Insert(ctx, e); err != nil {
			firstErr = err
		}
	}
	if l.audit != nil {
		actorRole := e.ActorRole
		if actorRole == "" {
			actorRole = "system"
		}
		_ = l.audit.Log(ctx, e.UserID, actorRole, string(e.Type), e.ResourceType, e.ResourceID, e.Metadata, e.IP)
	}
	return firstErr
}

// sanitizeMetadata returns a shallow copy of meta with any key that
// matches secretLikeKeys (case-insensitively) removed entirely — not
// masked, removed, so a bug that accidentally attaches a secret never
// even shows its presence/length.
func sanitizeMetadata(meta map[string]any) map[string]any {
	if meta == nil {
		return nil
	}
	out := make(map[string]any, len(meta))
	for k, v := range meta {
		if secretLikeKeys[normalizeKey(k)] {
			continue
		}
		out[k] = v
	}
	return out
}

func normalizeKey(k string) string {
	out := make([]byte, 0, len(k))
	for i := 0; i < len(k); i++ {
		c := k[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out = append(out, c)
	}
	return string(out)
}
