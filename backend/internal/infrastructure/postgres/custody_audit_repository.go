package postgres

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CustodyAuditRepository struct{ pool *pgxpool.Pool }

func NewCustodyAuditRepository(pool *pgxpool.Pool) *CustodyAuditRepository {
	return &CustodyAuditRepository{pool: pool}
}

func (r *CustodyAuditRepository) Log(ctx context.Context, cryptoAddressID *uuid.UUID, action, actor string, metadata map[string]any) error {
	var metaJSON []byte
	if metadata != nil {
		var err error
		metaJSON, err = json.Marshal(metadata)
		if err != nil {
			return err
		}
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO custody_audit_logs (id, crypto_address_id, action, actor, metadata)
		VALUES ($1,$2,$3,$4,$5)
	`, uuid.New(), cryptoAddressID, action, actor, metaJSON)
	return err
}

// LogCustodyWallet records an audit event for a custody_wallets row
// (Bloque 7.1 — HOT/GAS/TREASURY infrastructure wallets), reusing this
// same audit table and repository rather than creating a parallel
// infrastructure, via the custody_wallet_id column added by
// 0009_custody_registry.up.sql. It is intentionally the mirror of Log
// (which uses crypto_address_id): a given row only ever populates one of
// the two id columns.
func (r *CustodyAuditRepository) LogCustodyWallet(ctx context.Context, custodyWalletID *uuid.UUID, action, actor string, metadata map[string]any) error {
	var metaJSON []byte
	if metadata != nil {
		var err error
		metaJSON, err = json.Marshal(metadata)
		if err != nil {
			return err
		}
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO custody_audit_logs (id, custody_wallet_id, action, actor, metadata)
		VALUES ($1,$2,$3,$4,$5)
	`, uuid.New(), custodyWalletID, action, actor, metaJSON)
	return err
}
