package compliance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/domain"
)

// UserResolver is satisfied by domain.UserRepository.
type UserResolver interface {
	FindByID(ctx context.Context, id uuid.UUID) (*domain.User, error)
}

// Service is the application entry point for KYC, AML flags/holds/risk and
// screening. It never touches the Ledger, wallet_accounts or balances.
type Service struct {
	store    Store
	cfg      Config
	reg      *Registry
	auditLog domain.AuditLogger // optional
	users    UserResolver       // optional (only needed for screening names)
	now      func() time.Time
}

func NewService(store Store, cfg Config, reg *Registry, audit domain.AuditLogger, users UserResolver) *Service {
	if reg == nil {
		reg = NewRegistry()
	}
	return &Service{store: store, cfg: cfg, reg: reg, auditLog: audit, users: users, now: func() time.Time { return time.Now().UTC() }}
}

// SetClock overrides the clock (tests).
func (s *Service) SetClock(f func() time.Time) { s.now = f }

func (s *Service) Config() Config { return s.cfg }

func hashParts(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x1f")))
	return hex.EncodeToString(h[:])
}

// claim wraps Tx.ClaimIdempotency. Empty keys are rejected: every critical
// operation requires one (spec §21).
func claim(ctx context.Context, tx Tx, scope string, actorID uuid.UUID, key string, parts ...string) (*uuid.UUID, bool, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, false, ErrIdempotencyKeyRequired
	}
	if len(key) > 200 {
		return nil, false, ErrInvalidArgument
	}
	return tx.ClaimIdempotency(ctx, scope, actorID, key, hashParts(parts...))
}

func actorID(a Actor) uuid.UUID {
	if a.UserID != nil {
		return *a.UserID
	}
	return uuid.Nil
}

func validReason(r string) (string, error) {
	r = strings.TrimSpace(r)
	if r == "" {
		return "", ErrReasonRequired
	}
	if len(r) > maxAuditString {
		return "", ErrInvalidArgument
	}
	return r, nil
}

// EffectiveStatus derives the single KYC status the rest of the system
// acts on from a user's current (non-superseded) cases. Fail-safe: a
// VERIFIED case past expires_at counts as EXPIRED even if the expiration
// worker has not run yet.
func EffectiveStatus(cases []*KYCCase, now time.Time) KYCStatus {
	var (
		suspended, verifiedValid, verifiedExpired, rejected bool
		best                                                KYCStatus
	)
	rank := map[KYCStatus]int{KYCNotStarted: 1, KYCPending: 2, KYCInReview: 3}
	for _, c := range cases {
		if c.SupersededAt != nil {
			continue
		}
		switch c.Status {
		case KYCSuspended:
			suspended = true
		case KYCVerified:
			if c.ExpiresAt != nil && !c.ExpiresAt.After(now) {
				verifiedExpired = true
			} else {
				verifiedValid = true
			}
		case KYCExpired:
			verifiedExpired = true
		case KYCRejected:
			rejected = true
		default:
			if rank[c.Status] > rank[best] {
				best = c.Status
			}
		}
	}
	switch {
	case suspended:
		return KYCSuspended
	case verifiedValid:
		return KYCVerified
	case best != "":
		return best
	case verifiedExpired:
		return KYCExpired
	case rejected:
		return KYCRejected
	}
	return KYCNotStarted
}

func sortCasesNewestFirst(cs []*KYCCase) {
	sort.SliceStable(cs, func(i, j int) bool { return cs[i].CreatedAt.After(cs[j].CreatedAt) })
}
