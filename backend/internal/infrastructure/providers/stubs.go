package providers

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
)

// ErrNotConfigured se retorna por todos los stubs. Esto es intencional:
// el sistema NUNCA debe fingir que un depósito/retiro/verificación real ocurrió
// cuando en realidad no hay proveedor conectado (regla #2 y #20 del spec).
var ErrNotConfigured = errors.New("proveedor no configurado: esta es una interfaz preparada para " +
	"integración futura, no procesa dinero ni verificaciones reales todavía")

// SandboxPaymentProvider es el único "proveedor" activo por defecto.
// Se usa solo en entornos de desarrollo/staging explícitamente marcados como sandbox,
// y todo lo que produce se etiqueta como tal en la respuesta de la API — nunca se presenta
// como una confirmación de dinero real.
type SandboxPaymentProvider struct{}

func NewSandboxPaymentProvider() *SandboxPaymentProvider { return &SandboxPaymentProvider{} }

func (p *SandboxPaymentProvider) Name() string { return "sandbox" }

func (p *SandboxPaymentProvider) CreateDeposit(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, assetCode string) (string, error) {
	// Queda deliberadamente en "pending": un depósito sandbox no se autocompleta.
	// Requiere una confirmación explícita (endpoint administrativo o webhook simulado)
	// para avanzar de estado, igual que ocurriría con un proveedor real.
	return "sandbox_" + uuid.New().String(), nil
}

func (p *SandboxPaymentProvider) CreateWithdrawal(ctx context.Context, userID uuid.UUID, amount decimal.Decimal, assetCode string, destination map[string]any) (string, error) {
	return "sandbox_" + uuid.New().String(), nil
}

func (p *SandboxPaymentProvider) HandleWebhook(ctx context.Context, payload []byte) error {
	return ErrNotConfigured
}

// StubCryptoProvider — sin proveedor de custodia/cripto real conectado.
type StubCryptoProvider struct{}

func NewStubCryptoProvider() *StubCryptoProvider { return &StubCryptoProvider{} }

func (p *StubCryptoProvider) Name() string { return "stub" }

func (p *StubCryptoProvider) GenerateAddress(ctx context.Context, userID uuid.UUID, assetCode string) (string, error) {
	return "", ErrNotConfigured
}

func (p *StubCryptoProvider) GetTransactionStatus(ctx context.Context, txHash string) (domain.TransactionStatus, error) {
	return "", ErrNotConfigured
}

// StubKYCProvider — sin proveedor KYC/AML conectado.
type StubKYCProvider struct{}

func NewStubKYCProvider() *StubKYCProvider { return &StubKYCProvider{} }

func (p *StubKYCProvider) Name() string { return "stub" }

func (p *StubKYCProvider) CreateVerification(ctx context.Context, userID uuid.UUID) (string, error) {
	return "", ErrNotConfigured
}

func (p *StubKYCProvider) GetVerificationStatus(ctx context.Context, ref string) (domain.KYCVerificationStatus, error) {
	return domain.KYCNotStarted, ErrNotConfigured
}

func (p *StubKYCProvider) HandleWebhook(ctx context.Context, payload []byte) error {
	return ErrNotConfigured
}
