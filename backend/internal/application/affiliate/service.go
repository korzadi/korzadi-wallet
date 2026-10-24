package affiliate

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/postgres"
)

const defaultCommissionRate = "0.05" // 5%

// systemCommissionPoolWalletID es la wallet de sistema sembrada en la migración 0001,
// desde donde se debitan las comisiones pagadas a afiliados (ver 0001_init.up.sql).
var systemCommissionPoolWalletID = uuid.MustParse("00000000-0000-0000-0000-000000000002")

// Repository agrupa las operaciones de afiliados necesarias (implementadas por postgres.AffiliateRepository).
type Repository interface {
	Create(ctx context.Context, a *postgres.Affiliate) error
	FindByUserID(ctx context.Context, userID uuid.UUID) (*postgres.Affiliate, error)
	FindByCode(ctx context.Context, code string) (*postgres.Affiliate, error)
	AddReferral(ctx context.Context, affiliateID, referredUserID uuid.UUID) error
	RecordCommission(ctx context.Context, affiliateID uuid.UUID, amount decimal.Decimal, assetCode string, ledgerTxID uuid.UUID) error
}

type Service struct {
	repo    Repository
	wallets domain.WalletRepository
	ledger  domain.LedgerRepository
	audit   domain.AuditLogger
}

func NewService(repo Repository, wallets domain.WalletRepository, ledger domain.LedgerRepository, audit domain.AuditLogger) *Service {
	return &Service{repo: repo, wallets: wallets, ledger: ledger, audit: audit}
}

// Enroll convierte a un usuario en afiliado y genera su código único.
func (s *Service) Enroll(ctx context.Context, userID uuid.UUID) (*postgres.Affiliate, error) {
	if existing, _ := s.repo.FindByUserID(ctx, userID); existing != nil {
		return existing, nil
	}
	rate, _ := decimal.NewFromString(defaultCommissionRate)
	a := &postgres.Affiliate{
		ID: uuid.New(), UserID: userID, AffiliateCode: generateCode(),
		Status: "active", CommissionRate: rate,
	}
	if err := s.repo.Create(ctx, a); err != nil {
		return nil, err
	}
	return a, nil
}

// RegisterReferral vincula un nuevo usuario referido a un afiliado por su código.
func (s *Service) RegisterReferral(ctx context.Context, affiliateCode string, referredUserID uuid.UUID) error {
	aff, err := s.repo.FindByCode(ctx, affiliateCode)
	if err != nil {
		return err
	}
	if aff == nil {
		return &domain.DomainError{Code: "affiliate_not_found", Message: "código de afiliado inválido"}
	}
	return s.repo.AddReferral(ctx, aff.ID, referredUserID)
}

// PayCommission calcula la comisión sobre un monto base (ej. actividad interna del referido)
// y la acredita al afiliado como un movimiento REAL del ledger (nunca sumando directo al total_commissions).
// idempotencyKey debe derivarse del evento origen (ej. transfer_id) para no pagar la misma comisión dos veces.
func (s *Service) PayCommission(ctx context.Context, affiliateUserID uuid.UUID, baseAmount decimal.Decimal,
	assetCode, idempotencyKey, sourceDescription string) error {
	aff, err := s.repo.FindByUserID(ctx, affiliateUserID)
	if err != nil {
		return err
	}
	if aff == nil || aff.Status != "active" {
		return nil // sin afiliado activo, no hay comisión que pagar
	}

	commission := baseAmount.Mul(aff.CommissionRate).Round(2)
	if commission.LessThanOrEqual(decimal.Zero) {
		return nil
	}

	wallet, err := s.wallets.FindWalletByUserID(ctx, affiliateUserID)
	if err != nil || wallet == nil {
		return domain.ErrUserNotFound
	}
	account, err := s.wallets.GetOrCreateAccount(ctx, wallet.ID, assetCode)
	if err != nil {
		return err
	}

	// La comisión se debita de la cuenta "pool" de la plataforma (sembrada en la migración)
	// y se acredita al afiliado — el ledger queda balanceado a 0, nunca se crea dinero
	// de la nada (regla explícita del spec, sección 13). Si el pool no tiene fondos
	// suficientes, ExecuteTransaction rechaza la operación con ErrInsufficientFunds,
	// lo cual es la señal correcta de que el pool necesita fondearse primero.
	poolAccount, err := s.wallets.GetOrCreateAccount(ctx, systemCommissionPoolWalletID, assetCode)
	if err != nil {
		return err
	}

	ledgerTx, err := s.ledger.ExecuteTransaction(ctx, domain.TxTypeCommission, idempotencyKey, nil,
		sourceDescription, &affiliateUserID,
		[]domain.LedgerEntryInput{
			{WalletAccountID: poolAccount.ID, AssetCode: assetCode, Amount: commission.Neg()},
			{WalletAccountID: account.ID, AssetCode: assetCode, Amount: commission},
		})
	if err != nil {
		return err
	}

	if err := s.repo.RecordCommission(ctx, aff.ID, commission, assetCode, ledgerTx.ID); err != nil {
		return err
	}
	if s.audit != nil {
		_ = s.audit.Log(ctx, &affiliateUserID, "affiliate", "commission_paid", "affiliate_commission", &ledgerTx.ID,
			map[string]any{"amount": commission.String(), "asset": assetCode}, "")
	}
	return nil
}

func generateCode() string {
	buf := make([]byte, 5)
	_, _ = rand.Read(buf)
	return fmt.Sprintf("KZ-%s", base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf))
}
