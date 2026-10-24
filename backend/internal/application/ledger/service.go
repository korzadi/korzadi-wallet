// Package ledger is the Application-layer home for financial-ledger
// business logic (Bloque 2). Before this block, ExecuteTransaction's
// validation/logging lived directly inside the Postgres repository, and
// HTTP handlers / the blockchain watcher reached infrastructure types
// almost directly. This package is the seam the spec asks for:
//
//	Watcher/Handler -> Application Service (this package) -> Repository -> PostgreSQL
//
// Service is a thin, deliberately un-clever wrapper: all atomicity,
// locking and idempotency guarantees live in the repository
// implementation (infrastructure/postgres), because those are
// database-transaction concerns that cannot be correctly implemented
// above the DB driver. What belongs here is input validation, structured
// audit logging (spec §"OBSERVABILIDAD"), and the reservation API.
package ledger

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/application/security"
	"korzadi-wallet/backend/internal/domain"
)

var ErrAdjustmentRequiresService = domain.ErrAdjustmentRequiresService

var ErrAdjustmentForbidden = fmt.Errorf("ledger: adjustment requires super_admin")

// Repository is the persistence port this service depends on — exactly
// domain.LedgerRepository, named locally so this package documents its own
// dependency shape without forcing every caller to import domain just to
// read the interface.
type Repository = domain.LedgerRepository

// Reservations is the persistence port for lock/release/consume.
type Reservations = domain.ReservationRepository

// AdjustmentRepository is implemented by the PostgreSQL ledger repository.
// It records the adjustment, audit row and security event in one SQL tx.
type AdjustmentRepository interface {
	ExecuteAdjustment(ctx context.Context, idempotencyKey, reason string, actorID uuid.UUID, ip string,
		entries []domain.LedgerEntryInput) (*domain.LedgerTransaction, error)
}

type AdjustmentRoleResolver interface {
	GetRolesForUser(ctx context.Context, userID uuid.UUID) ([]string, error)
}

type AdjustmentSecurityEvents interface {
	Log(ctx context.Context, event security.Event) error
}

type Service struct {
	repo         Repository
	reservations Reservations
	adjustments  AdjustmentRepository
	roles        AdjustmentRoleResolver
	events       AdjustmentSecurityEvents
}

func NewService(repo Repository, reservations Reservations) *Service {
	s := &Service{repo: repo, reservations: reservations}
	s.adjustments, _ = repo.(AdjustmentRepository)
	return s
}

// WithAdjustmentAuthorization wires the existing RBAC source and security
// event logger. Missing dependencies fail closed when ExecuteAdjustment is used.
func (s *Service) WithAdjustmentAuthorization(roles AdjustmentRoleResolver, events AdjustmentSecurityEvents) *Service {
	s.roles = roles
	s.events = events
	return s
}

// ExecuteTransaction is THE single entrypoint for posting a balanced
// ledger transaction in this codebase — every money-moving flow
// (transfers, blockchain deposits, BSC withdrawals, affiliate payouts,
// KorzadiVPN charges) is expected to route through either this method or
// directly through the same underlying domain.LedgerRepository it wraps.
// There is exactly one posting engine; this method does not add a second
// one. Named to match domain.LedgerRepository.ExecuteTransaction exactly
// so this Service is a drop-in for any port expecting that shape
// (deposit.LedgerPoster, bscwithdrawal.LedgerPoster).
func (s *Service) ExecuteTransaction(
	ctx context.Context,
	txType domain.TransactionType,
	idempotencyKey string,
	referenceID *string,
	description string,
	createdBy *uuid.UUID,
	entries []domain.LedgerEntryInput,
) (*domain.LedgerTransaction, error) {
	if txType == domain.TxTypeAdjustment {
		return nil, ErrAdjustmentRequiresService
	}
	if idempotencyKey == "" {
		return nil, fmt.Errorf("ledger: idempotency_key requerido")
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("ledger: se requiere al menos un entry")
	}
	for _, e := range entries {
		if e.WalletAccountID == uuid.Nil {
			return nil, fmt.Errorf("ledger: wallet_account_id inválido en entry")
		}
		if e.AssetCode == "" {
			return nil, fmt.Errorf("ledger: asset_code requerido en entry")
		}
		if e.Amount.IsZero() {
			return nil, fmt.Errorf("ledger: entry con amount cero (wallet_account=%s) — no aporta nada a la transacción y probablemente indica un bug del caller", e.WalletAccountID)
		}
	}

	tx, err := s.repo.ExecuteTransaction(ctx, txType, idempotencyKey, referenceID, description, createdBy, entries)
	if err != nil {
		log.Printf("ledger: tipo=%s idempotency_key=%s FALLÓ: %v", txType, idempotencyKey, err)
		return nil, err
	}

	// Observability (spec: "ledger transaction id, account id, asset,
	// operation type, amount, source, status" — never private keys,
	// seeds, or credentials, none of which ever pass through this layer).
	for _, e := range entries {
		log.Printf("ledger: tx=%s tipo=%s cuenta=%s asset=%s monto=%s estado=%s",
			tx.ID, txType, e.WalletAccountID, e.AssetCode, e.Amount.String(), tx.Status)
	}

	return tx, nil
}

// ExecuteAdjustment is the only application entry point for adjustments.
// Roles are resolved from the persistent role repository rather than trusted
// from a request claim; the SQL repository independently enforces the same
// actor, reason and service-only guard and commits both audit records atomically.
func (s *Service) ExecuteAdjustment(
	ctx context.Context,
	actorID uuid.UUID,
	ip string,
	idempotencyKey string,
	reason string,
	entries []domain.LedgerEntryInput,
) (*domain.LedgerTransaction, error) {
	reason = strings.TrimSpace(reason)
	if actorID == uuid.Nil {
		return nil, fmt.Errorf("ledger: actor requerido para adjustment")
	}
	if idempotencyKey == "" {
		return nil, fmt.Errorf("ledger: idempotency_key requerido para adjustment")
	}
	if reason == "" {
		return nil, fmt.Errorf("ledger: motivo requerido para adjustment")
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("ledger: se requiere al menos un entry para adjustment")
	}
	if s.adjustments == nil || s.roles == nil {
		return nil, fmt.Errorf("ledger: adjustment service no está configurado")
	}
	roles, err := s.roles.GetRolesForUser(ctx, actorID)
	if err != nil {
		return nil, fmt.Errorf("ledger: verificar rol del adjustment: %w", err)
	}
	isSuperAdmin := false
	for _, role := range roles {
		if role == "super_admin" {
			isSuperAdmin = true
			break
		}
	}
	if !isSuperAdmin {
		if s.events != nil {
			actor := actorID
			_ = s.events.Log(ctx, security.Event{
				Type: security.EventAuthorizationDenied, Severity: "critical", UserID: &actor,
				ActorRole: "user", Operation: security.OpAdminOperation,
				ResourceType: "ledger_adjustment",
				Metadata:     map[string]any{"reason": "super_admin role required"}, IP: ip,
			})
		}
		return nil, ErrAdjustmentForbidden
	}
	for _, entry := range entries {
		if entry.WalletAccountID == uuid.Nil || entry.AssetCode == "" || entry.Amount.IsZero() {
			return nil, fmt.Errorf("ledger: adjustment entry inválido")
		}
	}

	tx, err := s.adjustments.ExecuteAdjustment(ctx, idempotencyKey, reason, actorID, ip, entries)
	if err != nil {
		log.Printf("ledger: adjustment actor=%s idempotency_key=%s FALLÓ: %v", actorID, idempotencyKey, err)
		return nil, err
	}
	for _, entry := range entries {
		log.Printf("ledger: adjustment tx=%s actor=%s account=%s asset=%s amount=%s reason=%q",
			tx.ID, actorID, entry.WalletAccountID, entry.AssetCode, entry.Amount.String(), reason)
	}
	return tx, nil
}

func (s *Service) GetBalance(ctx context.Context, walletAccountID uuid.UUID) (domain.Balance, error) {
	return s.reservations.GetBalance(ctx, walletAccountID)
}

func (s *Service) FindByIdempotencyKey(ctx context.Context, key string) (*domain.LedgerTransaction, error) {
	return s.repo.FindByIdempotencyKey(ctx, key)
}

func (s *Service) ListEntriesByAccount(ctx context.Context, walletAccountID uuid.UUID, limit, offset int) ([]*domain.LedgerEntry, error) {
	return s.repo.ListEntriesByAccount(ctx, walletAccountID, limit, offset)
}

// ---- Reservations (lock / release / consume) ----
//
// These do not move money and do not touch ledger_entries — they only move
// balance between "available" and "locked" on a single wallet_account
// (see domain.ReservationRepository's doc comment). They exist so a future
// block (real withdrawals, holds) has a tested, correct primitive to build
// on, per spec: "No implementar todavía el sistema de retiros. Solo
// construir la base correcta para operaciones futuras."

func (s *Service) Reserve(ctx context.Context, walletAccountID uuid.UUID, assetCode string, amount decimal.Decimal, reason string, referenceID *string) (*domain.Reservation, error) {
	if !amount.IsPositive() {
		return nil, fmt.Errorf("ledger: el monto de una reserva debe ser positivo")
	}
	r, err := s.reservations.Reserve(ctx, walletAccountID, assetCode, amount, reason, referenceID)
	if err != nil {
		log.Printf("ledger: reserva FALLÓ cuenta=%s asset=%s monto=%s: %v", walletAccountID, assetCode, amount.String(), err)
		return nil, err
	}
	log.Printf("ledger: reserva=%s creada cuenta=%s asset=%s monto=%s reason=%q", r.ID, walletAccountID, assetCode, amount.String(), reason)
	return r, nil
}

func (s *Service) Release(ctx context.Context, reservationID uuid.UUID) error {
	err := s.reservations.Release(ctx, reservationID)
	if err != nil {
		log.Printf("ledger: liberar reserva=%s FALLÓ: %v", reservationID, err)
		return err
	}
	log.Printf("ledger: reserva=%s liberada", reservationID)
	return nil
}

func (s *Service) Consume(ctx context.Context, reservationID uuid.UUID) error {
	err := s.reservations.Consume(ctx, reservationID)
	if err != nil {
		log.Printf("ledger: consumir reserva=%s FALLÓ: %v", reservationID, err)
		return err
	}
	log.Printf("ledger: reserva=%s consumida", reservationID)
	return nil
}

func (s *Service) FindReservation(ctx context.Context, reservationID uuid.UUID) (*domain.Reservation, error) {
	return s.reservations.FindByID(ctx, reservationID)
}

// ---- Liquidación atómica reserva + débito (Bloque 6B) ----

// ErrSettlementNotSupported se devuelve si el repositorio configurado no
// implementa domain.ReservationSettler. Falla cerrado: nunca se degrada a
// "Consume; luego ExecuteTransaction" en dos pasos (ventana de double
// spend, ver postgres.LedgerRepository.SettleReservations).
var ErrSettlementNotSupported = fmt.Errorf("ledger: el repositorio no soporta liquidación atómica de reservas")

// SettleReservations consume las reservas indicadas y postea el débito
// contable en UNA transacción de base de datos. Es un wrapper delgado:
// valida entradas, delega en el repositorio (que implementa
// domain.ReservationSettler — hoy postgres.LedgerRepository) y registra logs
// sin datos sensibles. Se resuelve por type-assertion para no cambiar
// NewService ni romper a los callers/fakes existentes.
func (s *Service) SettleReservations(ctx context.Context, in domain.ReservationSettlementInput) (*domain.LedgerTransaction, error) {
	if in.IdempotencyKey == "" {
		return nil, fmt.Errorf("ledger: idempotency_key requerido")
	}
	if len(in.ReservationIDs) == 0 {
		return nil, fmt.Errorf("ledger: se requiere al menos una reserva")
	}
	if len(in.Entries) == 0 {
		return nil, fmt.Errorf("ledger: se requiere al menos un entry")
	}
	for _, e := range in.Entries {
		if e.WalletAccountID == uuid.Nil {
			return nil, fmt.Errorf("ledger: wallet_account_id inválido en entry")
		}
		if e.AssetCode == "" {
			return nil, fmt.Errorf("ledger: asset_code requerido en entry")
		}
		if e.Amount.IsZero() {
			return nil, fmt.Errorf("ledger: entry con amount cero (wallet_account=%s)", e.WalletAccountID)
		}
	}

	settler, ok := s.repo.(domain.ReservationSettler)
	if !ok {
		return nil, ErrSettlementNotSupported
	}

	tx, err := settler.SettleReservations(ctx, in)
	if err != nil {
		log.Printf("ledger: liquidación FALLÓ tipo=%s idempotency_key=%s reservas=%d: %v",
			in.TxType, in.IdempotencyKey, len(in.ReservationIDs), err)
		return nil, err
	}
	log.Printf("ledger: liquidación tx=%s tipo=%s idempotency_key=%s reservas=%d entries=%d",
		tx.ID, in.TxType, in.IdempotencyKey, len(in.ReservationIDs), len(in.Entries))
	return tx, nil
}

// FindReservationByReference localiza una reserva por (reference_id,
// reason). Devuelve (nil, nil) si no existe. Falla cerrado si el repositorio
// de reservas no soporta la búsqueda (domain.ReservationFinder).
func (s *Service) FindReservationByReference(ctx context.Context, referenceID, reason string) (*domain.Reservation, error) {
	finder, ok := s.reservations.(domain.ReservationFinder)
	if !ok {
		return nil, fmt.Errorf("ledger: el repositorio de reservas no soporta búsqueda por referencia")
	}
	return finder.FindByReference(ctx, referenceID, reason)
}
