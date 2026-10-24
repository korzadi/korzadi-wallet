package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"korzadi-wallet/backend/internal/application/transfer"
)

// AssetRepository implements read access to the `assets` table — the
// global ledger-level asset catalog (USD, USDT, USDC, BTC, ETH, BNB, POL).
// This is deliberately distinct from NetworkAssetRepository (Bloque 1's
// network_assets table, which additionally carries per-network
// contract/standard/decimals): a transfer between two Korzadi users never
// touches a network or a contract, so it only ever needs "does this asset
// code exist and is it enabled", which is exactly what this table already
// records. No write methods are added here — this block never creates or
// disables assets, only validates a code a caller supplied.
type AssetRepository struct {
	pool *pgxpool.Pool
}

func NewAssetRepository(pool *pgxpool.Pool) *AssetRepository {
	return &AssetRepository{pool: pool}
}

// Find returns the asset for `code`, or found=false if no such code is
// registered at all (as opposed to registered-but-disabled, which the
// caller can distinguish via IsEnabled).
func (r *AssetRepository) Find(ctx context.Context, code string) (transfer.LedgerAsset, bool, error) {
	var asset transfer.LedgerAsset
	err := r.pool.QueryRow(ctx, `
		SELECT code, name, decimals, is_enabled FROM assets WHERE code = $1
	`, code).Scan(&asset.Code, &asset.Name, &asset.Decimals, &asset.IsEnabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return transfer.LedgerAsset{}, false, nil
	}
	if err != nil {
		return transfer.LedgerAsset{}, false, fmt.Errorf("buscar asset %s: %w", code, err)
	}
	return asset, true, nil
}
