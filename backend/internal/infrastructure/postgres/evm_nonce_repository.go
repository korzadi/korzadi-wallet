package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"korzadi-wallet/backend/internal/domain"
)

// EVMNonceRepository coordinates EVM nonces in PostgreSQL (evm_nonces,
// 0008) — the persistent replacement for a bare PendingNonceAt. It
// implements blockchainwithdrawal.NonceStore and
// bscwithdrawal.NonceAllocator.
//
// Allocation for one (network, address) is serialized by a PostgreSQL
// advisory lock held for the duration of the transaction, so concurrent
// withdrawals — in one process or many — can never receive the same nonce,
// survive restarts (the table is the state), and never depend on a mutex.
type EVMNonceRepository struct {
	pool *pgxpool.Pool
}

func NewEVMNonceRepository(pool *pgxpool.Pool) *EVMNonceRepository {
	return &EVMNonceRepository{pool: pool}
}

func normalizeNonceAddress(address string) string { return strings.ToLower(strings.TrimSpace(address)) }

// Allocate returns the nonce owned by `owner` on (network, address). It is
// idempotent per owner: a retry after a crash gets the SAME nonce back.
//
// A new nonce is max(chainPending, highest live allocation + 1), except that
// the lowest previously RELEASED nonce in [chainPending, next) is reused
// first, so a failed pre-broadcast attempt never leaves a permanent gap that
// would stall every later transaction of that address.
func (r *EVMNonceRepository) Allocate(ctx context.Context, network domain.Network, address, owner string, chainPending uint64) (uint64, error) {
	addr := normalizeNonceAddress(address)
	if addr == "" || owner == "" {
		return 0, fmt.Errorf("evm_nonces: address y owner requeridos")
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("evm_nonces: iniciar tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "evm_nonce:"+string(network)+":"+addr); err != nil {
		return 0, fmt.Errorf("evm_nonces: lock: %w", err)
	}

	var existing int64
	err = tx.QueryRow(ctx, `
		SELECT nonce FROM evm_nonces
		WHERE network = $1 AND address = $2 AND owner_ref = $3 AND status <> 'released'
	`, string(network), addr, owner).Scan(&existing)
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return 0, fmt.Errorf("evm_nonces: commit: %w", err)
		}
		return uint64(existing), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("evm_nonces: buscar existente: %w", err)
	}

	var nextLive int64
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(nonce) + 1, 0) FROM evm_nonces
		WHERE network = $1 AND address = $2 AND status <> 'released'
	`, string(network), addr).Scan(&nextLive); err != nil {
		return 0, fmt.Errorf("evm_nonces: máximo vivo: %w", err)
	}
	next := uint64(nextLive)
	if chainPending > next {
		next = chainPending
	}

	chosen := next
	var released int64
	err = tx.QueryRow(ctx, `
		SELECT nonce FROM evm_nonces
		WHERE network = $1 AND address = $2 AND status = 'released' AND nonce >= $3 AND nonce < $4
		ORDER BY nonce
		LIMIT 1
	`, string(network), addr, int64(chainPending), int64(next)).Scan(&released)
	if err == nil {
		chosen = uint64(released)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("evm_nonces: buscar liberado: %w", err)
	}

	// Insert a new row, or take over a released one. The WHERE on the
	// conflict branch makes taking over a LIVE nonce impossible.
	tag, err := tx.Exec(ctx, `
		INSERT INTO evm_nonces (network, address, nonce, owner_ref, status)
		VALUES ($1, $2, $3, $4, 'reserved')
		ON CONFLICT (network, address, nonce) DO UPDATE
		SET owner_ref = EXCLUDED.owner_ref, status = 'reserved', tx_hash = NULL
		WHERE evm_nonces.status = 'released'
	`, string(network), addr, int64(chosen), owner)
	if err != nil {
		return 0, fmt.Errorf("evm_nonces: asignar: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return 0, fmt.Errorf("evm_nonces: colisión al asignar nonce %d de %s", chosen, addr)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("evm_nonces: commit: %w", err)
	}
	return chosen, nil
}

// MarkSigned records that a transaction with txHash was signed for this
// nonce (it may or may not be on the network). Idempotent.
func (r *EVMNonceRepository) MarkSigned(ctx context.Context, network domain.Network, address, owner, txHash string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE evm_nonces SET status = 'signed', tx_hash = $4
		WHERE network = $1 AND address = $2 AND owner_ref = $3 AND status IN ('reserved','signed')
	`, string(network), normalizeNonceAddress(address), owner, txHash)
	if err != nil {
		return fmt.Errorf("evm_nonces: marcar firmado: %w", err)
	}
	return nil
}

// MarkConfirmed records that the nonce was consumed on-chain by our
// transaction. Idempotent.
func (r *EVMNonceRepository) MarkConfirmed(ctx context.Context, network domain.Network, address, owner string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE evm_nonces SET status = 'confirmed'
		WHERE network = $1 AND address = $2 AND owner_ref = $3 AND status IN ('reserved','signed','confirmed')
	`, string(network), normalizeNonceAddress(address), owner)
	if err != nil {
		return fmt.Errorf("evm_nonces: marcar confirmado: %w", err)
	}
	return nil
}

// Release frees a nonce whose transaction was NEVER accepted by the network
// so a later allocation can reuse it. A confirmed nonce is never released.
func (r *EVMNonceRepository) Release(ctx context.Context, network domain.Network, address, owner string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE evm_nonces SET status = 'released'
		WHERE network = $1 AND address = $2 AND owner_ref = $3 AND status IN ('reserved','signed')
	`, string(network), normalizeNonceAddress(address), owner)
	if err != nil {
		return fmt.Errorf("evm_nonces: liberar: %w", err)
	}
	return nil
}
