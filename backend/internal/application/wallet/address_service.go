// Package wallet is the Application-layer home for wallet/address
// infrastructure (Bloque 3) — the seam between "a user wants a deposit
// address" and the network-specific key generation packages
// (evmwallet, btcwallet, tronwallet) plus the address registry
// (postgres.CryptoAddressRepository).
//
// This is deliberately separate from internal/application/crypto.Service
// (Bloque 1), which keeps its existing TRON/BSC-specific methods
// untouched — this package adds the generic path for every other network
// (Ethereum, Polygon, Arbitrum, Optimism, Base, Bitcoin) without touching
// that already-working code. A future cleanup could fold crypto.Service's
// TRON/BSC logic into this package, but that's a refactor with no
// functional payoff for this block, so it isn't done here (spec: "no
// romper Bloque 1").
package wallet

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/btcwallet"
	"korzadi-wallet/backend/internal/infrastructure/cryptutil"
	"korzadi-wallet/backend/internal/infrastructure/evmwallet"
	"korzadi-wallet/backend/internal/infrastructure/postgres"
)

// AddressType mirrors the crypto_addresses.address_type column — exported
// here so callers outside infrastructure/postgres don't need to know the
// DB's string literals.
type AddressType string

const (
	AddressTypeEVM     AddressType = "evm"
	AddressTypeBitcoin AddressType = "bitcoin_segwit"
	AddressTypeTRON    AddressType = "tron"
)

// Address is the public-facing view of a crypto_addresses row — it
// deliberately excludes EncryptedPrivateKey, DerivationIndex, and the
// internal UserID (spec §"PRIVACY": "no exponer innecesariamente
// información interna como... derivation path completo... metadata de
// custodia"). ID is included since it's an opaque reference, not
// sensitive metadata.
type Address struct {
	ID        uuid.UUID
	Network   domain.Network
	Address   string
	Type      AddressType
	Status    string
	CreatedAt string
}

// supportedNetworks is the single place that says which networks this
// service knows how to generate an address for, and which key-generation
// family each belongs to. Adding a network this service doesn't already
// support (were Korzadi to add a 7th chain later) means adding one line
// here — not a new service, not a new handler.
var supportedNetworks = map[domain.Network]AddressType{
	domain.NetworkETHEREUM: AddressTypeEVM,
	domain.NetworkPOLYGON:  AddressTypeEVM,
	domain.NetworkARBITRUM: AddressTypeEVM,
	domain.NetworkOPTIMISM: AddressTypeEVM,
	domain.NetworkBASE:     AddressTypeEVM,
	domain.NetworkBITCOIN:  AddressTypeBitcoin,
	// NetworkBSC and NetworkTRON are intentionally absent: they already
	// have working, network-specific flows in application/crypto.Service
	// (Bloque 1) with real users and encrypted keys in production — this
	// service does not duplicate or take over those two networks.
}

// ErrUnsupportedNetwork is returned for any network this service doesn't
// know how to generate an address for — either because it isn't a real
// network, or because it's TRON/BSC (still served by crypto.Service).
var ErrUnsupportedNetwork = fmt.Errorf("wallet: red no soportada por este servicio de direcciones")

// AddressStore is the narrow persistence port this service needs — exactly
// the three postgres.CryptoAddressRepository methods relevant to
// multi-network address issuance, named here so this service can be unit
// tested against a fake instead of a real Postgres instance.
type AddressStore interface {
	FindByUserAndNetwork(ctx context.Context, userID uuid.UUID, network string) (*postgres.CryptoAddress, error)
	ListByUser(ctx context.Context, userID uuid.UUID) ([]*postgres.CryptoAddress, error)
	CreateGenericAddress(ctx context.Context, userID uuid.UUID, network, addressType, address, encryptedPrivateKey string) (*postgres.CryptoAddress, error)
}

// Encryptor is the narrow custody port — exactly cryptutil.Encryptor's
// Encrypt method, the only one this service calls.
type Encryptor interface {
	Encrypt(plaintext string, aad cryptutil.AAD) (string, error)
}

// AuditLogger is the narrow custody-audit port.
type AuditLogger interface {
	Log(ctx context.Context, cryptoAddressID *uuid.UUID, action, actor string, metadata map[string]any) error
}

type AddressService struct {
	addresses AddressStore
	encryptor Encryptor
	audit     AuditLogger
	btcNet    btcwallet.Network
}

// btcNetwork lets the deployment choose mainnet vs testnet Bitcoin without
// a code change — mirrors BTC_NETWORK from btcclient's config (Bloque 1),
// read once at construction time in main.go, not re-read per call.
func NewAddressService(
	addresses AddressStore,
	encryptor Encryptor,
	audit AuditLogger,
	btcNetwork btcwallet.Network,
) *AddressService {
	return &AddressService{addresses: addresses, encryptor: encryptor, audit: audit, btcNet: btcNetwork}
}

// GetOrCreateAddress returns the user's existing address for `network`, or
// generates and persists a new one if none exists yet. Safe under
// concurrency: two simultaneous calls for the same (user, network) never
// produce two different addresses — see
// postgres.CryptoAddressRepository.CreateGenericAddress's doc comment.
func (s *AddressService) GetOrCreateAddress(ctx context.Context, userID uuid.UUID, network domain.Network) (*Address, error) {
	addrType, ok := supportedNetworks[network]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedNetwork, network)
	}

	existing, err := s.addresses.FindByUserAndNetwork(ctx, userID, string(network))
	if err != nil {
		return nil, fmt.Errorf("wallet: consultar dirección existente: %w", err)
	}
	if existing != nil {
		return toPublicAddress(existing), nil
	}

	address, privateKeyHex, err := s.generateKeyMaterial(addrType)
	if err != nil {
		return nil, err
	}

	encryptedKey, err := s.encryptor.Encrypt(privateKeyHex, cryptutil.ForCryptoAddress(address))
	if err != nil {
		return nil, fmt.Errorf("wallet: cifrar clave privada: %w", err)
	}

	created, err := s.addresses.CreateGenericAddress(ctx, userID, string(network), string(addrType), address, encryptedKey)
	if err != nil {
		return nil, fmt.Errorf("wallet: guardar dirección: %w", err)
	}

	// Log only that a key was generated — never the address's private
	// material. If CreateGenericAddress returned an existing row because
	// this call lost a concurrent creation race, this log entry is
	// slightly imprecise (says "generated" when it was actually
	// discarded) but harmless: it's an audit trail of intent, not of the
	// exact winning row, and custody_audit_logs already ties back to the
	// concrete crypto_address_id for reconciliation.
	if s.audit != nil {
		_ = s.audit.Log(ctx, &created.ID, "key_generated", "system",
			map[string]any{"user_id": userID.String(), "network": string(network), "address_type": string(addrType)})
	}

	return toPublicAddress(created), nil
}

// ListAddresses returns every address a user owns, across every network —
// the backing call for GET /wallet/addresses.
func (s *AddressService) ListAddresses(ctx context.Context, userID uuid.UUID) ([]Address, error) {
	rows, err := s.addresses.ListByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("wallet: listar direcciones: %w", err)
	}
	out := make([]Address, 0, len(rows))
	for _, r := range rows {
		out = append(out, *toPublicAddress(r))
	}
	return out, nil
}

// SupportedNetworks returns the networks this service can create an
// address for — used by the handler to validate input and to advertise
// what's available without hardcoding the list twice.
func SupportedNetworks() []domain.Network {
	out := make([]domain.Network, 0, len(supportedNetworks))
	for n := range supportedNetworks {
		out = append(out, n)
	}
	return out
}

func (s *AddressService) generateKeyMaterial(addrType AddressType) (address string, privateKeyHex string, err error) {
	switch addrType {
	case AddressTypeEVM:
		kp, err := evmwallet.GenerateKeyPair()
		if err != nil {
			return "", "", fmt.Errorf("wallet: generar par de claves EVM: %w", err)
		}
		return kp.Address, kp.PrivateKeyHex, nil
	case AddressTypeBitcoin:
		kp, err := btcwallet.GenerateKeyPair(s.btcNet)
		if err != nil {
			return "", "", fmt.Errorf("wallet: generar par de claves Bitcoin: %w", err)
		}
		return kp.Address, kp.PrivateKeyHex, nil
	default:
		// Unreachable given supportedNetworks only maps to these two
		// values, but explicit rather than a silent zero-value return.
		return "", "", fmt.Errorf("wallet: tipo de dirección sin generador: %s", addrType)
	}
}

func toPublicAddress(a *postgres.CryptoAddress) *Address {
	return &Address{
		ID:        a.ID,
		Network:   domain.Network(a.Network),
		Address:   a.Address,
		Type:      AddressType(a.AddressType),
		Status:    a.Status,
		CreatedAt: a.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}
