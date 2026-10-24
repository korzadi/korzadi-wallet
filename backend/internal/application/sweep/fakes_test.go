package sweep

import (
	"context"
	"errors"
	"math/big"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
)

// ---- in-memory Store (mirrors blockchainwithdrawal's fakeStore) ----

type fakeStore struct {
	mu   sync.Mutex
	byID map[uuid.UUID]*Sweep
	// inFlight simulates uq_sweeps_inflight_source: at most one non-terminal
	// sweep per (network, asset, from_address).
	inFlight map[string]uuid.UUID
	events   []Event
}

func newFakeStore() *fakeStore {
	return &fakeStore{byID: map[uuid.UUID]*Sweep{}, inFlight: map[string]uuid.UUID{}}
}

func inflightKey(network domain.Network, assetCode, fromAddress string) string {
	return string(network) + "|" + assetCode + "|" + fromAddress
}

func (s *fakeStore) Create(ctx context.Context, sw *Sweep) (*Sweep, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := inflightKey(sw.Network, sw.AssetCode, sw.FromAddress)
	if _, taken := s.inFlight[k]; taken {
		return nil, ErrInFlight
	}
	cp := *sw
	if cp.ID == uuid.Nil {
		cp.ID = uuid.New()
	}
	cp.Status = StatusPending
	now := time.Now().UTC()
	cp.CreatedAt, cp.UpdatedAt = now, now
	s.byID[cp.ID] = &cp
	s.inFlight[k] = cp.ID
	s.events = append(s.events, Event{Type: "detected"})
	out := cp
	return &out, nil
}

func (s *fakeStore) Get(ctx context.Context, id uuid.UUID) (*Sweep, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sw, ok := s.byID[id]
	if !ok {
		return nil, nil
	}
	out := *sw
	return &out, nil
}

func (s *fakeStore) Apply(ctx context.Context, id uuid.UUID, expectFrom, to Status, u Update, ev Event) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sw, ok := s.byID[id]
	if !ok || sw.Status != expectFrom {
		return false, nil
	}
	if u.RequireUnsigned && len(sw.SignedRawTx) != 0 {
		return false, nil
	}
	if expectFrom != to {
		sw.Attempts = 0
	}
	sw.Status = to
	if u.Amount != nil {
		sw.Amount = *u.Amount
	}
	if u.Nonce != nil {
		sw.Nonce = u.Nonce
	}
	if u.GasLimit != nil {
		sw.GasLimit = u.GasLimit
	}
	if u.GasPriceWei != nil {
		sw.GasPriceWei = decimal.NewNullDecimal(*u.GasPriceWei)
	}
	if u.SignedRawTx != nil {
		sw.SignedRawTx = u.SignedRawTx
	}
	if u.TxHash != nil {
		sw.TxHash = *u.TxHash
	}
	if u.MarkSigned {
		now := time.Now().UTC()
		sw.SignedAt = &now
	}
	if u.ClearSigned {
		sw.SignedRawTx, sw.TxHash, sw.SignedAt, sw.Nonce, sw.GasLimit = nil, "", nil, nil, nil
		sw.GasPriceWei = decimal.NullDecimal{}
	}
	if u.BlockNumber != nil {
		sw.BlockNumber = u.BlockNumber
	}
	if u.BlockHash != nil {
		sw.BlockHash = *u.BlockHash
	}
	if u.ClearBlock {
		sw.BlockNumber, sw.BlockHash = nil, ""
	}
	if u.Confirmations != nil {
		sw.Confirmations = *u.Confirmations
	}
	if u.RequiredConfirmations != nil {
		sw.RequiredConfirmations = u.RequiredConfirmations
	}
	if u.ErrorDetail != nil {
		sw.ErrorDetail = *u.ErrorDetail
	}
	if u.NeedsReview != nil {
		sw.NeedsReview = *u.NeedsReview
	}
	if u.MarkBroadcasted {
		now := time.Now().UTC()
		sw.BroadcastedAt = &now
	}
	if u.MarkCompleted {
		now := time.Now().UTC()
		sw.CompletedAt = &now
	}
	sw.UpdatedAt = time.Now().UTC()
	// A terminal status frees the in-flight slot — a later candidate scan
	// (e.g. a new deposit to the same address) may sweep it again.
	if to.IsTerminal() {
		delete(s.inFlight, inflightKey(sw.Network, sw.AssetCode, sw.FromAddress))
	}
	if ev.Type != "" {
		s.events = append(s.events, ev)
	}
	return true, nil
}

func (s *fakeStore) AddEvent(ctx context.Context, id uuid.UUID, eventType string, from, to Status, detail map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, Event{Type: eventType, Detail: detail})
	return nil
}

func (s *fakeStore) ClaimDue(ctx context.Context, owner string, lease time.Duration, limit int, statuses []Status) ([]*Sweep, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	want := map[Status]bool{}
	for _, st := range statuses {
		want[st] = true
	}
	var out []*Sweep
	for _, sw := range s.byID {
		if want[sw.Status] && sw.LockedBy == "" && len(out) < limit {
			sw.LockedBy = owner
			cp := *sw
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (s *fakeStore) Unlease(ctx context.Context, id uuid.UUID, owner string, delay time.Duration, bump bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sw, ok := s.byID[id]; ok && sw.LockedBy == owner {
		sw.LockedBy = ""
		if bump {
			sw.Attempts++
		}
	}
	return nil
}

func (s *fakeStore) eventTypes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.events))
	for _, e := range s.events {
		out = append(out, e.Type)
	}
	return out
}

// ---- in-memory CandidateSource ----

type fakeCandidates struct {
	mu    sync.Mutex
	items []Candidate
}

func (c *fakeCandidates) FindCandidates(ctx context.Context, limit int) ([]Candidate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if limit > 0 && limit < len(c.items) {
		return append([]Candidate{}, c.items[:limit]...), nil
	}
	return append([]Candidate{}, c.items...), nil
}

// ---- in-memory NonceStore (mirrors blockchainwithdrawal's fakeNonces) ----

type fakeNonces struct {
	mu       sync.Mutex
	live     map[string]uint64
	next     map[string]uint64
	released []string
}

func newFakeNonces() *fakeNonces {
	return &fakeNonces{live: map[string]uint64{}, next: map[string]uint64{}}
}

func (n *fakeNonces) Allocate(ctx context.Context, network domain.Network, address, owner string, chainPending uint64) (uint64, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	ownerKey := string(network) + "|" + address + "|" + owner
	if v, ok := n.live[ownerKey]; ok {
		return v, nil
	}
	addrKey := string(network) + "|" + address
	next := n.next[addrKey]
	if chainPending > next {
		next = chainPending
	}
	n.live[ownerKey] = next
	n.next[addrKey] = next + 1
	return next, nil
}
func (n *fakeNonces) MarkSigned(ctx context.Context, network domain.Network, address, owner, txHash string) error {
	return nil
}
func (n *fakeNonces) MarkConfirmed(ctx context.Context, network domain.Network, address, owner string) error {
	return nil
}
func (n *fakeNonces) Release(ctx context.Context, network domain.Network, address, owner string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	k := string(network) + "|" + address + "|" + owner
	delete(n.live, k)
	n.released = append(n.released, k)
	return nil
}

// ---- fake asset registry ----

type fakeRegistry struct {
	assets map[string]domain.NetworkAsset
}

func newFakeRegistry() *fakeRegistry { return &fakeRegistry{assets: map[string]domain.NetworkAsset{}} }
func (r *fakeRegistry) add(a domain.NetworkAsset) {
	r.assets[string(a.Network)+"|"+a.AssetCode] = a
}
func (r *fakeRegistry) Get(ctx context.Context, network domain.Network, assetCode string) (domain.NetworkAsset, bool, error) {
	a, ok := r.assets[string(network)+"|"+assetCode]
	return a, ok, nil
}
func (r *fakeRegistry) ListEnabled(ctx context.Context, network domain.Network) ([]domain.NetworkAsset, error) {
	var out []domain.NetworkAsset
	for _, a := range r.assets {
		if a.Network == network && a.Enabled {
			out = append(out, a)
		}
	}
	return out, nil
}

// ---- fake custody wallet registry ----

type fakeWallets struct {
	mu      sync.Mutex
	byKey   map[string]*CustodyWallet
	lookups int
	failErr error
}

func newFakeWallets() *fakeWallets { return &fakeWallets{byKey: map[string]*CustodyWallet{}} }

func walletKey(walletType, network, assetCode string) string {
	return walletType + "|" + network + "|" + assetCode
}

func (w *fakeWallets) add(walletType, network, assetCode string, wallet *CustodyWallet) {
	w.byKey[walletKey(walletType, network, assetCode)] = wallet
}

func (w *fakeWallets) FindActive(ctx context.Context, walletType, network, assetCode string) (*CustodyWallet, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.lookups++
	if w.failErr != nil {
		return nil, w.failErr
	}
	found, ok := w.byKey[walletKey(walletType, network, assetCode)]
	if !ok {
		return nil, errors.New("no treasury wallet configured")
	}
	return found, nil
}

// ---- fake audit logger ----

type fakeAudit struct {
	mu   sync.Mutex
	logs []string // action names, in order
}

func (a *fakeAudit) LogCustodyWallet(ctx context.Context, custodyWalletID *uuid.UUID, action, actor string, metadata map[string]any) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.logs = append(a.logs, action)
	return nil
}

func (a *fakeAudit) actions() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string{}, a.logs...)
}

// ---- fake gas top-up ----

type fakeGas struct {
	mu       sync.Mutex
	calls    int
	failErr  error
	onEnsure func(owner, target string, requiredWei *big.Int)
}

func (g *fakeGas) EnsureGasFor(ctx context.Context, owner, targetAddress string, requiredWei *big.Int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	if g.onEnsure != nil {
		g.onEnsure(owner, targetAddress, requiredWei)
	}
	return g.failErr
}

// ---- fake signer ----

type fakeSigner struct {
	fail bool
	sign func(SignRequest) (*SignedTx, error)
}

func (s *fakeSigner) Sign(ctx context.Context, req SignRequest) (*SignedTx, error) {
	if s.sign != nil {
		return s.sign(req)
	}
	if s.fail {
		return nil, errFakeSign
	}
	return &SignedTx{Raw: []byte{0x01}, TxHash: "0xaaaa", Nonce: req.Nonce}, nil
}

var errFakeSign = errors.New("fake signer: fallo forzado en la prueba")

// ---- fake EVM chain (mirrors blockchainwithdrawal's fakeChain) ----

type fakeChain struct {
	network       domain.Network
	chainID       int64
	confirmations uint64
	nativeBalance decimal.Decimal // human units, converted to wei on read
	tokenBalance  map[string]decimal.Decimal
	gasPrice      int64
	broadcastErr  error
	broadcastHash string
	receipts      map[string]*domain.TransactionReceipt
	knownHashes   map[string]bool
	pendingNonce  uint64
}

func newFakeChain(network domain.Network) *fakeChain {
	return &fakeChain{
		network: network, chainID: 1337, confirmations: 2, gasPrice: 10,
		nativeBalance: decimal.NewFromInt(1000), // generous default; tests that need "insufficient" set it explicitly
		tokenBalance:  map[string]decimal.Decimal{}, receipts: map[string]*domain.TransactionReceipt{}, knownHashes: map[string]bool{},
	}
}

func (c *fakeChain) Network() domain.Network       { return c.network }
func (c *fakeChain) ChainID() *big.Int             { return big.NewInt(c.chainID) }
func (c *fakeChain) ConfirmationsRequired() uint64 { return c.confirmations }
func (c *fakeChain) PendingNonce(ctx context.Context, address string) (uint64, error) {
	return c.pendingNonce, nil
}
func (c *fakeChain) NativeBalanceWei(ctx context.Context, address string) (*big.Int, error) {
	return c.nativeBalance.Shift(18).BigInt(), nil
}
func (c *fakeChain) TokenBalanceRaw(ctx context.Context, contract, address string) (*big.Int, error) {
	bal := c.tokenBalance[contract]
	return bal.Shift(18).BigInt(), nil
}
func (c *fakeChain) SuggestGasPrice(ctx context.Context) (*big.Int, error) {
	return big.NewInt(c.gasPrice), nil
}
func (c *fakeChain) EstimateGasNative(ctx context.Context, from, to string, valueWei *big.Int) (uint64, error) {
	return 21000, nil
}
func (c *fakeChain) EstimateGasToken(ctx context.Context, from, contract, to string, amountRaw *big.Int) (uint64, error) {
	return 65000, nil
}
func (c *fakeChain) Broadcast(ctx context.Context, signedRawTx []byte) (string, error) {
	if c.broadcastErr != nil {
		return "", c.broadcastErr
	}
	return c.broadcastHash, nil
}
func (c *fakeChain) Receipt(ctx context.Context, txHash string) (*domain.TransactionReceipt, error) {
	return c.receipts[txHash], nil
}
func (c *fakeChain) TxKnown(ctx context.Context, txHash string) (bool, error) {
	return c.knownHashes[txHash], nil
}
