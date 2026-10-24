package postgres

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

// ---- gas_station_policies ----

// GasStationPolicy represents one row of gas_station_policies (Bloque 7.2):
// the refill thresholds for one specific GAS custody wallet. All amounts
// are in the wallet's native asset units (e.g. BNB), never wei — the same
// convention blockchain_withdrawals already uses for native amounts.
type GasStationPolicy struct {
	CustodyWalletID uuid.UUID
	MinimumBalance  decimal.Decimal
	TargetBalance   decimal.Decimal
	MaxRefill       decimal.Decimal
	Enabled         bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

var (
	ErrGasStationPolicyInvalid = errors.New("postgres: política de gas station inválida (target >= minimum, 0 < max_refill <= target)")
)

type GasStationPolicyRepository struct{ pool *pgxpool.Pool }

func NewGasStationPolicyRepository(pool *pgxpool.Pool) *GasStationPolicyRepository {
	return &GasStationPolicyRepository{pool: pool}
}

const gasStationPolicyColumns = `custody_wallet_id, minimum_balance, target_balance, max_refill, enabled, created_at, updated_at`

func scanGasStationPolicy(row pgx.Row) (*GasStationPolicy, error) {
	var p GasStationPolicy
	if err := row.Scan(&p.CustodyWalletID, &p.MinimumBalance, &p.TargetBalance, &p.MaxRefill, &p.Enabled, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}
	return &p, nil
}

// Get returns nil, nil if no policy is configured for that custody wallet
// yet — the Gas Station treats "no policy" the same as "disabled".
func (r *GasStationPolicyRepository) Get(ctx context.Context, custodyWalletID uuid.UUID) (*GasStationPolicy, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+gasStationPolicyColumns+` FROM gas_station_policies WHERE custody_wallet_id = $1`, custodyWalletID)
	p, err := scanGasStationPolicy(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("leer política de gas station: %w", err)
	}
	return p, nil
}

// Upsert creates or replaces the policy for one custody wallet. This is an
// operator/admin action (not called by the automatic refill path itself)
// — no HTTP handler is wired to it in this block (out of scope for 7.2;
// see final report), but the repository is complete so wiring one later is
// a thin handler, not new persistence logic.
func (r *GasStationPolicyRepository) Upsert(ctx context.Context, p GasStationPolicy) (*GasStationPolicy, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO gas_station_policies (custody_wallet_id, minimum_balance, target_balance, max_refill, enabled)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (custody_wallet_id) DO UPDATE SET
			minimum_balance = EXCLUDED.minimum_balance,
			target_balance  = EXCLUDED.target_balance,
			max_refill      = EXCLUDED.max_refill,
			enabled         = EXCLUDED.enabled
		RETURNING `+gasStationPolicyColumns,
		p.CustodyWalletID, p.MinimumBalance, p.TargetBalance, p.MaxRefill, p.Enabled)
	updated, err := scanGasStationPolicy(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23514" { // check_violation
			return nil, ErrGasStationPolicyInvalid
		}
		return nil, fmt.Errorf("guardar política de gas station: %w", err)
	}
	return updated, nil
}

// ---- gas_station_refills ----

// RefillStatus mirrors gas_station_refills.status.
type RefillStatus string

const (
	RefillStatusPending     RefillStatus = "pending"
	RefillStatusBroadcasted RefillStatus = "broadcasted"
	RefillStatusConfirmed   RefillStatus = "confirmed"
	RefillStatusFailed      RefillStatus = "failed"
)

// GasStationRefill represents one row of gas_station_refills (Bloque 7.2):
// one attempt to top up `TargetAddress` with native gas from one GAS
// custody wallet. AmountWei is the exact base-unit amount decided by the
// policy (never re-derived at broadcast time, so what gets audited is
// exactly what got signed).
type GasStationRefill struct {
	ID              uuid.UUID
	CustodyWalletID uuid.UUID
	Network         string
	TargetAddress   string
	OwnerRef        string
	AmountWei       *big.Int
	Status          RefillStatus
	TxHash          string
	ErrorDetail     string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// ErrRefillInFlight is returned by Create when another refill is already
// pending/broadcasted for the same (network, target_address) — the
// uq_gas_station_refills_inflight_target partial unique index translated
// into a typed error, so the application layer can react (wait/observe)
// instead of sending a second on-chain transaction.
var ErrRefillInFlight = errors.New("postgres: ya hay un refill en curso para esa dirección")

type GasStationRefillRepository struct{ pool *pgxpool.Pool }

func NewGasStationRefillRepository(pool *pgxpool.Pool) *GasStationRefillRepository {
	return &GasStationRefillRepository{pool: pool}
}

const gasStationRefillColumns = `id, custody_wallet_id, network, target_address, owner_ref, amount_wei, status, COALESCE(tx_hash,''), COALESCE(error_detail,''), created_at, updated_at`

func scanGasStationRefill(row pgx.Row) (*GasStationRefill, error) {
	var (
		r         GasStationRefill
		amountStr string
	)
	if err := row.Scan(&r.ID, &r.CustodyWalletID, &r.Network, &r.TargetAddress, &r.OwnerRef, &amountStr,
		&r.Status, &r.TxHash, &r.ErrorDetail, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return nil, err
	}
	amount, ok := new(big.Int).SetString(amountStr, 10)
	if !ok {
		return nil, fmt.Errorf("gas_station_refills: amount_wei ilegible: %q", amountStr)
	}
	r.AmountWei = amount
	return &r, nil
}

// FindOwned returns the caller's own non-failed refill row for
// (network, targetAddress, ownerRef) if one exists — the resume path for a
// retry after a crash (spec: "una misma operación de refill no debe poder
// ejecutarse dos veces accidentalmente"). (nil, nil) if none.
func (r *GasStationRefillRepository) FindOwned(ctx context.Context, network, targetAddress, ownerRef string) (*GasStationRefill, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT `+gasStationRefillColumns+` FROM gas_station_refills
		WHERE network = $1 AND target_address = $2 AND owner_ref = $3 AND status <> 'failed'`,
		network, normalizeNonceAddress(targetAddress), ownerRef)
	rf, err := scanGasStationRefill(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("buscar refill propio: %w", err)
	}
	return rf, nil
}

// FindInFlight returns whatever pending/broadcasted refill currently owns
// (network, targetAddress) — used to observe someone else's in-flight
// refill after Create reports ErrRefillInFlight. (nil, nil) if none (it
// may have just been confirmed/failed between the two calls).
func (r *GasStationRefillRepository) FindInFlight(ctx context.Context, network, targetAddress string) (*GasStationRefill, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT `+gasStationRefillColumns+` FROM gas_station_refills
		WHERE network = $1 AND target_address = $2 AND status IN ('pending','broadcasted')`,
		network, normalizeNonceAddress(targetAddress))
	rf, err := scanGasStationRefill(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("buscar refill en curso: %w", err)
	}
	return rf, nil
}

// Create inserts a new refill row in 'pending' status. Returns
// ErrRefillInFlight (not a raw pg error) if uq_gas_station_refills_inflight_target
// rejects it — this is the concurrency guarantee: two concurrent calls for
// the same (network, target_address) can never both get a fresh 'pending'
// row back.
func (r *GasStationRefillRepository) Create(ctx context.Context, rf GasStationRefill) (*GasStationRefill, error) {
	id := rf.ID
	if id == uuid.Nil {
		id = uuid.New()
	}
	row := r.pool.QueryRow(ctx, `
		INSERT INTO gas_station_refills (id, custody_wallet_id, network, target_address, owner_ref, amount_wei, status)
		VALUES ($1,$2,$3,$4,$5,$6,'pending')
		RETURNING `+gasStationRefillColumns,
		id, rf.CustodyWalletID, rf.Network, normalizeNonceAddress(rf.TargetAddress), rf.OwnerRef, rf.AmountWei.String())
	created, err := scanGasStationRefill(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
			if strings.Contains(pgErr.ConstraintName, "inflight") {
				return nil, ErrRefillInFlight
			}
			// Owner-uniqueness race: someone else concurrently created the
			// SAME owner's row (extremely unlikely — one owner is one
			// withdrawal — but handled rather than surfaced as a raw
			// driver error). Callers should FindOwned and resume.
			return nil, fmt.Errorf("%w: %s", ErrRefillInFlight, pgErr.ConstraintName)
		}
		return nil, fmt.Errorf("crear refill: %w", err)
	}
	return created, nil
}

// MarkBroadcasted records the signed tx hash BEFORE the caller's own
// broadcast call returns, mirroring blockchainwithdrawal's own
// "persist the signed tx before you can lose track of it" discipline —
// so a crash between signing and broadcasting still leaves a record of
// what was signed, under this same owner, for the resume path to find.
func (r *GasStationRefillRepository) MarkBroadcasted(ctx context.Context, id uuid.UUID, txHash string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE gas_station_refills SET status = 'broadcasted', tx_hash = $2
		WHERE id = $1 AND status = 'pending'`, id, txHash)
	if err != nil {
		return fmt.Errorf("marcar refill transmitido: %w", err)
	}
	return nil
}

// MarkConfirmed closes out a successful refill.
func (r *GasStationRefillRepository) MarkConfirmed(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE gas_station_refills SET status = 'confirmed'
		WHERE id = $1 AND status IN ('pending','broadcasted')`, id)
	if err != nil {
		return fmt.Errorf("marcar refill confirmado: %w", err)
	}
	return nil
}

// MarkFailed closes out a refill that is DEFINITELY not going to land
// (e.g. the broadcast was cleanly rejected, not ambiguous) and records why.
// A failed row is invisible to both unique indexes, so the same
// (network, target_address) — and even the same owner_ref — can retry
// cleanly afterwards.
func (r *GasStationRefillRepository) MarkFailed(ctx context.Context, id uuid.UUID, reason string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE gas_station_refills SET status = 'failed', error_detail = $2
		WHERE id = $1 AND status IN ('pending','broadcasted')`, id, reason)
	if err != nil {
		return fmt.Errorf("marcar refill fallido: %w", err)
	}
	return nil
}
