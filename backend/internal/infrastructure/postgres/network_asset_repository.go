package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"korzadi-wallet/backend/internal/domain"
)

// NetworkAssetRepository implements domain.AssetRegistry against the
// network_assets table (migration 0004). This is the concrete answer to
// the CRÍTICO audit finding: assets per network now live in one queryable
// place instead of being hardcoded inside a single network's client
// config.
type NetworkAssetRepository struct {
	pool *pgxpool.Pool
}

func NewNetworkAssetRepository(pool *pgxpool.Pool) *NetworkAssetRepository {
	return &NetworkAssetRepository{pool: pool}
}

// UpsertMany writes/updates a batch of NetworkAsset rows — called at
// startup with whatever assetregistry.LoadEVMNetworkAssets read from the
// environment for each configured network, so the DB reflects current
// configuration without requiring a manual migration every time an
// operator adds a token.
func (r *NetworkAssetRepository) UpsertMany(ctx context.Context, assets []domain.NetworkAsset) error {
	if len(assets) == 0 {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("network_assets: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	for _, a := range assets {
		var contract interface{}
		if a.ContractAddress != "" {
			contract = a.ContractAddress
		}
		var confirmations interface{}
		if a.ConfirmationsRequired > 0 {
			confirmations = a.ConfirmationsRequired
		}

		_, err := tx.Exec(ctx, `
			INSERT INTO network_assets (network, asset_code, kind, contract_address, token_standard, decimals, confirmations_required, enabled)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (network, asset_code, kind) DO UPDATE SET
				contract_address       = EXCLUDED.contract_address,
				token_standard         = EXCLUDED.token_standard,
				decimals               = EXCLUDED.decimals,
				confirmations_required = EXCLUDED.confirmations_required,
				enabled                = EXCLUDED.enabled
		`, string(a.Network), a.AssetCode, string(a.Kind), contract, string(a.Standard), a.Decimals, confirmations, a.Enabled)
		if err != nil {
			return fmt.Errorf("network_assets: upsert %s/%s: %w", a.Network, a.AssetCode, err)
		}
	}

	return tx.Commit(ctx)
}

func (r *NetworkAssetRepository) ListEnabled(ctx context.Context, network domain.Network) ([]domain.NetworkAsset, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT network, asset_code, kind, COALESCE(contract_address, ''), token_standard, decimals,
		       COALESCE(confirmations_required, 0), enabled
		FROM network_assets
		WHERE network = $1 AND enabled = true
		ORDER BY kind DESC, asset_code -- native first, then tokens alphabetically
	`, string(network))
	if err != nil {
		return nil, fmt.Errorf("network_assets: list enabled(%s): %w", network, err)
	}
	defer rows.Close()

	var out []domain.NetworkAsset
	for rows.Next() {
		var a domain.NetworkAsset
		var net, kind, standard string
		if err := rows.Scan(&net, &a.AssetCode, &kind, &a.ContractAddress, &standard, &a.Decimals, &a.ConfirmationsRequired, &a.Enabled); err != nil {
			return nil, fmt.Errorf("network_assets: scan: %w", err)
		}
		a.Network = domain.Network(net)
		a.Kind = domain.AssetKind(kind)
		a.Standard = domain.TokenStandard(standard)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *NetworkAssetRepository) Get(ctx context.Context, network domain.Network, assetCode string) (domain.NetworkAsset, bool, error) {
	var a domain.NetworkAsset
	var net, kind, standard string
	err := r.pool.QueryRow(ctx, `
		SELECT network, asset_code, kind, COALESCE(contract_address, ''), token_standard, decimals,
		       COALESCE(confirmations_required, 0), enabled
		FROM network_assets
		WHERE network = $1 AND asset_code = $2
		LIMIT 1
	`, string(network), assetCode).Scan(&net, &a.AssetCode, &kind, &a.ContractAddress, &standard, &a.Decimals, &a.ConfirmationsRequired, &a.Enabled)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.NetworkAsset{}, false, nil
		}
		return domain.NetworkAsset{}, false, fmt.Errorf("network_assets: get(%s,%s): %w", network, assetCode, err)
	}
	a.Network = domain.Network(net)
	a.Kind = domain.AssetKind(kind)
	a.Standard = domain.TokenStandard(standard)
	return a, true, nil
}
