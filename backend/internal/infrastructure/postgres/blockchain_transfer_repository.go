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
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
)

type BlockchainTransferRepository struct {
	pool *pgxpool.Pool
}

func NewBlockchainTransferRepository(pool *pgxpool.Pool) *BlockchainTransferRepository {
	return &BlockchainTransferRepository{pool: pool}
}

// InsertDetected records a newly-seen Transfer event (or native-coin
// transfer) as 'detected'. It is safe to call concurrently from multiple
// watcher instances/goroutines scanning the same block range: the unique
// index guarantees at most one row per (network, contract, tx_hash,
// log_index) for tokens, or (network, tx_hash) for native transfers
// (contract_address IS NULL) — see migration 0004. Callers should treat
// duplicate=true as a normal, expected outcome — not an error to alarm on.
//
// contract_address is stored as NULL for native-coin transfers
// (t.ContractAddress == ""), never as an empty string — the unique index
// relies on SQL NULL semantics to distinguish "no contract" per spec §12
// (native transfer vs token Transfer event are never confused).
func (r *BlockchainTransferRepository) InsertDetected(ctx context.Context, t domain.BlockchainTransfer, cryptoAddressID uuid.UUID) (id uuid.UUID, duplicate bool, err error) {
	var contractAddress interface{}
	if t.ContractAddress != "" {
		contractAddress = t.ContractAddress
	}

	err = r.pool.QueryRow(ctx, `
		INSERT INTO blockchain_transfers
			(network, asset_code, contract_address, tx_hash, log_index, block_number, block_hash,
			 from_address, to_address, amount, decimals, confirmations, status,
			 crypto_address_id, detected_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'detected',$13,now())
		RETURNING id
	`, t.Network, t.AssetCode, contractAddress, t.TxHash, t.LogIndex, t.BlockNumber, t.BlockHash,
		t.FromAddress, t.ToAddress, t.Amount, t.Decimals, t.Confirmations, cryptoAddressID,
	).Scan(&id)

	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
			return uuid.Nil, true, nil
		}
		return uuid.Nil, false, fmt.Errorf("insertar blockchain_transfer: %w", err)
	}
	return id, false, nil
}

// UpdateConfirmations advances confirmations and, once
// confirmations >= required, moves status from 'confirming'/'detected' to
// 'confirmed' and stamps confirmed_at. This is the only place that decides
// a transfer is confirmed — never based merely on the transfer existing.
func (r *BlockchainTransferRepository) UpdateConfirmations(ctx context.Context, id uuid.UUID, confirmations uint64, required uint64) (domain.TransferStatus, error) {
	newStatus := domain.TransferStatusConfirming
	var confirmedAtSet bool
	if confirmations >= required {
		newStatus = domain.TransferStatusConfirmed
		confirmedAtSet = true
	}

	var confirmedAt any
	if confirmedAtSet {
		confirmedAt = time.Now()
	}

	tag, err := r.pool.Exec(ctx, `
		UPDATE blockchain_transfers
		SET confirmations = $2, status = $3, confirmed_at = COALESCE(confirmed_at, $4)
		WHERE id = $1 AND status NOT IN ('confirmed','credited','reorged','failed')
	`, id, confirmations, newStatus, confirmedAt)
	if err != nil {
		return "", fmt.Errorf("actualizar confirmaciones: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Already past this state (or reorged/failed) — not an error,
		// just means another worker got there first or it's terminal.
		return "", nil
	}
	return newStatus, nil
}

// MarkReorged flips a previously detected/confirming/confirmed transfer to
// 'reorged' when the watcher discovers its block is no longer canonical
// (spec §9). 'confirmed' is included because Bloque 4 widened
// FindPendingByNetwork to keep confirmed-but-not-yet-credited transfers
// under active reorg surveillance until they're actually credited — no
// money has moved for those yet, so flipping their status is safe.
// 'credited' is deliberately excluded: once a deposit has been credited,
// its ledger transaction is the authoritative record. A reorg discovered
// after credit is a genuine incident, but "fixing" it by silently
// flipping this status (with no corresponding ledger correction) would
// misrepresent the books far worse than leaving it alone — spec §9
// explicitly forbids inventing an automatic reversal mechanism here. That
// scenario is logged for manual reconciliation elsewhere (see the
// watcher), not handled by this method.
func (r *BlockchainTransferRepository) MarkReorged(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE blockchain_transfers SET status = 'reorged'
		WHERE id = $1 AND status IN ('detected','confirming','confirmed')
	`, id)
	return err
}

func (r *BlockchainTransferRepository) MarkCredited(ctx context.Context, id uuid.UUID, depositID uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE blockchain_transfers
		SET status = 'credited', blockchain_deposit_id = $2
		WHERE id = $1 AND status = 'confirmed'
	`, id, depositID)
	if err != nil {
		return fmt.Errorf("marcar transfer como credited: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("transfer %s no estaba en estado 'confirmed' (posible doble procesamiento)", id)
	}
	return nil
}

func (r *BlockchainTransferRepository) FindPendingByNetwork(ctx context.Context, network domain.Network) ([]domain.BlockchainTransfer, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, network, asset_code, COALESCE(contract_address, ''), tx_hash, log_index, block_number, block_hash,
		       from_address, to_address, amount, decimals, confirmations, status, detected_at, confirmed_at
		FROM blockchain_transfers
		WHERE network = $1 AND status IN ('detected','confirming','confirmed')
		ORDER BY detected_at ASC
	`, network)
	if err != nil {
		return nil, fmt.Errorf("consultar transfers pendientes: %w", err)
	}
	defer rows.Close()

	var out []domain.BlockchainTransfer
	for rows.Next() {
		var t domain.BlockchainTransfer
		var amount decimal.Decimal
		if err := rows.Scan(&t.ID, &t.Network, &t.AssetCode, &t.ContractAddress, &t.TxHash, &t.LogIndex,
			&t.BlockNumber, &t.BlockHash, &t.FromAddress, &t.ToAddress, &amount, &t.Decimals,
			&t.Confirmations, &t.Status, &t.DetectedAt, &t.ConfirmedAt); err != nil {
			return nil, fmt.Errorf("leer fila blockchain_transfer: %w", err)
		}
		t.Amount = amount
		if t.ContractAddress != "" {
			t.AssetKind = domain.AssetKindToken
		} else {
			t.AssetKind = domain.AssetKindNative
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *BlockchainTransferRepository) FindByID(ctx context.Context, id uuid.UUID) (*domain.BlockchainTransfer, error) {
	var t domain.BlockchainTransfer
	var amount decimal.Decimal
	err := r.pool.QueryRow(ctx, `
		SELECT id, network, asset_code, COALESCE(contract_address, ''), tx_hash, log_index, block_number, block_hash,
		       from_address, to_address, amount, decimals, confirmations, status, detected_at, confirmed_at
		FROM blockchain_transfers WHERE id = $1
	`, id).Scan(&t.ID, &t.Network, &t.AssetCode, &t.ContractAddress, &t.TxHash, &t.LogIndex,
		&t.BlockNumber, &t.BlockHash, &t.FromAddress, &t.ToAddress, &amount, &t.Decimals,
		&t.Confirmations, &t.Status, &t.DetectedAt, &t.ConfirmedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("buscar blockchain_transfer por id: %w", err)
	}
	t.Amount = amount
	if t.ContractAddress != "" {
		t.AssetKind = domain.AssetKindToken
	} else {
		t.AssetKind = domain.AssetKindNative
	}
	return &t, nil
}

// FindRecentlyCredited devuelve los depósitos 'credited' cuyo último cambio
// (el momento de acreditación) es posterior a `since`. Lo usa el watcher para
// re-verificar contra la cadena si un depósito ya acreditado sufrió un reorg.
func (r *BlockchainTransferRepository) FindRecentlyCredited(ctx context.Context, network domain.Network, since time.Time, limit int) ([]domain.BlockchainTransfer, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, network, asset_code, COALESCE(contract_address, ''), tx_hash, log_index, block_number, block_hash,
		       from_address, to_address, amount, decimals, confirmations, status, detected_at, confirmed_at
		FROM blockchain_transfers
		WHERE network = $1 AND status = 'credited' AND updated_at >= $2
		ORDER BY updated_at DESC
		LIMIT $3
	`, network, since, limit)
	if err != nil {
		return nil, fmt.Errorf("consultar depósitos acreditados recientes: %w", err)
	}
	defer rows.Close()

	var out []domain.BlockchainTransfer
	for rows.Next() {
		var t domain.BlockchainTransfer
		var amount decimal.Decimal
		if err := rows.Scan(&t.ID, &t.Network, &t.AssetCode, &t.ContractAddress, &t.TxHash, &t.LogIndex,
			&t.BlockNumber, &t.BlockHash, &t.FromAddress, &t.ToAddress, &amount, &t.Decimals,
			&t.Confirmations, &t.Status, &t.DetectedAt, &t.ConfirmedAt); err != nil {
			return nil, fmt.Errorf("leer fila blockchain_transfer acreditada: %w", err)
		}
		t.Amount = amount
		if t.ContractAddress != "" {
			t.AssetKind = domain.AssetKindToken
		} else {
			t.AssetKind = domain.AssetKindNative
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
