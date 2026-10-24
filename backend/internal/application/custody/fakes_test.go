package custody

import (
	"context"
	"sync"

	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/cryptutil"
	"korzadi-wallet/backend/internal/infrastructure/postgres"
)

// ---- fakeRepository ----
//
// Simulates the real CustodyWalletRepository's guarantees: address
// uniqueness (postgres.ErrCustodyWalletDuplicateAddress), unknown
// network/asset rejected by "foreign keys" (postgres.ErrCustodyWalletUnknownNetwork/Asset),
// and a compare-and-set UpdateStatus.
type fakeRepository struct {
	mu           sync.Mutex
	byID         map[uuid.UUID]*postgres.CustodyWallet
	byAddress    map[string]uuid.UUID
	knownNetwork map[string]bool
	knownAsset   map[string]bool
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{
		byID:      map[uuid.UUID]*postgres.CustodyWallet{},
		byAddress: map[string]uuid.UUID{},
		knownNetwork: map[string]bool{
			"BSC": true, "TRON": true, "ETHEREUM": true,
		},
		knownAsset: map[string]bool{
			"USDT": true, "BNB": true, "TRX": true,
		},
	}
}

func (f *fakeRepository) Create(ctx context.Context, w *postgres.CustodyWallet) (*postgres.CustodyWallet, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if !f.knownNetwork[w.Network] {
		return nil, postgres.ErrCustodyWalletUnknownNetwork
	}
	if !f.knownAsset[w.AssetCode] {
		return nil, postgres.ErrCustodyWalletUnknownAsset
	}
	if _, exists := f.byAddress[w.Address]; exists {
		return nil, postgres.ErrCustodyWalletDuplicateAddress
	}

	cp := *w
	if cp.ID == uuid.Nil {
		cp.ID = uuid.New()
	}
	if cp.Status == "" {
		cp.Status = "active"
	}
	f.byID[cp.ID] = &cp
	f.byAddress[cp.Address] = cp.ID

	out := cp
	return &out, nil
}

func (f *fakeRepository) Get(ctx context.Context, id uuid.UUID) (*postgres.CustodyWallet, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w, ok := f.byID[id]
	if !ok {
		return nil, nil
	}
	out := *w
	return &out, nil
}

func (f *fakeRepository) List(ctx context.Context, filter postgres.CustodyWalletFilter) ([]*postgres.CustodyWallet, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*postgres.CustodyWallet
	for _, w := range f.byID {
		if filter.Network != nil && w.Network != *filter.Network {
			continue
		}
		if filter.AssetCode != nil && w.AssetCode != *filter.AssetCode {
			continue
		}
		if filter.WalletType != nil && w.WalletType != *filter.WalletType {
			continue
		}
		if filter.Status != nil && w.Status != *filter.Status {
			continue
		}
		cp := *w
		out = append(out, &cp)
	}
	return out, nil
}

func (f *fakeRepository) UpdateStatus(ctx context.Context, id uuid.UUID, expectFrom, to string) (*postgres.CustodyWallet, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w, ok := f.byID[id]
	if !ok || w.Status != expectFrom {
		return nil, nil
	}
	w.Status = to
	out := *w
	return &out, nil
}

// ---- fakeEncryptor ----

type fakeEncryptor struct {
	mu    sync.Mutex
	calls int
}

func (f *fakeEncryptor) Encrypt(plaintext string, _ cryptutil.AAD) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return "enc:" + plaintext, nil
}

// ---- fakeAuditLogger ----

type auditEvent struct {
	CustodyWalletID *uuid.UUID
	Action          string
	Actor           string
	Metadata        map[string]any
}

type fakeAuditLogger struct {
	mu     sync.Mutex
	events []auditEvent
}

func (f *fakeAuditLogger) LogCustodyWallet(ctx context.Context, custodyWalletID *uuid.UUID, action, actor string, metadata map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, auditEvent{CustodyWalletID: custodyWalletID, Action: action, Actor: actor, Metadata: metadata})
	return nil
}

// ---- fakeAssetValidator ----

// fakeAssetValidator simulates network_assets: only pairs explicitly
// enabled here are accepted, independent of fakeRepository's more lenient
// "does the network/asset exist at all" checks.
type fakeAssetValidator struct {
	enabled map[string]bool // "NETWORK|ASSET" -> true
}

func newFakeAssetValidator(pairs ...string) *fakeAssetValidator {
	m := map[string]bool{}
	for _, p := range pairs {
		m[p] = true
	}
	return &fakeAssetValidator{enabled: m}
}

func (f *fakeAssetValidator) IsEnabled(ctx context.Context, network domain.Network, assetCode string) (bool, error) {
	return f.enabled[string(network)+"|"+assetCode], nil
}
