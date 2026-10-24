package gasstation

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/cryptutil"
	"korzadi-wallet/backend/internal/infrastructure/evmwallet"
	"korzadi-wallet/backend/internal/infrastructure/postgres"
)

const testNetwork = domain.NetworkBSC

// ---- fakes ----

type fakeWallets struct {
	mu   sync.Mutex
	rows map[string]*postgres.CustodyWallet // key: walletType|network|assetCode
}

func newFakeWallets() *fakeWallets               { return &fakeWallets{rows: map[string]*postgres.CustodyWallet{}} }
func (f *fakeWallets) key(t, n, a string) string { return t + "|" + n + "|" + a }
func (f *fakeWallets) add(w *postgres.CustodyWallet) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[f.key(w.WalletType, w.Network, w.AssetCode)] = w
}
func (f *fakeWallets) FindActive(ctx context.Context, walletType, network, assetCode string) (*postgres.CustodyWallet, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w, ok := f.rows[f.key(walletType, network, assetCode)]
	if !ok || w.Status != "active" {
		return nil, nil
	}
	return w, nil
}

type fakePolicies struct {
	mu   sync.Mutex
	rows map[uuid.UUID]*postgres.GasStationPolicy
}

func newFakePolicies() *fakePolicies {
	return &fakePolicies{rows: map[uuid.UUID]*postgres.GasStationPolicy{}}
}
func (f *fakePolicies) set(p *postgres.GasStationPolicy) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[p.CustodyWalletID] = p
}
func (f *fakePolicies) Get(ctx context.Context, custodyWalletID uuid.UUID) (*postgres.GasStationPolicy, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rows[custodyWalletID], nil
}

// fakeRefills reproduces, in memory, the exact two uniqueness rules the
// real migration (0010_gas_station.up.sql) enforces in Postgres — this is
// what lets the Concurrency test below exercise the real dedup logic
// without a database.
type fakeRefills struct {
	mu   sync.Mutex
	rows map[uuid.UUID]*postgres.GasStationRefill
}

func newFakeRefills() *fakeRefills {
	return &fakeRefills{rows: map[uuid.UUID]*postgres.GasStationRefill{}}
}

func (f *fakeRefills) FindOwned(ctx context.Context, network, targetAddress, ownerRef string) (*postgres.GasStationRefill, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.rows {
		if r.Network == network && strings.EqualFold(r.TargetAddress, targetAddress) && r.OwnerRef == ownerRef && r.Status != postgres.RefillStatusFailed {
			return r, nil
		}
	}
	return nil, nil
}

func (f *fakeRefills) FindInFlight(ctx context.Context, network, targetAddress string) (*postgres.GasStationRefill, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.rows {
		if r.Network == network && strings.EqualFold(r.TargetAddress, targetAddress) &&
			(r.Status == postgres.RefillStatusPending || r.Status == postgres.RefillStatusBroadcasted) {
			return r, nil
		}
	}
	return nil, nil
}

func (f *fakeRefills) Create(ctx context.Context, rf postgres.GasStationRefill) (*postgres.GasStationRefill, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.rows {
		if r.Network == rf.Network && strings.EqualFold(r.TargetAddress, rf.TargetAddress) &&
			(r.Status == postgres.RefillStatusPending || r.Status == postgres.RefillStatusBroadcasted) {
			return nil, postgres.ErrRefillInFlight
		}
	}
	rf.ID = uuid.New()
	rf.Status = postgres.RefillStatusPending
	rf.CreatedAt, rf.UpdatedAt = time.Now(), time.Now()
	row := rf
	f.rows[row.ID] = &row
	cp := row
	return &cp, nil
}

func (f *fakeRefills) MarkBroadcasted(ctx context.Context, id uuid.UUID, txHash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.rows[id]; ok && r.Status == postgres.RefillStatusPending {
		r.Status = postgres.RefillStatusBroadcasted
		r.TxHash = txHash
		r.UpdatedAt = time.Now()
	}
	return nil
}
func (f *fakeRefills) MarkConfirmed(ctx context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.rows[id]; ok {
		r.Status = postgres.RefillStatusConfirmed
	}
	return nil
}
func (f *fakeRefills) MarkFailed(ctx context.Context, id uuid.UUID, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.rows[id]; ok {
		r.Status = postgres.RefillStatusFailed
		r.ErrorDetail = reason
	}
	return nil
}
func (f *fakeRefills) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.rows)
}
func (f *fakeRefills) nonFailedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.rows {
		if r.Status != postgres.RefillStatusFailed {
			n++
		}
	}
	return n
}

type fakeNonces struct {
	mu   sync.Mutex
	next map[string]uint64
	log  []string
}

func newFakeNonces() *fakeNonces { return &fakeNonces{next: map[string]uint64{}} }
func (f *fakeNonces) Allocate(ctx context.Context, network domain.Network, address, owner string, chainPending uint64) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := string(network) + "|" + address
	n := f.next[key]
	if chainPending > n {
		n = chainPending
	}
	f.next[key] = n + 1
	f.log = append(f.log, fmt.Sprintf("allocate:%s:%d", owner, n))
	return n, nil
}
func (f *fakeNonces) MarkSigned(ctx context.Context, network domain.Network, address, owner, txHash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log = append(f.log, "signed:"+owner)
	return nil
}
func (f *fakeNonces) MarkConfirmed(ctx context.Context, network domain.Network, address, owner string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log = append(f.log, "confirmed:"+owner)
	return nil
}
func (f *fakeNonces) Release(ctx context.Context, network domain.Network, address, owner string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log = append(f.log, "released:"+owner)
	return nil
}

// fakeChain simulates one EVM RPC endpoint: balances live in `balances`
// (mutable — a successful broadcast in these tests can bump the target's
// balance, the same way a real chain eventually would), and broadcast
// behavior is scripted via broadcastErr/receiptStatus so each test can
// force success / rejected / ambiguous / already-known deterministically.
type fakeChain struct {
	mu            sync.Mutex
	balances      map[string]*big.Int
	broadcastErr  error
	broadcastHash string
	receiptReady  bool
	receiptOK     bool
	pendingNonce  uint64
}

func newFakeChain() *fakeChain {
	return &fakeChain{balances: map[string]*big.Int{}, receiptReady: true, receiptOK: true}
}
func (c *fakeChain) setBalance(addr string, wei *big.Int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.balances[strings.ToLower(addr)] = wei
}
func (c *fakeChain) ChainID() *big.Int { return big.NewInt(97) }
func (c *fakeChain) PendingNonce(ctx context.Context, address string) (uint64, error) {
	return c.pendingNonce, nil
}
func (c *fakeChain) NativeBalanceWei(ctx context.Context, address string) (*big.Int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, ok := c.balances[strings.ToLower(address)]
	if !ok {
		return big.NewInt(0), nil
	}
	return new(big.Int).Set(b), nil
}
func (c *fakeChain) SuggestGasPrice(ctx context.Context) (*big.Int, error) {
	return big.NewInt(5_000_000_000), nil
}
func (c *fakeChain) Broadcast(ctx context.Context, signedRawTx []byte) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.broadcastErr != nil {
		return "", c.broadcastErr
	}
	return c.broadcastHash, nil
}
func (c *fakeChain) Receipt(ctx context.Context, txHash string) (*domain.TransactionReceipt, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.receiptReady {
		return nil, nil
	}
	return &domain.TransactionReceipt{TxHash: txHash, Status: c.receiptOK, Confirmations: 3}, nil
}
func (c *fakeChain) TxKnown(ctx context.Context, txHash string) (bool, error) { return true, nil }

type fakeAssets struct{ assets []domain.NetworkAsset }

func (f *fakeAssets) ListEnabled(ctx context.Context, network domain.Network) ([]domain.NetworkAsset, error) {
	return f.assets, nil
}
func (f *fakeAssets) Get(ctx context.Context, network domain.Network, assetCode string) (domain.NetworkAsset, bool, error) {
	for _, a := range f.assets {
		if a.AssetCode == assetCode {
			return a, true, nil
		}
	}
	return domain.NetworkAsset{}, false, nil
}

type fakeDecryptor struct{ plaintext string }

func (f *fakeDecryptor) Decrypt(_ context.Context, ciphertext string, _ cryptutil.AAD) (string, error) {
	if ciphertext == "" {
		return "", errors.New("empty ciphertext")
	}
	return f.plaintext, nil
}

type fakeAudit struct {
	mu     sync.Mutex
	events []string
	blobs  []map[string]any
}

func (f *fakeAudit) LogCustodyWallet(ctx context.Context, custodyWalletID *uuid.UUID, action, actor string, metadata map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, action)
	f.blobs = append(f.blobs, metadata)
	return nil
}

// ---- harness ----

type harness struct {
	svc      *Service
	wallets  *fakeWallets
	policies *fakePolicies
	refills  *fakeRefills
	nonces   *fakeNonces
	chain    *fakeChain
	audit    *fakeAudit
	dec      *fakeDecryptor
	gasAddr  string
	walletID uuid.UUID
}

func newHarness(t *testing.T, minimum, target, maxRefill string, enabled bool) *harness {
	t.Helper()
	kp, err := evmwallet.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generar key pair de prueba: %v", err)
	}

	h := &harness{
		wallets: newFakeWallets(), policies: newFakePolicies(), refills: newFakeRefills(),
		nonces: newFakeNonces(), chain: newFakeChain(), audit: &fakeAudit{},
		dec: &fakeDecryptor{plaintext: kp.PrivateKeyHex}, gasAddr: kp.Address,
		walletID: uuid.New(),
	}
	h.chain.broadcastHash = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	wallet := &postgres.CustodyWallet{
		ID: h.walletID, Network: string(testNetwork), AssetCode: "BNB", WalletType: "GAS",
		Address: h.gasAddr, AddressType: "evm", EncryptedPrivateKey: "encrypted:" + kp.PrivateKeyHex, Status: "active",
	}
	h.wallets.add(wallet)
	h.policies.set(&postgres.GasStationPolicy{
		CustodyWalletID: h.walletID,
		MinimumBalance:  decimal.RequireFromString(minimum),
		TargetBalance:   decimal.RequireFromString(target),
		MaxRefill:       decimal.RequireFromString(maxRefill),
		Enabled:         enabled,
	})

	assets := &fakeAssets{assets: []domain.NetworkAsset{{Network: testNetwork, AssetCode: "BNB", Kind: domain.AssetKindNative, Decimals: 18, Enabled: true}}}

	svc, err := NewService(Deps{
		Network: testNetwork, Wallets: h.wallets, Policies: h.policies, Refills: h.refills,
		Nonces: h.nonces, Chain: h.chain, Assets: assets, Decryptor: h.dec, Audit: h.audit,
		Now:   time.Now,
		Sleep: func(ctx context.Context, d time.Duration) error { return nil }, // instant in tests
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	h.svc = svc
	return h
}

func bnb(s string) *big.Int {
	d := decimal.RequireFromString(s)
	return d.Shift(18).BigInt()
}

const target1 = "0x1111111111111111111111111111111111111111"

// ---- Networks: only EVM networks are accepted ----

func TestNewService_RejectsNonEVMNetwork(t *testing.T) {
	_, err := NewService(Deps{Network: domain.NetworkTRON})
	if err == nil {
		t.Fatal("se esperaba error para una red no-EVM (TRON)")
	}
}

// ---- Registry: resolves the correct GAS wallet, never crypto_addresses ----

func TestEnsureGasFor_NoGasWallet(t *testing.T) {
	h := newHarness(t, "0.01", "0.05", "0.05", true)
	h.wallets.rows = map[string]*postgres.CustodyWallet{} // remove it
	err := h.svc.EnsureGasFor(context.Background(), "gas:w1", target1, bnb("0.01"))
	if !errors.Is(err, ErrNoGasWallet) {
		t.Fatalf("esperaba ErrNoGasWallet, obtuve %v", err)
	}
}

// ---- Balance / Threshold ----

func TestEnsureGasFor_SufficientBalance_NoRefill(t *testing.T) {
	h := newHarness(t, "0.01", "0.05", "0.05", true)
	h.chain.setBalance(target1, bnb("0.02")) // above minimum and above required
	if err := h.svc.EnsureGasFor(context.Background(), "gas:w1", target1, bnb("0.005")); err != nil {
		t.Fatalf("no debería refillear: %v", err)
	}
	if h.refills.count() != 0 {
		t.Fatalf("no debió crearse ninguna fila de refill, hay %d", h.refills.count())
	}
}

func TestEnsureGasFor_BelowMinimum_Refills(t *testing.T) {
	h := newHarness(t, "0.01", "0.05", "0.05", true)
	h.chain.setBalance(target1, bnb("0.001")) // below minimum
	if err := h.svc.EnsureGasFor(context.Background(), "gas:w1", target1, bnb("0.0005")); err != nil {
		t.Fatalf("EnsureGasFor: %v", err)
	}
	if h.refills.count() != 1 {
		t.Fatalf("esperaba exactamente 1 fila de refill, hay %d", h.refills.count())
	}
}

func TestEnsureGasFor_BelowRequired_Refills(t *testing.T) {
	// balance is ABOVE minimum but still below what THIS operation needs.
	h := newHarness(t, "0.001", "0.05", "0.05", true)
	h.chain.setBalance(target1, bnb("0.01"))
	if err := h.svc.EnsureGasFor(context.Background(), "gas:w1", target1, bnb("0.02")); err != nil {
		t.Fatalf("EnsureGasFor: %v", err)
	}
	if h.refills.count() != 1 {
		t.Fatalf("esperaba 1 refill, hay %d", h.refills.count())
	}
}

// ---- Limits: refill never exceeds target_balance or max_refill ----

func TestPlanRefill_NeverExceedsMaxRefill(t *testing.T) {
	current := bnb("0.0")
	required := bnb("0.001")
	targetWei := bnb("1.0")  // huge target
	maxRefill := bnb("0.02") // small cap
	plan, err := planRefill(current, required, targetWei, maxRefill)
	if err != nil {
		t.Fatalf("planRefill: %v", err)
	}
	if plan.amountWei.Cmp(maxRefill) > 0 {
		t.Fatalf("el plan (%s) excede max_refill (%s)", plan.amountWei, maxRefill)
	}
}

func TestPlanRefill_NeverExceedsTarget(t *testing.T) {
	current := bnb("0.0")
	required := bnb("0.001") // small requirement
	targetWei := bnb("0.02")
	maxRefill := bnb("1.0") // cap is not the binding constraint here
	plan, err := planRefill(current, required, targetWei, maxRefill)
	if err != nil {
		t.Fatalf("planRefill: %v", err)
	}
	newBalance := new(big.Int).Add(current, plan.amountWei)
	if newBalance.Cmp(targetWei) > 0 {
		t.Fatalf("el balance resultante (%s) excede target_balance (%s)", newBalance, targetWei)
	}
}

func TestPlanRefill_RefusesWhenCapIsInsufficient(t *testing.T) {
	current := bnb("0.0")
	required := bnb("0.05") // the operation needs more than the policy allows
	targetWei := bnb("0.02")
	maxRefill := bnb("0.02")
	_, err := planRefill(current, required, targetWei, maxRefill)
	if !errors.Is(err, ErrPolicyExceeded) {
		t.Fatalf("esperaba ErrPolicyExceeded, obtuve %v", err)
	}
}

// ---- Signing / Nonce ----

func TestEnsureGasFor_SignsAndAllocatesNonce(t *testing.T) {
	h := newHarness(t, "0.01", "0.05", "0.05", true)
	h.chain.setBalance(target1, bnb("0.0"))
	if err := h.svc.EnsureGasFor(context.Background(), "gas:w42", target1, bnb("0.01")); err != nil {
		t.Fatalf("EnsureGasFor: %v", err)
	}
	foundAllocate, foundSigned, foundConfirmed := false, false, false
	for _, l := range h.nonces.log {
		if strings.Contains(l, "allocate:gas:w42") {
			foundAllocate = true
		}
		if l == "signed:gas:w42" {
			foundSigned = true
		}
		if l == "confirmed:gas:w42" {
			foundConfirmed = true
		}
	}
	if !foundAllocate || !foundSigned || !foundConfirmed {
		t.Fatalf("secuencia de nonce incompleta: %v", h.nonces.log)
	}
}

// ---- Security: the private key never appears in any returned error ----

func TestEnsureGasFor_NeverLeaksPrivateKey(t *testing.T) {
	h := newHarness(t, "0.01", "0.05", "0.05", true)
	h.chain.setBalance(target1, bnb("0.0"))
	h.chain.broadcastErr = errors.New("intrinsic gas too low") // force a definitive-rejection path
	err := h.svc.EnsureGasFor(context.Background(), "gas:leak", target1, bnb("0.01"))
	if err == nil {
		t.Fatal("se esperaba un error (rechazo definitivo)")
	}
	if strings.Contains(err.Error(), h.dec.plaintext) {
		t.Fatalf("¡la clave privada apareció en el error!: %v", err)
	}
	for _, blob := range h.audit.blobs {
		for k, v := range blob {
			if s, ok := v.(string); ok && strings.Contains(s, h.dec.plaintext) {
				t.Fatalf("¡la clave privada apareció en auditoría (%s)!: %v", k, s)
			}
		}
	}
}

// ---- Broadcast: success / rejected / ambiguous ----

func TestEnsureGasFor_BroadcastSuccess(t *testing.T) {
	h := newHarness(t, "0.01", "0.05", "0.05", true)
	h.chain.setBalance(target1, bnb("0.0"))
	if err := h.svc.EnsureGasFor(context.Background(), "gas:ok", target1, bnb("0.01")); err != nil {
		t.Fatalf("EnsureGasFor: %v", err)
	}
	if h.refills.nonFailedCount() != 1 {
		t.Fatalf("esperaba 1 refill no-fallido, hay %d", h.refills.nonFailedCount())
	}
	for _, r := range h.refills.rows {
		if r.Status != postgres.RefillStatusConfirmed {
			t.Fatalf("esperaba status confirmed, fue %s", r.Status)
		}
	}
}

func TestEnsureGasFor_BroadcastRejected_MarksFailedAndReleasesNonce(t *testing.T) {
	h := newHarness(t, "0.01", "0.05", "0.05", true)
	h.chain.setBalance(target1, bnb("0.0"))
	h.chain.broadcastErr = errors.New("invalid chain id")
	err := h.svc.EnsureGasFor(context.Background(), "gas:rej", target1, bnb("0.01"))
	if err == nil {
		t.Fatal("se esperaba error")
	}
	for _, r := range h.refills.rows {
		if r.Status != postgres.RefillStatusFailed {
			t.Fatalf("esperaba status failed, fue %s", r.Status)
		}
	}
	released := false
	for _, l := range h.nonces.log {
		if l == "released:gas:rej" {
			released = true
		}
	}
	if !released {
		t.Fatalf("esperaba que se liberara el nonce tras un rechazo definitivo: %v", h.nonces.log)
	}
}

func TestEnsureGasFor_BroadcastAmbiguous_NeverAutoRetries(t *testing.T) {
	h := newHarness(t, "0.01", "0.05", "0.05", true)
	h.chain.setBalance(target1, bnb("0.0"))
	h.chain.broadcastErr = errors.New("i/o timeout") // unrecognized -> ambiguous by design
	err := h.svc.EnsureGasFor(context.Background(), "gas:amb", target1, bnb("0.01"))
	if !errors.Is(err, ErrBroadcastAmbiguous) {
		t.Fatalf("esperaba ErrBroadcastAmbiguous, obtuve %v", err)
	}
	for _, r := range h.refills.rows {
		if r.Status == postgres.RefillStatusFailed {
			t.Fatalf("un resultado ambiguo NUNCA debe marcarse failed automáticamente")
		}
		if r.TxHash == "" {
			t.Fatalf("el tx_hash firmado debe quedar registrado aunque el broadcast sea ambiguo")
		}
	}
	released := false
	for _, l := range h.nonces.log {
		if strings.HasPrefix(l, "released:") {
			released = true
		}
	}
	if released {
		t.Fatalf("un resultado ambiguo NUNCA debe liberar el nonce (podría haber sido aceptado)")
	}
}

// ---- Concurrency: two simultaneous refills for the same target → only one sends ----

func TestEnsureGasFor_Concurrency_OnlyOneRefillExecutes(t *testing.T) {
	h := newHarness(t, "0.01", "0.05", "0.05", true)
	h.chain.setBalance(target1, bnb("0.0"))
	// Second call resolves via observeInFlight; make the balance sufficient
	// almost immediately so it converges to "no action needed" instead of
	// blocking for the full confirmMaxWait in this test.
	h.svc.confirmMaxWait = 200 * time.Millisecond
	h.svc.confirmPollEvery = 10 * time.Millisecond

	var wg sync.WaitGroup
	results := make([]error, 2)
	owners := []string{"gas:c1", "gas:c2"}
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i] = h.svc.EnsureGasFor(context.Background(), owners[i], target1, bnb("0.01"))
			if results[i] == nil {
				h.chain.setBalance(target1, bnb("0.02")) // simulate the winner's tx having landed
			}
		}(i)
	}
	close(start)
	wg.Wait()

	successes := 0
	for _, err := range results {
		if err == nil {
			successes++
		}
	}
	if successes < 1 {
		t.Fatalf("al menos una de las dos llamadas debe tener éxito: %v", results)
	}
	if h.refills.count() != 1 {
		t.Fatalf("solo debió crearse UNA fila de refill para la misma dirección, hay %d", h.refills.count())
	}
}

// ---- Audit ----

func TestEnsureGasFor_AuditsConfirmedRefill(t *testing.T) {
	h := newHarness(t, "0.01", "0.05", "0.05", true)
	h.chain.setBalance(target1, bnb("0.0"))
	if err := h.svc.EnsureGasFor(context.Background(), "gas:aud", target1, bnb("0.01")); err != nil {
		t.Fatalf("EnsureGasFor: %v", err)
	}
	found := false
	for _, e := range h.audit.events {
		if e == "gas_station_refill_confirmed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("esperaba un evento de auditoría gas_station_refill_confirmed, eventos: %v", h.audit.events)
	}
}

func TestEnsureGasFor_AuditsFailedRefill(t *testing.T) {
	h := newHarness(t, "0.01", "0.05", "0.05", true)
	h.chain.setBalance(target1, bnb("0.0"))
	h.chain.broadcastErr = errors.New("invalid chain id")
	_ = h.svc.EnsureGasFor(context.Background(), "gas:audfail", target1, bnb("0.01"))
	found := false
	for _, e := range h.audit.events {
		if e == "gas_station_refill_failed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("esperaba un evento de auditoría gas_station_refill_failed, eventos: %v", h.audit.events)
	}
}

// ---- Disabled policy: never refills automatically ----

func TestEnsureGasFor_DisabledPolicy(t *testing.T) {
	h := newHarness(t, "0.01", "0.05", "0.05", false) // enabled=false
	h.chain.setBalance(target1, bnb("0.0"))
	err := h.svc.EnsureGasFor(context.Background(), "gas:dis", target1, bnb("0.01"))
	if !errors.Is(err, ErrDisabled) {
		t.Fatalf("esperaba ErrDisabled, obtuve %v", err)
	}
	if h.refills.count() != 0 {
		t.Fatalf("una política deshabilitada nunca debe crear un refill")
	}
}
