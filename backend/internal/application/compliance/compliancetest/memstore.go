// Package compliancetest provides test doubles for Bloque 9: an in-memory,
// transactional compliance.Store that enforces the SAME uniqueness/version
// rules as migration 0016, a fake KYC provider (HMAC-signed webhooks), a fake
// screener and a capturing audit logger.
//
// NOTHING here is a real provider and it is never registered by main. A fake
// "approval" proves state-machine behavior only; it is not identity
// verification.
package compliancetest

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/application/compliance"
)

type idemRow struct {
	hash string
	res  *uuid.UUID
}

type state struct {
	cases    map[uuid.UUID]compliance.KYCCase
	history  []compliance.StatusHistory
	webhooks map[string]compliance.WebhookEvent
	idem     map[string]idemRow
	flags    map[uuid.UUID]compliance.Flag
	holds    map[uuid.UUID]compliance.Hold
	risk     map[uuid.UUID]compliance.RiskProfile
	riskHist []compliance.RiskHistoryEntry
	sreq     map[uuid.UUID]compliance.ScreeningRequest
	sres     map[uuid.UUID]compliance.ScreeningResult
}

func newState() *state {
	return &state{
		cases: map[uuid.UUID]compliance.KYCCase{}, webhooks: map[string]compliance.WebhookEvent{},
		idem: map[string]idemRow{}, flags: map[uuid.UUID]compliance.Flag{}, holds: map[uuid.UUID]compliance.Hold{},
		risk: map[uuid.UUID]compliance.RiskProfile{}, sreq: map[uuid.UUID]compliance.ScreeningRequest{},
		sres: map[uuid.UUID]compliance.ScreeningResult{},
	}
}

func (s *state) clone() *state {
	n := newState()
	for k, v := range s.cases {
		n.cases[k] = v
	}
	n.history = append([]compliance.StatusHistory(nil), s.history...)
	for k, v := range s.webhooks {
		n.webhooks[k] = v
	}
	for k, v := range s.idem {
		n.idem[k] = v
	}
	for k, v := range s.flags {
		n.flags[k] = v
	}
	for k, v := range s.holds {
		n.holds[k] = v
	}
	for k, v := range s.risk {
		n.risk[k] = v
	}
	n.riskHist = append([]compliance.RiskHistoryEntry(nil), s.riskHist...)
	for k, v := range s.sreq {
		n.sreq[k] = v
	}
	for k, v := range s.sres {
		n.sres[k] = v
	}
	return n
}

// MemStore serializes transactions with one mutex (standing in for row
// locks) and rolls back on error by restoring a snapshot. Real locking is
// covered by the SQL in postgres.ComplianceRepository; these tests prove the
// service logic on top of the Store contract.
type MemStore struct {
	mu sync.Mutex
	st *state
}

func NewMemStore() *MemStore { return &MemStore{st: newState()} }

var _ compliance.Store = (*MemStore)(nil)

func (m *MemStore) WithTx(ctx context.Context, fn func(tx compliance.Tx) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	snap := m.st.clone()
	if err := fn(&memTx{st: m.st}); err != nil {
		m.st = snap
		return err
	}
	return nil
}

func (m *MemStore) read(fn func(st *state)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fn(m.st)
}

func (m *MemStore) CurrentCases(_ context.Context, u uuid.UUID) (out []*compliance.KYCCase, _ error) {
	m.read(func(st *state) { out = currentCases(st, u) })
	return
}

func (m *MemStore) AllCases(_ context.Context, u uuid.UUID) (out []*compliance.KYCCase, _ error) {
	m.read(func(st *state) {
		for _, c := range st.cases {
			if c.UserID == u {
				cc := c
				out = append(out, &cc)
			}
		}
	})
	return
}

func (m *MemStore) History(_ context.Context, u uuid.UUID, limit int) (out []*compliance.StatusHistory, _ error) {
	m.read(func(st *state) {
		for i := len(st.history) - 1; i >= 0 && len(out) < limit; i-- {
			if st.history[i].UserID == u {
				h := st.history[i]
				out = append(out, &h)
			}
		}
	})
	return
}

func (m *MemStore) ActiveHolds(_ context.Context, u uuid.UUID) (out []*compliance.Hold, _ error) {
	m.read(func(st *state) {
		for _, h := range st.holds {
			if h.UserID == u && h.Status == compliance.HoldActive {
				hh := h
				out = append(out, &hh)
			}
		}
	})
	return
}

func (m *MemStore) OpenFlags(_ context.Context, u uuid.UUID, limit int) (out []*compliance.Flag, _ error) {
	m.read(func(st *state) {
		for _, f := range st.flags {
			if f.UserID == u && f.Status == compliance.FlagOpen && len(out) < limit {
				ff := f
				out = append(out, &ff)
			}
		}
	})
	return
}

func (m *MemStore) RiskProfileOf(_ context.Context, u uuid.UUID) (out *compliance.RiskProfile, _ error) {
	m.read(func(st *state) {
		if p, ok := st.risk[u]; ok {
			pp := p
			out = &pp
		}
	})
	return
}

func (m *MemStore) ScreeningResults(_ context.Context, u uuid.UUID, limit int) (out []*compliance.ScreeningResult, _ error) {
	m.read(func(st *state) {
		for _, r := range st.sres {
			if r.UserID == u && len(out) < limit {
				rr := r
				out = append(out, &rr)
			}
		}
	})
	return
}

// Inspection helpers for tests (take the lock).
func (m *MemStore) HistoryCount(caseID uuid.UUID) (n int) {
	m.read(func(st *state) {
		for _, h := range st.history {
			if h.CaseID == caseID {
				n++
			}
		}
	})
	return
}

func (m *MemStore) WebhookCount() (n int) {
	m.read(func(st *state) { n = len(st.webhooks) })
	return
}

func (m *MemStore) WebhookOutcome(provider, eventID string) (p compliance.WebhookProcessing) {
	m.read(func(st *state) { p = st.webhooks[provider+"|"+eventID].Processing })
	return
}

func (m *MemStore) ScreeningRequestCount() (n int) {
	m.read(func(st *state) { n = len(st.sreq) })
	return
}

func (m *MemStore) RiskHistory(u uuid.UUID) (out []compliance.RiskHistoryEntry) {
	m.read(func(st *state) {
		for _, e := range st.riskHist {
			if e.UserID == u {
				out = append(out, e)
			}
		}
	})
	return
}

func (m *MemStore) AllHolds(u uuid.UUID) (out []compliance.Hold) {
	m.read(func(st *state) {
		for _, h := range st.holds {
			if h.UserID == u {
				out = append(out, h)
			}
		}
	})
	return
}

func (m *MemStore) AllFlags(u uuid.UUID) (out []compliance.Flag) {
	m.read(func(st *state) {
		for _, f := range st.flags {
			if f.UserID == u {
				out = append(out, f)
			}
		}
	})
	return
}

func currentCases(st *state, u uuid.UUID) (out []*compliance.KYCCase) {
	for _, c := range st.cases {
		if c.UserID == u && c.SupersededAt == nil {
			cc := c
			out = append(out, &cc)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID.String() < out[j].ID.String() })
	return
}

// ---------------- Tx ----------------

type memTx struct{ st *state }

func (t *memTx) LockUser(context.Context, uuid.UUID) error { return nil } // whole-store mutex

func idemKey(scope string, actor uuid.UUID, key string) string {
	return scope + "|" + actor.String() + "|" + key
}

func (t *memTx) ClaimIdempotency(_ context.Context, scope string, actor uuid.UUID, key, hash string) (*uuid.UUID, bool, error) {
	k := idemKey(scope, actor, key)
	if r, ok := t.st.idem[k]; ok {
		if r.hash != hash {
			return nil, false, compliance.ErrIdempotencyKeyReuse
		}
		return r.res, true, nil
	}
	t.st.idem[k] = idemRow{hash: hash}
	return nil, false, nil
}

func (t *memTx) SetIdempotencyResource(_ context.Context, scope string, actor uuid.UUID, key string, id uuid.UUID) error {
	k := idemKey(scope, actor, key)
	r := t.st.idem[k]
	r.res = &id
	t.st.idem[k] = r
	return nil
}

func (t *memTx) CurrentCasesForUpdate(_ context.Context, u uuid.UUID) ([]*compliance.KYCCase, error) {
	return currentCases(t.st, u), nil
}

func (t *memTx) GetCaseForUpdate(_ context.Context, id uuid.UUID) (*compliance.KYCCase, error) {
	c, ok := t.st.cases[id]
	if !ok {
		return nil, compliance.ErrCaseNotFound
	}
	return &c, nil
}

func (t *memTx) FindCaseByProviderRef(_ context.Context, provider, ref string) (*compliance.KYCCase, error) {
	for _, c := range t.st.cases {
		if c.Provider == provider && c.ProviderReference == ref {
			cc := c
			return &cc, nil
		}
	}
	return nil, nil
}

// uniqueOK mirrors uq_kyc_cases_inflight / uq_kyc_cases_settled / uq_kyc_cases_provider_ref.
func (t *memTx) uniqueOK(c compliance.KYCCase) bool {
	for id, o := range t.st.cases {
		if id == c.ID {
			continue
		}
		if c.ProviderReference != "" && o.Provider == c.Provider && o.ProviderReference == c.ProviderReference {
			return false
		}
		if o.UserID != c.UserID || o.SupersededAt != nil || c.SupersededAt != nil {
			continue
		}
		if c.Status.InFlight() && o.Status.InFlight() {
			return false
		}
		settled := func(s compliance.KYCStatus) bool { return s == compliance.KYCVerified || s == compliance.KYCSuspended }
		if settled(c.Status) && settled(o.Status) {
			return false
		}
	}
	return true
}

func (t *memTx) InsertCase(_ context.Context, c *compliance.KYCCase) error {
	if !t.uniqueOK(*c) {
		return compliance.ErrActiveCaseExists
	}
	t.st.cases[c.ID] = *c
	return nil
}

func (t *memTx) UpdateCase(_ context.Context, c *compliance.KYCCase) error {
	cur, ok := t.st.cases[c.ID]
	if !ok || cur.Version != c.Version {
		return compliance.ErrConcurrentUpdate
	}
	if !t.uniqueOK(*c) {
		return compliance.ErrConcurrentUpdate
	}
	c.Version++
	c.UpdatedAt = time.Now().UTC()
	t.st.cases[c.ID] = *c
	return nil
}

func (t *memTx) InsertHistory(_ context.Context, h *compliance.StatusHistory) error {
	t.st.history = append(t.st.history, *h)
	return nil
}

func (t *memTx) DueForExpiryForUpdate(_ context.Context, now time.Time, limit int) (out []*compliance.KYCCase, _ error) {
	for _, c := range t.st.cases {
		if c.Status == compliance.KYCVerified && c.SupersededAt == nil && c.ExpiresAt != nil && !c.ExpiresAt.After(now) && len(out) < limit {
			cc := c
			out = append(out, &cc)
		}
	}
	return
}

func (t *memTx) InsertWebhookEvent(_ context.Context, e *compliance.WebhookEvent) (bool, error) {
	k := e.Provider + "|" + e.EventID
	if _, ok := t.st.webhooks[k]; ok {
		return false, nil
	}
	t.st.webhooks[k] = *e
	return true, nil
}

func (t *memTx) SetWebhookOutcome(_ context.Context, provider, eventID string, p compliance.WebhookProcessing, caseID *uuid.UUID) error {
	k := provider + "|" + eventID
	e := t.st.webhooks[k]
	e.Processing, e.CaseID = p, caseID
	t.st.webhooks[k] = e
	return nil
}

func (t *memTx) InsertFlag(_ context.Context, f *compliance.Flag) (*compliance.Flag, bool, error) {
	for _, o := range t.st.flags {
		if o.Status == compliance.FlagOpen && o.UserID == f.UserID && o.Type == f.Type && o.ReasonCode == f.ReasonCode {
			oo := o
			return &oo, false, nil
		}
	}
	t.st.flags[f.ID] = *f
	return f, true, nil
}

func (t *memTx) GetFlagForUpdate(_ context.Context, id uuid.UUID) (*compliance.Flag, error) {
	f, ok := t.st.flags[id]
	if !ok {
		return nil, compliance.ErrFlagNotFound
	}
	return &f, nil
}

func (t *memTx) UpdateFlag(_ context.Context, f *compliance.Flag) error {
	cur := t.st.flags[f.ID]
	if cur.Version != f.Version {
		return compliance.ErrConcurrentUpdate
	}
	f.Version++
	t.st.flags[f.ID] = *f
	return nil
}

func (t *memTx) ActiveHoldForUpdate(_ context.Context, u uuid.UUID, ht compliance.HoldType) (*compliance.Hold, error) {
	for _, h := range t.st.holds {
		if h.UserID == u && h.Type == ht && h.Status == compliance.HoldActive {
			hh := h
			return &hh, nil
		}
	}
	return nil, nil
}

func (t *memTx) InsertHold(_ context.Context, h *compliance.Hold) error {
	for _, o := range t.st.holds {
		if o.UserID == h.UserID && o.Type == h.Type && o.Status == compliance.HoldActive && h.Status == compliance.HoldActive {
			return compliance.ErrConcurrentUpdate
		}
	}
	t.st.holds[h.ID] = *h
	return nil
}

func (t *memTx) GetHoldForUpdate(_ context.Context, id uuid.UUID) (*compliance.Hold, error) {
	h, ok := t.st.holds[id]
	if !ok {
		return nil, compliance.ErrHoldNotFound
	}
	return &h, nil
}

func (t *memTx) UpdateHold(_ context.Context, h *compliance.Hold) error {
	if t.st.holds[h.ID].Version != h.Version {
		return compliance.ErrConcurrentUpdate
	}
	h.Version++
	t.st.holds[h.ID] = *h
	return nil
}

func (t *memTx) RiskProfileForUpdate(_ context.Context, u uuid.UUID) (*compliance.RiskProfile, error) {
	if p, ok := t.st.risk[u]; ok {
		pp := p
		return &pp, nil
	}
	return nil, nil
}

func (t *memTx) UpsertRiskProfile(_ context.Context, p *compliance.RiskProfile) error {
	if cur, ok := t.st.risk[p.UserID]; ok {
		if cur.Version != p.Version {
			return compliance.ErrConcurrentUpdate
		}
		p.Version++
	} else {
		p.Version = 1
	}
	t.st.risk[p.UserID] = *p
	return nil
}

func (t *memTx) InsertRiskHistory(_ context.Context, e *compliance.RiskHistoryEntry) error {
	t.st.riskHist = append(t.st.riskHist, *e)
	return nil
}

func (t *memTx) InsertScreeningRequest(_ context.Context, r *compliance.ScreeningRequest) (*compliance.ScreeningRequest, bool, error) {
	for _, o := range t.st.sreq {
		if o.UserID == r.UserID && o.Kind == r.Kind && o.IdempotencyKey == r.IdempotencyKey {
			oo := o
			return &oo, false, nil
		}
	}
	t.st.sreq[r.ID] = *r
	return r, true, nil
}

func (t *memTx) GetScreeningRequestForUpdate(_ context.Context, id uuid.UUID) (*compliance.ScreeningRequest, error) {
	r, ok := t.st.sreq[id]
	if !ok {
		return nil, compliance.ErrScreeningNotFound
	}
	return &r, nil
}

func (t *memTx) UpdateScreeningRequest(_ context.Context, r *compliance.ScreeningRequest) error {
	t.st.sreq[r.ID] = *r
	return nil
}

func (t *memTx) RetryableScreeningForUpdate(_ context.Context, now time.Time, limit int) (out []*compliance.ScreeningRequest, _ error) {
	for _, r := range t.st.sreq {
		if r.Status == compliance.ScreenReqFailed && r.NextRetryAt != nil && !r.NextRetryAt.After(now) && len(out) < limit {
			rr := r
			out = append(out, &rr)
		}
	}
	return
}

func (t *memTx) InsertScreeningResult(_ context.Context, r *compliance.ScreeningResult) error {
	for _, o := range t.st.sres {
		if o.RequestID == r.RequestID {
			return compliance.ErrConcurrentUpdate // UNIQUE(request_id)
		}
	}
	t.st.sres[r.ID] = *r
	return nil
}

func (t *memTx) ScreeningResultByRequest(_ context.Context, id uuid.UUID) (*compliance.ScreeningResult, error) {
	for _, r := range t.st.sres {
		if r.RequestID == id {
			rr := r
			return &rr, nil
		}
	}
	return nil, nil
}

func (t *memTx) GetScreeningResultForUpdate(_ context.Context, id uuid.UUID) (*compliance.ScreeningResult, error) {
	r, ok := t.st.sres[id]
	if !ok {
		return nil, compliance.ErrScreeningNotFound
	}
	return &r, nil
}

func (t *memTx) UpdateScreeningResult(_ context.Context, r *compliance.ScreeningResult) error {
	if t.st.sres[r.ID].Version != r.Version {
		return compliance.ErrConcurrentUpdate
	}
	r.Version++
	t.st.sres[r.ID] = *r
	return nil
}
