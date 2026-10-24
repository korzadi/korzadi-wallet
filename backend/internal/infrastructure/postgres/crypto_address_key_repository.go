package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"korzadi-wallet/backend/internal/domain"
)

// ASSUMPTION FLAGGED FOR REVIEW: this queries crypto_addresses assuming a
// column named "encrypted_private_key", matching the naming already used
// when the address was created (see crypto/service.go's
// CreateWithNextIndex call, which passes an "encryptedKey" argument). This
// file was written without visibility into the real CryptoAddressRepository
// or the exact crypto_addresses schema — verify the column name matches
// your actual migration before relying on this in production. If it
// differs, this is a one-line fix (the column name in the SELECT below).
type CryptoAddressKeyRepository struct {
	pool *pgxpool.Pool
}

func NewCryptoAddressKeyRepository(pool *pgxpool.Pool) *CryptoAddressKeyRepository {
	return &CryptoAddressKeyRepository{pool: pool}
}

// FindEncryptedPrivateKey returns the encrypted private key blob stored for
// a given (network, address), verifying it belongs to userID — a withdrawal
// must never be signed using a key belonging to a different user's address.
func (r *CryptoAddressKeyRepository) FindEncryptedPrivateKey(ctx context.Context, network domain.Network, address string, userID uuid.UUID) (string, error) {
	var encryptedKey string
	err := r.pool.QueryRow(ctx, `
		SELECT encrypted_private_key FROM crypto_addresses
		WHERE network = $1 AND address = $2 AND user_id = $3 AND status = 'active'
	`, network, address, userID).Scan(&encryptedKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("no se encontró dirección %s/%s activa para el usuario %s", network, address, userID)
	}
	if err != nil {
		return "", fmt.Errorf("consultar llave cifrada: %w", err)
	}
	return encryptedKey, nil
}

// FindActiveDepositAddress returns the user's active deposit address for a
// network/asset, used to determine which address a withdrawal is paid from.
func (r *CryptoAddressKeyRepository) FindActiveDepositAddress(ctx context.Context, network domain.Network, userID uuid.UUID) (address string, err error) {
	err = r.pool.QueryRow(ctx, `
		SELECT address FROM crypto_addresses
		WHERE network = $1 AND user_id = $2 AND status = 'active'
	`, network, userID).Scan(&address)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("el usuario %s no tiene dirección %s asignada todavía", userID, network)
	}
	if err != nil {
		return "", fmt.Errorf("consultar dirección de depósito: %w", err)
	}
	return address, nil
}
