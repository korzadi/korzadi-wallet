package compliancetest

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/application/compliance"
)

// ---------------- fake KYC provider ----------------

// FakeKYCProvider signs webhooks as: x-fake-timestamp (unix seconds) and
// x-fake-signature = hex(HMAC-SHA256(secret, ts + "." + body)).
type FakeKYCProvider struct {
	NameValue string

	mu       sync.Mutex
	Fail     bool
	Created  int
	byKey    map[string]string
	Canceled []string
}

func NewFakeKYCProvider(name string) *FakeKYCProvider {
	return &FakeKYCProvider{NameValue: name, byKey: map[string]string{}}
}

func (f *FakeKYCProvider) Name() string { return f.NameValue }

func (f *FakeKYCProvider) CreateVerification(_ context.Context, in compliance.CreateVerificationInput) (*compliance.CreateVerificationResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Fail {
		return nil, errors.New("provider down")
	}
	ref, ok := f.byKey[in.IdempotencyKey] // provider-side idempotency
	if !ok {
		f.Created++
		ref = "fake-" + in.IdempotencyKey
		f.byKey[in.IdempotencyKey] = ref
	}
	return &compliance.CreateVerificationResult{ProviderReference: ref, RedirectURL: "https://fake.invalid/verify/" + ref}, nil
}

func (f *FakeKYCProvider) GetVerificationStatus(context.Context, string) (*compliance.ProviderStatus, error) {
	return nil, compliance.ErrProviderUnavailable
}

func (f *FakeKYCProvider) CancelVerification(_ context.Context, ref string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Canceled = append(f.Canceled, ref)
	return nil
}

func sign(secret, ts string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(ts + "."))
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

// Sign returns the headers a valid fake webhook would carry.
func Sign(secret string, at time.Time, body []byte) map[string]string {
	ts := strconv.FormatInt(at.Unix(), 10)
	return map[string]string{"x-fake-timestamp": ts, "x-fake-signature": sign(secret, ts, body)}
}

func (f *FakeKYCProvider) VerifyWebhookSignature(h map[string]string, body []byte, secret string) (time.Time, error) {
	ts, sig := h["x-fake-timestamp"], h["x-fake-signature"]
	if ts == "" || sig == "" || secret == "" {
		return time.Time{}, errors.New("missing signature")
	}
	want, err := hex.DecodeString(sign(secret, ts, body))
	got, err2 := hex.DecodeString(sig)
	if err != nil || err2 != nil || !hmac.Equal(want, got) {
		return time.Time{}, errors.New("bad signature")
	}
	n, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return time.Time{}, errors.New("bad timestamp")
	}
	return time.Unix(n, 0).UTC(), nil
}

type fakeBody struct {
	EventID           string     `json:"event_id"`
	Type              string     `json:"type"`
	ProviderReference string     `json:"provider_reference"`
	OccurredAt        *time.Time `json:"occurred_at"`
	ReasonCode        string     `json:"reason_code"`
	ExpiresAt         *time.Time `json:"expires_at"`
	Country           string     `json:"country"`
}

func (f *FakeKYCProvider) HandleWebhook(_ context.Context, body []byte) (*compliance.ProviderEvent, error) {
	var b fakeBody
	if err := json.Unmarshal(body, &b); err != nil {
		return nil, err
	}
	ev := &compliance.ProviderEvent{EventID: b.EventID, Type: b.Type, ProviderReference: b.ProviderReference,
		ReasonCode: b.ReasonCode, ExpiresAt: b.ExpiresAt, Country: b.Country}
	if b.OccurredAt != nil {
		ev.OccurredAt = b.OccurredAt.UTC()
	}
	return ev, nil
}

// Body builds a webhook body for tests.
func Body(eventID, typ, ref string, occurred time.Time, reason string, expires *time.Time) []byte {
	b := fakeBody{EventID: eventID, Type: typ, ProviderReference: ref, ReasonCode: reason, ExpiresAt: expires}
	if !occurred.IsZero() {
		o := occurred.UTC()
		b.OccurredAt = &o
	}
	out, _ := json.Marshal(b)
	return out
}

// ---------------- fake screener ----------------

// FakeScreener returns the next queued outcome; FailTimes makes the first N
// calls fail (provider outage).
type FakeScreener struct {
	mu        sync.Mutex
	NameValue string
	Outcome   compliance.MatchStatus
	FailTimes int
	Calls     int
}

func (f *FakeScreener) Name() string { return f.NameValue }

func (f *FakeScreener) Screen(_ context.Context, s compliance.ScreeningSubject) (*compliance.ScreeningOutcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls++
	if f.FailTimes > 0 {
		f.FailTimes--
		return nil, errors.New("provider down")
	}
	return &compliance.ScreeningOutcome{Match: f.Outcome, ProviderReference: "scr-" + s.RequestID.String()}, nil
}

// ---------------- capturing audit logger ----------------

type AuditEntry struct {
	ActorUserID *uuid.UUID
	ActorRole   string
	Action      string
	EntityType  string
	EntityID    *uuid.UUID
	Metadata    map[string]any
	IP          string
}

type FakeAudit struct {
	mu      sync.Mutex
	Entries []AuditEntry
}

func (a *FakeAudit) Log(_ context.Context, actor *uuid.UUID, role, action, entityType string, entityID *uuid.UUID, meta map[string]any, ip string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Entries = append(a.Entries, AuditEntry{actor, role, action, entityType, entityID, meta, ip})
	return nil
}

func (a *FakeAudit) Snapshot() []AuditEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]AuditEntry(nil), a.Entries...)
}

func (a *FakeAudit) Actions() (out []string) {
	for _, e := range a.Snapshot() {
		out = append(out, e.Action)
	}
	return
}
