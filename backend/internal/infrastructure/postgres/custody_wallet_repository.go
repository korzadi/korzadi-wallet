package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"korzadi-wallet/backend/internal/domain"
)

// CustodyWallet represents one row of custody_wallets (Bloque 7.1) —
// Korzadi's OWN infrastructure wallets (HOT / GAS / TREASURY). It is a
// deliberately separate type from CryptoAddress: custody_wallets is a
// separate table, and the two must never be confused (spec: "Estas
// wallets NO son las wallets lógicas de los usuarios... NO deben
// mezclarse con el Ledger de usuarios").
type CustodyWallet struct {
	ID                  uuid.UUID
	Network             string
	AssetCode           string
	WalletType          string // "HOT" | "GAS" | "TREASURY"
	Address             string
	AddressType         string // "evm" | "bitcoin_segwit" | "tron" — same vocabulary as crypto_addresses.address_type
	EncryptedPrivateKey string
	Status              string // "active" | "disabled" | "retired" — same vocabulary as crypto_addresses.status
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// Sentinel errors surfaced from Postgres constraint violations, so callers
// (the application layer) can react by errors.Is without depending on
// pgconn or on guessing driver-specific messages.
var (
	ErrCustodyWalletDuplicateAddress = errors.New("postgres: la dirección ya está registrada en custody_wallets")
	ErrCustodyWalletUnknownNetwork   = errors.New("postgres: la red no está registrada en hd_wallet_state")
	ErrCustodyWalletUnknownAsset     = errors.New("postgres: el asset no está registrado en assets")
)

// CustodyWalletFilter narrows List(). Every non-nil field is ANDed;
// omit everything to list all custody wallets.
type CustodyWalletFilter struct {
	Network    *string
	AssetCode  *string
	WalletType *string
	Status     *string
}

type CustodyWalletRepository struct{ pool *pgxpool.Pool }

func NewCustodyWalletRepository(pool *pgxpool.Pool) *CustodyWalletRepository {
	return &CustodyWalletRepository{pool: pool}
}

const custodyWalletColumns = `id, network, asset_code, wallet_type, address, address_type, encrypted_private_key, status, created_at, updated_at`

func scanCustodyWallet(row pgx.Row) (*CustodyWallet, error) {
	var w CustodyWallet
	err := row.Scan(&w.ID, &w.Network, &w.AssetCode, &w.WalletType, &w.Address, &w.AddressType,
		&w.EncryptedPrivateKey, &w.Status, &w.CreatedAt, &w.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &w, nil
}

// Create inserts a new custody wallet. w.ID is generated here if left
// uuid.Nil. Constraint violations are translated into the typed sentinel
// errors above instead of leaking the raw driver error, matching the
// unique_violation handling already used by CreateGenericAddress.
func (r *CustodyWalletRepository) Create(ctx context.Context, w *CustodyWallet) (*CustodyWallet, error) {
	id := w.ID
	if id == uuid.Nil {
		id = uuid.New()
	}
	status := w.Status
	if status == "" {
		status = "active"
	}

	row := r.pool.QueryRow(ctx, `
		INSERT INTO custody_wallets (id, network, asset_code, wallet_type, address, address_type, encrypted_private_key, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING `+custodyWalletColumns,
		id, w.Network, w.AssetCode, w.WalletType, w.Address, w.AddressType, w.EncryptedPrivateKey, status)

	created, err := scanCustodyWallet(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.ConstraintName {
			case "uq_custody_wallets_address":
				return nil, fmt.Errorf("%w: %s", ErrCustodyWalletDuplicateAddress, w.Address)
			case "fk_custody_wallets_network":
				return nil, fmt.Errorf("%w: %s", ErrCustodyWalletUnknownNetwork, w.Network)
			case "fk_custody_wallets_asset":
				return nil, fmt.Errorf("%w: %s", ErrCustodyWalletUnknownAsset, w.AssetCode)
			}
		}
		return nil, fmt.Errorf("crear custody wallet: %w", err)
	}
	return created, nil
}

// Get returns nil, nil if the id does not exist.
func (r *CustodyWalletRepository) Get(ctx context.Context, id uuid.UUID) (*CustodyWallet, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+custodyWalletColumns+` FROM custody_wallets WHERE id=$1`, id)
	w, err := scanCustodyWallet(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("obtener custody wallet: %w", err)
	}
	return w, nil
}

// FindActive returns the active custody wallet for (walletType, network,
// assetCode), or (nil, nil) if none exists. It uses the same
// idx_custody_wallets_type_network_asset partial index Bloque 7.1 created
// specifically for this lookup ("dame la wallet activa de tipo X en la red
// Y para el asset Z"). If more than one active wallet somehow matches
// (e.g. a rotation left two rows ACTIVE at once), it deterministically
// picks the most recently created one and callers must not assume
// exactly-one — this method exists to serve exactly one caller need
// (resolve THE current operational wallet), not to enumerate candidates.
func (r *CustodyWalletRepository) FindActive(ctx context.Context, walletType, network, assetCode string) (*CustodyWallet, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT `+custodyWalletColumns+` FROM custody_wallets
		WHERE wallet_type = $1 AND network = $2 AND asset_code = $3 AND status = 'active'
		ORDER BY created_at DESC
		LIMIT 1`,
		walletType, network, assetCode)
	w, err := scanCustodyWallet(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("buscar custody wallet activa: %w", err)
	}
	return w, nil
}

// List returns custody wallets matching every non-nil field of f, ordered
// by created_at ascending.
func (r *CustodyWalletRepository) List(ctx context.Context, f CustodyWalletFilter) ([]*CustodyWallet, error) {
	var (
		conditions []string
		args       []any
	)
	add := func(col string, val *string) {
		if val == nil {
			return
		}
		args = append(args, *val)
		conditions = append(conditions, fmt.Sprintf("%s = $%d", col, len(args)))
	}
	add("network", f.Network)
	add("asset_code", f.AssetCode)
	add("wallet_type", f.WalletType)
	add("status", f.Status)

	query := `SELECT ` + custodyWalletColumns + ` FROM custody_wallets`
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY created_at ASC"

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listar custody wallets: %w", err)
	}
	defer rows.Close()

	var out []*CustodyWallet
	for rows.Next() {
		w, err := scanCustodyWallet(rows)
		if err != nil {
			return nil, fmt.Errorf("leer custody wallet: %w", err)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// UpdateStatus performs a compare-and-set: it only applies if the row's
// current status equals expectFrom, returning (nil, nil) if it did not
// (either a concurrent change, or the caller's assumption about the
// current status was stale). This mirrors the CAS pattern already used by
// blockchainwithdrawal.Store.Apply, so two concurrent status changes can
// never race silently.
func (r *CustodyWalletRepository) UpdateStatus(ctx context.Context, id uuid.UUID, expectFrom, to string) (*CustodyWallet, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE custody_wallets
		SET status = $3
		WHERE id = $1 AND status = $2
		RETURNING `+custodyWalletColumns,
		id, expectFrom, to)

	w, err := scanCustodyWallet(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23514" { // check_violation
			return nil, fmt.Errorf("actualizar status de custody wallet: status inválido: %s", to)
		}
		return nil, fmt.Errorf("actualizar status de custody wallet: %w", err)
	}
	return w, nil
}

// FindActiveHotAddress returns the address of the ACTIVE custody HOT wallet
// that pays withdrawals for (network, asset), or "" when none exists.
// Satisfies blockchainwithdrawal.HotWalletBook.
func (r *CustodyWalletRepository) FindActiveHotAddress(ctx context.Context, network domain.Network, assetCode string) (string, error) {
	w, err := r.FindActive(ctx, "HOT", string(network), assetCode)
	if err != nil {
		return "", err
	}
	if w == nil {
		return "", nil
	}
	return w.Address, nil
}

// FindActiveHotKey returns the ENCRYPTED private key of the ACTIVE HOT wallet
// at (network, address). Only wallet_type = 'HOT' rows with status 'active'
// qualify, so GAS/TREASURY keys, disabled or retired wallets can never be
// returned through this path. Satisfies blockchainwithdrawal.HotKeyLookup.
func (r *CustodyWalletRepository) FindActiveHotKey(ctx context.Context, network domain.Network, address string) (string, bool, error) {
	var enc string
	err := r.pool.QueryRow(ctx, `
		SELECT encrypted_private_key FROM custody_wallets
		WHERE wallet_type = 'HOT' AND status = 'active' AND network = $1 AND address = $2
	`, string(network), address).Scan(&enc)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("buscar llave HOT: %w", err)
	}
	return enc, true, nil
}
