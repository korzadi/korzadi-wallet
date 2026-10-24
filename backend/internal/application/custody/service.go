package custody

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/cryptutil"
	"korzadi-wallet/backend/internal/infrastructure/postgres"
)

// Service is the application-layer entry point for Bloque 7.1. It only
// registers, reads, lists and changes the status of custody wallets — it
// deliberately does NOT move funds, sign transactions, or talk to any
// blockchain client (that belongs to 7.2+).
type Service struct {
	repo      Repository
	encryptor Encryptor
	audit     AuditLogger           // may be nil: audit logging is best-effort, never blocking
	assets    NetworkAssetValidator // may be nil: see ports.go
}

// NewService wires the service. audit and assets are optional (nil is
// valid); repo and encryptor are required.
func NewService(repo Repository, encryptor Encryptor, audit AuditLogger, assets NetworkAssetValidator) *Service {
	return &Service{repo: repo, encryptor: encryptor, audit: audit, assets: assets}
}

// Register validates and persists a new custody wallet. The private key
// is encrypted here, with the SAME encryptor the rest of custody
// (application/crypto.Service, application/wallet.AddressService) already
// uses — never a second encryption mechanism.
func (s *Service) Register(ctx context.Context, in RegisterInput) (*CustodyWallet, error) {
	if err := ValidateWalletType(in.WalletType); err != nil {
		return nil, err
	}
	if err := ValidateAddressType(in.AddressType); err != nil {
		return nil, err
	}
	network := domain.Network(strings.TrimSpace(string(in.Network)))
	if network == "" {
		return nil, fmt.Errorf("%w: red vacía", ErrUnknownNetwork)
	}
	assetCode := strings.TrimSpace(in.AssetCode)
	if assetCode == "" {
		return nil, fmt.Errorf("%w: asset vacío", ErrUnknownAsset)
	}
	canonicalAddress, err := ValidateAddress(in.AddressType, in.Address)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.PrivateKeyPlaintext) == "" {
		return nil, ErrEmptyPrivateKey
	}

	if s.assets != nil {
		enabled, err := s.assets.IsEnabled(ctx, network, assetCode)
		if err != nil {
			return nil, fmt.Errorf("custody: validar asset/red: %w", err)
		}
		if !enabled {
			return nil, fmt.Errorf("%w: %s/%s", ErrAssetNotEnabledOnNetwork, network, assetCode)
		}
	}

	encrypted, err := s.encryptor.Encrypt(in.PrivateKeyPlaintext, cryptutil.ForCustodyWallet(canonicalAddress))
	if err != nil {
		return nil, fmt.Errorf("custody: cifrar clave privada: %w", err)
	}

	row := &postgres.CustodyWallet{
		Network:             string(network),
		AssetCode:           assetCode,
		WalletType:          string(in.WalletType),
		Address:             canonicalAddress,
		AddressType:         in.AddressType,
		EncryptedPrivateKey: encrypted,
		Status:              string(StatusActive),
	}

	created, err := s.repo.Create(ctx, row)
	if err != nil {
		switch {
		case errors.Is(err, postgres.ErrCustodyWalletDuplicateAddress):
			return nil, fmt.Errorf("%w: %s", ErrDuplicateAddress, canonicalAddress)
		case errors.Is(err, postgres.ErrCustodyWalletUnknownNetwork):
			return nil, fmt.Errorf("%w: %s", ErrUnknownNetwork, network)
		case errors.Is(err, postgres.ErrCustodyWalletUnknownAsset):
			return nil, fmt.Errorf("%w: %s", ErrUnknownAsset, assetCode)
		default:
			return nil, fmt.Errorf("custody: crear custody wallet: %w", err)
		}
	}

	// Best-effort: a failed audit write never rolls back a successful
	// registration, and never logs the plaintext or encrypted key.
	if s.audit != nil {
		_ = s.audit.LogCustodyWallet(ctx, &created.ID, "custody_wallet_created", actorOrSystem(in.Actor), map[string]any{
			"network":     created.Network,
			"asset_code":  created.AssetCode,
			"wallet_type": created.WalletType,
			"address":     created.Address,
		})
	}

	return toPublic(created), nil
}

// Get returns ErrNotFound (not nil, nil) when the id doesn't exist, so
// callers get a single error-handling path.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*CustodyWallet, error) {
	row, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("custody: obtener custody wallet: %w", err)
	}
	if row == nil {
		return nil, ErrNotFound
	}
	return toPublic(row), nil
}

// List returns custody wallets matching f, in creation order.
func (s *Service) List(ctx context.Context, f ListFilter) ([]*CustodyWallet, error) {
	pf := postgres.CustodyWalletFilter{AssetCode: f.AssetCode}
	if f.Network != nil {
		n := string(*f.Network)
		pf.Network = &n
	}
	if f.WalletType != nil {
		wt := string(*f.WalletType)
		pf.WalletType = &wt
	}
	if f.Status != nil {
		st := string(*f.Status)
		pf.Status = &st
	}

	rows, err := s.repo.List(ctx, pf)
	if err != nil {
		return nil, fmt.Errorf("custody: listar custody wallets: %w", err)
	}
	out := make([]*CustodyWallet, 0, len(rows))
	for _, r := range rows {
		out = append(out, toPublic(r))
	}
	return out, nil
}

// allowedTransitions encodes the Bloque 7.1 status state machine:
//   - ACTIVE  <-> DISABLED: reversible, an operator can pause and resume
//     a wallet.
//   - ACTIVE  ->  RETIRED, DISABLED -> RETIRED: one-way.
//   - RETIRED is terminal: "una wallet RETIRED no debe volver a activarse
//     para nuevas operaciones" (spec). The only allowed "transition" out
//     of RETIRED is the identity one (idempotent no-op).
//
// Whether a DISABLED/RETIRED wallet can be USED for new operations is
// enforced by callers of this registry (7.2+ checking Status == active),
// not by this state machine — this block only governs which status
// changes are legal to make.
var allowedTransitions = map[Status]map[Status]bool{
	StatusActive:   {StatusActive: true, StatusDisabled: true, StatusRetired: true},
	StatusDisabled: {StatusDisabled: true, StatusActive: true, StatusRetired: true},
	StatusRetired:  {StatusRetired: true},
}

// UpdateStatus transitions a custody wallet's status, enforcing the state
// machine above and a compare-and-set at the database level (so two
// concurrent status changes never race silently — see
// CustodyWalletRepository.UpdateStatus).
func (s *Service) UpdateStatus(ctx context.Context, id uuid.UUID, newStatus Status, actor string) (*CustodyWallet, error) {
	if err := ValidateStatus(newStatus); err != nil {
		return nil, err
	}

	current, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("custody: obtener custody wallet: %w", err)
	}
	if current == nil {
		return nil, ErrNotFound
	}

	from := Status(current.Status)
	if !allowedTransitions[from][newStatus] {
		return nil, fmt.Errorf("%w: %s -> %s", ErrInvalidStatusTransition, from, newStatus)
	}

	// No-op: already in the target status. Avoids a pointless UPDATE and,
	// more importantly, avoids ever emitting a fake "status_changed"
	// audit event for a status that didn't actually change.
	if from == newStatus {
		return toPublic(current), nil
	}

	updated, err := s.repo.UpdateStatus(ctx, id, string(from), string(newStatus))
	if err != nil {
		return nil, fmt.Errorf("custody: actualizar status: %w", err)
	}
	if updated == nil {
		// The status changed between Get and UpdateStatus (e.g. a
		// concurrent request retired it first). Deliberately NOT
		// retried automatically: silently retrying could, depending on
		// timing, attempt to move a now-RETIRED wallet again.
		return nil, ErrConcurrentStatusChange
	}

	if s.audit != nil {
		_ = s.audit.LogCustodyWallet(ctx, &updated.ID, "custody_wallet_status_changed", actorOrSystem(actor), map[string]any{
			"from": string(from),
			"to":   string(newStatus),
		})
	}

	return toPublic(updated), nil
}

func actorOrSystem(actor string) string {
	if strings.TrimSpace(actor) == "" {
		return "system"
	}
	return actor
}

func toPublic(w *postgres.CustodyWallet) *CustodyWallet {
	return &CustodyWallet{
		ID:          w.ID,
		Network:     domain.Network(w.Network),
		AssetCode:   w.AssetCode,
		WalletType:  WalletType(w.WalletType),
		Address:     w.Address,
		AddressType: w.AddressType,
		Status:      Status(w.Status),
		CreatedAt:   w.CreatedAt,
		UpdatedAt:   w.UpdatedAt,
	}
}
