package crypto

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/bscclient"
	"korzadi-wallet/backend/internal/infrastructure/cryptutil"
	"korzadi-wallet/backend/internal/infrastructure/evmwallet"
	"korzadi-wallet/backend/internal/infrastructure/postgres"
	"korzadi-wallet/backend/internal/infrastructure/tronclient"
	"korzadi-wallet/backend/internal/infrastructure/tronwallet"
)

const (
	NetworkTRON  = domain.NetworkTRON
	NetworkBSC   = domain.NetworkBSC
	AssetUSDT    = "USDT"
	tronDecimals = 6
)

// Service now supports multiple networks for the same asset (USDT),
// keeping the original TRON flow completely untouched while adding BSC
// alongside it (spec §1: "La arquitectura sí debe quedar preparada para
// agregarlas posteriormente mediante adapters/providers independientes").
// Ethereum/Bitcoin/Polygon are NOT added here yet, per spec instruction to
// stay scoped to BSC this phase.
type Service struct {
	addresses        *postgres.CryptoAddressRepository
	custodyEncryptor *cryptutil.Encryptor
	custodyAudit     *postgres.CustodyAuditRepository

	tron             tronclient.TRC20BalanceReader // TRON balance reader
	usdtTronContract string

	bscProvider *bscclient.Provider // new
	bscCfg      bscclient.Config
}

func NewService(
	addresses *postgres.CryptoAddressRepository,
	custodyEncryptor *cryptutil.Encryptor,
	custodyAudit *postgres.CustodyAuditRepository,
	tron tronclient.TRC20BalanceReader,
	usdtTronContract string,
	bscProvider *bscclient.Provider, // nil is valid if BSC hasn't been configured yet
	bscCfg bscclient.Config,
) *Service {
	return &Service{
		addresses:        addresses,
		custodyEncryptor: custodyEncryptor,
		custodyAudit:     custodyAudit,
		tron:             tron,
		usdtTronContract: usdtTronContract,
		bscProvider:      bscProvider,
		bscCfg:           bscCfg,
	}
}

// ---- TRON (existing, unchanged behavior) ----

func (s *Service) GetOrCreateDepositAddress(ctx context.Context, userID uuid.UUID) (string, error) {
	return s.getOrCreateTronAddress(ctx, userID)
}

func (s *Service) GetOnChainBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	addr, err := s.addresses.FindByUser(ctx, userID, string(NetworkTRON), AssetUSDT)
	if err != nil {
		return decimal.Zero, err
	}
	if addr == nil {
		return decimal.Zero, nil
	}
	return s.tron.GetTRC20Balance(ctx, addr.Address, s.usdtTronContract, tronDecimals)
}

func (s *Service) getOrCreateTronAddress(ctx context.Context, userID uuid.UUID) (string, error) {
	existing, err := s.addresses.FindByUser(ctx, userID, string(NetworkTRON), AssetUSDT)
	if err != nil {
		return "", fmt.Errorf("consultar dirección existente: %w", err)
	}
	if existing != nil {
		return existing.Address, nil
	}

	keyPair, err := tronwallet.GenerateKeyPair()
	if err != nil {
		return "", fmt.Errorf("generar par de claves: %w", err)
	}

	encryptedKey, err := s.custodyEncryptor.Encrypt(keyPair.PrivateKeyHex, cryptutil.ForCryptoAddress(keyPair.Address))
	if err != nil {
		return "", fmt.Errorf("cifrar clave privada: %w", err)
	}

	created, err := s.addresses.CreateWithNextIndex(ctx, userID, string(NetworkTRON), AssetUSDT, keyPair.Address, encryptedKey)
	if err != nil {
		return "", fmt.Errorf("guardar dirección: %w", err)
	}

	if s.custodyAudit != nil {
		_ = s.custodyAudit.Log(ctx, &created.ID, "key_generated", "system",
			map[string]any{"user_id": userID.String(), "network": NetworkTRON, "asset": AssetUSDT})
	}

	return created.Address, nil
}

// ---- BSC / USDT BEP-20 (new) ----

// ErrBSCNotConfigured is returned when BSC endpoints are hit before
// BSC_RPC_URL/USDT_BSC_CONTRACT etc. are configured. This lets the rest of
// the app boot even if BSC isn't set up yet, rather than crashing startup —
// but every BSC-specific call fails loudly and explicitly instead of
// silently falling back to TRON or returning a zero balance.
var ErrBSCNotConfigured = fmt.Errorf("crypto: BSC no está configurado (falta BSC_RPC_URL / USDT_BSC_CONTRACT)")

func (s *Service) GetOrCreateBSCDepositAddress(ctx context.Context, userID uuid.UUID) (string, error) {
	if s.bscProvider == nil {
		return "", ErrBSCNotConfigured
	}

	existing, err := s.addresses.FindByUser(ctx, userID, string(NetworkBSC), AssetUSDT)
	if err != nil {
		return "", fmt.Errorf("consultar dirección BSC existente: %w", err)
	}
	if existing != nil {
		return existing.Address, nil
	}

	keyPair, err := evmwallet.GenerateKeyPair()
	if err != nil {
		return "", fmt.Errorf("generar par de claves EVM: %w", err)
	}

	encryptedKey, err := s.custodyEncryptor.Encrypt(keyPair.PrivateKeyHex, cryptutil.ForCryptoAddress(keyPair.Address))
	if err != nil {
		return "", fmt.Errorf("cifrar clave privada: %w", err)
	}

	created, err := s.addresses.CreateWithNextIndex(ctx, userID, string(NetworkBSC), AssetUSDT, keyPair.Address, encryptedKey)
	if err != nil {
		return "", fmt.Errorf("guardar dirección BSC: %w", err)
	}

	if s.custodyAudit != nil {
		_ = s.custodyAudit.Log(ctx, &created.ID, "key_generated", "system",
			map[string]any{"user_id": userID.String(), "network": NetworkBSC, "asset": AssetUSDT})
	}

	return created.Address, nil
}

func (s *Service) GetBSCOnChainBalance(ctx context.Context, userID uuid.UUID) (decimal.Decimal, error) {
	if s.bscProvider == nil {
		return decimal.Zero, ErrBSCNotConfigured
	}
	addr, err := s.addresses.FindByUser(ctx, userID, string(NetworkBSC), AssetUSDT)
	if err != nil {
		return decimal.Zero, err
	}
	if addr == nil {
		return decimal.Zero, nil
	}
	return s.bscProvider.TokenBalance(ctx, addr.Address, s.bscCfg.USDTContractAddress, int32(s.bscCfg.USDTDecimals))
}

// BSCHealth exposes the provider health check for the /health/blockchain/bsc
// endpoint (spec §4).
func (s *Service) BSCHealth(ctx context.Context) (domain.ProviderHealth, error) {
	if s.bscProvider == nil {
		return domain.ProviderHealth{Network: NetworkBSC, Err: ErrBSCNotConfigured.Error()}, ErrBSCNotConfigured
	}
	return s.bscProvider.HealthCheck(ctx, s.bscCfg.USDTContractAddress)
}
