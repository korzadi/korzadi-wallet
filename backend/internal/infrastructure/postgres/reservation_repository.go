package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
)

// ReservationRepository implementa domain.ReservationRepository — la base
// lock/release/consume para operaciones futuras que necesiten retener
// fondos sin moverlos todavía (spec Bloque 2 §"RESERVAS"). No crea ningún
// ledger_transaction: solo mueve balance entre "available" y "locked"
// dentro de wallet_accounts.locked_balance.
type ReservationRepository struct {
	pool *pgxpool.Pool
}

func NewReservationRepository(pool *pgxpool.Pool) *ReservationRepository {
	return &ReservationRepository{pool: pool}
}

// Reserve mueve `amount` de available a locked, bajo un lock de fila sobre
// la wallet_account — dos reservas concurrentes sobre la misma cuenta
// nunca pueden ambas ver el mismo saldo "available" desactualizado, porque
// la segunda espera a que la primera libere el lock (FOR UPDATE) antes de
// leer el balance.
func (r *ReservationRepository) Reserve(ctx context.Context, walletAccountID uuid.UUID, assetCode string, amount decimal.Decimal, reason string, referenceID *string) (*domain.Reservation, error) {
	if !amount.IsPositive() {
		return nil, fmt.Errorf("reservation: el monto debe ser positivo")
	}
	if reason == "" {
		return nil, fmt.Errorf("reservation: reason requerido (auditoría)")
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("reservation: iniciar tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var balanceCache, lockedBalance decimal.Decimal
	var accountType string

	err = tx.QueryRow(ctx, `
		SELECT balance_cache, locked_balance, account_type
		FROM wallet_accounts
		WHERE id = $1
		FOR UPDATE
	`, walletAccountID).Scan(&balanceCache, &lockedBalance, &accountType)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("reservation: wallet_account %s no existe", walletAccountID)
		}
		return nil, fmt.Errorf("reservation: lock cuenta: %w", err)
	}

	available := balanceCache.Sub(lockedBalance)
	if available.LessThan(amount) {
		return nil, domain.ErrInsufficientFunds
	}

	reservationID := uuid.New()

	_, err = tx.Exec(ctx, `
		INSERT INTO wallet_account_reservations (id, wallet_account_id, asset_code, amount, status, reason, reference_id)
		VALUES ($1, $2, $3, $4, 'active', $5, $6)
	`, reservationID, walletAccountID, assetCode, amount, reason, referenceID)
	if err != nil {
		return nil, fmt.Errorf("reservation: insertar reserva: %w", err)
	}

	_, err = tx.Exec(ctx, `
		UPDATE wallet_accounts SET locked_balance = locked_balance + $1, updated_at = now()
		WHERE id = $2
	`, amount, walletAccountID)
	if err != nil {
		return nil, fmt.Errorf("reservation: actualizar locked_balance: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("reservation: commit: %w", err)
	}

	return r.FindByID(ctx, reservationID)
}

// Release y Consume comparten la misma mecánica de resolución (mover
// amount fuera de locked_balance, marcar la reserva resuelta) — la única
// diferencia de negocio es si el monto vuelve a available (Release) o
// desaparece del todo porque el caller ya lo debitó vía
// LedgerRepository.ExecuteTransaction (Consume). En ambos casos
// locked_balance simplemente disminuye; Release no toca balance_cache
// (por eso el monto "vuelve a aparecer" en available = balance_cache -
// locked_balance) y Consume tampoco lo toca aquí — el débito real de
// balance_cache ya ocurrió (o debe ocurrir) en la transacción de ledger
// correspondiente, nunca en este repositorio.
func (r *ReservationRepository) Release(ctx context.Context, reservationID uuid.UUID) error {
	return r.resolve(ctx, reservationID, domain.ReservationReleased)
}

func (r *ReservationRepository) Consume(ctx context.Context, reservationID uuid.UUID) error {
	return r.resolve(ctx, reservationID, domain.ReservationConsumed)
}

func (r *ReservationRepository) resolve(ctx context.Context, reservationID uuid.UUID, newStatus domain.ReservationStatus) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("reservation: iniciar tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var walletAccountID uuid.UUID
	var amount decimal.Decimal
	var status string

	err = tx.QueryRow(ctx, `
		SELECT wallet_account_id, amount, status
		FROM wallet_account_reservations
		WHERE id = $1
		FOR UPDATE
	`, reservationID).Scan(&walletAccountID, &amount, &status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrReservationNotFound
		}
		return fmt.Errorf("reservation: lock reserva: %w", err)
	}

	if status != string(domain.ReservationActive) {
		// Resolver una reserva ya resuelta es un bug del caller (doble
		// release, o release después de consume), no un reintento seguro
		// — se hace explícito en vez de tratarlo como no-op silencioso
		// (spec: "no permitir modificaciones silenciosas del historial").
		return domain.ErrReservationNotActive
	}

	_, err = tx.Exec(ctx, `
		UPDATE wallet_account_reservations
		SET status = $1, resolved_at = now()
		WHERE id = $2
	`, string(newStatus), reservationID)
	if err != nil {
		return fmt.Errorf("reservation: actualizar estado: %w", err)
	}

	_, err = tx.Exec(ctx, `
		UPDATE wallet_accounts SET locked_balance = locked_balance - $1, updated_at = now()
		WHERE id = $2
	`, amount, walletAccountID)
	if err != nil {
		return fmt.Errorf("reservation: actualizar locked_balance: %w", err)
	}

	return tx.Commit(ctx)
}

func (r *ReservationRepository) FindByID(ctx context.Context, reservationID uuid.UUID) (*domain.Reservation, error) {
	var res domain.Reservation
	var status string

	err := r.pool.QueryRow(ctx, `
		SELECT id, wallet_account_id, asset_code, amount, status, reason, reference_id, created_at, updated_at, resolved_at
		FROM wallet_account_reservations
		WHERE id = $1
	`, reservationID).Scan(
		&res.ID, &res.WalletAccountID, &res.AssetCode, &res.Amount, &status,
		&res.Reason, &res.ReferenceID, &res.CreatedAt, &res.UpdatedAt, &res.ResolvedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrReservationNotFound
		}
		return nil, fmt.Errorf("reservation: buscar por id: %w", err)
	}
	res.Status = domain.ReservationStatus(status)
	return &res, nil
}

// FindByReference implementa domain.ReservationFinder: devuelve la reserva
// más reciente con ese (reference_id, reason) sea cual sea su estado, o
// (nil, nil) si no existe. Bloque 6B lo usa para que "reservar el monto de
// un retiro" sea idempotente ante un crash entre Reserve y persistir su id.
func (r *ReservationRepository) FindByReference(ctx context.Context, referenceID, reason string) (*domain.Reservation, error) {
	var res domain.Reservation
	var status string

	err := r.pool.QueryRow(ctx, `
		SELECT id, wallet_account_id, asset_code, amount, status, reason, reference_id, created_at, updated_at, resolved_at
		FROM wallet_account_reservations
		WHERE reference_id = $1 AND reason = $2
		ORDER BY created_at DESC
		LIMIT 1
	`, referenceID, reason).Scan(
		&res.ID, &res.WalletAccountID, &res.AssetCode, &res.Amount, &status,
		&res.Reason, &res.ReferenceID, &res.CreatedAt, &res.UpdatedAt, &res.ResolvedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("reservation: buscar por referencia: %w", err)
	}
	res.Status = domain.ReservationStatus(status)
	return &res, nil
}

// GetBalance combina Total (derivado de ledger_entries, la fuente de
// verdad) con Locked (suma de reservas activas) bajo una única consulta,
// evitando una lectura fragmentada entre dos llamadas separadas.
func (r *ReservationRepository) GetBalance(ctx context.Context, walletAccountID uuid.UUID) (domain.Balance, error) {
	var assetCode string
	var total, locked decimal.Decimal

	err := r.pool.QueryRow(ctx, `
		SELECT
			wa.asset_code,
			COALESCE((SELECT SUM(le.amount) FROM ledger_entries le WHERE le.wallet_account_id = wa.id), 0) AS total,
			wa.locked_balance
		FROM wallet_accounts wa
		WHERE wa.id = $1
	`, walletAccountID).Scan(&assetCode, &total, &locked)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Balance{}, fmt.Errorf("reservation: wallet_account %s no existe", walletAccountID)
		}
		return domain.Balance{}, fmt.Errorf("reservation: obtener balance: %w", err)
	}

	return domain.Balance{
		WalletAccountID: walletAccountID,
		AssetCode:       assetCode,
		Total:           total,
		Locked:          locked,
		Available:       total.Sub(locked),
	}, nil
}
