package compliance

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// RiskHistoryEntry is one immutable row of risk_level_history.
type RiskHistoryEntry struct {
	UserID      uuid.UUID
	OldLevel    RiskLevel
	NewLevel    RiskLevel
	ReasonCodes []string
	Source      string // AUTOMATED | MANUAL | PROVIDER
	ActorUserID *uuid.UUID
	Reason      string
	RequestID   string
}

// Store is the persistence port. WithTx MUST run fn in one database
// transaction (commit on nil, rollback on error). Row locking (SELECT ...
// FOR UPDATE), optimistic versions and unique constraints live behind it —
// never an in-process mutex (spec §22).
type Store interface {
	WithTx(ctx context.Context, fn func(tx Tx) error) error

	// Non-locking reads.
	CurrentCases(ctx context.Context, userID uuid.UUID) ([]*KYCCase, error) // superseded_at IS NULL
	AllCases(ctx context.Context, userID uuid.UUID) ([]*KYCCase, error)
	History(ctx context.Context, userID uuid.UUID, limit int) ([]*StatusHistory, error)
	ActiveHolds(ctx context.Context, userID uuid.UUID) ([]*Hold, error)
	OpenFlags(ctx context.Context, userID uuid.UUID, limit int) ([]*Flag, error)
	RiskProfileOf(ctx context.Context, userID uuid.UUID) (*RiskProfile, error) // nil if none
	ScreeningResults(ctx context.Context, userID uuid.UUID, limit int) ([]*ScreeningResult, error)
}

// Tx is the transactional surface. Every Get*ForUpdate takes a row lock.
type Tx interface {
	// LockUser serializes compliance mutations for one user
	// (pg_advisory_xact_lock) — protects "no row yet" races such as a
	// double start.
	LockUser(ctx context.Context, userID uuid.UUID) error

	// ClaimIdempotency records (scope, actor, key). replay=true means the
	// same key+hash was already used; resourceID is what it produced.
	// Same key with a different requestHash -> ErrIdempotencyKeyReuse.
	ClaimIdempotency(ctx context.Context, scope string, actorID uuid.UUID, key, requestHash string) (resourceID *uuid.UUID, replay bool, err error)
	SetIdempotencyResource(ctx context.Context, scope string, actorID uuid.UUID, key string, resourceID uuid.UUID) error

	// KYC cases.
	CurrentCasesForUpdate(ctx context.Context, userID uuid.UUID) ([]*KYCCase, error)
	GetCaseForUpdate(ctx context.Context, id uuid.UUID) (*KYCCase, error) // ErrCaseNotFound
	// FindCaseByProviderRef is a NON-locking lookup (nil,nil if none). Callers
	// then LockUser and GetCaseForUpdate, keeping one global lock order:
	// user advisory lock -> case rows (ordered by id).
	FindCaseByProviderRef(ctx context.Context, provider, ref string) (*KYCCase, error)
	InsertCase(ctx context.Context, c *KYCCase) error // ErrActiveCaseExists on unique violation
	// UpdateCase writes c guarded by c.Version (must equal the stored
	// version); on success c.Version is incremented. Mismatch -> ErrConcurrentUpdate.
	UpdateCase(ctx context.Context, c *KYCCase) error
	InsertHistory(ctx context.Context, h *StatusHistory) error
	DueForExpiryForUpdate(ctx context.Context, now time.Time, limit int) ([]*KYCCase, error) // SKIP LOCKED

	// Webhooks.
	InsertWebhookEvent(ctx context.Context, e *WebhookEvent) (inserted bool, err error)
	SetWebhookOutcome(ctx context.Context, provider, eventID string, p WebhookProcessing, caseID *uuid.UUID) error

	// Flags.
	// InsertFlag dedupes OPEN flags on (user,type,reason): created=false and
	// the existing flag is returned when one is already open.
	InsertFlag(ctx context.Context, f *Flag) (out *Flag, created bool, err error)
	GetFlagForUpdate(ctx context.Context, id uuid.UUID) (*Flag, error) // ErrFlagNotFound
	UpdateFlag(ctx context.Context, f *Flag) error

	// Holds.
	ActiveHoldForUpdate(ctx context.Context, userID uuid.UUID, t HoldType) (*Hold, error) // nil,nil if none
	InsertHold(ctx context.Context, h *Hold) error
	GetHoldForUpdate(ctx context.Context, id uuid.UUID) (*Hold, error) // ErrHoldNotFound
	UpdateHold(ctx context.Context, h *Hold) error

	// Risk.
	RiskProfileForUpdate(ctx context.Context, userID uuid.UUID) (*RiskProfile, error) // nil if none
	UpsertRiskProfile(ctx context.Context, p *RiskProfile) error
	InsertRiskHistory(ctx context.Context, e *RiskHistoryEntry) error

	// Screening.
	InsertScreeningRequest(ctx context.Context, r *ScreeningRequest) (out *ScreeningRequest, created bool, err error)
	GetScreeningRequestForUpdate(ctx context.Context, id uuid.UUID) (*ScreeningRequest, error)
	UpdateScreeningRequest(ctx context.Context, r *ScreeningRequest) error
	RetryableScreeningForUpdate(ctx context.Context, now time.Time, limit int) ([]*ScreeningRequest, error) // SKIP LOCKED
	InsertScreeningResult(ctx context.Context, r *ScreeningResult) error
	ScreeningResultByRequest(ctx context.Context, requestID uuid.UUID) (*ScreeningResult, error) // nil if none
	GetScreeningResultForUpdate(ctx context.Context, id uuid.UUID) (*ScreeningResult, error)     // ErrScreeningNotFound
	UpdateScreeningResult(ctx context.Context, r *ScreeningResult) error
}

// ---- Provider abstractions (no real provider exists yet) ----

// CreateVerificationInput carries only what the provider needs. IdempotencyKey
// is the internal case ID so a retry can never create two provider-side
// verifications.
type CreateVerificationInput struct {
	UserID           uuid.UUID
	CaseID           uuid.UUID
	IdempotencyKey   string
	Country          string
	ResidenceCountry string
}

type CreateVerificationResult struct {
	ProviderReference string
	// RedirectURL / SDK token to hand to the client so the USER completes
	// the verification at the provider (documents never touch our servers).
	RedirectURL string
}

type ProviderStatus struct {
	Status     KYCStatus
	ReasonCode string
	ExpiresAt  *time.Time
}

// Normalized provider event types (spec §5).
const (
	EvtCreated   = "verification.created"
	EvtPending   = "verification.pending"
	EvtInReview  = "verification.in_review"
	EvtApproved  = "verification.approved"
	EvtRejected  = "verification.rejected"
	EvtExpired   = "verification.expired"
	EvtSuspended = "verification.suspended"
)

// ProviderEvent is the provider-agnostic, already-parsed webhook. It is only
// produced AFTER the signature was verified.
type ProviderEvent struct {
	EventID           string
	Type              string
	ProviderReference string
	OccurredAt        time.Time // zero if the provider does not supply one
	ReasonCode        string
	ExpiresAt         *time.Time
	Country           string
	ResidenceCountry  string
	DocumentType      string
	DocumentCountry   string
	DocumentHash      string // hash only; never the document number
}

// KYCProvider abstracts an external identity-verification vendor.
type KYCProvider interface {
	Name() string
	CreateVerification(ctx context.Context, in CreateVerificationInput) (*CreateVerificationResult, error)
	GetVerificationStatus(ctx context.Context, providerReference string) (*ProviderStatus, error)
	// CancelVerification may return ErrProviderUnavailable if unsupported.
	CancelVerification(ctx context.Context, providerReference string) error
	// VerifyWebhookSignature authenticates the raw body using the shared
	// secret and returns the signed timestamp if the scheme has one (zero
	// otherwise). It must use constant-time comparison.
	VerifyWebhookSignature(headers map[string]string, body []byte, secret string) (signedAt time.Time, err error)
	// HandleWebhook parses an already-authenticated body into a normalized
	// event. It must have no side effects.
	HandleWebhook(ctx context.Context, body []byte) (*ProviderEvent, error)
}

// ScreeningSubject is what a screener may see. Personal data is passed to
// the provider only and is NOT persisted by this system.
type ScreeningSubject struct {
	UserID    uuid.UUID
	RequestID uuid.UUID // use as the provider-side idempotency key
	FullName  string
	Country   string
}

type ScreeningOutcome struct {
	Match             MatchStatus
	ProviderReference string
}

// Screener is the common shape of the three screening abstractions.
type Screener interface {
	Name() string
	Screen(ctx context.Context, s ScreeningSubject) (*ScreeningOutcome, error)
}

// SanctionsProvider, PEPProvider and AdverseMediaProvider are distinct
// types so a vendor can implement only what it supports. No sanctions list
// is bundled or downloaded here (spec §9).
type SanctionsProvider interface{ Screener }
type PEPProvider interface{ Screener }
type AdverseMediaProvider interface{ Screener }

// TravelRuleCarrier is a CONTRACT ONLY (spec §11): nothing implements or
// calls it, and no operation is blocked because of it.
type TravelRuleCarrier interface {
	Name() string
	AttachTravelRuleData(ctx context.Context, transferRef string, data TravelRuleData) error
}

type TravelRuleData struct {
	OriginatorRef  string // opaque reference held by the provider
	BeneficiaryRef string
	Extra          map[string]string
}

// Registry holds the providers wired at startup. It starts EMPTY: no
// provider is active unless main explicitly registers one.
type Registry struct {
	kyc       map[string]KYCProvider
	screeners map[ScreeningKind]Screener
}

func NewRegistry() *Registry {
	return &Registry{kyc: map[string]KYCProvider{}, screeners: map[ScreeningKind]Screener{}}
}

func (r *Registry) RegisterKYC(p KYCProvider) { r.kyc[p.Name()] = p }

func (r *Registry) RegisterScreener(kind ScreeningKind, s Screener) { r.screeners[kind] = s }

func (r *Registry) KYC(name string) (KYCProvider, bool) {
	if r == nil {
		return nil, false
	}
	p, ok := r.kyc[name]
	return p, ok
}

func (r *Registry) Screener(kind ScreeningKind) (Screener, bool) {
	if r == nil {
		return nil, false
	}
	s, ok := r.screeners[kind]
	return s, ok
}
