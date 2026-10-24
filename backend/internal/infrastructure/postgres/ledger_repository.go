package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/application/security"
	"korzadi-wallet/backend/internal/domain"
)

// LedgerRepository implementa domain.LedgerRepository usando PostgreSQL/pgx.
//
// El ledger es la fuente de verdad del dinero. balance_cache es solamente
// un cache derivado y siempre se actualiza dentro de la misma transacción
// SQL que inserta el ledger entry.
//
// Este es EL ÚNICO motor de posteo financiero del sistema (Bloque 2): antes
// existía un segundo camino paralelo (LedgerBlockchainPoster) solo para
// depósitos/retiros blockchain, creado porque este motor no distinguía
// account_type al validar saldo negativo. Esa distinción ahora vive aquí
// (ver el chequeo de balance más abajo), así que todo movimiento de dinero
// — transfers, depósitos blockchain, retiros BSC, comisiones de afiliados,
// pagos de KorzadiVPN — pasa por el mismo código, con el mismo locking y la
// misma idempotencia. Ya no existe un segundo camino.
type LedgerRepository struct {
	pool *pgxpool.Pool
}

func NewLedgerRepository(pool *pgxpool.Pool) *LedgerRepository {
	return &LedgerRepository{pool: pool}
}

// ExecuteTransaction ejecuta una operación financiera atómica.
//
// Garantías:
//   - idempotencia fuerte mediante idempotency_key UNIQUE — un reintento
//     con la misma key devuelve la transacción ya existente, sin crear un
//     segundo movimiento (ni error: es un éxito idempotente).
//   - doble partida: suma cero por asset (excepto TxTypeAdjustment).
//   - locks determinísticos sobre wallet_accounts (orden por UUID
//     ascendente) para evitar deadlocks entre operaciones concurrentes que
//     tocan las mismas dos cuentas en distinto orden.
//   - protección contra saldo negativo aplicada SOLO a cuentas
//     account_type = 'user'. Una cuenta 'system_clearing' puede ir
//     negativa: representa valor reconocido on-chain (Bloque 1), no un
//     saldo gastable, y se reconcilia contra la custodia real por
//     separado — nunca contra cero. Esta es la única distinción de negocio
//     en todo el motor.
//   - balance_cache actualizado atómicamente en la misma transacción SQL.
//   - rollback completo ante cualquier error.
//
// Aislamiento: READ COMMITTED (el default de Postgres) + FOR UPDATE sobre
// cada wallet_account involucrada, en orden determinístico. Esto evita
// tanto lost updates como deadlocks sin necesitar SERIALIZABLE — y sin el
// costo de SERIALIZABLE de abortar transacciones concurrentes válidas que
// tocan la misma fila (algo que iba a volverse frecuente: todos los
// depósitos de un mismo asset/red comparten una única cuenta de clearing).
func (r *LedgerRepository) ExecuteTransaction(
	ctx context.Context,
	txType domain.TransactionType,
	idempotencyKey string,
	referenceID *string,
	description string,
	createdBy *uuid.UUID,
	entries []domain.LedgerEntryInput,
) (*domain.LedgerTransaction, error) {
	if txType == domain.TxTypeAdjustment {
		return nil, domain.ErrAdjustmentRequiresService
	}
	return r.executeTransaction(ctx, txType, idempotencyKey, referenceID, description, createdBy, entries, nil)
}

type ledgerAdjustment struct {
	actorID uuid.UUID
	reason  string
	ip      string
}

// ExecuteAdjustment is the persistence half of application.ledger.Service's
// privileged adjustment flow. PostgreSQL independently checks the local
// transaction marker, super_admin role and non-empty reason before insert.
func (r *LedgerRepository) ExecuteAdjustment(
	ctx context.Context,
	idempotencyKey string,
	reason string,
	actorID uuid.UUID,
	ip string,
	entries []domain.LedgerEntryInput,
) (*domain.LedgerTransaction, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" || actorID == uuid.Nil {
		return nil, fmt.Errorf("adjustment requiere actor y motivo")
	}
	return r.executeTransaction(ctx, domain.TxTypeAdjustment, idempotencyKey, nil, reason, &actorID, entries,
		&ledgerAdjustment{actorID: actorID, reason: reason, ip: ip})
}

func (r *LedgerRepository) executeTransaction(
	ctx context.Context,
	txType domain.TransactionType,
	idempotencyKey string,
	referenceID *string,
	description string,
	createdBy *uuid.UUID,
	entries []domain.LedgerEntryInput,
	adjustment *ledgerAdjustment,
) (*domain.LedgerTransaction, error) {

	if idempotencyKey == "" {
		return nil, fmt.Errorf("idempotency_key requerido")
	}

	if len(entries) == 0 {
		return nil, fmt.Errorf("ledger sin entries")
	}

	// Validamos el balance antes de abrir la transacción.
	if err := validateBalancedEntries(txType, entries); err != nil {
		return nil, err
	}

	// Camino rápido para reintentos normales — evita abrir una transacción
	// SQL completa cuando ya sabemos que esta operación fue procesada.
	existing, err := r.FindByIdempotencyKey(ctx, idempotencyKey)
	if err != nil {
		return nil, fmt.Errorf("buscar idempotency_key: %w", err)
	}
	if existing != nil {
		if err := verifyReplayPayload(ctx, r.pool, existing, txType, entries); err != nil {
			log.Printf("ledger: idempotency_key=%s REUTILIZADA con otro payload (tx=%s tipo=%s, pedido=%s) — rechazada",
				idempotencyKey, existing.ID, existing.Type, txType)
			return nil, err
		}
		if err := verifyAdjustmentReplay(existing, adjustment); err != nil {
			return nil, err
		}
		log.Printf("ledger: idempotency_key=%s ya procesada (tx=%s tipo=%s) — replay idempotente, sin nuevo movimiento",
			idempotencyKey, existing.ID, existing.Type)
		return existing, nil
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("iniciar transacción ledger: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Dentro de la misma transacción volvemos a comprobar la idempotencia:
	// otra petición pudo haber creado la operación después del primer
	// FindByIdempotencyKey() y antes de que esta transacción abriera.
	existing, err = findByIdempotencyKeyTx(ctx, tx, idempotencyKey)
	if err != nil {
		return nil, fmt.Errorf("buscar idempotencia dentro de tx: %w", err)
	}
	if existing != nil {
		if err := verifyReplayPayload(ctx, tx, existing, txType, entries); err != nil {
			return nil, err
		}
		if err := verifyAdjustmentReplay(existing, adjustment); err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit idempotent replay: %w", err)
		}
		return existing, nil
	}

	// Lock determinístico de todas las cuentas involucradas, en orden por
	// UUID — dos operaciones concurrentes que tocan las mismas dos cuentas
	// (p. ej. usuario + clearing) siempre las bloquean en el mismo orden,
	// así que nunca pueden deadlockearse entre sí. De paso, leemos
	// account_type: es la única información que este motor necesita para
	// decidir a qué cuentas aplicar la protección de saldo negativo.
	accountIDs := uniqueAccountIDs(entries)
	accountTypes := make(map[uuid.UUID]string, len(accountIDs))

	for _, id := range accountIDs {
		var lockedID uuid.UUID
		var accountType string

		err := tx.QueryRow(ctx, `
			SELECT id, account_type
			FROM wallet_accounts
			WHERE id = $1
			FOR UPDATE
		`, id).Scan(&lockedID, &accountType)

		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, fmt.Errorf("cuenta %s no existe", id)
			}
			return nil, fmt.Errorf("lock cuenta %s: %w", id, err)
		}
		accountTypes[id] = accountType
	}

	// Insertamos la transacción financiera. idempotency_key tiene UNIQUE en
	// PostgreSQL: si dos peticiones llegan simultáneamente con la misma
	// key, PostgreSQL garantiza que solamente una podrá crear la fila.
	txID := uuid.New()
	if adjustment != nil {
		if _, err := tx.Exec(ctx, `SELECT set_config('korzadi.ledger_adjustment_authorized', 'on', true)`); err != nil {
			return nil, fmt.Errorf("autorizar ajuste en transacción: %w", err)
		}
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO ledger_transactions (
			id, type, status, idempotency_key, reference_id, description, created_by, completed_at
		)
		VALUES ($1, $2, 'completed', $3, $4, $5, $6, now())
	`,
		txID, string(txType), idempotencyKey, referenceID, description, createdBy,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
			// Perdimos la carrera contra una petición idéntica concurrente
			// que ya insertó esta idempotency_key. No es un fallo: es la
			// garantía de idempotencia funcionando. Devolvemos la fila
			// ganadora en vez de propagar el error.
			if winner, lookupErr := r.FindByIdempotencyKey(ctx, idempotencyKey); lookupErr == nil && winner != nil {
				if verr := verifyReplayPayload(ctx, r.pool, winner, txType, entries); verr != nil {
					return nil, verr
				}
				if verr := verifyAdjustmentReplay(winner, adjustment); verr != nil {
					return nil, verr
				}
				return winner, nil
			}
		}
		return nil, fmt.Errorf("insertar ledger_transaction: %w", err)
	}

	// Procesamos las entries (helper compartido con SettleReservations —
	// un único motor de posteo).
	if err := postEntriesTx(ctx, tx, txID, entries, accountTypes); err != nil {
		return nil, err
	}
	if adjustment != nil {
		if err := insertAdjustmentAudit(ctx, tx, txID, idempotencyKey, *adjustment, entries); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit ledger: %w", err)
	}

	log.Printf("ledger: tx=%s tipo=%s idempotency_key=%s entries=%d completada",
		txID, txType, idempotencyKey, len(entries))

	// Recuperamos la transacción ya confirmada desde PostgreSQL.
	resultTx, err := r.FindByIdempotencyKey(ctx, idempotencyKey)
	if err != nil {
		return nil, fmt.Errorf("leer ledger confirmado: %w", err)
	}
	if resultTx == nil {
		return nil, fmt.Errorf("ledger confirmado pero no encontrado por idempotency_key")
	}

	return resultTx, nil
}

func verifyAdjustmentReplay(existing *domain.LedgerTransaction, adjustment *ledgerAdjustment) error {
	if existing.Type != domain.TxTypeAdjustment {
		return nil
	}
	if adjustment == nil || existing.Description != adjustment.reason || existing.CreatedBy == nil || *existing.CreatedBy != adjustment.actorID {
		return domain.ErrIdempotencyPayloadMismatch
	}
	return nil
}

func insertAdjustmentAudit(ctx context.Context, tx pgx.Tx, txID uuid.UUID, idempotencyKey string,
	adjustment ledgerAdjustment, entries []domain.LedgerEntryInput) error {
	entryMetadata := make([]map[string]string, 0, len(entries))
	for _, entry := range entries {
		entryMetadata = append(entryMetadata, map[string]string{
			"wallet_account_id": entry.WalletAccountID.String(),
			"asset_code":        entry.AssetCode,
			"amount":            entry.Amount.String(),
		})
	}
	metadata, err := json.Marshal(map[string]any{
		"reason": adjustment.reason, "idempotency_key": idempotencyKey, "entries": entryMetadata,
	})
	if err != nil {
		return fmt.Errorf("serializar auditoría de adjustment: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (id, actor_user_id, actor_role, action, entity_type, entity_id, metadata, ip_address)
		VALUES ($1,$2,'super_admin','ledger_adjustment_posted','ledger_transaction',$3,$4,$5)
	`, uuid.New(), adjustment.actorID, txID, metadata, nullIP(adjustment.ip)); err != nil {
		return fmt.Errorf("registrar auditoría de adjustment: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO security_events (
			id, event_type, severity, user_id, actor_role, operation, resource_type, resource_id, ip_address, metadata
		)
		VALUES ($1,$2,'warning',$3,'super_admin',$4,'ledger_transaction',$5,$6,$7)
	`, uuid.New(), string(security.EventLedgerAdjustment), adjustment.actorID.String(),
		string(security.OpAdminOperation), txID, nullIP(adjustment.ip), metadata); err != nil {
		return fmt.Errorf("registrar evento de seguridad de adjustment: %w", err)
	}
	return nil
}

// postEntriesTx inserta los ledger_entries de una transacción ya creada y
// actualiza balance_cache, todo dentro de la tx SQL recibida. Es el ÚNICO
// lugar donde se escriben ledger_entries / balance_cache: tanto
// ExecuteTransaction como SettleReservations lo usan, así que sigue
// existiendo un solo motor de posteo. Requisitos del caller: las cuentas
// de `entries` ya están bloqueadas FOR UPDATE y accountTypes contiene su
// account_type.
func postEntriesTx(ctx context.Context, tx pgx.Tx, txID uuid.UUID, entries []domain.LedgerEntryInput, accountTypes map[uuid.UUID]string) error {
	for _, e := range entries {

		if e.Amount.IsNegative() && accountTypes[e.WalletAccountID] == "user" {
			// La protección de saldo negativo se aplica únicamente a
			// cuentas de usuario. Una cuenta system_clearing queda exenta
			// a propósito (ver comentario del método) — es la única
			// excepción de negocio en todo este motor.
			var current decimal.Decimal

			err := tx.QueryRow(ctx, `
				SELECT COALESCE(SUM(amount), 0)
				FROM ledger_entries
				WHERE wallet_account_id = $1
			`, e.WalletAccountID).Scan(&current)

			if err != nil {
				return fmt.Errorf("consultar saldo %s: %w", e.WalletAccountID, err)
			}

			resultingBalance := current.Add(e.Amount)

			if resultingBalance.IsNegative() {
				return domain.ErrInsufficientFunds
			}
		}

		entryID := uuid.New()

		_, err := tx.Exec(ctx, `
			INSERT INTO ledger_entries (id, ledger_transaction_id, wallet_account_id, amount, asset_code)
			VALUES ($1, $2, $3, $4, $5)
		`,
			entryID, txID, e.WalletAccountID, e.Amount, e.AssetCode,
		)
		if err != nil {
			return fmt.Errorf("insertar ledger_entry: %w", err)
		}

		// balance_cache es derivado. Se modifica solamente dentro de esta
		// misma transacción, nunca desde otro lugar del código.
		result, err := tx.Exec(ctx, `
			UPDATE wallet_accounts
			SET balance_cache = balance_cache + $1, updated_at = now()
			WHERE id = $2
		`,
			e.Amount, e.WalletAccountID,
		)
		if err != nil {
			return fmt.Errorf("actualizar balance_cache: %w", err)
		}
		if result.RowsAffected() != 1 {
			return fmt.Errorf("no se pudo actualizar balance_cache de cuenta %s", e.WalletAccountID)
		}
	}
	return nil
}

func validateBalancedEntries(txType domain.TransactionType, entries []domain.LedgerEntryInput) error {
	sums := make(map[string]decimal.Decimal)

	for _, e := range entries {
		if e.WalletAccountID == uuid.Nil {
			return fmt.Errorf("wallet_account_id inválido")
		}
		if e.AssetCode == "" {
			return fmt.Errorf("asset_code requerido")
		}
		sums[e.AssetCode] = sums[e.AssetCode].Add(e.Amount)
	}

	if txType != domain.TxTypeAdjustment {
		for asset, sum := range sums {
			if !sum.IsZero() {
				return fmt.Errorf("ledger desbalanceado para asset %s: suma=%s (debe ser 0)", asset, sum.String())
			}
		}
	}

	return nil
}

func (r *LedgerRepository) GetBalance(ctx context.Context, walletAccountID uuid.UUID) (decimal.Decimal, error) {
	var balance decimal.Decimal
	err := r.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount), 0)
		FROM ledger_entries
		WHERE wallet_account_id = $1
	`, walletAccountID).Scan(&balance)
	return balance, err
}

func (r *LedgerRepository) FindByIdempotencyKey(ctx context.Context, key string) (*domain.LedgerTransaction, error) {
	return findByIdempotencyKeyPool(ctx, r.pool, key)
}

func findByIdempotencyKeyPool(ctx context.Context, pool *pgxpool.Pool, key string) (*domain.LedgerTransaction, error) {
	row := pool.QueryRow(ctx, `
		SELECT id, type, status, idempotency_key, reference_id, description, created_by, created_at, updated_at, completed_at
		FROM ledger_transactions
		WHERE idempotency_key = $1
	`, key)
	return scanLedgerTransaction(row)
}

func findByIdempotencyKeyTx(ctx context.Context, tx pgx.Tx, key string) (*domain.LedgerTransaction, error) {
	row := tx.QueryRow(ctx, `
		SELECT id, type, status, idempotency_key, reference_id, description, created_by, created_at, updated_at, completed_at
		FROM ledger_transactions
		WHERE idempotency_key = $1
	`, key)
	return scanLedgerTransaction(row)
}

type ledgerRowScanner interface {
	Scan(dest ...any) error
}

func scanLedgerTransaction(row ledgerRowScanner) (*domain.LedgerTransaction, error) {
	var t domain.LedgerTransaction
	var typ string
	var status string

	err := row.Scan(
		&t.ID, &typ, &status, &t.IdempotencyKey, &t.ReferenceID, &t.Description,
		&t.CreatedBy, &t.CreatedAt, &t.UpdatedAt, &t.CompletedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	t.Type = domain.TransactionType(typ)
	t.Status = domain.TransactionStatus(status)
	return &t, nil
}

func (r *LedgerRepository) ListEntriesByAccount(ctx context.Context, walletAccountID uuid.UUID, limit, offset int) ([]*domain.LedgerEntry, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, ledger_transaction_id, wallet_account_id, amount, asset_code, created_at
		FROM ledger_entries
		WHERE wallet_account_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`, walletAccountID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*domain.LedgerEntry
	for rows.Next() {
		var e domain.LedgerEntry
		if err := rows.Scan(&e.ID, &e.LedgerTransactionID, &e.WalletAccountID, &e.Amount, &e.AssetCode, &e.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, &e)
	}
	return result, rows.Err()
}

func uniqueAccountIDs(entries []domain.LedgerEntryInput) []uuid.UUID {
	seen := make(map[uuid.UUID]bool)
	ids := make([]uuid.UUID, 0, len(entries))

	for _, e := range entries {
		if !seen[e.WalletAccountID] {
			seen[e.WalletAccountID] = true
			ids = append(ids, e.WalletAccountID)
		}
	}

	sort.Slice(ids, func(i, j int) bool {
		return ids[i].String() < ids[j].String()
	})

	return ids
}
