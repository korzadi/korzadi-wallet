package wallet

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/btcwallet"
	"korzadi-wallet/backend/internal/infrastructure/cryptutil"
	"korzadi-wallet/backend/internal/infrastructure/postgres"
)

// ---- fakes ----

// fakeStore simulates postgres.CryptoAddressRepository's real concurrency
// guarantee: CreateGenericAddress is safe under a race for the same
// (user, network) — exactly one row is ever created, and every caller
// (winner or loser) gets that same row back, never an error.
type fakeStore struct {
	mu    sync.Mutex
	byKey map[string]*postgres.CryptoAddress // "userID|network" -> row
}

func newFakeStore() *fakeStore {
	return &fakeStore{byKey: map[string]*postgres.CryptoAddress{}}
}

func key(userID uuid.UUID, network string) string { return userID.String() + "|" + network }

func (f *fakeStore) FindByUserAndNetwork(ctx context.Context, userID uuid.UUID, network string) (*postgres.CryptoAddress, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.byKey[key(userID, network)], nil
}

func (f *fakeStore) ListByUser(ctx context.Context, userID uuid.UUID) ([]*postgres.CryptoAddress, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*postgres.CryptoAddress
	for _, a := range f.byKey {
		if a.UserID == userID {
			out = append(out, a)
		}
	}
	return out, nil
}

func (f *fakeStore) CreateGenericAddress(ctx context.Context, userID uuid.UUID, network, addressType, address, encryptedPrivateKey string) (*postgres.CryptoAddress, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := key(userID, network)
	if existing, ok := f.byKey[k]; ok {
		return existing, nil // simulates losing the creation race
	}
	row := &postgres.CryptoAddress{
		ID: uuid.New(), UserID: userID, Network: network, Address: address,
		AddressType: addressType, EncryptedPrivateKey: encryptedPrivateKey,
		Status: "active", CreatedAt: time.Now(),
	}
	f.byKey[k] = row
	return row, nil
}

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

type fakeAuditLogger struct {
	mu    sync.Mutex
	calls int
}

func (f *fakeAuditLogger) Log(ctx context.Context, cryptoAddressID *uuid.UUID, action, actor string, metadata map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return nil
}

func newTestService() (*AddressService, *fakeStore, *fakeEncryptor) {
	store := newFakeStore()
	enc := &fakeEncryptor{}
	return NewAddressService(store, enc, &fakeAuditLogger{}, btcwallet.Mainnet), store, enc
}

// ---- tests ----

func TestGetOrCreateAddress_CreatesOnFirstCall(t *testing.T) {
	svc, store, enc := newTestService()
	userID := uuid.New()

	addr, err := svc.GetOrCreateAddress(context.Background(), userID, domain.NetworkETHEREUM)
	if err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}
	if addr.Network != domain.NetworkETHEREUM {
		t.Errorf("network = %s, esperaba ETHEREUM", addr.Network)
	}
	if addr.Type != AddressTypeEVM {
		t.Errorf("type = %s, esperaba evm", addr.Type)
	}
	if enc.calls != 1 {
		t.Errorf("Encrypt debe llamarse exactamente una vez, se llamó %d", enc.calls)
	}
	if len(store.byKey) != 1 {
		t.Errorf("filas creadas = %d, esperaba 1", len(store.byKey))
	}
}

func TestGetOrCreateAddress_SecondCallReturnsSameAddress_NeverRegeneratesKey(t *testing.T) {
	svc, _, enc := newTestService()
	userID := uuid.New()

	first, err := svc.GetOrCreateAddress(context.Background(), userID, domain.NetworkBITCOIN)
	if err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}
	second, err := svc.GetOrCreateAddress(context.Background(), userID, domain.NetworkBITCOIN)
	if err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}
	if first.Address != second.Address {
		t.Errorf("direcciones distintas entre llamadas: %s vs %s — una dirección ya asignada nunca debe regenerarse", first.Address, second.Address)
	}
	if enc.calls != 1 {
		t.Errorf("Encrypt debe llamarse solo en la primera llamada (no se genera una clave nueva en la segunda), se llamó %d veces", enc.calls)
	}
}

func TestGetOrCreateAddress_DifferentNetworksGetDifferentAddresses(t *testing.T) {
	svc, _, _ := newTestService()
	userID := uuid.New()

	eth, err := svc.GetOrCreateAddress(context.Background(), userID, domain.NetworkETHEREUM)
	if err != nil {
		t.Fatalf("ETHEREUM: %v", err)
	}
	polygon, err := svc.GetOrCreateAddress(context.Background(), userID, domain.NetworkPOLYGON)
	if err != nil {
		t.Fatalf("POLYGON: %v", err)
	}
	// Bloque 3 decisión: clave independiente por red EVM (no reutilización).
	if eth.Address == polygon.Address {
		t.Error("ETHEREUM y POLYGON no deben compartir la misma dirección con la estrategia elegida (clave independiente por red)")
	}
}

func TestGetOrCreateAddress_RejectsUnsupportedNetwork(t *testing.T) {
	svc, _, _ := newTestService()
	_, err := svc.GetOrCreateAddress(context.Background(), uuid.New(), domain.Network("DOGECOIN"))
	if err == nil {
		t.Fatal("esperaba error para una red no soportada")
	}
}

func TestGetOrCreateAddress_RejectsBSCAndTRON(t *testing.T) {
	// BSC y TRON siguen siendo responsabilidad de application/crypto.Service
	// (Bloque 1) — este servicio no debe pisar esas direcciones ya
	// existentes en producción.
	svc, _, _ := newTestService()
	for _, network := range []domain.Network{domain.NetworkBSC, domain.NetworkTRON} {
		if _, err := svc.GetOrCreateAddress(context.Background(), uuid.New(), network); err == nil {
			t.Errorf("esperaba error para %s — debe seguir siendo manejada por crypto.Service, no por este servicio", network)
		}
	}
}

// TestGetOrCreateAddress_ConcurrentCallsNeverCreateTwoAddresses is the
// Bloque 3 regression test for the spec's explicit concurrency
// requirement: "Request A → index 0, Request B → index 1. Nunca: Request A
// → index 0, Request B → index 0."
func TestGetOrCreateAddress_ConcurrentCallsNeverCreateTwoAddresses(t *testing.T) {
	svc, store, _ := newTestService()
	userID := uuid.New()

	const workers = 20
	var wg sync.WaitGroup
	addresses := make([]string, workers)
	errs := make([]error, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			addr, err := svc.GetOrCreateAddress(context.Background(), userID, domain.NetworkARBITRUM)
			if err == nil {
				addresses[idx] = addr.Address
			}
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("worker %d: no esperaba error: %v", i, err)
		}
	}
	first := addresses[0]
	for i, a := range addresses {
		if a != first {
			t.Errorf("worker %d obtuvo una dirección distinta (%s) a la del worker 0 (%s) — dos requests concurrentes generaron direcciones diferentes para el mismo (usuario, red)", i, a, first)
		}
	}
	if len(store.byKey) != 1 {
		t.Errorf("filas creadas en total = %d, esperaba exactamente 1 pese a %d llamadas concurrentes", len(store.byKey), workers)
	}
}

func TestListAddresses_ReturnsOnlyOwnAddresses(t *testing.T) {
	svc, _, _ := newTestService()
	userA := uuid.New()
	userB := uuid.New()

	if _, err := svc.GetOrCreateAddress(context.Background(), userA, domain.NetworkETHEREUM); err != nil {
		t.Fatalf("userA: %v", err)
	}
	if _, err := svc.GetOrCreateAddress(context.Background(), userB, domain.NetworkETHEREUM); err != nil {
		t.Fatalf("userB: %v", err)
	}

	listA, err := svc.ListAddresses(context.Background(), userA)
	if err != nil {
		t.Fatalf("ListAddresses userA: %v", err)
	}
	if len(listA) != 1 {
		t.Fatalf("direcciones de userA = %d, esperaba 1 — nunca debe ver direcciones de otro usuario", len(listA))
	}
}

func TestSupportedNetworks_DoesNotIncludeTRONOrBSC(t *testing.T) {
	for _, n := range SupportedNetworks() {
		if n == domain.NetworkTRON || n == domain.NetworkBSC {
			t.Errorf("SupportedNetworks() incluye %s, que debe seguir siendo exclusivo de crypto.Service", n)
		}
	}
}
