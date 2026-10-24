package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"korzadi-wallet/backend/internal/infrastructure/cryptutil"
)

// ErrRekeyStale means the row no longer holds the ciphertext we read (a
// concurrent writer got there first); nothing was changed.
var ErrRekeyStale = errors.New("postgres: el valor cifrado cambió desde que se leyó")

// CustodyRekeyRepository re-writes custody ciphertext columns with
// compare-and-swap semantics. It serves both the lazy migration hooks
// (cryptutil.Rewriter) and the bulk cmd/custody-reencrypt. It never logs or
// returns ciphertext/plaintext in errors.
type CustodyRekeyRepository struct{ pool *pgxpool.Pool }

func NewCustodyRekeyRepository(pool *pgxpool.Pool) *CustodyRekeyRepository {
	return &CustodyRekeyRepository{pool: pool}
}

// RekeyRow is one encrypted value plus how to find its row again.
type RekeyRow struct {
	Cursor     string // opaque keyset cursor (primary key as text)
	Ref        string // address, or user id for two_factor_credentials
	Ciphertext string
}

// RekeyTables are the tables holding custody ciphertext, in a stable order.
var RekeyTables = []string{
	cryptutil.TableCryptoAddresses,
	cryptutil.TableCustodyWallets,
	cryptutil.TableTwoFactor,
}

// ListEncrypted returns up to `limit` rows after `cursor` (keyset pagination).
func (r *CustodyRekeyRepository) ListEncrypted(ctx context.Context, table, cursor string, limit int) ([]RekeyRow, error) {
	var q string
	switch table {
	case cryptutil.TableCryptoAddresses:
		q = `SELECT id::text, address, encrypted_private_key FROM crypto_addresses
		     WHERE id::text > $1 ORDER BY id::text LIMIT $2`
	case cryptutil.TableCustodyWallets:
		q = `SELECT id::text, address, encrypted_private_key FROM custody_wallets
		     WHERE id::text > $1 ORDER BY id::text LIMIT $2`
	case cryptutil.TableTwoFactor:
		q = `SELECT user_id::text, user_id::text, secret_encrypted FROM two_factor_credentials
		     WHERE user_id::text > $1 ORDER BY user_id::text LIMIT $2`
	default:
		return nil, fmt.Errorf("postgres: tabla de re-cifrado desconocida: %s", table)
	}
	rows, err := r.pool.Query(ctx, q, cursor, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: listar %s: %w", table, err)
	}
	defer rows.Close()
	var out []RekeyRow
	for rows.Next() {
		var row RekeyRow
		if err := rows.Scan(&row.Cursor, &row.Ref, &row.Ciphertext); err != nil {
			return nil, fmt.Errorf("postgres: leer %s: %w", table, err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// Rewrite is a cryptutil.Rewriter: CAS-replace oldCT by newCT for the row
// identified by aad. It dispatches on the AAD's table.
func (r *CustodyRekeyRepository) Rewrite(ctx context.Context, aad cryptutil.AAD, oldCT, newCT string) error {
	var (
		tag interface{ RowsAffected() int64 }
		err error
	)
	switch aad.Table {
	case cryptutil.TableCryptoAddresses:
		tag, err = r.pool.Exec(ctx, `
			UPDATE crypto_addresses SET encrypted_private_key = $1
			WHERE encrypted_private_key = $2 AND lower(address) = lower($3)`,
			newCT, oldCT, aad.Ref)
	case cryptutil.TableCustodyWallets:
		tag, err = r.pool.Exec(ctx, `
			UPDATE custody_wallets SET encrypted_private_key = $1
			WHERE encrypted_private_key = $2 AND lower(address) = lower($3)`,
			newCT, oldCT, aad.Ref)
	case cryptutil.TableTwoFactor:
		uid, perr := uuid.Parse(aad.Ref)
		if perr != nil {
			return errors.New("postgres: user id inválido")
		}
		tag, err = r.pool.Exec(ctx, `
			UPDATE two_factor_credentials SET secret_encrypted = $1
			WHERE secret_encrypted = $2 AND user_id = $3`,
			newCT, oldCT, uid)
	default:
		return fmt.Errorf("postgres: tabla de re-cifrado desconocida: %s", aad.Table)
	}
	if err != nil {
		return fmt.Errorf("postgres: re-cifrar %s: %w", aad.Table, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrRekeyStale
	}
	return nil
}
