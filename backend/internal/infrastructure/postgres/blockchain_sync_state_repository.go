package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"korzadi-wallet/backend/internal/domain"
)

type BlockchainSyncStateRepository struct {
	pool *pgxpool.Pool
}

func NewBlockchainSyncStateRepository(pool *pgxpool.Pool) *BlockchainSyncStateRepository {
	return &BlockchainSyncStateRepository{pool: pool}
}

// GetLastProcessedBlock returns the last block fully processed for
// `network`, and ok=false if no checkpoint exists yet (fresh start).
func (r *BlockchainSyncStateRepository) GetLastProcessedBlock(ctx context.Context, network domain.Network) (block uint64, ok bool, err error) {
	err = r.pool.QueryRow(ctx, `
		SELECT last_processed_block FROM blockchain_sync_state WHERE network = $1
	`, network).Scan(&block)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("leer checkpoint de %s: %w", network, err)
	}
	return block, true, nil
}

// AdvanceTo upserts the checkpoint. It only ever moves forward: an UPDATE
// that would decrease last_processed_block is rejected, since a watcher
// restarting from an old in-memory value must never rewind a checkpoint
// another instance already advanced.
func (r *BlockchainSyncStateRepository) AdvanceTo(ctx context.Context, network domain.Network, block uint64) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO blockchain_sync_state (network, last_processed_block)
		VALUES ($1, $2)
		ON CONFLICT (network) DO UPDATE
		SET last_processed_block = $2
		WHERE blockchain_sync_state.last_processed_block < $2
	`, network, block)
	if err != nil {
		return fmt.Errorf("actualizar checkpoint de %s: %w", network, err)
	}
	return nil
}
