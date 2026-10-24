package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
)

// WatcherFailedEventRepository persiste los eventos on-chain que el watcher
// detectó pero no pudo registrar, para reintentarlos con backoff en vez de
// perderlos. Implementa watcher.FailedEventStore.
type WatcherFailedEventRepository struct {
	pool *pgxpool.Pool
}

func NewWatcherFailedEventRepository(pool *pgxpool.Pool) *WatcherFailedEventRepository {
	return &WatcherFailedEventRepository{pool: pool}
}

const maxStoredErrLen = 1000

func truncateErr(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if len(s) > maxStoredErrLen {
		s = s[:maxStoredErrLen]
	}
	// Cortar a mitad de un carácter UTF-8 haría fallar el INSERT.
	return strings.ToValidUTF8(s, "")
}

// Save aparca el evento. Si ya existía (misma clave natural) incrementa
// intentos y, si estaba 'resolved', lo reabre como 'pending'.
func (r *WatcherFailedEventRepository) Save(ctx context.Context, t domain.BlockchainTransfer, cause error) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO watcher_failed_events
			(network, asset_code, asset_kind, contract_address, tx_hash, log_index, block_number, block_hash,
			 from_address, to_address, amount, decimals, last_error)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		ON CONFLICT (network, contract_address, tx_hash, log_index) DO UPDATE SET
			attempts        = watcher_failed_events.attempts + 1,
			last_error      = EXCLUDED.last_error,
			last_attempt_at = now(),
			status          = CASE WHEN watcher_failed_events.status = 'resolved'
			                       THEN 'pending' ELSE watcher_failed_events.status END
	`, t.Network, t.AssetCode, string(t.AssetKind), t.ContractAddress, t.TxHash, int(t.LogIndex),
		int64(t.BlockNumber), t.BlockHash, t.FromAddress, t.ToAddress, t.Amount, t.Decimals, truncateErr(cause))
	if err != nil {
		return fmt.Errorf("aparcar evento fallido: %w", err)
	}
	return nil
}

// ListRetryable devuelve los eventos 'pending' cuyo backoff (10s × intentos,
// tope 200s) ya venció, del más antiguo al más reciente.
func (r *WatcherFailedEventRepository) ListRetryable(ctx context.Context, network domain.Network, limit int) ([]domain.FailedDepositEvent, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, network, asset_code, asset_kind, contract_address, tx_hash, log_index, block_number,
		       block_hash, from_address, to_address, amount, decimals, attempts, last_error
		FROM watcher_failed_events
		WHERE network = $1
		  AND status = 'pending'
		  AND last_attempt_at <= now() - (LEAST(attempts, 20) * interval '10 seconds')
		ORDER BY last_attempt_at ASC
		LIMIT $2
	`, network, limit)
	if err != nil {
		return nil, fmt.Errorf("listar eventos fallidos: %w", err)
	}
	defer rows.Close()

	var out []domain.FailedDepositEvent
	for rows.Next() {
		var fe domain.FailedDepositEvent
		var kind string
		var logIndex int
		var block int64
		var amount decimal.Decimal
		if err := rows.Scan(&fe.ID, &fe.Transfer.Network, &fe.Transfer.AssetCode, &kind,
			&fe.Transfer.ContractAddress, &fe.Transfer.TxHash, &logIndex, &block,
			&fe.Transfer.BlockHash, &fe.Transfer.FromAddress, &fe.Transfer.ToAddress, &amount,
			&fe.Transfer.Decimals, &fe.Attempts, &fe.LastError); err != nil {
			return nil, fmt.Errorf("leer evento fallido: %w", err)
		}
		fe.Transfer.AssetKind = domain.AssetKind(kind)
		fe.Transfer.LogIndex = uint(logIndex)
		fe.Transfer.BlockNumber = uint64(block)
		fe.Transfer.Amount = amount
		out = append(out, fe)
	}
	return out, rows.Err()
}

func (r *WatcherFailedEventRepository) MarkResolved(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE watcher_failed_events SET status = 'resolved', resolved_at = now()
		WHERE id = $1 AND status = 'pending'
	`, id)
	if err != nil {
		return fmt.Errorf("marcar evento resuelto: %w", err)
	}
	return nil
}

// RecordAttemptFailure suma un intento fallido; al llegar a poisonAfter el
// evento pasa a 'poisoned' (deja de reintentarse) y devuelve poisoned=true.
func (r *WatcherFailedEventRepository) RecordAttemptFailure(ctx context.Context, id uuid.UUID, cause error, poisonAfter int) (bool, error) {
	var status string
	err := r.pool.QueryRow(ctx, `
		UPDATE watcher_failed_events
		SET attempts        = attempts + 1,
		    last_error      = $2,
		    last_attempt_at = now(),
		    status          = CASE WHEN attempts + 1 >= $3 THEN 'poisoned' ELSE status END
		WHERE id = $1 AND status = 'pending'
		RETURNING status
	`, id, truncateErr(cause), poisonAfter).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil // ya resuelto/envenenado por otro proceso
	}
	if err != nil {
		return false, fmt.Errorf("registrar intento fallido: %w", err)
	}
	return status == "poisoned", nil
}

func (r *WatcherFailedEventRepository) CountPending(ctx context.Context, network domain.Network) (int, error) {
	var n int
	if err := r.pool.QueryRow(ctx, `
		SELECT count(*) FROM watcher_failed_events WHERE network = $1 AND status = 'pending'
	`, network).Scan(&n); err != nil {
		return 0, fmt.Errorf("contar eventos fallidos: %w", err)
	}
	return n, nil
}
