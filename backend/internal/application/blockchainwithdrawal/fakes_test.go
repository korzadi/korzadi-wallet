package blockchainwithdrawal

import (
	"context"
	"encoding/hex"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	appfee "korzadi-wallet/backend/internal/application/fee"
	"korzadi-wallet/backend/internal/domain"
)

// ---- in-memory Store ----

type fakeStore struct {
	mu        sync.Mutex
	byID      map[uuid.UUID]*Withdrawal
	byKey     map[string]uuid.UUID // userID+"|"+key
	dailyUsed map[string]decimal.Decimal
	events    []Event
}

func newFakeStore() *fakeStore {
	return &fakeStore{byID: map[uuid.UUID]*Withdrawal{}, byKey: map[string]uuid.UUID{}, dailyUsed: map[string]decimal.Decimal{}}
}

func keyOf(userID uuid.UUID, key string) string { return userID.String() + "|" + key }

func (s *fakeStore) CreateIfAbsent(ctx context.Context, w *Withdrawal, dailyMax decimal.Decimal, leaseOwner string, lease time.Duration) (*Withdrawal, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := keyOf(w.UserID, w.IdempotencyKey)
	if id, ok := s.byKey[k]; ok {
		return s.byID[id], false, nil
	}
	dailyKey := w.UserID.String() + "|" + w.AssetCode
	if dailyMax.IsPositive() && s.dailyUsed[dailyKey].Add(w.Amount).GreaterThan(dailyMax) {
		return nil, false, ErrDailyLimitExceeded
	}
	cp := *w
	cp.LockedBy = leaseOwner
	now := time.Now().UTC()
	cp.CreatedAt, cp.UpdatedAt = now, now
	s.byID[cp.ID] = &cp
	s.byKey[k] = cp.ID
	s.dailyUsed[dailyKey] = s.dailyUsed[dailyKey].Add(w.Amount)
	out := cp
	return &out, true, nil
}

func (s *fakeStore) Get(ctx context.Context, id uuid.UUID) (*Withdrawal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.byID[id]
	if !ok {
		return nil, nil
	}
	out := *w
	return &out, nil
}

func (s *fakeStore) GetForUser(ctx context.Context, id, userID uuid.UUID) (*Withdrawal, error) {
	w, err := s.Get(ctx, id)
	if err != nil || w == nil || w.UserID != userID {
		return nil, err
	}
	return w, nil
}

func (s *fakeStore) FindByUserKey(ctx context.Context, userID uuid.UUID, key string) (*Withdrawal, error) {
	s.mu.Lock()
	id, ok := s.byKey[keyOf(userID, key)]
	s.mu.Unlock()
	if !ok {
		return nil, nil
	}
	return s.Get(ctx, id)
}

func (s *fakeStore) ListByUser(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*Withdrawal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*Withdrawal
	for _, w := range s.byID {
		if w.UserID == userID {
			cp := *w
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (s *fakeStore) Apply(ctx context.Context, id uuid.UUID, expectFrom, to Status, u Update, ev Event) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.byID[id]
	if !ok || w.Status != expectFrom {
		return false, nil
	}
	if u.RequireUnsigned && len(w.SignedRawTx) != 0 {
		return false, nil
	}
	if expectFrom != to {
		w.Attempts = 0
	}
	w.Status = to
	if u.AmountReservationID != nil {
		w.AmountReservationID = u.AmountReservationID
	}
	if u.FeeReservationID != nil {
		w.FeeReservationID = u.FeeReservationID
	}
	if u.Nonce != nil {
		w.Nonce = u.Nonce
	}
	if u.GasLimit != nil {
		w.GasLimit = u.GasLimit
	}
	if u.GasPriceWei != nil {
		w.GasPriceWei = decimal.NewNullDecimal(*u.GasPriceWei)
	}
	if u.SignedRawTx != nil {
		w.SignedRawTx = u.SignedRawTx
	}
	if u.TxHash != nil {
		w.TxHash = *u.TxHash
	}
	if u.MarkSigned {
		now := time.Now().UTC()
		w.SignedAt = &now
	}
	if u.ClearSigned {
		w.SignedRawTx, w.TxHash, w.SignedAt, w.Nonce, w.GasLimit = nil, "", nil, nil, nil
		w.GasPriceWei = decimal.NullDecimal{}
	}
	if u.BlockNumber != nil {
		w.BlockNumber = u.BlockNumber
	}
	if u.BlockHash != nil {
		w.BlockHash = *u.BlockHash
	}
	if u.ClearBlock {
		w.BlockNumber, w.BlockHash = nil, ""
	}
	if u.Confirmations != nil {
		w.Confirmations = *u.Confirmations
	}
	if u.RequiredConfirmations != nil {
		w.RequiredConfirmations = u.RequiredConfirmations
	}
	if u.ActualNetworkFeeUSDT != nil {
		w.ActualNetworkFeeUSDT = decimal.NewNullDecimal(*u.ActualNetworkFeeUSDT)
	}
	if u.ActualNetworkFeeNative != nil {
		w.ActualNetworkFeeNative = decimal.NewNullDecimal(*u.ActualNetworkFeeNative)
	}
	if u.LedgerTransactionID != nil {
		w.LedgerTransactionID = u.LedgerTransactionID
	}
	if u.ErrorDetail != nil {
		w.ErrorDetail = *u.ErrorDetail
	}
	if u.NeedsReview != nil {
		w.NeedsReview = *u.NeedsReview
	}
	if u.MarkBroadcasted {
		now := time.Now().UTC()
		w.BroadcastedAt = &now
	}
	if u.MarkCompleted {
		now := time.Now().UTC()
		w.CompletedAt = &now
	}
	w.UpdatedAt = time.Now().UTC()
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

func (s *fakeStore) ClaimDue(ctx context.Context, owner string, lease time.Duration, limit int, statuses []Status) ([]*Withdrawal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	want := map[Status]bool{}
	for _, st := range statuses {
		want[st] = true
	}
	var out []*Withdrawal
	for _, w := range s.byID {
		if want[w.Status] && w.LockedBy == "" && len(out) < limit {
			w.LockedBy = owner
			cp := *w
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (s *fakeStore) Lease(ctx context.Context, id uuid.UUID, owner string, lease time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.byID[id]
	if !ok || w.LockedBy != "" {
		return false, nil
	}
	w.LockedBy = owner
	return true, nil
}

func (s *fakeStore) Unlease(ctx context.Context, id uuid.UUID, owner string, delay time.Duration, bump bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if w, ok := s.byID[id]; ok && w.LockedBy == owner {
		w.LockedBy = ""
		if bump {
			w.Attempts++
		}
	}
	return nil
}

// ---- in-memory Nonces ----

type fakeNonces struct {
	mu   sync.Mutex
	live map[string]uint64 // network|address|owner -> nonce
	next map[string]uint64 // network|address -> next free
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
	delete(n.live, string(network)+"|"+address+"|"+owner)
	return nil
}

// ---- in-memory Ledger ----

type fakeLedger struct {
	mu           sync.Mutex
	balances     map[uuid.UUID]decimal.Decimal // account -> available
	reservations map[uuid.UUID]*domain.Reservation
	settled      []domain.ReservationSettlementInput
	failReserve  error
	failSettle   error
}

func newFakeLedger() *fakeLedger {
	return &fakeLedger{balances: map[uuid.UUID]decimal.Decimal{}, reservations: map[uuid.UUID]*domain.Reservation{}}
}

func (l *fakeLedger) fund(account uuid.UUID, amount decimal.Decimal) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.balances[account] = l.balances[account].Add(amount)
}

func (l *fakeLedger) Reserve(ctx context.Context, walletAccountID uuid.UUID, assetCode string, amount decimal.Decimal, reason string, referenceID *string) (*domain.Reservation, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.failReserve != nil {
		return nil, l.failReserve
	}
	if l.balances[walletAccountID].LessThan(amount) {
		return nil, domain.ErrInsufficientFunds
	}
	l.balances[walletAccountID] = l.balances[walletAccountID].Sub(amount)
	r := &domain.Reservation{ID: uuid.New(), WalletAccountID: walletAccountID, AssetCode: assetCode, Amount: amount, Status: domain.ReservationActive, Reason: reason, ReferenceID: referenceID}
	l.reservations[r.ID] = r
	return r, nil
}

func (l *fakeLedger) Release(ctx context.Context, reservationID uuid.UUID) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	r, ok := l.reservations[reservationID]
	if !ok {
		return domain.ErrReservationNotFound
	}
	if r.Status != domain.ReservationActive {
		return domain.ErrReservationNotActive
	}
	l.balances[r.WalletAccountID] = l.balances[r.WalletAccountID].Add(r.Amount)
	r.Status = domain.ReservationReleased
	return nil
}

func (l *fakeLedger) FindReservation(ctx context.Context, reservationID uuid.UUID) (*domain.Reservation, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	r, ok := l.reservations[reservationID]
	if !ok {
		return nil, domain.ErrReservationNotFound
	}
	cp := *r
	return &cp, nil
}

func (l *fakeLedger) FindReservationByReference(ctx context.Context, referenceID, reason string) (*domain.Reservation, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, r := range l.reservations {
		if r.ReferenceID != nil && *r.ReferenceID == referenceID && r.Reason == reason {
			cp := *r
			return &cp, nil
		}
	}
	return nil, nil
}

func (l *fakeLedger) SettleReservations(ctx context.Context, in domain.ReservationSettlementInput) (*domain.LedgerTransaction, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.failSettle != nil {
		return nil, l.failSettle
	}
	for _, id := range in.ReservationIDs {
		r, ok := l.reservations[id]
		if !ok {
			return nil, domain.ErrReservationNotFound
		}
		if r.Status != domain.ReservationActive {
			return nil, domain.ErrReservationNotActive
		}
	}
	for _, id := range in.ReservationIDs {
		l.reservations[id].Status = domain.ReservationConsumed
	}
	l.settled = append(l.settled, in)
	return &domain.LedgerTransaction{ID: uuid.New(), Type: in.TxType, IdempotencyKey: in.IdempotencyKey}, nil
}

// ---- other fakes ----

type fakeAccounts struct{ accounts map[string]uuid.UUID }

func newFakeAccounts() *fakeAccounts { return &fakeAccounts{accounts: map[string]uuid.UUID{}} }

func (a *fakeAccounts) FindOrCreateUserAccount(ctx context.Context, userID uuid.UUID, assetCode string) (uuid.UUID, error) {
	k := userID.String() + "|" + assetCode
	if id, ok := a.accounts[k]; ok {
		return id, nil
	}
	id := uuid.New()
	a.accounts[k] = id
	return id, nil
}

type fakeClearing struct{ accounts map[string]uuid.UUID }

func newFakeClearing() *fakeClearing { return &fakeClearing{accounts: map[string]uuid.UUID{}} }

func (c *fakeClearing) FindClearingAccount(ctx context.Context, network domain.Network, assetCode string) (uuid.UUID, error) {
	k := string(network) + "|" + assetCode
	if id, ok := c.accounts[k]; ok {
		return id, nil
	}
	id := uuid.New()
	c.accounts[k] = id
	return id, nil
}

type fakeFeeEngine struct {
	mu      sync.Mutex // guards quotes: Create() is exercised concurrently under -race
	quotes  map[uuid.UUID]*appfee.FeeQuote
	nextErr error
}

func (f *fakeFeeEngine) quote(id uuid.UUID) (*appfee.FeeQuote, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	q, ok := f.quotes[id]
	return q, ok
}

func newFakeFeeEngine() *fakeFeeEngine {
	return &fakeFeeEngine{quotes: map[uuid.UUID]*appfee.FeeQuote{}}
}

func (f *fakeFeeEngine) addQuote(q *appfee.FeeQuote) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.quotes[q.ID] = q
}

func (f *fakeFeeEngine) EstimateFee(ctx context.Context, in appfee.EstimateFeeInput) (*appfee.FeeQuote, error) {
	if f.nextErr != nil {
		return nil, f.nextErr
	}
	q := &appfee.FeeQuote{
		ID: uuid.New(), UserID: in.UserID, OperationType: in.OperationType, Network: in.Network,
		OperationAssetCode: in.OperationAssetCode, FeeAssetCode: FeeAssetCode,
		ServiceFee: decimal.NewFromFloat(1), NetworkFee: decimal.NewFromFloat(0.5), TotalFee: decimal.NewFromFloat(1.5),
		NetworkFeeNative: decimal.NewFromFloat(0.0002), NativeAssetCode: "ETH",
		Status: appfee.QuoteStatusActive, ExpiresAt: time.Now().Add(time.Hour), ExchangeRate: decimal.NewFromInt(2500),
	}
	f.mu.Lock()
	f.quotes[q.ID] = q
	f.mu.Unlock()
	return q, nil
}

func (f *fakeFeeEngine) GetQuote(ctx context.Context, quoteID, userID uuid.UUID) (*appfee.FeeQuote, error) {
	q, ok := f.quote(quoteID)
	if !ok || q.UserID != userID {
		return nil, appfee.ErrQuoteNotFound
	}
	return q, nil
}

func (f *fakeFeeEngine) ValidateQuote(quote *appfee.FeeQuote, now time.Time) error {
	if quote.Status != appfee.QuoteStatusActive && quote.Status != appfee.QuoteStatusReserved {
		return appfee.ErrQuoteNotActive
	}
	if now.After(quote.ExpiresAt) {
		return appfee.ErrQuoteExpired
	}
	return nil
}

type fakeFeeReserver struct {
	ledger *fakeLedger
	engine *fakeFeeEngine
	feeAcc uuid.UUID
}

func (r *fakeFeeReserver) ReserveFee(ctx context.Context, quoteID uuid.UUID) (*domain.Reservation, error) {
	q, _ := r.engine.quote(quoteID)
	ref := quoteID.String()
	res, err := r.ledger.Reserve(ctx, r.feeAcc, FeeAssetCode, q.TotalFee, feeReservationReason(quoteID), &ref)
	if err != nil {
		return nil, err
	}
	q.ReservationID = &res.ID
	q.Status = appfee.QuoteStatusReserved
	return res, nil
}

type fakeQuotes struct{ engine *fakeFeeEngine }

func (q *fakeQuotes) FindByID(ctx context.Context, id uuid.UUID) (*appfee.FeeQuote, error) {
	quote, ok := q.engine.quote(id)
	if !ok {
		return nil, nil
	}
	return quote, nil
}
func (q *fakeQuotes) SetReservationID(ctx context.Context, quoteID, reservationID uuid.UUID) (bool, error) {
	quote, ok := q.engine.quote(quoteID)
	if !ok {
		return false, nil
	}
	if quote.ReservationID != nil {
		return false, nil
	}
	quote.ReservationID = &reservationID
	return true, nil
}
func (q *fakeQuotes) UpdateStatus(ctx context.Context, id uuid.UUID, status appfee.QuoteStatus) error {
	if quote, ok := q.engine.quote(id); ok {
		quote.Status = status
	}
	return nil
}

type fakeUsers struct{ users map[uuid.UUID]*domain.User }

func newFakeUsers() *fakeUsers { return &fakeUsers{users: map[uuid.UUID]*domain.User{}} }
func (u *fakeUsers) FindByID(ctx context.Context, id uuid.UUID) (*domain.User, error) {
	return u.users[id], nil
}

type fakeWallets struct{ wallets map[uuid.UUID]*domain.Wallet }

func newFakeWallets() *fakeWallets { return &fakeWallets{wallets: map[uuid.UUID]*domain.Wallet{}} }
func (w *fakeWallets) FindWalletByUserID(ctx context.Context, userID uuid.UUID) (*domain.Wallet, error) {
	return w.wallets[userID], nil
}

type fakeLedgerAssets struct{ assets map[string]bool }

func newFakeLedgerAssets(codes ...string) *fakeLedgerAssets {
	m := map[string]bool{}
	for _, c := range codes {
		m[c] = true
	}
	return &fakeLedgerAssets{assets: m}
}

// LedgerAsset mirrors transfer.LedgerAsset's shape without importing it in
// this fake's signature ambiguity; we import transfer directly in the port.
type fakeLedgerAssetResult = struct {
	Code      string
	Name      string
	Decimals  int32
	IsEnabled bool
}

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

type fakeAddresses struct{ addresses map[string]string }

func newFakeAddresses() *fakeAddresses { return &fakeAddresses{addresses: map[string]string{}} }
func (a *fakeAddresses) FindActiveDepositAddress(ctx context.Context, network domain.Network, userID uuid.UUID) (string, error) {
	addr, ok := a.addresses[string(network)+"|"+userID.String()]
	if !ok {
		return "", nil
	}
	return addr, nil
}

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

type fakeChain struct {
	network        domain.Network
	chainID        int64
	confirmations  uint64
	nativeBalance  decimal.Decimal // wei-equivalent stored as decimal for test convenience
	tokenBalance   map[string]decimal.Decimal
	gasPrice       int64
	broadcastErr   error
	broadcastHash  string
	receipts       map[string]*domain.TransactionReceipt
	knownHashes    map[string]bool
	pendingNonce   uint64
	confirmedNonce uint64
	// echoHash: Broadcast devuelve "0x"+hex(raw) (útil con varios retiros
	// concurrentes, cada uno con su propia firma).
	echoHash bool
}

func newFakeChain(network domain.Network) *fakeChain {
	return &fakeChain{
		network: network, chainID: 1337, confirmations: 2, gasPrice: 10,
		// Saldo nativo por defecto generoso: cubre el monto retirado + gas
		// en los tests de ciclo de vida. Los tests de "fondos on-chain
		// insuficientes" bajan este valor explícitamente.
		nativeBalance: decimal.NewFromInt(1000),
		tokenBalance:  map[string]decimal.Decimal{}, receipts: map[string]*domain.TransactionReceipt{}, knownHashes: map[string]bool{},
	}
}

func (c *fakeChain) Network() domain.Network       { return c.network }
func (c *fakeChain) ChainID() *big.Int             { return big.NewInt(c.chainID) }
func (c *fakeChain) ConfirmationsRequired() uint64 { return c.confirmations }
func (c *fakeChain) PendingNonce(ctx context.Context, address string) (uint64, error) {
	return c.pendingNonce, nil
}
func (c *fakeChain) ConfirmedNonce(ctx context.Context, address string) (uint64, error) {
	return c.confirmedNonce, nil
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
	if c.echoHash {
		return "0x" + hex.EncodeToString(signedRawTx), nil
	}
	return c.broadcastHash, nil
}
func (c *fakeChain) Receipt(ctx context.Context, txHash string) (*domain.TransactionReceipt, error) {
	return c.receipts[txHash], nil
}
func (c *fakeChain) TxKnown(ctx context.Context, txHash string) (bool, error) {
	return c.knownHashes[txHash], nil
}

var errFakeSign = fmtErrorf("fake signer: fallo forzado en la prueba")

func fmtErrorf(s string) error { return &simpleErr{s} }

type simpleErr struct{ s string }

func (e *simpleErr) Error() string { return e.s }

// ---- Camino B (HOT wallet) fakes ----

// SumInFlight mirrors postgres.BlockchainWithdrawalRepository.SumInFlight:
// signed-but-unfinished withdrawals from one source address and asset.
func (s *fakeStore) SumInFlight(ctx context.Context, network domain.Network, fromAddress, assetCode string, excludeID uuid.UUID) (decimal.Decimal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	total := decimal.Zero
	for id, w := range s.byID {
		if id == excludeID || w.Network != network || w.AssetCode != assetCode || !strings.EqualFold(w.FromAddress, fromAddress) {
			continue
		}
		if len(w.SignedRawTx) == 0 {
			continue
		}
		switch w.Status {
		case StatusProcessing, StatusBroadcasted, StatusConfirming:
			total = total.Add(w.Amount)
		}
	}
	return total, nil
}

type fakeHotBook struct{ addrs map[string]string }

func (b *fakeHotBook) FindActiveHotAddress(ctx context.Context, network domain.Network, assetCode string) (string, error) {
	return b.addrs[string(network)+"|"+assetCode], nil
}
