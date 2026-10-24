package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"korzadi-wallet/backend/internal/application/compliance"
)

// ComplianceRepository implements compliance.Store (Bloque 9). Concurrency
// control is entirely in the database: transactions, SELECT ... FOR UPDATE
// (SKIP LOCKED for workers), optimistic `version` columns, advisory locks
// per user, and unique / partial-unique indexes from migration 0016.
type ComplianceRepository struct{ pool *pgxpool.Pool }

func NewComplianceRepository(pool *pgxpool.Pool) *ComplianceRepository {
	return &ComplianceRepository{pool: pool}
}

var _ compliance.Store = (*ComplianceRepository)(nil)

type dbq interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func (r *ComplianceRepository) WithTx(ctx context.Context, fn func(tx compliance.Tx) error) error {
	pt, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("compliance: begin: %w", err)
	}
	if err := fn(&complianceTx{q: pt}); err != nil {
		_ = pt.Rollback(ctx)
		return err
	}
	if err := pt.Commit(ctx); err != nil {
		return fmt.Errorf("compliance: commit: %w", err)
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505"
}

func sv(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// ---------------- scanning ----------------

const caseCols = `id, user_id, status, verification_provider, provider_reference, country, residence_country,
	document_type, document_country, document_hash, submitted_at, started_at, verified_at, rejected_at, expires_at,
	rejection_reason_code, review_reason_code, risk_level, last_provider_event_at, superseded_at, version, created_at, updated_at`

func scanCase(row pgx.Row) (*compliance.KYCCase, error) {
	var (
		c                                             compliance.KYCCase
		status, risk                                  string
		prov, ref, country, res, dt, dc, dh, rej, rev *string
	)
	err := row.Scan(&c.ID, &c.UserID, &status, &prov, &ref, &country, &res, &dt, &dc, &dh,
		&c.SubmittedAt, &c.StartedAt, &c.VerifiedAt, &c.RejectedAt, &c.ExpiresAt, &rej, &rev, &risk,
		&c.LastProviderEventAt, &c.SupersededAt, &c.Version, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	c.Status, c.RiskLevel = compliance.KYCStatus(status), compliance.RiskLevel(risk)
	c.Provider, c.ProviderReference, c.Country, c.ResidenceCountry = sv(prov), sv(ref), sv(country), sv(res)
	c.DocumentType, c.DocumentCountry, c.DocumentHash = sv(dt), sv(dc), sv(dh)
	c.RejectionReasonCode, c.ReviewReasonCode = sv(rej), sv(rev)
	return &c, nil
}

func collectCases(rows pgx.Rows) ([]*compliance.KYCCase, error) {
	defer rows.Close()
	var out []*compliance.KYCCase
	for rows.Next() {
		c, err := scanCase(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

const flagCols = `id, user_id, type, severity, status, source, reason_code, description, created_by, created_at,
	resolved_at, resolved_by, resolution_reason, version`

func scanFlag(row pgx.Row) (*compliance.Flag, error) {
	var f compliance.Flag
	var t, sev, st, src string
	var desc, res *string
	err := row.Scan(&f.ID, &f.UserID, &t, &sev, &st, &src, &f.ReasonCode, &desc, &f.CreatedBy, &f.CreatedAt,
		&f.ResolvedAt, &f.ResolvedBy, &res, &f.Version)
	if err != nil {
		return nil, err
	}
	f.Type, f.Severity, f.Status, f.Source = compliance.FlagType(t), compliance.RiskLevel(sev), compliance.FlagStatus(st), compliance.FlagSource(src)
	f.Description, f.ResolutionReason = sv(desc), sv(res)
	return &f, nil
}

const holdCols = `id, user_id, hold_type, status, reason_code, reason, placed_by, placed_at, released_by, released_at, release_reason, version`

func scanHold(row pgx.Row) (*compliance.Hold, error) {
	var h compliance.Hold
	var t, st string
	var reason, rel *string
	err := row.Scan(&h.ID, &h.UserID, &t, &st, &h.ReasonCode, &reason, &h.PlacedBy, &h.PlacedAt, &h.ReleasedBy, &h.ReleasedAt, &rel, &h.Version)
	if err != nil {
		return nil, err
	}
	h.Type, h.Status = compliance.HoldType(t), compliance.HoldStatus(st)
	h.Reason, h.ReleaseReason = sv(reason), sv(rel)
	return &h, nil
}

const screenReqCols = `id, user_id, kind, status, idempotency_key, provider, provider_reference, attempt_count, next_retry_at, last_error_code, requested_by, created_at`

func scanScreenReq(row pgx.Row) (*compliance.ScreeningRequest, error) {
	var s compliance.ScreeningRequest
	var kind, st string
	var prov, ref, lerr *string
	err := row.Scan(&s.ID, &s.UserID, &kind, &st, &s.IdempotencyKey, &prov, &ref, &s.AttemptCount, &s.NextRetryAt, &lerr, &s.RequestedBy, &s.CreatedAt)
	if err != nil {
		return nil, err
	}
	s.Kind, s.Status = compliance.ScreeningKind(kind), compliance.ScreeningRequestStatus(st)
	s.Provider, s.ProviderReference, s.LastErrorCode = sv(prov), sv(ref), sv(lerr)
	return &s, nil
}

const screenResCols = `id, request_id, user_id, kind, match_status, review_status, provider_reference, reviewed_by, reviewed_at, review_reason, screened_at, version`

func scanScreenRes(row pgx.Row) (*compliance.ScreeningResult, error) {
	var s compliance.ScreeningResult
	var kind, m, rv string
	var ref, rr *string
	err := row.Scan(&s.ID, &s.RequestID, &s.UserID, &kind, &m, &rv, &ref, &s.ReviewedBy, &s.ReviewedAt, &rr, &s.ScreenedAt, &s.Version)
	if err != nil {
		return nil, err
	}
	s.Kind, s.Match, s.Review = compliance.ScreeningKind(kind), compliance.MatchStatus(m), compliance.ReviewStatus(rv)
	s.ProviderReference, s.ReviewReason = sv(ref), sv(rr)
	return &s, nil
}

// ---------------- Store (non-locking reads) ----------------

func (r *ComplianceRepository) CurrentCases(ctx context.Context, userID uuid.UUID) ([]*compliance.KYCCase, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+caseCols+` FROM kyc_cases WHERE user_id=$1 AND superseded_at IS NULL ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	return collectCases(rows)
}

func (r *ComplianceRepository) AllCases(ctx context.Context, userID uuid.UUID) ([]*compliance.KYCCase, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+caseCols+` FROM kyc_cases WHERE user_id=$1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	return collectCases(rows)
}

func (r *ComplianceRepository) History(ctx context.Context, userID uuid.UUID, limit int) ([]*compliance.StatusHistory, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, case_id, user_id, old_status, new_status, actor_type, actor_user_id, actor_ref, reason_code, reason, request_id, created_at
		FROM kyc_status_history WHERE user_id=$1 ORDER BY created_at DESC, id LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*compliance.StatusHistory
	for rows.Next() {
		var h compliance.StatusHistory
		var old *string
		var nw, at string
		var aref, rc, rs, rid *string
		if err := rows.Scan(&h.ID, &h.CaseID, &h.UserID, &old, &nw, &at, &h.ActorUserID, &aref, &rc, &rs, &rid, &h.CreatedAt); err != nil {
			return nil, err
		}
		h.OldStatus, h.NewStatus, h.ActorType = compliance.KYCStatus(sv(old)), compliance.KYCStatus(nw), compliance.ActorType(at)
		h.ActorRef, h.ReasonCode, h.Reason, h.RequestID = sv(aref), sv(rc), sv(rs), sv(rid)
		out = append(out, &h)
	}
	return out, rows.Err()
}

func (r *ComplianceRepository) ActiveHolds(ctx context.Context, userID uuid.UUID) ([]*compliance.Hold, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+holdCols+` FROM compliance_holds WHERE user_id=$1 AND status='ACTIVE' ORDER BY placed_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*compliance.Hold
	for rows.Next() {
		h, err := scanHold(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (r *ComplianceRepository) OpenFlags(ctx context.Context, userID uuid.UUID, limit int) ([]*compliance.Flag, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+flagCols+` FROM compliance_flags WHERE user_id=$1 AND status='OPEN' ORDER BY created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*compliance.Flag
	for rows.Next() {
		f, err := scanFlag(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func scanRisk(row pgx.Row) (*compliance.RiskProfile, error) {
	var p compliance.RiskProfile
	var lvl string
	if err := row.Scan(&p.UserID, &lvl, &p.ReasonCodes, &p.Version); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	p.Level = compliance.RiskLevel(lvl)
	return &p, nil
}

func (r *ComplianceRepository) RiskProfileOf(ctx context.Context, userID uuid.UUID) (*compliance.RiskProfile, error) {
	return scanRisk(r.pool.QueryRow(ctx, `SELECT user_id, risk_level, reason_codes, version FROM risk_profiles WHERE user_id=$1`, userID))
}

func (r *ComplianceRepository) ScreeningResults(ctx context.Context, userID uuid.UUID, limit int) ([]*compliance.ScreeningResult, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+screenResCols+` FROM screening_results WHERE user_id=$1 ORDER BY screened_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*compliance.ScreeningResult
	for rows.Next() {
		s, err := scanScreenRes(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ---------------- Tx ----------------

type complianceTx struct{ q dbq }

func (t *complianceTx) LockUser(ctx context.Context, userID uuid.UUID) error {
	_, err := t.q.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('compliance:'||$1::text, 0))`, userID.String())
	return err
}

func (t *complianceTx) ClaimIdempotency(ctx context.Context, scope string, actorID uuid.UUID, key, requestHash string) (*uuid.UUID, bool, error) {
	tag, err := t.q.Exec(ctx, `
		INSERT INTO compliance_idempotency (scope, actor_id, idem_key, request_hash)
		VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING`, scope, actorID, key, requestHash)
	if err != nil {
		return nil, false, err
	}
	if tag.RowsAffected() == 1 {
		return nil, false, nil
	}
	var hash string
	var res *uuid.UUID
	if err := t.q.QueryRow(ctx, `SELECT request_hash, resource_id FROM compliance_idempotency WHERE scope=$1 AND actor_id=$2 AND idem_key=$3`,
		scope, actorID, key).Scan(&hash, &res); err != nil {
		return nil, false, err
	}
	if hash != requestHash {
		return nil, false, compliance.ErrIdempotencyKeyReuse
	}
	return res, true, nil
}

func (t *complianceTx) SetIdempotencyResource(ctx context.Context, scope string, actorID uuid.UUID, key string, resourceID uuid.UUID) error {
	_, err := t.q.Exec(ctx, `UPDATE compliance_idempotency SET resource_id=$4 WHERE scope=$1 AND actor_id=$2 AND idem_key=$3`, scope, actorID, key, resourceID)
	return err
}

func (t *complianceTx) CurrentCasesForUpdate(ctx context.Context, userID uuid.UUID) ([]*compliance.KYCCase, error) {
	rows, err := t.q.Query(ctx, `SELECT `+caseCols+` FROM kyc_cases WHERE user_id=$1 AND superseded_at IS NULL ORDER BY id FOR UPDATE`, userID)
	if err != nil {
		return nil, err
	}
	return collectCases(rows)
}

func (t *complianceTx) GetCaseForUpdate(ctx context.Context, id uuid.UUID) (*compliance.KYCCase, error) {
	c, err := scanCase(t.q.QueryRow(ctx, `SELECT `+caseCols+` FROM kyc_cases WHERE id=$1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, compliance.ErrCaseNotFound
	}
	return c, err
}

func (t *complianceTx) FindCaseByProviderRef(ctx context.Context, provider, ref string) (*compliance.KYCCase, error) {
	c, err := scanCase(t.q.QueryRow(ctx, `SELECT `+caseCols+` FROM kyc_cases WHERE verification_provider=$1 AND provider_reference=$2`, provider, ref))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return c, err
}

func (t *complianceTx) InsertCase(ctx context.Context, c *compliance.KYCCase) error {
	_, err := t.q.Exec(ctx, `
		INSERT INTO kyc_cases (id, user_id, status, verification_provider, provider_reference, country, residence_country,
			document_type, document_country, document_hash, submitted_at, started_at, verified_at, rejected_at, expires_at,
			rejection_reason_code, review_reason_code, risk_level, last_provider_event_at, superseded_at, version)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)`,
		c.ID, c.UserID, string(c.Status), nullString(c.Provider), nullString(c.ProviderReference), nullString(c.Country), nullString(c.ResidenceCountry),
		nullString(c.DocumentType), nullString(c.DocumentCountry), nullString(c.DocumentHash), c.SubmittedAt, c.StartedAt, c.VerifiedAt, c.RejectedAt, c.ExpiresAt,
		nullString(c.RejectionReasonCode), nullString(c.ReviewReasonCode), string(c.RiskLevel), c.LastProviderEventAt, c.SupersededAt, c.Version)
	if isUniqueViolation(err) {
		return compliance.ErrActiveCaseExists
	}
	return err
}

func (t *complianceTx) UpdateCase(ctx context.Context, c *compliance.KYCCase) error {
	tag, err := t.q.Exec(ctx, `
		UPDATE kyc_cases SET status=$2, verification_provider=$3, provider_reference=$4, country=$5, residence_country=$6,
			document_type=$7, document_country=$8, document_hash=$9, submitted_at=$10, started_at=$11, verified_at=$12,
			rejected_at=$13, expires_at=$14, rejection_reason_code=$15, review_reason_code=$16, risk_level=$17,
			last_provider_event_at=$18, superseded_at=$19, version=version+1
		WHERE id=$1 AND version=$20`,
		c.ID, string(c.Status), nullString(c.Provider), nullString(c.ProviderReference), nullString(c.Country), nullString(c.ResidenceCountry),
		nullString(c.DocumentType), nullString(c.DocumentCountry), nullString(c.DocumentHash), c.SubmittedAt, c.StartedAt, c.VerifiedAt,
		c.RejectedAt, c.ExpiresAt, nullString(c.RejectionReasonCode), nullString(c.ReviewReasonCode), string(c.RiskLevel),
		c.LastProviderEventAt, c.SupersededAt, c.Version)
	if err != nil {
		if isUniqueViolation(err) {
			return compliance.ErrConcurrentUpdate
		}
		return err
	}
	if tag.RowsAffected() != 1 {
		return compliance.ErrConcurrentUpdate
	}
	c.Version++
	return nil
}

func (t *complianceTx) InsertHistory(ctx context.Context, h *compliance.StatusHistory) error {
	var old *string
	if h.OldStatus != "" {
		s := string(h.OldStatus)
		old = &s
	}
	_, err := t.q.Exec(ctx, `
		INSERT INTO kyc_status_history (id, case_id, user_id, old_status, new_status, actor_type, actor_user_id, actor_ref, reason_code, reason, request_id, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		h.ID, h.CaseID, h.UserID, old, string(h.NewStatus), string(h.ActorType), h.ActorUserID, nullString(h.ActorRef),
		nullString(h.ReasonCode), nullString(h.Reason), nullString(h.RequestID), h.CreatedAt)
	return err
}

func (t *complianceTx) DueForExpiryForUpdate(ctx context.Context, now time.Time, limit int) ([]*compliance.KYCCase, error) {
	rows, err := t.q.Query(ctx, `SELECT `+caseCols+` FROM kyc_cases
		WHERE status='VERIFIED' AND superseded_at IS NULL AND expires_at IS NOT NULL AND expires_at <= $1
		ORDER BY expires_at LIMIT $2 FOR UPDATE SKIP LOCKED`, now, limit)
	if err != nil {
		return nil, err
	}
	return collectCases(rows)
}

func (t *complianceTx) InsertWebhookEvent(ctx context.Context, e *compliance.WebhookEvent) (bool, error) {
	tag, err := t.q.Exec(ctx, `
		INSERT INTO kyc_webhook_events (id, provider, event_id, event_type, provider_reference, occurred_at, payload_hash)
		VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (provider, event_id) DO NOTHING`,
		e.ID, e.Provider, e.EventID, e.EventType, nullString(e.ProviderReference), e.OccurredAt, e.PayloadHash)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (t *complianceTx) SetWebhookOutcome(ctx context.Context, provider, eventID string, p compliance.WebhookProcessing, caseID *uuid.UUID) error {
	_, err := t.q.Exec(ctx, `UPDATE kyc_webhook_events SET processing_status=$3, case_id=$4, processed_at=now() WHERE provider=$1 AND event_id=$2`,
		provider, eventID, string(p), caseID)
	return err
}

func (t *complianceTx) InsertFlag(ctx context.Context, f *compliance.Flag) (*compliance.Flag, bool, error) {
	tag, err := t.q.Exec(ctx, `
		INSERT INTO compliance_flags (id, user_id, type, severity, status, source, reason_code, description, created_by, created_at, version)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT (user_id, type, reason_code) WHERE status = 'OPEN' DO NOTHING`,
		f.ID, f.UserID, string(f.Type), string(f.Severity), string(f.Status), string(f.Source), f.ReasonCode,
		nullString(f.Description), f.CreatedBy, f.CreatedAt, f.Version)
	if err != nil {
		return nil, false, err
	}
	if tag.RowsAffected() == 1 {
		return f, true, nil
	}
	ex, err := scanFlag(t.q.QueryRow(ctx, `SELECT `+flagCols+` FROM compliance_flags WHERE user_id=$1 AND type=$2 AND reason_code=$3 AND status='OPEN'`,
		f.UserID, string(f.Type), f.ReasonCode))
	if err != nil {
		return nil, false, err
	}
	return ex, false, nil
}

func (t *complianceTx) GetFlagForUpdate(ctx context.Context, id uuid.UUID) (*compliance.Flag, error) {
	f, err := scanFlag(t.q.QueryRow(ctx, `SELECT `+flagCols+` FROM compliance_flags WHERE id=$1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, compliance.ErrFlagNotFound
	}
	return f, err
}

func (t *complianceTx) UpdateFlag(ctx context.Context, f *compliance.Flag) error {
	tag, err := t.q.Exec(ctx, `UPDATE compliance_flags SET status=$2, resolved_at=$3, resolved_by=$4, resolution_reason=$5, version=version+1
		WHERE id=$1 AND version=$6`, f.ID, string(f.Status), f.ResolvedAt, f.ResolvedBy, nullString(f.ResolutionReason), f.Version)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return compliance.ErrConcurrentUpdate
	}
	f.Version++
	return nil
}

func (t *complianceTx) ActiveHoldForUpdate(ctx context.Context, userID uuid.UUID, ht compliance.HoldType) (*compliance.Hold, error) {
	h, err := scanHold(t.q.QueryRow(ctx, `SELECT `+holdCols+` FROM compliance_holds WHERE user_id=$1 AND hold_type=$2 AND status='ACTIVE' FOR UPDATE`, userID, string(ht)))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return h, err
}

func (t *complianceTx) InsertHold(ctx context.Context, h *compliance.Hold) error {
	_, err := t.q.Exec(ctx, `
		INSERT INTO compliance_holds (id, user_id, hold_type, status, reason_code, reason, placed_by, placed_at, version)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		h.ID, h.UserID, string(h.Type), string(h.Status), h.ReasonCode, nullString(h.Reason), h.PlacedBy, h.PlacedAt, h.Version)
	if isUniqueViolation(err) {
		return compliance.ErrConcurrentUpdate
	}
	return err
}

func (t *complianceTx) GetHoldForUpdate(ctx context.Context, id uuid.UUID) (*compliance.Hold, error) {
	h, err := scanHold(t.q.QueryRow(ctx, `SELECT `+holdCols+` FROM compliance_holds WHERE id=$1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, compliance.ErrHoldNotFound
	}
	return h, err
}

func (t *complianceTx) UpdateHold(ctx context.Context, h *compliance.Hold) error {
	tag, err := t.q.Exec(ctx, `UPDATE compliance_holds SET status=$2, released_by=$3, released_at=$4, release_reason=$5, version=version+1
		WHERE id=$1 AND version=$6`, h.ID, string(h.Status), h.ReleasedBy, h.ReleasedAt, nullString(h.ReleaseReason), h.Version)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return compliance.ErrConcurrentUpdate
	}
	h.Version++
	return nil
}

func (t *complianceTx) RiskProfileForUpdate(ctx context.Context, userID uuid.UUID) (*compliance.RiskProfile, error) {
	return scanRisk(t.q.QueryRow(ctx, `SELECT user_id, risk_level, reason_codes, version FROM risk_profiles WHERE user_id=$1 FOR UPDATE`, userID))
}

// UpsertRiskProfile: p.Version is the version the caller read (1 for a new
// row). UPDATE is version-guarded; if no row exists it INSERTs.
func (t *complianceTx) UpsertRiskProfile(ctx context.Context, p *compliance.RiskProfile) error {
	codes := p.ReasonCodes
	if codes == nil {
		codes = []string{}
	}
	tag, err := t.q.Exec(ctx, `UPDATE risk_profiles SET risk_level=$2, reason_codes=$3, version=version+1 WHERE user_id=$1 AND version=$4`,
		p.UserID, string(p.Level), codes, p.Version)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		p.Version++
		return nil
	}
	tag, err = t.q.Exec(ctx, `INSERT INTO risk_profiles (user_id, risk_level, reason_codes, version) VALUES ($1,$2,$3,1) ON CONFLICT DO NOTHING`,
		p.UserID, string(p.Level), codes)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return compliance.ErrConcurrentUpdate
	}
	p.Version = 1
	return nil
}

func (t *complianceTx) InsertRiskHistory(ctx context.Context, e *compliance.RiskHistoryEntry) error {
	codes := e.ReasonCodes
	if codes == nil {
		codes = []string{}
	}
	var old *string
	if e.OldLevel != "" {
		s := string(e.OldLevel)
		old = &s
	}
	_, err := t.q.Exec(ctx, `
		INSERT INTO risk_level_history (user_id, old_level, new_level, reason_codes, source, actor_user_id, reason, request_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		e.UserID, old, string(e.NewLevel), codes, e.Source, e.ActorUserID, nullString(e.Reason), nullString(e.RequestID))
	return err
}

func (t *complianceTx) InsertScreeningRequest(ctx context.Context, s *compliance.ScreeningRequest) (*compliance.ScreeningRequest, bool, error) {
	tag, err := t.q.Exec(ctx, `
		INSERT INTO screening_requests (id, user_id, kind, status, idempotency_key, requested_by, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (user_id, kind, idempotency_key) DO NOTHING`,
		s.ID, s.UserID, string(s.Kind), string(s.Status), s.IdempotencyKey, s.RequestedBy, s.CreatedAt)
	if err != nil {
		return nil, false, err
	}
	if tag.RowsAffected() == 1 {
		return s, true, nil
	}
	ex, err := scanScreenReq(t.q.QueryRow(ctx, `SELECT `+screenReqCols+` FROM screening_requests WHERE user_id=$1 AND kind=$2 AND idempotency_key=$3`,
		s.UserID, string(s.Kind), s.IdempotencyKey))
	return ex, false, err
}

func (t *complianceTx) GetScreeningRequestForUpdate(ctx context.Context, id uuid.UUID) (*compliance.ScreeningRequest, error) {
	s, err := scanScreenReq(t.q.QueryRow(ctx, `SELECT `+screenReqCols+` FROM screening_requests WHERE id=$1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, compliance.ErrScreeningNotFound
	}
	return s, err
}

func (t *complianceTx) UpdateScreeningRequest(ctx context.Context, s *compliance.ScreeningRequest) error {
	_, err := t.q.Exec(ctx, `UPDATE screening_requests SET status=$2, provider=$3, provider_reference=$4, attempt_count=$5,
		next_retry_at=$6, last_error_code=$7 WHERE id=$1`,
		s.ID, string(s.Status), nullString(s.Provider), nullString(s.ProviderReference), s.AttemptCount, s.NextRetryAt, nullString(s.LastErrorCode))
	return err
}

func (t *complianceTx) RetryableScreeningForUpdate(ctx context.Context, now time.Time, limit int) ([]*compliance.ScreeningRequest, error) {
	rows, err := t.q.Query(ctx, `SELECT `+screenReqCols+` FROM screening_requests
		WHERE status='FAILED' AND next_retry_at <= $1 ORDER BY next_retry_at LIMIT $2 FOR UPDATE SKIP LOCKED`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*compliance.ScreeningRequest
	for rows.Next() {
		s, err := scanScreenReq(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (t *complianceTx) InsertScreeningResult(ctx context.Context, s *compliance.ScreeningResult) error {
	_, err := t.q.Exec(ctx, `
		INSERT INTO screening_results (id, request_id, user_id, kind, match_status, review_status, provider_reference, screened_at, version)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		s.ID, s.RequestID, s.UserID, string(s.Kind), string(s.Match), string(s.Review), nullString(s.ProviderReference), s.ScreenedAt, s.Version)
	return err
}

func (t *complianceTx) ScreeningResultByRequest(ctx context.Context, requestID uuid.UUID) (*compliance.ScreeningResult, error) {
	s, err := scanScreenRes(t.q.QueryRow(ctx, `SELECT `+screenResCols+` FROM screening_results WHERE request_id=$1`, requestID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return s, err
}

func (t *complianceTx) GetScreeningResultForUpdate(ctx context.Context, id uuid.UUID) (*compliance.ScreeningResult, error) {
	s, err := scanScreenRes(t.q.QueryRow(ctx, `SELECT `+screenResCols+` FROM screening_results WHERE id=$1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, compliance.ErrScreeningNotFound
	}
	return s, err
}

func (t *complianceTx) UpdateScreeningResult(ctx context.Context, s *compliance.ScreeningResult) error {
	tag, err := t.q.Exec(ctx, `UPDATE screening_results SET review_status=$2, reviewed_by=$3, reviewed_at=$4, review_reason=$5, version=version+1
		WHERE id=$1 AND version=$6`, s.ID, string(s.Review), s.ReviewedBy, s.ReviewedAt, nullString(s.ReviewReason), s.Version)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return compliance.ErrConcurrentUpdate
	}
	s.Version++
	return nil
}
