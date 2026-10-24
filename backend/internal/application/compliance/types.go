// Package compliance implements Bloque 9 — KYC / AML / Risk foundation.
//
// Scope: identity-verification STATE (not identity data), internal risk
// flags/holds, screening abstractions and the policy that lets the existing
// Bloque 8 security layer ask "may this user perform this operation?".
//
// It deliberately does NOT: touch the Ledger or any balance, store identity
// documents/images/biometrics, invent a real provider, or make regulatory
// decisions. Holds and flags are internal control states that require human
// review; they never accuse a user of anything.
package compliance

import (
	"time"

	"github.com/google/uuid"
)

// ---- KYC status ----

type KYCStatus string

const (
	KYCNotStarted KYCStatus = "NOT_STARTED"
	KYCPending    KYCStatus = "PENDING"
	KYCInReview   KYCStatus = "IN_REVIEW"
	KYCVerified   KYCStatus = "VERIFIED"
	KYCRejected   KYCStatus = "REJECTED"
	KYCExpired    KYCStatus = "EXPIRED"
	KYCSuspended  KYCStatus = "SUSPENDED"
)

func (s KYCStatus) Valid() bool {
	switch s {
	case KYCNotStarted, KYCPending, KYCInReview, KYCVerified, KYCRejected, KYCExpired, KYCSuspended:
		return true
	}
	return false
}

// InFlight reports statuses in which a case is still being processed.
func (s KYCStatus) InFlight() bool {
	return s == KYCNotStarted || s == KYCPending || s == KYCInReview
}

// ---- Risk ----

type RiskLevel string

const (
	RiskLow      RiskLevel = "LOW"
	RiskMedium   RiskLevel = "MEDIUM"
	RiskHigh     RiskLevel = "HIGH"
	RiskCritical RiskLevel = "CRITICAL"
)

func (r RiskLevel) Valid() bool { return r.rank() > 0 }

func (r RiskLevel) rank() int {
	switch r {
	case RiskLow:
		return 1
	case RiskMedium:
		return 2
	case RiskHigh:
		return 3
	case RiskCritical:
		return 4
	}
	return 0
}

// ---- Flags ----

type FlagType string

const (
	FlagVelocity          FlagType = "VELOCITY"
	FlagHighValue         FlagType = "HIGH_VALUE"
	FlagSanctionsReview   FlagType = "SANCTIONS_REVIEW"
	FlagKYCMismatch       FlagType = "KYC_MISMATCH"
	FlagSuspiciousPattern FlagType = "SUSPICIOUS_PATTERN"
	FlagMultipleAccount   FlagType = "MULTIPLE_ACCOUNT"
	FlagManualReview      FlagType = "MANUAL_REVIEW"
	FlagProviderAlert     FlagType = "PROVIDER_ALERT"
)

func (t FlagType) Valid() bool {
	switch t {
	case FlagVelocity, FlagHighValue, FlagSanctionsReview, FlagKYCMismatch,
		FlagSuspiciousPattern, FlagMultipleAccount, FlagManualReview, FlagProviderAlert:
		return true
	}
	return false
}

type FlagStatus string

const (
	FlagOpen      FlagStatus = "OPEN"
	FlagResolved  FlagStatus = "RESOLVED"
	FlagDismissed FlagStatus = "DISMISSED"
)

type FlagSource string

const (
	SourceAutomated FlagSource = "AUTOMATED"
	SourceManual    FlagSource = "MANUAL"
	SourceProvider  FlagSource = "PROVIDER"
	SourceScreening FlagSource = "SCREENING"
)

// ---- Holds ----

type HoldType string

const (
	HoldCompliance     HoldType = "COMPLIANCE_HOLD"
	HoldSanctionReview HoldType = "SANCTIONS_REVIEW"
	HoldHighRisk       HoldType = "HIGH_RISK_REVIEW"
)

func (h HoldType) Valid() bool {
	return h == HoldCompliance || h == HoldSanctionReview || h == HoldHighRisk
}

type HoldStatus string

const (
	HoldActive   HoldStatus = "ACTIVE"
	HoldReleased HoldStatus = "RELEASED"
)

// ---- Screening ----

type ScreeningKind string

const (
	ScreenSanctions    ScreeningKind = "SANCTIONS"
	ScreenPEP          ScreeningKind = "PEP"
	ScreenAdverseMedia ScreeningKind = "ADVERSE_MEDIA"
)

func (k ScreeningKind) Valid() bool {
	return k == ScreenSanctions || k == ScreenPEP || k == ScreenAdverseMedia
}

type MatchStatus string

const (
	MatchNone           MatchStatus = "NO_MATCH"
	MatchPotential      MatchStatus = "POTENTIAL_MATCH"
	MatchConfirmed      MatchStatus = "CONFIRMED_MATCH"
	MatchReviewRequired MatchStatus = "REVIEW_REQUIRED"
)

func (m MatchStatus) Valid() bool {
	return m == MatchNone || m == MatchPotential || m == MatchConfirmed || m == MatchReviewRequired
}

type ReviewStatus string

const (
	ReviewNotRequired ReviewStatus = "NOT_REQUIRED"
	ReviewRequired    ReviewStatus = "REVIEW_REQUIRED"
	ReviewCleared     ReviewStatus = "CLEARED"
	ReviewConfirmed   ReviewStatus = "CONFIRMED"
)

type ScreeningRequestStatus string

const (
	ScreenReqPending   ScreeningRequestStatus = "PENDING"
	ScreenReqCompleted ScreeningRequestStatus = "COMPLETED"
	ScreenReqFailed    ScreeningRequestStatus = "FAILED"
	ScreenReqExhausted ScreeningRequestStatus = "EXHAUSTED"
)

// ---- Actors ----

type ActorType string

const (
	ActorUser     ActorType = "USER"
	ActorAdmin    ActorType = "ADMIN"
	ActorProvider ActorType = "PROVIDER"
	ActorSystem   ActorType = "SYSTEM"
)

// Actor identifies who causes a change. UserID is nil for provider/system.
type Actor struct {
	Type      ActorType
	UserID    *uuid.UUID
	Roles     []string // roles resolved server-side from the DB, never from the request
	Ref       string   // provider or worker name
	IP        string
	RequestID string
}

// Compliance role names (rows in the existing roles table; see 0016).
const (
	RoleReviewer = "compliance_reviewer"
	RoleAdmin    = "compliance_admin"
)

func (a Actor) hasRole(r string) bool {
	for _, x := range a.Roles {
		if x == r {
			return true
		}
	}
	return false
}

// IsComplianceAdmin: may decide (approve/reject/suspend/hold/release/resolve).
// admin/super_admin are deliberately NOT implied (separation of duties).
func (a Actor) IsComplianceAdmin() bool { return a.Type == ActorAdmin && a.hasRole(RoleAdmin) }

// IsReviewer: may view and move cases into review. Compliance admins too.
func (a Actor) IsReviewer() bool {
	return a.Type == ActorAdmin && (a.hasRole(RoleReviewer) || a.hasRole(RoleAdmin))
}

// ---- Entities ----

type KYCCase struct {
	ID                  uuid.UUID
	UserID              uuid.UUID
	Status              KYCStatus
	Provider            string
	ProviderReference   string
	Country             string
	ResidenceCountry    string
	DocumentType        string
	DocumentCountry     string
	DocumentHash        string
	SubmittedAt         *time.Time
	StartedAt           *time.Time
	VerifiedAt          *time.Time
	RejectedAt          *time.Time
	ExpiresAt           *time.Time
	RejectionReasonCode string
	ReviewReasonCode    string
	RiskLevel           RiskLevel
	LastProviderEventAt *time.Time
	SupersededAt        *time.Time
	Version             int
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type StatusHistory struct {
	ID          uuid.UUID
	CaseID      uuid.UUID
	UserID      uuid.UUID
	OldStatus   KYCStatus // "" for the first row
	NewStatus   KYCStatus
	ActorType   ActorType
	ActorUserID *uuid.UUID
	ActorRef    string
	ReasonCode  string
	Reason      string
	RequestID   string
	CreatedAt   time.Time
}

type WebhookProcessing string

const (
	WebhookApplied                  WebhookProcessing = "APPLIED"
	WebhookIgnoredNoop              WebhookProcessing = "IGNORED_NOOP"
	WebhookIgnoredStale             WebhookProcessing = "IGNORED_STALE"
	WebhookIgnoredInvalidTransition WebhookProcessing = "IGNORED_INVALID_TRANSITION"
	WebhookIgnoredUnknownReference  WebhookProcessing = "IGNORED_UNKNOWN_REFERENCE"
)

type WebhookEvent struct {
	ID                uuid.UUID
	Provider          string
	EventID           string
	EventType         string
	ProviderReference string
	OccurredAt        *time.Time
	PayloadHash       string
	Processing        WebhookProcessing
	CaseID            *uuid.UUID
}

type Flag struct {
	ID               uuid.UUID
	UserID           uuid.UUID
	Type             FlagType
	Severity         RiskLevel
	Status           FlagStatus
	Source           FlagSource
	ReasonCode       string
	Description      string
	CreatedBy        *uuid.UUID
	CreatedAt        time.Time
	ResolvedAt       *time.Time
	ResolvedBy       *uuid.UUID
	ResolutionReason string
	Version          int
}

type Hold struct {
	ID            uuid.UUID
	UserID        uuid.UUID
	Type          HoldType
	Status        HoldStatus
	ReasonCode    string
	Reason        string
	PlacedBy      *uuid.UUID
	PlacedAt      time.Time
	ReleasedBy    *uuid.UUID
	ReleasedAt    *time.Time
	ReleaseReason string
	Version       int
}

type RiskProfile struct {
	UserID      uuid.UUID
	Level       RiskLevel
	ReasonCodes []string
	Version     int
}

type ScreeningRequest struct {
	ID                uuid.UUID
	UserID            uuid.UUID
	Kind              ScreeningKind
	Status            ScreeningRequestStatus
	IdempotencyKey    string
	Provider          string
	ProviderReference string
	AttemptCount      int
	NextRetryAt       *time.Time
	LastErrorCode     string
	RequestedBy       *uuid.UUID
	CreatedAt         time.Time
}

type ScreeningResult struct {
	ID                uuid.UUID
	RequestID         uuid.UUID
	UserID            uuid.UUID
	Kind              ScreeningKind
	Match             MatchStatus
	Review            ReviewStatus
	ProviderReference string
	ReviewedBy        *uuid.UUID
	ReviewedAt        *time.Time
	ReviewReason      string
	ScreenedAt        time.Time
	Version           int
}
