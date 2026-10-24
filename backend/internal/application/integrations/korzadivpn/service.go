package korzadivpn

import (
	"context"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/postgres"
)

type OrderRepository interface {
	Create(ctx context.Context, o *postgres.PaymentOrder) error
	FindByExternalOrder(ctx context.Context, source, externalOrderID string) (*postgres.PaymentOrder, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status string, ledgerTxID *uuid.UUID) error
}

type Service struct {
	orders  OrderRepository
	wallets domain.WalletRepository
	ledger  domain.LedgerRepository
	audit   domain.AuditLogger
}

func NewService(orders OrderRepository, wallets domain.WalletRepository, ledger domain.LedgerRepository, audit domain.AuditLogger) *Service {
	return &Service{orders: orders, wallets: wallets, ledger: ledger, audit: audit}
}

type ChargeInput struct {
	UserID          uuid.UUID
	ExternalOrderID string // ID de la orden en KorzadiVPN — clave de idempotencia real
	AssetCode       string
	Amount          decimal.Decimal
}

type ChargeResult struct {
	Status string // completed | failed | pending
	Reason string
}

// Charge cobra el plan VPN debitando del saldo del usuario, de forma idempotente por
// ExternalOrderID (constraint UNIQUE(source, external_order_id) en la tabla).
// KorzadiVPN debe activar el plan SOLO si Status == "completed".
func (s *Service) Charge(ctx context.Context, in ChargeInput) (*ChargeResult, error) {
	if existing, _ := s.orders.FindByExternalOrder(ctx, "korzadivpn", in.ExternalOrderID); existing != nil {
		return &ChargeResult{Status: existing.Status}, nil
	}
	if in.Amount.LessThanOrEqual(decimal.Zero) {
		return nil, domain.ErrInvalidAmount
	}

	wallet, err := s.wallets.FindWalletByUserID(ctx, in.UserID)
	if err != nil || wallet == nil {
		return nil, domain.ErrUserNotFound
	}
	account, err := s.wallets.GetOrCreateAccount(ctx, wallet.ID, in.AssetCode)
	if err != nil {
		return nil, err
	}

	order := &postgres.PaymentOrder{
		ID: uuid.New(), UserID: in.UserID, Source: "korzadivpn", ExternalOrderID: in.ExternalOrderID,
		AssetCode: in.AssetCode, Amount: in.Amount, Status: "pending",
		IdempotencyKey: "korzadivpn:" + in.ExternalOrderID,
	}
	if err := s.orders.Create(ctx, order); err != nil {
		return nil, err
	}

	ref := order.ID.String()
	ledgerTx, err := s.ledger.ExecuteTransaction(ctx, domain.TxTypeVPNPayment, order.IdempotencyKey, &ref,
		"Pago de plan KorzadiVPN, orden "+in.ExternalOrderID, &in.UserID,
		[]domain.LedgerEntryInput{{WalletAccountID: account.ID, AssetCode: in.AssetCode, Amount: in.Amount.Neg()}})
	if err != nil {
		_ = s.orders.UpdateStatus(ctx, order.ID, "failed", nil)
		if err == domain.ErrInsufficientFunds {
			return &ChargeResult{Status: "failed", Reason: "insufficient_funds"}, nil
		}
		return nil, err
	}

	_ = s.orders.UpdateStatus(ctx, order.ID, "completed", &ledgerTx.ID)
	if s.audit != nil {
		_ = s.audit.Log(ctx, &in.UserID, "integration", "korzadivpn_payment_completed", "payment_order", &order.ID,
			map[string]any{"external_order_id": in.ExternalOrderID, "amount": in.Amount.String()}, "")
	}
	return &ChargeResult{Status: "completed"}, nil
}
