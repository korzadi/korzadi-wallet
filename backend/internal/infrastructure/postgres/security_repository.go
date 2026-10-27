package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/application/security"
)

// ---------------------------------------------------------------------
// pgxQuerier: the narrow slice of *pgxpool.Pool / pgx.Tx this file needs,
// so the same SQL for "resolve the applicable policy" can run either
// stand-alone (SecurityPolicyRepository.Get) or inside the very
// transaction that Reserve uses to lock limit_usage — reading the policy
// through a stale connection right before locking usage rows would be a
// TOCTOU gap on policy changes, however small.
// ---------------------------------------------------------------------

type pgxQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

const policyLookupSQL = `
	SELECT id, operation, asset_code, network, min_amount, max_amount, daily_limit,
	       daily_count_limit, monthly_limit, monthly_count_limit, step_up_threshold,
	       manual_approval_threshold, enabled
	FROM security_policies
	WHERE operation = $1
	  AND (asset_code IS NULL OR asset_code = $2)
	  AND (network IS NULL OR network = $3)
	ORDER BY (asset_code IS NOT NULL) DESC, (network IS NOT NULL) DESC
	LIMIT 1
`

func queryPolicy(ctx context.Context, q pgxQuerier, op security.OperationType, assetCode, network string) (*security.Policy, error) {
	var (
		id                                                          uuid.UUID
		operation                                                   string
		dbAsset, dbNetwork                                          *string
		minAmount                                                   decimal.Decimal
		maxAmount, dailyLimit, monthlyLimit, stepUp, manualApproval decimal.NullDecimal
		dailyCount, monthlyCount                                    *int32
		enabled                                                     bool
	)

	var assetArg, networkArg *string
	if assetCode != "" && assetCode != security.AnyAsset {
		assetArg = &assetCode
	}
	if network != "" {
		networkArg = &network
	}

	err := q.QueryRow(ctx, policyLookupSQL, string(op), assetArg, networkArg).Scan(
		&id, &operation, &dbAsset, &dbNetwork, &minAmount, &maxAmount, &dailyLimit,
		&dailyCount, &monthlyLimit, &monthlyCount, &stepUp, &manualApproval, &enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil // not managed by this table — caller treats as "no additional limits"
	}
	if err != nil {
		return nil, fmt.Errorf("security: consultar política de %s: %w", op, err)
	}

	p := &security.Policy{
		Operation: security.OperationType(operation),
		AssetCode: security.AnyAsset,
		MinAmount: minAmount,
		Enabled:   enabled,
	}
	if dbAsset != nil {
		p.AssetCode = *dbAsset
	}
	if dbNetwork != nil {
		p.Network = *dbNetwork
	}
	if maxAmount.Valid {
		v := maxAmount.Decimal
		p.MaxAmount = &v
	}
	if dailyLimit.Valid {
		v := dailyLimit.Decimal
		p.DailyLimit = &v
	}
	if monthlyLimit.Valid {
		v := monthlyLimit.Decimal
		p.MonthlyLimit = &v
	}
	if stepUp.Valid {
		v := stepUp.Decimal
		p.StepUpThreshold = &v
	}
	if manualApproval.Valid {
		v := manualApproval.Decimal
		p.ManualApprovalAbove = &v
	}
	if dailyCount != nil {
		v := int(*dailyCount)
		p.DailyCountLimit = &v
	}
	if monthlyCount != nil {
		v := int(*monthlyCount)
		p.MonthlyCountLimit = &v
	}
	return p, nil
}

// ---------------------------------------------------------------------
// SecurityPolicyRepository — security.PolicyRepository
// ---------------------------------------------------------------------

type SecurityPolicyRepository struct{ pool *pgxpool.Pool }

func NewSecurityPolicyRepository(pool *pgxpool.Pool) *SecurityPolicyRepository {
	return &SecurityPolicyRepository{pool: pool}
}

func (r *SecurityPolicyRepository) Get(ctx context.Context, op security.OperationType, assetCode, network string) (*security.Policy, error) {
	return queryPolicy(ctx, r.pool, op, assetCode, network)
}

func (r *SecurityPolicyRepository) List(ctx context.Context) ([]security.Policy, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT operation, asset_code, network, min_amount, max_amount, daily_limit,
		       daily_count_limit, monthly_limit, monthly_count_limit, step_up_threshold,
		       manual_approval_threshold, enabled
		FROM security_policies ORDER BY operation, asset_code NULLS FIRST, network NULLS FIRST
	`)
	if err != nil {
		return nil, fmt.Errorf("security: listar políticas: %w", err)
	}
	defer rows.Close()

	var out []security.Policy
	for rows.Next() {
		var (
			operation                                                   string
			dbAsset, dbNetwork                                          *string
			minAmount                                                   decimal.Decimal
			maxAmount, dailyLimit, monthlyLimit, stepUp, manualApproval decimal.NullDecimal
			dailyCount, monthlyCount                                    *int32
			enabled                                                     bool
		)
		if err := rows.Scan(&operation, &dbAsset, &dbNetwork, &minAmount, &maxAmount, &dailyLimit,
			&dailyCount, &monthlyLimit, &monthlyCount, &stepUp, &manualApproval, &enabled); err != nil {
			return nil, err
		}
		p := security.Policy{Operation: security.OperationType(operation), AssetCode: security.AnyAsset, MinAmount: minAmount, Enabled: enabled}
		if dbAsset != nil {
			p.AssetCode = *dbAsset
		}
		if dbNetwork != nil {
			p.Network = *dbNetwork
		}
		if maxAmount.Valid {
			v := maxAmount.Decimal
			p.MaxAmount = &v
		}
		if dailyLimit.Valid {
			v := dailyLimit.Decimal
			p.DailyLimit = &v
		}
		if monthlyLimit.Valid {
			v := monthlyLimit.Decimal
			p.MonthlyLimit = &v
		}
		if stepUp.Valid {
			v := stepUp.Decimal
			p.StepUpThreshold = &v
		}
		if manualApproval.Valid {
			v := manualApproval.Decimal
			p.ManualApprovalAbove = &v
		}
		if dailyCount != nil {
			v := int(*dailyCount)
			p.DailyCountLimit = &v
		}
		if monthlyCount != nil {
			v := int(*monthlyCount)
			p.MonthlyCountLimit = &v
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Upsert inserts or updates the security_policies row for (operation,
// asset_code, network). Called only from admin/security-admin paths —
// callers are expected to have already checked the caller's role.
func (r *SecurityPolicyRepository) Upsert(ctx context.Context, p security.Policy) error {
	if err := security.ValidatePolicyShape(p); err != nil {
		return err
	}
	var assetArg, networkArg *string
	if p.AssetCode != "" && p.AssetCode != security.AnyAsset {
		assetArg = &p.AssetCode
	}
	if p.Network != "" {
		networkArg = &p.Network
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO security_policies (operation, category, asset_code, network, min_amount, max_amount,
			daily_limit, daily_count_limit, monthly_limit, monthly_count_limit, step_up_threshold,
			manual_approval_threshold, enabled)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (operation, COALESCE(asset_code, ''), COALESCE(network, ''))
		DO UPDATE SET
			min_amount = EXCLUDED.min_amount, max_amount = EXCLUDED.max_amount,
			daily_limit = EXCLUDED.daily_limit, daily_count_limit = EXCLUDED.daily_count_limit,
			monthly_limit = EXCLUDED.monthly_limit, monthly_count_limit = EXCLUDED.monthly_count_limit,
			step_up_threshold = EXCLUDED.step_up_threshold, manual_approval_threshold = EXCLUDED.manual_approval_threshold,
			enabled = EXCLUDED.enabled, updated_at = now()
	`, string(p.Operation), categoryFor(p.Operation), assetArg, networkArg, p.MinAmount,
		nullableDecimal(p.MaxAmount), nullableDecimal(p.DailyLimit), nullableInt(p.DailyCountLimit),
		nullableDecimal(p.MonthlyLimit), nullableInt(p.MonthlyCountLimit),
		nullableDecimal(p.StepUpThreshold), nullableDecimal(p.ManualApprovalAbove), p.Enabled)
	if err != nil {
		return fmt.Errorf("security: guardar política: %w", err)
	}
	return nil
}

func categoryFor(op security.OperationType) string {
	switch op {
	case security.OpTransfer, security.OpWithdrawal:
		return string(security.CategoryUser)
	case security.OpTreasuryRebalance:
		return string(security.CategoryCustody)
	case security.OpGasRefill, security.OpSweep:
		return string(security.CategorySystem)
	case security.OpAdminOperation:
		return string(security.CategoryAdmin)
	default:
		return string(security.CategoryUser)
	}
}

func nullableDecimal(v *decimal.Decimal) decimal.NullDecimal {
	if v == nil {
		return decimal.NullDecimal{}
	}
	return decimal.NullDecimal{Decimal: *v, Valid: true}
}

func nullableInt(v *int) *int32 {
	if v == nil {
		return nil
	}
	i := int32(*v)
	return &i
}

// ---------------------------------------------------------------------
// SecurityLimitRepository — security.Limiter (reserve / commit / release)
// ---------------------------------------------------------------------

type SecurityLimitRepository struct{ pool *pgxpool.Pool }

func NewSecurityLimitRepository(pool *pgxpool.Pool) *SecurityLimitRepository {
	return &SecurityLimitRepository{pool: pool}
}

func periodKeys(now time.Time) (daily, monthly string) {
	utc := now.UTC()
	return utc.Format("2006-01-02"), utc.Format("2006-01")
}

// Reserve implements security.Limiter.Reserve. It runs entirely inside one
// database transaction: the applicable policy is read, the daily and
// monthly limit_usage rows are locked (always in the same order — daily
// then monthly — so two concurrent Reserve calls can never deadlock on
// each other), checked against the policy, incremented, and the
// reservation is inserted, all before commit. Two concurrent requests
// that would together exceed the limit can never both succeed: the
// second one blocks on the row lock until the first commits (or rolls
// back), then re-reads the now-updated usage and is rejected if it no
// longer fits — exactly the spec §5 guarantee (1000 daily limit, two
// concurrent 700s, at most one approved).
func (r *SecurityLimitRepository) Reserve(ctx context.Context, in security.ReserveInput) (*security.Reservation, error) {
	if !in.Amount.IsPositive() {
		return nil, fmt.Errorf("security: el monto a reservar debe ser positivo")
	}
	if !in.Operation.Valid() {
		return nil, security.ErrOperationUnknown
	}
	assetCode := in.AssetCode
	if assetCode == "" {
		assetCode = security.AnyAsset
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("security: iniciar tx de reserva: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// ---- replay protection (spec §9) ----
	if in.ReferenceID != "" {
		var existing uuid.UUID
		err := tx.QueryRow(ctx, `
			SELECT id FROM limit_reservations
			WHERE operation = $1 AND reference_id = $2 AND status = 'active'
			FOR UPDATE
		`, string(in.Operation), in.ReferenceID).Scan(&existing)
		if err == nil {
			return nil, security.ErrReplay
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("security: verificar replay: %w", err)
		}
	}

	// ---- policy (read inside the same tx: no TOCTOU vs. an admin
	// changing the policy between read and lock) ----
	policy, err := queryPolicy(ctx, tx, in.Operation, assetCode, in.Network)
	if err != nil {
		return nil, err
	}
	if policy != nil {
		if !policy.Enabled {
			return nil, security.ErrOperationDisabled
		}
		if in.Amount.LessThan(policy.MinAmount) {
			return nil, security.ErrBelowMinimum
		}
		if policy.MaxAmount != nil && in.Amount.GreaterThan(*policy.MaxAmount) {
			return nil, security.ErrAboveMaximum
		}
	}

	dailyKey, monthlyKey := periodKeys(time.Now())

	dailyUsed, dailyCount, err := lockUsageRow(ctx, tx, in.UserID, in.Operation, assetCode, "daily", dailyKey)
	if err != nil {
		return nil, err
	}
	monthlyUsed, monthlyCount, err := lockUsageRow(ctx, tx, in.UserID, in.Operation, assetCode, "monthly", monthlyKey)
	if err != nil {
		return nil, err
	}

	if policy != nil {
		if policy.DailyLimit != nil && dailyUsed.Add(in.Amount).GreaterThan(*policy.DailyLimit) {
			return nil, security.ErrDailyLimitExceeded
		}
		if policy.DailyCountLimit != nil && dailyCount+1 > *policy.DailyCountLimit {
			return nil, security.ErrDailyCountExceeded
		}
		if policy.MonthlyLimit != nil && monthlyUsed.Add(in.Amount).GreaterThan(*policy.MonthlyLimit) {
			return nil, security.ErrMonthlyLimitExceeded
		}
		if policy.MonthlyCountLimit != nil && monthlyCount+1 > *policy.MonthlyCountLimit {
			return nil, security.ErrMonthlyCountExceeded
		}
	}

	if _, err := tx.Exec(ctx, `
		UPDATE limit_usage SET used_amount = used_amount + $1, used_count = used_count + 1
		WHERE user_id = $2 AND operation = $3 AND asset_code = $4 AND period_type = 'daily' AND period_key = $5
	`, in.Amount, in.UserID, string(in.Operation), assetCode, dailyKey); err != nil {
		return nil, fmt.Errorf("security: actualizar uso diario: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE limit_usage SET used_amount = used_amount + $1, used_count = used_count + 1
		WHERE user_id = $2 AND operation = $3 AND asset_code = $4 AND period_type = 'monthly' AND period_key = $5
	`, in.Amount, in.UserID, string(in.Operation), assetCode, monthlyKey); err != nil {
		return nil, fmt.Errorf("security: actualizar uso mensual: %w", err)
	}

	reservationID := uuid.New()
	var refID *string
	if in.ReferenceID != "" {
		refID = &in.ReferenceID
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO limit_reservations (id, user_id, operation, asset_code, amount, period_key_daily,
			period_key_monthly, status, reference_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'active', $8)
	`, reservationID, in.UserID, string(in.Operation), assetCode, in.Amount, dailyKey, monthlyKey, refID); err != nil {
		return nil, fmt.Errorf("security: insertar reserva: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("security: commit de reserva: %w", err)
	}

	return &security.Reservation{ID: reservationID, UserID: in.UserID, Operation: in.Operation, AssetCode: assetCode, Amount: in.Amount}, nil
}

// lockUsageRow ensures a limit_usage row exists for the given scope, locks
// it with FOR UPDATE and returns its current used_amount/used_count.
// Always called in the same (daily, then monthly) order by Reserve to
// avoid lock-ordering deadlocks between concurrent reservations for the
// same user.
func lockUsageRow(ctx context.Context, tx pgx.Tx, userID uuid.UUID, op security.OperationType, assetCode, periodType, periodKey string) (decimal.Decimal, int, error) {
	if _, err := tx.Exec(ctx, `
		INSERT INTO limit_usage (user_id, operation, asset_code, period_type, period_key)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (user_id, operation, asset_code, period_type, period_key) DO NOTHING
	`, userID, string(op), assetCode, periodType, periodKey); err != nil {
		return decimal.Zero, 0, fmt.Errorf("security: crear fila de uso (%s): %w", periodType, err)
	}

	var used decimal.Decimal
	var count int
	err := tx.QueryRow(ctx, `
		SELECT used_amount, used_count FROM limit_usage
		WHERE user_id = $1 AND operation = $2 AND asset_code = $3 AND period_type = $4 AND period_key = $5
		FOR UPDATE
	`, userID, string(op), assetCode, periodType, periodKey).Scan(&used, &count)
	if err != nil {
		return decimal.Zero, 0, fmt.Errorf("security: lock de uso (%s): %w", periodType, err)
	}
	return used, count, nil
}

func (r *SecurityLimitRepository) Commit(ctx context.Context, reservationID uuid.UUID) error {
	var id uuid.UUID
	err := r.pool.QueryRow(ctx, `
		UPDATE limit_reservations SET status = 'committed', resolved_at = now()
		WHERE id = $1 AND status = 'active'
		RETURNING id
	`, reservationID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return classifyMissingReservation(ctx, r.pool, reservationID)
	}
	if err != nil {
		return fmt.Errorf("security: confirmar reserva: %w", err)
	}
	return nil
}

// Release cancels an active reservation and gives its amount back to the
// user's daily and monthly usage, locking both usage rows and the
// reservation itself inside one transaction.
func (r *SecurityLimitRepository) Release(ctx context.Context, reservationID uuid.UUID) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("security: iniciar tx de liberación: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var (
		userID                           uuid.UUID
		operation, assetCode             string
		amount                           decimal.Decimal
		periodKeyDaily, periodKeyMonthly string
		status                           string
	)
	err = tx.QueryRow(ctx, `
		SELECT user_id, operation, asset_code, amount, period_key_daily, period_key_monthly, status
		FROM limit_reservations WHERE id = $1 FOR UPDATE
	`, reservationID).Scan(&userID, &operation, &assetCode, &amount, &periodKeyDaily, &periodKeyMonthly, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return security.ErrReservationNotFound
	}
	if err != nil {
		return fmt.Errorf("security: leer reserva: %w", err)
	}
	if status != "active" {
		return security.ErrReservationNotActive
	}

	if _, err := tx.Exec(ctx, `
		UPDATE limit_usage SET used_amount = used_amount - $1, used_count = GREATEST(used_count - 1, 0)
		WHERE user_id = $2 AND operation = $3 AND asset_code = $4 AND period_type = 'daily' AND period_key = $5
	`, amount, userID, operation, assetCode, periodKeyDaily); err != nil {
		return fmt.Errorf("security: revertir uso diario: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE limit_usage SET used_amount = used_amount - $1, used_count = GREATEST(used_count - 1, 0)
		WHERE user_id = $2 AND operation = $3 AND asset_code = $4 AND period_type = 'monthly' AND period_key = $5
	`, amount, userID, operation, assetCode, periodKeyMonthly); err != nil {
		return fmt.Errorf("security: revertir uso mensual: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE limit_reservations SET status = 'released', resolved_at = now() WHERE id = $1
	`, reservationID); err != nil {
		return fmt.Errorf("security: marcar reserva liberada: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("security: commit de liberación: %w", err)
	}
	return nil
}

func classifyMissingReservation(ctx context.Context, pool *pgxpool.Pool, reservationID uuid.UUID) error {
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM limit_reservations WHERE id = $1)`, reservationID).Scan(&exists); err != nil {
		return fmt.Errorf("security: verificar reserva: %w", err)
	}
	if !exists {
		return security.ErrReservationNotFound
	}
	return security.ErrReservationNotActive
}

// ---------------------------------------------------------------------
// SecurityEventRepository — security.EventRepository
// ---------------------------------------------------------------------

type SecurityEventRepository struct{ pool *pgxpool.Pool }

func NewSecurityEventRepository(pool *pgxpool.Pool) *SecurityEventRepository {
	return &SecurityEventRepository{pool: pool}
}

func (r *SecurityEventRepository) Insert(ctx context.Context, e security.Event) error {
	var metaJSON []byte
	if e.Metadata != nil {
		var err error
		metaJSON, err = json.Marshal(e.Metadata)
		if err != nil {
			return fmt.Errorf("security: serializar metadata de evento: %w", err)
		}
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO security_events (id, event_type, severity, user_id, actor_role, operation,
			resource_type, resource_id, ip_address, request_id, metadata, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
	`, uuid.New(), string(e.Type), e.Severity, e.UserID, nullString(e.ActorRole), nullOperation(e.Operation),
		nullString(e.ResourceType), e.ResourceID, nullIP(e.IP), nullString(e.RequestID), metaJSON, e.CreatedAt)
	if err != nil {
		return fmt.Errorf("security: insertar evento de seguridad: %w", err)
	}
	return nil
}

// List returns the most recent security_events, newest first, optionally
// filtered to one user — used by the admin security dashboard (spec §16:
// events must be auditable/reviewable, not just written and forgotten).
func (r *SecurityEventRepository) List(ctx context.Context, userID *uuid.UUID, limit, offset int) ([]security.Event, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := r.pool.Query(ctx, `
		SELECT event_type, severity, user_id, actor_role, operation, resource_type, resource_id,
		       ip_address, request_id, metadata, created_at
		FROM security_events
		WHERE ($1::uuid IS NULL OR user_id = $1)
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("security: listar eventos de seguridad: %w", err)
	}
	defer rows.Close()

	var out []security.Event
	for rows.Next() {
		var (
			eventType, severity                         string
			eventUserID                                 *uuid.UUID
			actorRole, operation, resourceType, ip, req *string
			resourceID                                  *uuid.UUID
			metaJSON                                    []byte
			createdAt                                   time.Time
		)
		if err := rows.Scan(&eventType, &severity, &eventUserID, &actorRole, &operation, &resourceType,
			&resourceID, &ip, &req, &metaJSON, &createdAt); err != nil {
			return nil, err
		}
		e := security.Event{
			Type: security.EventType(eventType), Severity: severity, UserID: eventUserID,
			ResourceID: resourceID, CreatedAt: createdAt,
		}
		if actorRole != nil {
			e.ActorRole = *actorRole
		}
		if operation != nil {
			e.Operation = security.OperationType(*operation)
		}
		if resourceType != nil {
			e.ResourceType = *resourceType
		}
		if ip != nil {
			e.IP = *ip
		}
		if req != nil {
			e.RequestID = *req
		}
		if len(metaJSON) > 0 {
			var meta map[string]any
			if err := json.Unmarshal(metaJSON, &meta); err == nil {
				e.Metadata = meta
			}
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func nullString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func nullOperation(op security.OperationType) *string {
	if op == "" {
		return nil
	}
	s := string(op)
	return &s
}

// ---------------------------------------------------------------------
// SecurityFreezeRepository — security.FreezeRepository
// ---------------------------------------------------------------------

type SecurityFreezeRepository struct{ pool *pgxpool.Pool }

func NewSecurityFreezeRepository(pool *pgxpool.Pool) *SecurityFreezeRepository {
	return &SecurityFreezeRepository{pool: pool}
}

func (r *SecurityFreezeRepository) Get(ctx context.Context, op security.OperationType) (security.FreezeStatus, error) {
	var frozen bool
	var reason *string
	var frozenBy *uuid.UUID
	var frozenAt *time.Time
	err := r.pool.QueryRow(ctx, `
		SELECT frozen, reason, frozen_by, frozen_at FROM security_freezes WHERE operation = $1
	`, string(op)).Scan(&frozen, &reason, &frozenBy, &frozenAt)
	if errors.Is(err, pgx.ErrNoRows) {
		// No row = never configured for this operation = not frozen.
		return security.FreezeStatus{Operation: op, Frozen: false}, nil
	}
	if err != nil {
		return security.FreezeStatus{}, fmt.Errorf("security: consultar freeze de %s: %w", op, err)
	}
	status := security.FreezeStatus{Operation: op, Frozen: frozen, FrozenBy: frozenBy, FrozenAt: frozenAt}
	if reason != nil {
		status.Reason = *reason
	}
	return status, nil
}

func (r *SecurityFreezeRepository) List(ctx context.Context) ([]security.FreezeStatus, error) {
	rows, err := r.pool.Query(ctx, `SELECT operation, frozen, reason, frozen_by, frozen_at FROM security_freezes ORDER BY operation`)
	if err != nil {
		return nil, fmt.Errorf("security: listar freezes: %w", err)
	}
	defer rows.Close()

	var out []security.FreezeStatus
	for rows.Next() {
		var operation string
		var frozen bool
		var reason *string
		var frozenBy *uuid.UUID
		var frozenAt *time.Time
		if err := rows.Scan(&operation, &frozen, &reason, &frozenBy, &frozenAt); err != nil {
			return nil, err
		}
		s := security.FreezeStatus{Operation: security.OperationType(operation), Frozen: frozen, FrozenBy: frozenBy, FrozenAt: frozenAt}
		if reason != nil {
			s.Reason = *reason
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *SecurityFreezeRepository) Set(ctx context.Context, op security.OperationType, frozen bool, reason string, actorID uuid.UUID) error {
	now := time.Now().UTC()
	var frozenAtArg *time.Time
	if frozen {
		frozenAtArg = &now
	}
	var actor *uuid.UUID
	if actorID != uuid.Nil {
		actor = &actorID
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO security_freezes (operation, frozen, reason, frozen_by, frozen_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (operation) DO UPDATE SET
			frozen = EXCLUDED.frozen, reason = EXCLUDED.reason,
			frozen_by = EXCLUDED.frozen_by, frozen_at = EXCLUDED.frozen_at, updated_at = now()
	`, string(op), frozen, nullString(reason), actor, frozenAtArg)
	if err != nil {
		return fmt.Errorf("security: actualizar freeze de %s: %w", op, err)
	}
	return nil
}

// ---------------------------------------------------------------------
// SecurityDestinationRepository — security.DestinationRepository
// ---------------------------------------------------------------------

type SecurityDestinationRepository struct{ pool *pgxpool.Pool }

func NewSecurityDestinationRepository(pool *pgxpool.Pool) *SecurityDestinationRepository {
	return &SecurityDestinationRepository{pool: pool}
}

func (r *SecurityDestinationRepository) Lookup(ctx context.Context, userID uuid.UUID, assetCode, network, hash string) (security.DestinationStatus, error) {
	var status string
	err := r.pool.QueryRow(ctx, `
		SELECT status FROM known_destinations
		WHERE user_id = $1 AND asset_code = $2 AND network = $3 AND destination_hash = $4
	`, userID, assetCode, network, hash).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return security.DestinationNew, nil
	}
	if err != nil {
		return "", fmt.Errorf("security: consultar destino: %w", err)
	}
	if status == "pending" {
		// Vista por primera vez y todavía en espera: para el guardián sigue siendo "nueva".
		return security.DestinationNew, nil
	}
	return security.DestinationStatus(status), nil
}

func (r *SecurityDestinationRepository) RecordUse(ctx context.Context, userID uuid.UUID, assetCode, network, hash string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO known_destinations (user_id, asset_code, network, destination_hash, status, last_used_at, use_count)
		VALUES ($1, $2, $3, $4, 'known', now(), 1)
		ON CONFLICT (user_id, asset_code, network, destination_hash) DO UPDATE SET
			last_used_at = now(), use_count = known_destinations.use_count + 1,
			status = CASE WHEN known_destinations.status IN ('new', 'pending') THEN 'known' ELSE known_destinations.status END
	`, userID, assetCode, network, hash)
	if err != nil {
		return fmt.Errorf("security: registrar uso de destino: %w", err)
	}
	return nil
}

func (r *SecurityDestinationRepository) SetStatus(ctx context.Context, userID uuid.UUID, assetCode, network, hash string, status security.DestinationStatus) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO known_destinations (user_id, asset_code, network, destination_hash, status)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (user_id, asset_code, network, destination_hash) DO UPDATE SET status = EXCLUDED.status
	`, userID, assetCode, network, hash, string(status))
	if err != nil {
		return fmt.Errorf("security: cambiar estado de destino: %w", err)
	}
	return nil
}

// RegisterPending implementa security.DestinationCooldownRepository: registra la
// dirección como 'pending' la primera vez que se ve (sin degradar una fila
// existente) y devuelve cuándo se vio por primera vez.
func (r *SecurityDestinationRepository) RegisterPending(ctx context.Context, userID uuid.UUID, assetCode, network, hash string) (time.Time, bool, error) {
	var firstSeen time.Time
	var created bool
	err := r.pool.QueryRow(ctx, `
		INSERT INTO known_destinations (user_id, asset_code, network, destination_hash, status)
		VALUES ($1, $2, $3, $4, 'pending')
		ON CONFLICT (user_id, asset_code, network, destination_hash)
		DO UPDATE SET updated_at = known_destinations.updated_at
		RETURNING first_seen_at, (xmax = 0)
	`, userID, assetCode, network, hash).Scan(&firstSeen, &created)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("security: registrar destino pendiente: %w", err)
	}
	return firstSeen, created, nil
}
