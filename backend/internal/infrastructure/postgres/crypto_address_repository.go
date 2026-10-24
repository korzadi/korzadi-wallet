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
)

type CryptoAddress struct {
	ID                  uuid.UUID
	UserID              uuid.UUID
	Network             string
	AssetCode           string // vacío/NULL para direcciones EVM/Bitcoin (Bloque 3): una dirección no es exclusiva de un asset
	Address             string
	AddressType         string // "evm" | "bitcoin_segwit" | "tron"
	DerivationIndex     int64
	EncryptedPrivateKey string
	Status              string // "active" | "disabled" | "retired"
	CreatedAt           time.Time
}

type CryptoAddressRepository struct{ pool *pgxpool.Pool }

func NewCryptoAddressRepository(pool *pgxpool.Pool) *CryptoAddressRepository {
	return &CryptoAddressRepository{pool: pool}
}

func (r *CryptoAddressRepository) FindByUser(ctx context.Context, userID uuid.UUID, network, assetCode string) (*CryptoAddress, error) {
	var a CryptoAddress
	err := r.pool.QueryRow(ctx, `
		SELECT id, user_id, network, asset_code, address, derivation_index, encrypted_private_key
		FROM crypto_addresses WHERE user_id=$1 AND network=$2 AND asset_code=$3
	`, userID, network, assetCode).Scan(&a.ID, &a.UserID, &a.Network, &a.AssetCode, &a.Address, &a.DerivationIndex, &a.EncryptedPrivateKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &a, err
}

func (r *CryptoAddressRepository) CreateWithNextIndex(ctx context.Context, userID uuid.UUID, network, assetCode,
	address, encryptedPrivateKey string) (*CryptoAddress, error) {

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var nextIndex int64
	err = tx.QueryRow(ctx, `
		UPDATE hd_wallet_state SET next_derivation_index = next_derivation_index + 1
		WHERE network=$1 RETURNING next_derivation_index - 1
	`, network).Scan(&nextIndex)
	if err != nil {
		return nil, err
	}

	addressType := "evm"
	if network == "TRON" {
		addressType = "tron"
	}

	a := &CryptoAddress{
		ID:                  uuid.New(),
		UserID:              userID,
		Network:             network,
		AssetCode:           assetCode,
		Address:             address,
		AddressType:         addressType,
		DerivationIndex:     nextIndex,
		EncryptedPrivateKey: encryptedPrivateKey,
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO crypto_addresses
			(id, user_id, network, asset_code, address, address_type, derivation_index, encrypted_private_key)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
	`, a.ID, a.UserID, a.Network, a.AssetCode, a.Address, a.AddressType,
		a.DerivationIndex, a.EncryptedPrivateKey)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return a, nil
}

// ---- Bloque 3: registro de direcciones genérico (multi-red, asset-agnóstico) ----

// FindByUserAndNetwork looks up the single address a user has on a given
// network — asset-agnostic, since Bloque 3 makes (user, network) the
// unique key, not (user, network, asset).
func (r *CryptoAddressRepository) FindByUserAndNetwork(ctx context.Context, userID uuid.UUID, network string) (*CryptoAddress, error) {
	var a CryptoAddress
	var assetCode *string
	err := r.pool.QueryRow(ctx, `
		SELECT id, user_id, network, asset_code, address, address_type, derivation_index, status, created_at
		FROM crypto_addresses WHERE user_id=$1 AND network=$2
	`, userID, network).Scan(&a.ID, &a.UserID, &a.Network, &assetCode, &a.Address, &a.AddressType, &a.DerivationIndex, &a.Status, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("buscar dirección de %s en %s: %w", userID, network, err)
	}
	if assetCode != nil {
		a.AssetCode = *assetCode
	}
	return &a, nil
}

// ListByUser returns every address (any network, any status) belonging to
// a user — the backing query for GET /wallet/addresses. Callers decide
// whether to filter by status.
func (r *CryptoAddressRepository) ListByUser(ctx context.Context, userID uuid.UUID) ([]*CryptoAddress, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, user_id, network, asset_code, address, address_type, derivation_index, status, created_at
		FROM crypto_addresses WHERE user_id=$1
		ORDER BY created_at ASC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("listar direcciones de %s: %w", userID, err)
	}
	defer rows.Close()

	var result []*CryptoAddress
	for rows.Next() {
		var a CryptoAddress
		var assetCode *string
		if err := rows.Scan(&a.ID, &a.UserID, &a.Network, &assetCode, &a.Address, &a.AddressType, &a.DerivationIndex, &a.Status, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("leer dirección: %w", err)
		}
		if assetCode != nil {
			a.AssetCode = *assetCode
		}
		result = append(result, &a)
	}
	return result, rows.Err()
}

// CreateGenericAddress inserts a new (user, network) address row with no
// asset scope (asset_code NULL) — used for every EVM and Bitcoin network.
// TRON/BSC keep using CreateWithNextIndex unchanged, which still sets
// asset_code explicitly, matching their existing single-asset design.
//
// Concurrency: if two requests race to create the first address for the
// same (user, network), the unique index on (user_id, network) added in
// migration 0006 lets exactly one INSERT succeed — the loser detects the
// unique_violation and returns the winner's row instead of erroring,
// exactly the idempotent "get-or-create" behavior callers expect (mirrors
// the pattern already used for ledger idempotency in Bloque 2).
func (r *CryptoAddressRepository) CreateGenericAddress(ctx context.Context, userID uuid.UUID, network, addressType, address, encryptedPrivateKey string) (*CryptoAddress, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("crear dirección: iniciar tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var nextIndex int64
	err = tx.QueryRow(ctx, `
		UPDATE hd_wallet_state SET next_derivation_index = next_derivation_index + 1
		WHERE network=$1 RETURNING next_derivation_index - 1
	`, network).Scan(&nextIndex)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("crear dirección: red %s no está registrada en hd_wallet_state", network)
		}
		return nil, fmt.Errorf("crear dirección: avanzar índice de %s: %w", network, err)
	}

	newID := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO crypto_addresses (id, user_id, network, asset_code, address, address_type, derivation_index, encrypted_private_key)
		VALUES ($1,$2,$3,NULL,$4,$5,$6,$7)
	`, newID, userID, network, address, addressType, nextIndex, encryptedPrivateKey)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			// Perdimos la carrera contra una petición concurrente para el
			// mismo (usuario, red) — no es un fallo, es la garantía de
			// unicidad funcionando. Hacemos rollback de esta transacción
			// (que también revierte el avance del índice, evitando
			// "quemar" un índice para una dirección que nunca se creó) y
			// devolvemos la fila ganadora.
			tx.Rollback(ctx)
			existing, lookupErr := r.FindByUserAndNetwork(ctx, userID, network)
			if lookupErr != nil {
				return nil, fmt.Errorf("crear dirección: perdió la carrera de creación y no pudo leer la ganadora: %w", lookupErr)
			}
			if existing == nil {
				return nil, fmt.Errorf("crear dirección: violación de unicidad pero no se encontró fila existente (inesperado)")
			}
			return existing, nil
		}
		return nil, fmt.Errorf("crear dirección: insertar: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("crear dirección: commit: %w", err)
	}

	return r.FindByUserAndNetwork(ctx, userID, network)
}
