package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"korzadi-wallet/backend/internal/application/deposit"
	"korzadi-wallet/backend/internal/domain"
)

// AddressToUserResolver implements watcher.DepositAddressLookup by querying
// crypto_addresses directly. It is intentionally a thin, standalone type
// rather than an addition to CryptoAddressRepository (not included in the
// package under review), so it can be reviewed/merged into that repository
// later without this code depending on internals it can't see yet.
type AddressToUserResolver struct {
	pool *pgxpool.Pool
}

func NewAddressToUserResolver(pool *pgxpool.Pool) *AddressToUserResolver {
	return &AddressToUserResolver{pool: pool}
}

func (r *AddressToUserResolver) FindByAddress(ctx context.Context, network domain.Network, address string) (cryptoAddressID uuid.UUID, userID uuid.UUID, found bool, err error) {
	err = r.pool.QueryRow(ctx, `
		SELECT id, user_id FROM crypto_addresses
		WHERE network = $1 AND address = $2 AND status = 'active'
	`, network, address).Scan(&cryptoAddressID, &userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, uuid.Nil, false, fmt.Errorf("buscar dirección %s/%s: %w", network, address, err)
	}
	return cryptoAddressID, userID, true, nil
}

// ListActiveAddresses returns every active deposit address on `network`,
// lowercased for case-insensitive EVM address comparison. Used by
// watcher.EVMDepositWatcher to scan native-coin (ETH/BNB/...) transfers:
// unlike ERC-20 Transfer events (filterable server-side via FilterLogs),
// native transfers require pulling full blocks and checking each
// recipient locally, so the watcher needs the whole watched-address set
// up front.
func (r *AddressToUserResolver) ListActiveAddresses(ctx context.Context, network domain.Network) (map[string]bool, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT address FROM crypto_addresses WHERE network = $1 AND status = 'active'
	`, network)
	if err != nil {
		return nil, fmt.Errorf("listar direcciones activas de %s: %w", network, err)
	}
	defer rows.Close()

	out := make(map[string]bool)
	for rows.Next() {
		var addr string
		if err := rows.Scan(&addr); err != nil {
			return nil, fmt.Errorf("listar direcciones activas de %s: %w", network, err)
		}
		out[strings.ToLower(addr)] = true
	}
	return out, rows.Err()
}

// ClearingAccountRepository implements deposit.ClearingAccountResolver.
type ClearingAccountRepository struct{ pool *pgxpool.Pool }

func NewClearingAccountRepository(pool *pgxpool.Pool) *ClearingAccountRepository {
	return &ClearingAccountRepository{pool: pool}
}

// systemWalletID is the fixed system wallet every system_clearing account
// hangs off of, seeded in 0001_init.up.sql and used already by 0003 for
// the BSC/USDT clearing account.
const systemWalletID = "00000000-0000-0000-0000-000000000002"

func (r *ClearingAccountRepository) FindClearingAccount(ctx context.Context, network domain.Network, assetCode string) (uuid.UUID, error) {
	// One system_clearing account per asset (shared across every network
	// that settles the same asset — e.g. USDT arriving via BSC, Ethereum
	// or TRON all clear through the same USDT clearing account). If it
	// doesn't exist yet, provision it: this used to require a manual seed
	// row per asset (migration 0003 seeded USDT only), which meant
	// deposits in a second asset (USDC, native BNB/ETH, BTC) would fail
	// with "no existe cuenta system_clearing" until someone wrote a new
	// migration. Auto-provisioning here means the asset registry —
	// entirely config-driven — is the only thing that needs to change to
	// support a new asset. If per-network isolation is ever required,
	// add `network` to wallet_accounts and to this lookup/insert.
	var id uuid.UUID
	err := r.pool.QueryRow(ctx, `
		SELECT wa.id
		FROM wallet_accounts wa
		WHERE wa.account_type = 'system_clearing' AND wa.asset_code = $1
		LIMIT 1
	`, assetCode).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("buscar cuenta de clearing: %w", err)
	}

	err = r.pool.QueryRow(ctx, `
		INSERT INTO wallet_accounts (wallet_id, asset_code, account_type)
		VALUES ($1, $2, 'system_clearing')
		ON CONFLICT (wallet_id, asset_code) DO UPDATE SET account_type = 'system_clearing'
		RETURNING id
	`, systemWalletID, assetCode).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("provisionar cuenta de clearing para asset %s: %w", assetCode, err)
	}
	return id, nil
}

// UserWalletAccountRepository implements deposit.UserAccountResolver.
type UserWalletAccountRepository struct{ pool *pgxpool.Pool }

func NewUserWalletAccountRepository(pool *pgxpool.Pool) *UserWalletAccountRepository {
	return &UserWalletAccountRepository{pool: pool}
}

func (r *UserWalletAccountRepository) FindOrCreateUserAccount(ctx context.Context, userID uuid.UUID, assetCode string) (uuid.UUID, error) {
	var walletID uuid.UUID
	err := r.pool.QueryRow(ctx, `SELECT id FROM wallets WHERE user_id = $1`, userID).Scan(&walletID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("wallet no encontrada para usuario %s: %w", userID, err)
	}

	var accountID uuid.UUID
	err = r.pool.QueryRow(ctx, `
		SELECT id FROM wallet_accounts WHERE wallet_id = $1 AND asset_code = $2
	`, walletID, assetCode).Scan(&accountID)
	if err == nil {
		return accountID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("buscar wallet_account: %w", err)
	}

	err = r.pool.QueryRow(ctx, `
		INSERT INTO wallet_accounts (wallet_id, asset_code, account_type)
		VALUES ($1, $2, 'user')
		ON CONFLICT (wallet_id, asset_code) DO UPDATE SET asset_code = EXCLUDED.asset_code
		RETURNING id
	`, walletID, assetCode).Scan(&accountID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("crear wallet_account: %w", err)
	}
	return accountID, nil
}

// BlockchainDepositRecorderRepository implements deposit.BlockchainDepositRecorder.
type BlockchainDepositRecorderRepository struct{ pool *pgxpool.Pool }

func NewBlockchainDepositRecorderRepository(pool *pgxpool.Pool) *BlockchainDepositRecorderRepository {
	return &BlockchainDepositRecorderRepository{pool: pool}
}

func (r *BlockchainDepositRecorderRepository) RecordCompleted(ctx context.Context, in deposit.RecordCompletedInput) (uuid.UUID, error) {
	// $1=user_id $2=network $3=asset_code $4=tx_hash $5=from_address
	// $6=to_address $7=contract_address $8=log_index $9=block_number
	// $10=block_hash $11=amount $12=confirmations $13=ledger_transaction_id
	var id uuid.UUID
	err := r.pool.QueryRow(ctx, `
		INSERT INTO blockchain_deposits
			(user_id, crypto_address_id, network, asset_code, tx_hash, from_address, to_address,
			 contract_address, log_index, block_number, block_hash, amount, confirmations,
			 status, ledger_transaction_id, detected_at, completed_at)
		VALUES (
			$1,
			(SELECT id FROM crypto_addresses WHERE network = $2 AND address = $6 AND user_id = $1),
			$2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12,
			'completed', $13, now(), now()
		)
		ON CONFLICT (network, tx_hash, log_index) WHERE log_index IS NOT NULL
		DO UPDATE SET status = EXCLUDED.status -- no-op write, just so RETURNING still yields the row
		RETURNING id
	`,
		in.UserID, in.Network, in.AssetCode, in.TxHash, in.FromAddress, in.ToAddress,
		in.ContractAddress, in.LogIndex, in.BlockNumber, in.BlockHash, in.Amount,
		in.Confirmations, in.LedgerTransactionID,
	).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("registrar blockchain_deposits: %w", err)
	}
	return id, nil
}

// FindExisting looks up a previously recorded deposit by its natural key.
// Used on the LedgerPoster idempotency-replay path (spec §18): a concurrent
// call already ran RecordCompleted, so this call just needs the id to close
// the loop on blockchain_transfers.status.
func (r *BlockchainDepositRecorderRepository) FindExisting(ctx context.Context, network domain.Network, txHash string, logIndex int) (uuid.UUID, error) {
	var id uuid.UUID
	err := r.pool.QueryRow(ctx, `
		SELECT id FROM blockchain_deposits WHERE network = $1 AND tx_hash = $2 AND log_index = $3
	`, network, txHash, logIndex).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("no existe blockchain_deposits para network=%s tx_hash=%s log_index=%d", network, txHash, logIndex)
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("buscar blockchain_deposits existente: %w", err)
	}
	return id, nil
}

// ---- Bloque 4: historial de depósitos blockchain (lectura) ----

const blockchainDepositColumns = `
	id, network, asset_code, tx_hash, from_address, to_address, contract_address,
	log_index, block_number, block_hash, amount, confirmations, required_confirmations,
	status, detected_at, completed_at
`

// ListByUser returns a user's own blockchain deposits, newest first — the
// backing query for GET /wallet/deposits/crypto. Ownership is enforced in
// the WHERE clause itself (never "fetch all, filter in Go"), so there is
// no code path that could leak another user's rows.
func (r *BlockchainDepositRecorderRepository) ListByUser(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*deposit.BlockchainDepositRecord, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+blockchainDepositColumns+`
		FROM blockchain_deposits
		WHERE user_id = $1
		ORDER BY detected_at DESC
		LIMIT $2 OFFSET $3
	`, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("listar depósitos de %s: %w", userID, err)
	}
	defer rows.Close()

	var out []*deposit.BlockchainDepositRecord
	for rows.Next() {
		var d deposit.BlockchainDepositRecord
		var fromAddress, contractAddress, blockHash *string
		var logIndex *int
		var blockNumber *int64
		var completedAt *time.Time

		if err := rows.Scan(
			&d.ID, &d.Network, &d.AssetCode, &d.TxHash, &fromAddress, &d.ToAddress, &contractAddress,
			&logIndex, &blockNumber, &blockHash, &d.Amount, &d.Confirmations, &d.RequiredConfirmations,
			&d.Status, &d.DetectedAt, &completedAt,
		); err != nil {
			return nil, fmt.Errorf("leer depósito: %w", err)
		}
		applyNullableDepositFields(&d, fromAddress, contractAddress, blockHash, logIndex, blockNumber, completedAt)
		out = append(out, &d)
	}
	return out, rows.Err()
}

// FindByIDForUser looks up a single deposit, scoped to userID in the SQL
// itself — this is what makes IDOR structurally impossible here: a user
// can never even distinguish "not mine" from "doesn't exist" by timing or
// response shape, both return found=false identically.
func (r *BlockchainDepositRecorderRepository) FindByIDForUser(ctx context.Context, id, userID uuid.UUID) (*deposit.BlockchainDepositRecord, error) {
	var d deposit.BlockchainDepositRecord
	var fromAddress, contractAddress, blockHash *string
	var logIndex *int
	var blockNumber *int64
	var completedAt *time.Time

	err := r.pool.QueryRow(ctx, `
		SELECT `+blockchainDepositColumns+`
		FROM blockchain_deposits
		WHERE id = $1 AND user_id = $2
	`, id, userID).Scan(
		&d.ID, &d.Network, &d.AssetCode, &d.TxHash, &fromAddress, &d.ToAddress, &contractAddress,
		&logIndex, &blockNumber, &blockHash, &d.Amount, &d.Confirmations, &d.RequiredConfirmations,
		&d.Status, &d.DetectedAt, &completedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("buscar depósito %s: %w", id, err)
	}
	applyNullableDepositFields(&d, fromAddress, contractAddress, blockHash, logIndex, blockNumber, completedAt)
	return &d, nil
}

func applyNullableDepositFields(d *deposit.BlockchainDepositRecord, fromAddress, contractAddress, blockHash *string, logIndex *int, blockNumber *int64, completedAt *time.Time) {
	if fromAddress != nil {
		d.FromAddress = *fromAddress
	}
	if contractAddress != nil {
		d.ContractAddress = *contractAddress
	}
	if blockHash != nil {
		d.BlockHash = *blockHash
	}
	if logIndex != nil {
		d.LogIndex = *logIndex
	}
	if blockNumber != nil {
		d.BlockNumber = uint64(*blockNumber)
	}
	d.CompletedAt = completedAt
}
