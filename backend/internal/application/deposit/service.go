package deposit

import (
	"context"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
)

type Service struct {
	wallets  domain.WalletRepository
	deposits domain.DepositRepository
	ledger   domain.LedgerRepository
	provider domain.PaymentProvider
	audit    domain.AuditLogger
}

// systemSettlementWalletID es la misma wallet de sistema sembrada en la migración 0001.
// Para depósitos representa la contraparte de "dinero que entra a la plataforma desde
// un proveedor externo" — así el ledger queda balanceado a 0 en vez de tener una sola
// entrada suelta.
var systemSettlementWalletID = uuid.MustParse("00000000-0000-0000-0000-000000000002")

func NewService(w domain.WalletRepository, d domain.DepositRepository, l domain.LedgerRepository,
	p domain.PaymentProvider, a domain.AuditLogger) *Service {
	return &Service{wallets: w, deposits: d, ledger: l, provider: p, audit: a}
}

type CreateDepositInput struct {
	UserID         uuid.UUID
	AssetCode      string
	Amount         decimal.Decimal
	IdempotencyKey string
	IP             string
}

// Create solo REGISTRA la intención de depósito y solicita una referencia al proveedor.
// El estado queda en "pending" hasta que Confirm() sea invocado por un webhook real
// o, en sandbox, por una acción administrativa explícita. Nunca se marca completed aquí.
func (s *Service) Create(ctx context.Context, in CreateDepositInput) (*domain.Deposit, error) {
	if in.Amount.LessThanOrEqual(decimal.Zero) {
		return nil, domain.ErrInvalidAmount
	}
	if existing, _ := s.deposits.FindByIdempotencyKey(ctx, in.IdempotencyKey); existing != nil {
		// AUDIT FIX: never hand back another user's deposit (or one created with
		// different parameters) just because the Idempotency-Key matches.
		if existing.UserID != in.UserID || existing.AssetCode != in.AssetCode || !existing.Amount.Equal(in.Amount) {
			return nil, &domain.DomainError{Code: "idempotency_key_reused", Message: "el Idempotency-Key ya fue usado con otra solicitud"}
		}
		return existing, nil
	}

	wallet, err := s.wallets.FindWalletByUserID(ctx, in.UserID)
	if err != nil || wallet == nil {
		return nil, domain.ErrUserNotFound
	}

	providerRef, err := s.provider.CreateDeposit(ctx, in.UserID, in.Amount, in.AssetCode)
	if err != nil {
		return nil, err
	}

	dep := &domain.Deposit{
		ID: uuid.New(), UserID: in.UserID, WalletID: wallet.ID, AssetCode: in.AssetCode,
		Amount: in.Amount, Provider: s.provider.Name(), ProviderReference: &providerRef,
		Status: domain.StatusPending, IdempotencyKey: in.IdempotencyKey,
	}
	if err := s.deposits.Create(ctx, dep); err != nil {
		return nil, err
	}
	if s.audit != nil {
		_ = s.audit.Log(ctx, &in.UserID, "user", "deposit_initiated", "deposit", &dep.ID,
			map[string]any{"amount": in.Amount.String(), "asset": in.AssetCode, "provider_reference": providerRef}, in.IP)
	}
	return dep, nil
}

// Confirm aplica el movimiento real al ledger. Solo debe invocarse desde un webhook
// verificado del proveedor (o, en sandbox, desde el panel admin marcado explícitamente).
func (s *Service) Confirm(ctx context.Context, depositID uuid.UUID) (*domain.Deposit, error) {
	// Nota de implementación: en producción, este método recibe el evento ya validado
	// (firma del webhook verificada en la capa HTTP) antes de llegar aquí.
	dep, err := s.deposits.FindByID(ctx, depositID)
	if err != nil {
		return nil, err
	}
	if dep == nil {
		return nil, domain.ErrUserNotFound
	}
	if dep.Status == domain.StatusCompleted {
		return dep, nil // idempotente
	}

	account, err := s.wallets.GetOrCreateAccount(ctx, dep.WalletID, dep.AssetCode)
	if err != nil {
		return nil, err
	}
	settlementAccount, err := s.wallets.GetOrCreateAccount(ctx, systemSettlementWalletID, dep.AssetCode)
	if err != nil {
		return nil, err
	}

	ref := dep.ID.String()

	// La clave que manda el usuario se namespacea antes de llegar al ledger
	// (nunca puede coincidir con claves internas ni con las de otro usuario).
	// Compatibilidad: si una confirmación previa a este cambio ya posteó con la
	// clave cruda, esa tx (mismo tipo y reference_id) es la que cuenta; así no
	// se acredita dos veces.
	ledgerKey := domain.NamespacedIdempotencyKey(domain.IdemPrefixFiatDeposit, dep.UserID, dep.IdempotencyKey)
	if legacy, lerr := s.ledger.FindByIdempotencyKey(ctx, dep.IdempotencyKey); lerr == nil && legacy != nil &&
		legacy.Type == domain.TxTypeDeposit && legacy.ReferenceID != nil && *legacy.ReferenceID == ref {
		if err := s.deposits.UpdateStatus(ctx, dep.ID, domain.StatusCompleted, &legacy.ID); err != nil {
			return nil, err
		}
		dep.Status = domain.StatusCompleted
		dep.LedgerTransactionID = &legacy.ID
		return dep, nil
	}
	ledgerTx, err := s.ledger.ExecuteTransaction(ctx, domain.TxTypeDeposit, ledgerKey, &ref,
		"Depósito confirmado por proveedor "+dep.Provider, &dep.UserID,
		[]domain.LedgerEntryInput{
			{WalletAccountID: account.ID, AssetCode: dep.AssetCode, Amount: dep.Amount},
			{WalletAccountID: settlementAccount.ID, AssetCode: dep.AssetCode, Amount: dep.Amount.Neg()},
		})
	if err != nil {
		_ = s.deposits.UpdateStatus(ctx, dep.ID, domain.StatusFailed, nil)
		return nil, err
	}

	if err := s.deposits.UpdateStatus(ctx, dep.ID, domain.StatusCompleted, &ledgerTx.ID); err != nil {
		return nil, err
	}
	dep.Status = domain.StatusCompleted
	dep.LedgerTransactionID = &ledgerTx.ID
	return dep, nil
}
