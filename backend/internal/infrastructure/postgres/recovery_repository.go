package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	apprecovery "korzadi-wallet/backend/internal/application/recovery"
)

// ---- RecoveryRepository: recovery_incidents / recovery_events (0014) ----

type RecoveryRepository struct{ pool *pgxpool.Pool }

func NewRecoveryRepository(pool *pgxpool.Pool) *RecoveryRepository {
	return &RecoveryRepository{pool: pool}
}

const recoveryIncidentColumns = `id, subsystem, item_id, kind, status, detail, detected_at,
	resolved_at, resolved_by, resolution_note, created_at, updated_at`

type recoveryRowScanner interface {
	Scan(dest ...any) error
}

func scanRecoveryIncident(row recoveryRowScanner) (*apprecovery.Incident, error) {
	var in apprecovery.Incident
	var subsystem, kind, status string
	var detailRaw []byte
	var resolvedBy, note *string

	err := row.Scan(&in.ID, &subsystem, &in.ItemID, &kind, &status, &detailRaw, &in.DetectedAt,
		&in.ResolvedAt, &resolvedBy, &note, &in.CreatedAt, &in.UpdatedAt)
	if err != nil {
		return nil, err
	}
	in.Subsystem = apprecovery.Subsystem(subsystem)
	in.Kind = apprecovery.Kind(kind)
	in.Status = apprecovery.IncidentStatus(status)
	in.ResolvedBy = derefString(resolvedBy)
	in.ResolutionNote = derefString(note)
	if len(detailRaw) > 0 {
		_ = json.Unmarshal(detailRaw, &in.Detail)
	}
	return &in, nil
}

// Open creates a new OPEN incident, or returns the already-open one for
// the same (subsystem, item_id, kind) untouched — see
// uq_recovery_incidents_open_subject_kind (0014). Idempotent by
// construction: a repeated detection pass NEVER creates a duplicate row.
func (r *RecoveryRepository) Open(ctx context.Context, in *apprecovery.Incident, ev apprecovery.Event) (*apprecovery.Incident, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("recovery: iniciar tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	detailJSON, err := json.Marshal(in.Detail)
	if err != nil {
		return nil, fmt.Errorf("recovery: serializar detail: %w", err)
	}

	existing, err := scanRecoveryIncident(tx.QueryRow(ctx, `
		SELECT `+recoveryIncidentColumns+` FROM recovery_incidents
		WHERE subsystem = $1 AND item_id = $2 AND kind = $3 AND status IN ('open','acknowledged')
	`, string(in.Subsystem), in.ItemID, string(in.Kind)))
	if err == nil {
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return nil, fmt.Errorf("recovery: commit: %w", commitErr)
		}
		return existing, nil // already open — idempotent no-op
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("recovery: buscar incidente existente: %w", err)
	}

	id := uuid.New()
	row := tx.QueryRow(ctx, `
		INSERT INTO recovery_incidents (id, subsystem, item_id, kind, status, detail, detected_at)
		VALUES ($1,$2,$3,$4,'open',$5,$6)
		RETURNING `+recoveryIncidentColumns,
		id, string(in.Subsystem), in.ItemID, string(in.Kind), detailJSON, in.DetectedAt)

	created, err := scanRecoveryIncident(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			// Lost a race against a concurrent detection pass — re-read
			// the winner instead of failing (same idempotent-race handling
			// as ledger.ExecuteTransaction's idempotency_key race).
			existing, gerr := scanRecoveryIncident(tx.QueryRow(ctx, `
				SELECT `+recoveryIncidentColumns+` FROM recovery_incidents
				WHERE subsystem = $1 AND item_id = $2 AND kind = $3 AND status IN ('open','acknowledged')
			`, string(in.Subsystem), in.ItemID, string(in.Kind)))
			if gerr == nil {
				_ = tx.Commit(ctx)
				return existing, nil
			}
		}
		return nil, fmt.Errorf("recovery: crear incidente: %w", err)
	}
	if err := insertRecoveryEvent(ctx, tx, created.ID, ev.Type, ev.Detail); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("recovery: commit: %w", err)
	}
	return created, nil
}

func (r *RecoveryRepository) Get(ctx context.Context, id uuid.UUID) (*apprecovery.Incident, error) {
	in, err := scanRecoveryIncident(r.pool.QueryRow(ctx, `SELECT `+recoveryIncidentColumns+` FROM recovery_incidents WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return in, err
}

func (r *RecoveryRepository) List(ctx context.Context, f apprecovery.Filter) ([]*apprecovery.Incident, error) {
	var conditions []string
	var args []any
	if f.Subsystem != nil {
		args = append(args, string(*f.Subsystem))
		conditions = append(conditions, fmt.Sprintf("subsystem = $%d", len(args)))
	}
	if f.Status != nil {
		args = append(args, string(*f.Status))
		conditions = append(conditions, fmt.Sprintf("status = $%d", len(args)))
	}
	if f.Kind != nil {
		args = append(args, string(*f.Kind))
		conditions = append(conditions, fmt.Sprintf("kind = $%d", len(args)))
	}
	query := `SELECT ` + recoveryIncidentColumns + ` FROM recovery_incidents`
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY detected_at DESC"

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("recovery: listar incidentes: %w", err)
	}
	defer rows.Close()

	var out []*apprecovery.Incident
	for rows.Next() {
		in, err := scanRecoveryIncident(rows)
		if err != nil {
			return nil, fmt.Errorf("recovery: leer incidente: %w", err)
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

func (r *RecoveryRepository) Resolve(ctx context.Context, id uuid.UUID, resolvedBy, note string) (*apprecovery.Incident, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("recovery: iniciar tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	row := tx.QueryRow(ctx, `
		UPDATE recovery_incidents
		SET status = 'resolved', resolved_at = now(), resolved_by = $2, resolution_note = $3
		WHERE id = $1 AND status IN ('open','acknowledged')
		RETURNING `+recoveryIncidentColumns,
		id, resolvedBy, note)

	resolved, err := scanRecoveryIncident(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("recovery: resolver incidente: %w", err)
	}
	if err := insertRecoveryEvent(ctx, tx, id, "recovery_completed", map[string]any{"resolved_by": resolvedBy, "note": note}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("recovery: commit: %w", err)
	}
	return resolved, nil
}

func (r *RecoveryRepository) AddEvent(ctx context.Context, incidentID uuid.UUID, eventType string, detail map[string]any) error {
	return insertRecoveryEvent(ctx, r.pool, incidentID, eventType, detail)
}

type recoveryExecer interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

func insertRecoveryEvent(ctx context.Context, q recoveryExecer, incidentID uuid.UUID, eventType string, detail map[string]any) error {
	var detailArg any
	if len(detail) > 0 {
		encoded, err := json.Marshal(detail)
		if err != nil {
			return fmt.Errorf("recovery: serializar evento: %w", err)
		}
		detailArg = encoded
	}
	_, err := q.Exec(ctx, `
		INSERT INTO recovery_events (incident_id, event_type, detail)
		VALUES ($1, $2, $3)
	`, incidentID, eventType, detailArg)
	if err != nil {
		return fmt.Errorf("recovery: registrar evento %s: %w", eventType, err)
	}
	return nil
}

// ================================================================
// RecoverableStore adapters — one per watched subsystem (7.2/7.3/7.4).
// Each is a thin read/limited-write wrapper over that subsystem's OWN
// table; none of them introduce a new column or a new state field.
// ================================================================

// ---- Sweeps (7.3) ----

type recoverableSweeps struct{ pool *pgxpool.Pool }

func NewRecoverableSweeps(pool *pgxpool.Pool) *recoverableSweeps {
	return &recoverableSweeps{pool: pool}
}

func (a *recoverableSweeps) Subsystem() apprecovery.Subsystem { return apprecovery.SubsystemSweep }

func (a *recoverableSweeps) ScanActive(ctx context.Context, limit int) ([]apprecovery.Item, error) {
	return scanRecoverableItems(ctx, a.pool, `
		SELECT id, status, needs_review, COALESCE(error_detail,''), attempts, COALESCE(locked_by,''), locked_until, created_at, updated_at
		FROM sweeps WHERE status IN ('pending','processing','broadcasted','confirming') ORDER BY updated_at ASC LIMIT $1
	`, limit)
}

func (a *recoverableSweeps) ScanNeedsReview(ctx context.Context, limit int) ([]apprecovery.Item, error) {
	return scanRecoverableItems(ctx, a.pool, `
		SELECT id, status, needs_review, COALESCE(error_detail,''), attempts, COALESCE(locked_by,''), locked_until, created_at, updated_at
		FROM sweeps WHERE needs_review = true ORDER BY updated_at DESC LIMIT $1
	`, limit)
}

func (a *recoverableSweeps) ScanFailed(ctx context.Context, since time.Time, limit int) ([]apprecovery.Item, error) {
	return scanRecoverableItems(ctx, a.pool, `
		SELECT id, status, needs_review, COALESCE(error_detail,''), attempts, COALESCE(locked_by,''), locked_until, created_at, updated_at
		FROM sweeps WHERE status = 'failed' AND updated_at > $2 ORDER BY updated_at DESC LIMIT $1
	`, limit, since)
}

// Unstick solo limpia leases YA vencidos: con varias réplicas, otro worker pudo
// reclamar la fila entre el escaneo y esta escritura, y su lease vigente no se toca.
func (a *recoverableSweeps) Unstick(ctx context.Context, id uuid.UUID) error {
	_, err := a.pool.Exec(ctx, `UPDATE sweeps SET locked_by = NULL, locked_until = NULL WHERE id = $1 AND (locked_until IS NULL OR locked_until < now())`, id)
	return err
}

func (a *recoverableSweeps) EscalateToReview(ctx context.Context, id uuid.UUID, reason string) error {
	_, err := a.pool.Exec(ctx, `
		UPDATE sweeps SET needs_review = true, error_detail = COALESCE(error_detail, '') || CASE WHEN error_detail IS NULL OR error_detail = '' THEN '' ELSE ' | ' END || $2
		WHERE id = $1 AND needs_review = false
	`, id, "recovery_escalated: "+reason)
	return err
}

// ---- Treasury rebalances (7.4) ----

type recoverableTreasuryRebalances struct{ pool *pgxpool.Pool }

func NewRecoverableTreasuryRebalances(pool *pgxpool.Pool) *recoverableTreasuryRebalances {
	return &recoverableTreasuryRebalances{pool: pool}
}

func (a *recoverableTreasuryRebalances) Subsystem() apprecovery.Subsystem {
	return apprecovery.SubsystemTreasuryRebalance
}

func (a *recoverableTreasuryRebalances) ScanActive(ctx context.Context, limit int) ([]apprecovery.Item, error) {
	return scanRecoverableItems(ctx, a.pool, `
		SELECT id, status, needs_review, COALESCE(error_detail,''), attempts, COALESCE(locked_by,''), locked_until, created_at, updated_at
		FROM treasury_rebalances WHERE status IN ('pending','processing','broadcasted','confirming') ORDER BY updated_at ASC LIMIT $1
	`, limit)
}

func (a *recoverableTreasuryRebalances) ScanNeedsReview(ctx context.Context, limit int) ([]apprecovery.Item, error) {
	return scanRecoverableItems(ctx, a.pool, `
		SELECT id, status, needs_review, COALESCE(error_detail,''), attempts, COALESCE(locked_by,''), locked_until, created_at, updated_at
		FROM treasury_rebalances WHERE needs_review = true ORDER BY updated_at DESC LIMIT $1
	`, limit)
}

func (a *recoverableTreasuryRebalances) ScanFailed(ctx context.Context, since time.Time, limit int) ([]apprecovery.Item, error) {
	return scanRecoverableItems(ctx, a.pool, `
		SELECT id, status, needs_review, COALESCE(error_detail,''), attempts, COALESCE(locked_by,''), locked_until, created_at, updated_at
		FROM treasury_rebalances WHERE status = 'failed' AND updated_at > $2 ORDER BY updated_at DESC LIMIT $1
	`, limit, since)
}

func (a *recoverableTreasuryRebalances) Unstick(ctx context.Context, id uuid.UUID) error {
	_, err := a.pool.Exec(ctx, `UPDATE treasury_rebalances SET locked_by = NULL, locked_until = NULL WHERE id = $1 AND (locked_until IS NULL OR locked_until < now())`, id)
	return err
}

func (a *recoverableTreasuryRebalances) EscalateToReview(ctx context.Context, id uuid.UUID, reason string) error {
	_, err := a.pool.Exec(ctx, `
		UPDATE treasury_rebalances SET needs_review = true, error_detail = COALESCE(error_detail, '') || CASE WHEN error_detail IS NULL OR error_detail = '' THEN '' ELSE ' | ' END || $2
		WHERE id = $1 AND needs_review = false
	`, id, "recovery_escalated: "+reason)
	return err
}

// ---- Gas station refills (7.2) ----
//
// gas_station_refills (0010) has NO needs_review, attempts, locked_by or
// locked_until columns — it is processed synchronously by
// gasstation.Service, not by an async leased worker like sweeps/treasury.
// Unstick is therefore a documented no-op (there is nothing to unlock);
// EscalateToReview's equivalent here is moving a stuck
// pending/broadcasted row to 'failed' with an explanatory error_detail —
// that IS this subsystem's own review state (a failed refill already
// surfaces via ScanFailed and via 7.2's own audit trail).
type recoverableGasStationRefills struct{ pool *pgxpool.Pool }

func NewRecoverableGasStationRefills(pool *pgxpool.Pool) *recoverableGasStationRefills {
	return &recoverableGasStationRefills{pool: pool}
}

func (a *recoverableGasStationRefills) Subsystem() apprecovery.Subsystem {
	return apprecovery.SubsystemGasStationRefill
}

func (a *recoverableGasStationRefills) ScanActive(ctx context.Context, limit int) ([]apprecovery.Item, error) {
	rows, err := a.pool.Query(ctx, `
		SELECT id, status, COALESCE(error_detail,''), created_at, updated_at
		FROM gas_station_refills WHERE status IN ('pending','broadcasted') ORDER BY updated_at ASC LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []apprecovery.Item
	for rows.Next() {
		var it apprecovery.Item
		if err := rows.Scan(&it.ID, &it.Status, &it.ErrorDetail, &it.CreatedAt, &it.UpdatedAt); err != nil {
			return nil, err
		}
		it.Subsystem = apprecovery.SubsystemGasStationRefill
		out = append(out, it)
	}
	return out, rows.Err()
}

func (a *recoverableGasStationRefills) ScanNeedsReview(ctx context.Context, limit int) ([]apprecovery.Item, error) {
	return nil, nil // no needs_review column for this subsystem — see type doc
}

func (a *recoverableGasStationRefills) ScanFailed(ctx context.Context, since time.Time, limit int) ([]apprecovery.Item, error) {
	rows, err := a.pool.Query(ctx, `
		SELECT id, status, COALESCE(error_detail,''), created_at, updated_at
		FROM gas_station_refills WHERE status = 'failed' AND updated_at > $2 ORDER BY updated_at DESC LIMIT $1
	`, limit, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []apprecovery.Item
	for rows.Next() {
		var it apprecovery.Item
		if err := rows.Scan(&it.ID, &it.Status, &it.ErrorDetail, &it.CreatedAt, &it.UpdatedAt); err != nil {
			return nil, err
		}
		it.Subsystem = apprecovery.SubsystemGasStationRefill
		out = append(out, it)
	}
	return out, rows.Err()
}

func (a *recoverableGasStationRefills) Unstick(ctx context.Context, id uuid.UUID) error {
	return nil // no lock columns on this subsystem — nothing to clear, see type doc
}

func (a *recoverableGasStationRefills) EscalateToReview(ctx context.Context, id uuid.UUID, reason string) error {
	_, err := a.pool.Exec(ctx, `
		UPDATE gas_station_refills SET status = 'failed', error_detail = COALESCE(error_detail, '') || CASE WHEN error_detail IS NULL OR error_detail = '' THEN '' ELSE ' | ' END || $2
		WHERE id = $1 AND status IN ('pending','broadcasted')
	`, id, "recovery_escalated: "+reason)
	return err
}

// ---- shared scan helper (sweeps/treasury share identical column shape) ----

func scanRecoverableItems(ctx context.Context, pool *pgxpool.Pool, query string, args ...any) ([]apprecovery.Item, error) {
	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []apprecovery.Item
	for rows.Next() {
		var it apprecovery.Item
		var lockedBy string
		if err := rows.Scan(&it.ID, &it.Status, &it.NeedsReview, &it.ErrorDetail, &it.Attempts, &lockedBy, &it.LockedUntil, &it.CreatedAt, &it.UpdatedAt); err != nil {
			return nil, err
		}
		it.LockedBy = lockedBy
		out = append(out, it)
	}
	return out, rows.Err()
}
