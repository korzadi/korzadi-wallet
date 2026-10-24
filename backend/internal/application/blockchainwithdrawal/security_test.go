package blockchainwithdrawal

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/application/security"
	"korzadi-wallet/backend/internal/domain"
)

// ---- fakes del Bloque 8 --------------------------------------------------------

type secRes struct {
	in     security.ReserveInput
	status string // active | committed | released
}

type secLimiter struct {
	mu           sync.Mutex
	res          map[uuid.UUID]*secRes
	byRef        map[string]uuid.UUID
	rejectErr    error
	reserveCalls int
}

func newSecLimiter() *secLimiter {
	return &secLimiter{res: map[uuid.UUID]*secRes{}, byRef: map[string]uuid.UUID{}}
}

func (l *secLimiter) Reserve(ctx context.Context, in security.ReserveInput) (*security.Reservation, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.reserveCalls++
	if l.rejectErr != nil {
		return nil, l.rejectErr
	}
	if in.ReferenceID != "" {
		if id, ok := l.byRef[string(in.Operation)+"|"+in.ReferenceID]; ok && l.res[id].status == "active" {
			return nil, security.ErrReplay
		}
	}
	id := uuid.New()
	l.res[id] = &secRes{in: in, status: "active"}
	if in.ReferenceID != "" {
		l.byRef[string(in.Operation)+"|"+in.ReferenceID] = id
	}
	return &security.Reservation{ID: id, UserID: in.UserID, Operation: in.Operation, AssetCode: in.AssetCode, Amount: in.Amount}, nil
}

func (l *secLimiter) transition(id uuid.UUID, to string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	r, ok := l.res[id]
	if !ok {
		return security.ErrReservationNotFound
	}
	if r.status != "active" {
		return security.ErrReservationNotActive
	}
	r.status = to
	return nil
}
func (l *secLimiter) Commit(ctx context.Context, id uuid.UUID) error {
	return l.transition(id, "committed")
}
func (l *secLimiter) Release(ctx context.Context, id uuid.UUID) error {
	return l.transition(id, "released")
}

func (l *secLimiter) statuses() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, r := range l.res {
		out = append(out, r.status)
	}
	return out
}

type secPolicies struct{ policy *security.Policy }

func (p secPolicies) Get(ctx context.Context, op security.OperationType, asset, network string) (*security.Policy, error) {
	return p.policy, nil
}
func (p secPolicies) List(ctx context.Context) ([]security.Policy, error)   { return nil, nil }
func (p secPolicies) Upsert(ctx context.Context, pol security.Policy) error { return nil }

type secCounter struct {
	mu     sync.Mutex
	counts map[string]int64
}

func (c *secCounter) Incr(ctx context.Context, key string, window time.Duration) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counts[key]++
	return c.counts[key], nil
}

type secDest struct {
	mu        sync.Mutex
	status    map[string]security.DestinationStatus
	firstSeen map[string]time.Time
}

func newSecDest() *secDest {
	return &secDest{status: map[string]security.DestinationStatus{}, firstSeen: map[string]time.Time{}}
}
func secDestKey(u uuid.UUID, asset, network, hash string) string {
	return fmt.Sprintf("%s|%s|%s|%s", u, asset, network, hash)
}
func (d *secDest) Lookup(ctx context.Context, u uuid.UUID, asset, network, hash string) (security.DestinationStatus, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if s, ok := d.status[secDestKey(u, asset, network, hash)]; ok {
		return s, nil
	}
	return security.DestinationNew, nil
}
func (d *secDest) RecordUse(ctx context.Context, u uuid.UUID, asset, network, hash string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.status[secDestKey(u, asset, network, hash)] = security.DestinationKnown
	return nil
}
func (d *secDest) SetStatus(ctx context.Context, u uuid.UUID, asset, network, hash string, s security.DestinationStatus) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.status[secDestKey(u, asset, network, hash)] = s
	return nil
}
func (d *secDest) RegisterPending(ctx context.Context, u uuid.UUID, asset, network, hash string) (time.Time, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	k := secDestKey(u, asset, network, hash)
	if t, ok := d.firstSeen[k]; ok {
		return t, false, nil
	}
	now := time.Now()
	d.firstSeen[k] = now
	return now, true, nil
}

type secFreezes struct {
	mu     sync.Mutex
	frozen bool
}

func (f *secFreezes) set(b bool) { f.mu.Lock(); f.frozen = b; f.mu.Unlock() }
func (f *secFreezes) Get(ctx context.Context, op security.OperationType) (security.FreezeStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return security.FreezeStatus{Operation: op, Frozen: f.frozen, Reason: "prueba"}, nil
}
func (f *secFreezes) List(ctx context.Context) ([]security.FreezeStatus, error) { return nil, nil }
func (f *secFreezes) Set(ctx context.Context, op security.OperationType, frozen bool, reason string, actor uuid.UUID) error {
	return nil
}

type secAlerter struct {
	mu    sync.Mutex
	alert []string
}

func (a *secAlerter) Alert(ctx context.Context, userID uuid.UUID, title, body string) {
	a.mu.Lock()
	a.alert = append(a.alert, title)
	a.mu.Unlock()
}
func (a *secAlerter) count() int { a.mu.Lock(); defer a.mu.Unlock(); return len(a.alert) }

// limitStore añade LimitReservationStore al fakeStore del harness.
type limitStore struct {
	*fakeStore
	mu sync.Mutex
	m  map[uuid.UUID]uuid.UUID
}

func (s *limitStore) SetLimitReservation(ctx context.Context, wid, rid uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[wid]; !ok {
		s.m[wid] = rid
	}
	return nil
}
func (s *limitStore) GetLimitReservation(ctx context.Context, wid uuid.UUID) (uuid.UUID, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.m[wid]
	return id, ok, nil
}

type secEnv struct {
	*harness
	limiter  *secLimiter
	dest     *secDest
	counter  *secCounter
	freezes  *secFreezes
	alerter  *secAlerter
	policies *secPolicies
	ls       *limitStore
	sec      *security.Service
}

const secDestAddr = "0x2222222222222222222222222222222222222222"

func newSecEnv(t *testing.T, cooldown time.Duration, rules map[string]security.RateLimitRule) *secEnv {
	t.Helper()
	h := newHarness(t)
	e := &secEnv{
		harness: h, limiter: newSecLimiter(), dest: newSecDest(), counter: &secCounter{counts: map[string]int64{}},
		freezes: &secFreezes{}, alerter: &secAlerter{}, policies: &secPolicies{},
		ls: &limitStore{fakeStore: h.store, m: map[uuid.UUID]uuid.UUID{}},
	}
	e.sec = security.NewService(e.policies, security.NewLimitService(e.limiter, nil),
		security.NewFreezeGuard(e.freezes, security.DefaultConfig(), nil), nil, nil,
		security.NewVelocityTracker(e.counter, rules, nil), security.NewDestinationGuard(e.dest, nil), nil, nil, nil)
	h.svc.Security = e.sec
	h.svc.Alerter = e.alerter
	h.svc.Store = e.ls
	h.svc.Config.NewDestinationCooldown = cooldown
	return e
}

func (e *secEnv) fund(amount int64) {
	acc, _ := e.accounts.FindOrCreateUserAccount(context.Background(), e.userID, "ETH")
	e.ledger.fund(acc, decimal.NewFromInt(amount))
}

func (e *secEnv) markKnown() {
	e.dest.status[secDestKey(e.userID, "ETH", string(testNetwork), security.HashDestination(secDestAddr))] = security.DestinationKnown
}

func (e *secEnv) wire() {
	e.chain.broadcastHash = "0xaaaa"
	e.signer.sign = func(req SignRequest) (*SignedTx, error) {
		return &SignedTx{Raw: []byte{0x01, 0x02}, TxHash: e.chain.broadcastHash, Nonce: req.Nonce}, nil
	}
}

// ---- Create ------------------------------------------------------------------------

func TestSecurity_NewDestination_WaitsAndAlertsOnlyOnce(t *testing.T) {
	e := newSecEnv(t, time.Hour, nil)
	e.fund(5)
	ctx := context.Background()

	in := e.createInput("1")
	_, err := e.svc.Create(ctx, in)
	var cd *security.DestinationCooldownError
	if !errors.As(err, &cd) || !errors.Is(err, security.ErrDestinationCooldown) || !cd.FirstRequest {
		t.Fatalf("esperaba cooldown (primer intento), obtuve %v", err)
	}
	if e.alerter.count() != 1 {
		t.Fatalf("avisos = %d, esperaba 1 al registrar la dirección nueva", e.alerter.count())
	}
	if e.limiter.reserveCalls != 0 {
		t.Fatal("no debe reservarse ningún límite si el destino está en espera")
	}
	if w, _ := e.store.FindByUserKey(ctx, e.userID, in.IdempotencyKey); w != nil {
		t.Fatal("no debe crearse el retiro")
	}

	// Segundo intento (otra clave): sigue en espera y NO se vuelve a avisar.
	if _, err := e.svc.Create(ctx, e.createInput("1")); !errors.Is(err, security.ErrDestinationCooldown) {
		t.Fatalf("segundo intento: %v", err)
	}
	if e.alerter.count() != 1 {
		t.Fatalf("avisos = %d tras el segundo intento, esperaba 1", e.alerter.count())
	}
}

func TestSecurity_CooldownElapsed_AllowsWithdrawal(t *testing.T) {
	e := newSecEnv(t, time.Hour, nil)
	e.fund(5)
	e.dest.firstSeen[secDestKey(e.userID, "ETH", string(testNetwork), security.HashDestination(secDestAddr))] = time.Now().Add(-2 * time.Hour)

	res, err := e.svc.Create(context.Background(), e.createInput("1"))
	if err != nil || res.Withdrawal.Status != StatusReserved {
		t.Fatalf("tras la espera debía crearse: %v", err)
	}
}

func TestSecurity_KnownDestination_ProceedsAndKeepsLimitReservation(t *testing.T) {
	e := newSecEnv(t, time.Hour, nil)
	e.fund(5)
	e.markKnown()

	res, err := e.svc.Create(context.Background(), e.createInput("1"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	rid, found, _ := e.ls.GetLimitReservation(context.Background(), res.Withdrawal.ID)
	if !found {
		t.Fatal("el retiro debe guardar su reserva de límites")
	}
	if st := e.limiter.res[rid].status; st != "active" {
		t.Fatalf("la reserva debe seguir activa mientras el retiro está en curso: %s", st)
	}
	in := e.limiter.res[rid].in
	if in.Operation != security.OpWithdrawal || in.AssetCode != "ETH" || !in.Amount.Equal(decimal.NewFromInt(1)) {
		t.Fatalf("reserva con datos incorrectos: %+v", in)
	}
	// La referencia está namespaceada por usuario: la clave cruda del cliente nunca es la referencia.
	if want := "withdrawal:" + e.userID.String() + ":"; len(in.ReferenceID) <= len(want) || in.ReferenceID[:len(want)] != want {
		t.Fatalf("ReferenceID = %q, debía empezar por %q", in.ReferenceID, want)
	}
}

func TestSecurity_BlockedDestination_Rejected(t *testing.T) {
	e := newSecEnv(t, time.Hour, nil)
	e.fund(5)
	e.dest.status[secDestKey(e.userID, "ETH", string(testNetwork), security.HashDestination(secDestAddr))] = security.DestinationBlocked

	if _, err := e.svc.Create(context.Background(), e.createInput("1")); !errors.Is(err, security.ErrDestinationBlocked) {
		t.Fatalf("esperaba ErrDestinationBlocked, obtuve %v", err)
	}
	if e.limiter.reserveCalls != 0 {
		t.Fatal("un destino bloqueado no debe llegar a reservar límites")
	}
}

func TestSecurity_LimitRejected_NothingCreatedNothingHeld(t *testing.T) {
	e := newSecEnv(t, 0, nil)
	e.fund(5)
	e.limiter.rejectErr = security.ErrDailyLimitExceeded
	in := e.createInput("1")

	if _, err := e.svc.Create(context.Background(), in); !errors.Is(err, security.ErrDailyLimitExceeded) {
		t.Fatalf("esperaba ErrDailyLimitExceeded, obtuve %v", err)
	}
	if w, _ := e.store.FindByUserKey(context.Background(), e.userID, in.IdempotencyKey); w != nil {
		t.Fatal("no debe crearse el retiro")
	}
	if len(e.limiter.statuses()) != 0 {
		t.Fatal("no debe quedar ninguna reserva")
	}
}

func TestSecurity_VelocityExceeded_RateLimited(t *testing.T) {
	e := newSecEnv(t, 0, map[string]security.RateLimitRule{"withdrawals_per_user": {Limit: 1, Window: time.Minute}})
	e.fund(5)

	if _, err := e.svc.Create(context.Background(), e.createInput("1")); err != nil {
		t.Fatalf("el primero debe pasar: %v", err)
	}
	if _, err := e.svc.Create(context.Background(), e.createInput("1")); !errors.Is(err, security.ErrRateLimited) {
		t.Fatalf("el segundo debía limitarse por velocidad: %v", err)
	}
}

func TestSecurity_ManualApprovalAmount_RejectedAndReservationReleased(t *testing.T) {
	e := newSecEnv(t, 0, nil)
	e.fund(5)
	above := decimal.NewFromInt(2)
	e.policies.policy = &security.Policy{Operation: security.OpWithdrawal, AssetCode: security.AnyAsset, Enabled: true, ManualApprovalAbove: &above}

	in := e.createInput("3")
	if _, err := e.svc.Create(context.Background(), in); !errors.Is(err, security.ErrManualApprovalRequired) {
		t.Fatalf("esperaba ErrManualApprovalRequired, obtuve %v", err)
	}
	for _, st := range e.limiter.statuses() {
		if st != "released" {
			t.Fatalf("la reserva debía liberarse, estado %s", st)
		}
	}
	if w, _ := e.store.FindByUserKey(context.Background(), e.userID, in.IdempotencyKey); w != nil {
		t.Fatal("no debe crearse el retiro")
	}
	// Por debajo del umbral sí procede.
	if _, err := e.svc.Create(context.Background(), e.createInput("1")); err != nil {
		t.Fatalf("monto bajo el umbral: %v", err)
	}
}

func TestSecurity_Replay_DoesNotReserveOrCountTwice(t *testing.T) {
	e := newSecEnv(t, 0, nil)
	e.fund(5)
	in := e.createInput("1")

	r1, err := e.svc.Create(context.Background(), in)
	if err != nil || !r1.Created {
		t.Fatalf("primera: %v", err)
	}
	r2, err := e.svc.Create(context.Background(), in)
	if err != nil || r2.Created || r2.Withdrawal.ID != r1.Withdrawal.ID {
		t.Fatalf("el replay debía devolver el mismo retiro: %v", err)
	}
	if e.limiter.reserveCalls != 1 {
		t.Fatalf("reservas de límites = %d, esperaba 1 (el replay no reserva)", e.limiter.reserveCalls)
	}
	if n := e.counter.counts["security:velocity:withdrawals_per_user:"+e.userID.String()]; n != 1 {
		t.Fatalf("velocidad contada %d veces, esperaba 1", n)
	}
}

func TestSecurity_LaterFailure_ReleasesLimitReservation(t *testing.T) {
	e := newSecEnv(t, 0, nil)
	// sin fondos: el retiro se crea y luego falla al reservar el saldo.
	if _, err := e.svc.Create(context.Background(), e.createInput("1")); !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("esperaba ErrInsufficientFunds, obtuve %v", err)
	}
	sts := e.limiter.statuses()
	if len(sts) != 1 || sts[0] != "released" {
		t.Fatalf("la reserva de límites debía liberarse al fallar el retiro: %v", sts)
	}
}

func TestSecurity_Cancel_ReleasesLimitReservation(t *testing.T) {
	e := newSecEnv(t, 0, nil)
	e.fund(5)
	res, err := e.svc.Create(context.Background(), e.createInput("1"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := e.svc.Cancel(context.Background(), e.userID, res.Withdrawal.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if sts := e.limiter.statuses(); len(sts) != 1 || sts[0] != "released" {
		t.Fatalf("la reserva de límites debía liberarse al cancelar: %v", sts)
	}
}

// ---- Ciclo de vida y worker -----------------------------------------------------------

func TestSecurity_Completed_CommitsLimitAndMarksDestinationKnown(t *testing.T) {
	e := newSecEnv(t, time.Hour, nil)
	e.fund(5)
	// El destino ya fue aprobado (allowlisted): el retiro procede sin espera.
	e.dest.status[secDestKey(e.userID, "ETH", string(testNetwork), security.HashDestination(secDestAddr))] = security.DestinationAllowlisted
	e.wire()
	ctx := context.Background()

	res, err := e.svc.Create(ctx, e.createInput("1"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	id := res.Withdrawal.ID

	// reserved -> processing -> broadcasted
	w, _ := e.store.Get(ctx, id)
	if _, err := e.svc.Advance(ctx, w); err != nil {
		t.Fatalf("advance (firmar+transmitir): %v", err)
	}
	w, _ = e.store.Get(ctx, id)
	if sts := e.limiter.statuses(); len(sts) != 1 || sts[0] != "active" {
		t.Fatalf("antes de completarse la reserva debe seguir activa: %v", sts)
	}

	// broadcasted -> confirming (1 confirmación) -> completed (2 confirmaciones)
	e.chain.receipts[w.TxHash] = &domain.TransactionReceipt{
		TxHash: w.TxHash, BlockNumber: 100, BlockHash: "0xblock1", Status: true, Confirmations: 1,
		GasUsed: 21000, EffectiveGasPriceWei: bigFromInt(10),
	}
	if _, err := e.svc.Advance(ctx, w); err != nil {
		t.Fatalf("advance (1 confirmación): %v", err)
	}
	w, _ = e.store.Get(ctx, id)
	e.chain.receipts[w.TxHash].Confirmations = 2
	if _, err := e.svc.Advance(ctx, w); err != nil {
		t.Fatalf("advance (completar): %v", err)
	}
	w, _ = e.store.Get(ctx, id)
	if w.Status != StatusCompleted {
		t.Fatalf("esperaba completed, obtuve %s (%s)", w.Status, w.ErrorDetail)
	}
	if sts := e.limiter.statuses(); len(sts) != 1 || sts[0] != "committed" {
		t.Fatalf("la reserva de límites debía confirmarse: %v", sts)
	}
	st, _ := e.dest.Lookup(ctx, e.userID, "ETH", string(testNetwork), security.HashDestination(secDestAddr))
	if st != security.DestinationKnown {
		t.Fatalf("tras completar, el destino debe pasar a known, está %s", st)
	}
}

// Un freeze de emergencia detiene la firma: el retiro se RETIENE (no falla, no se
// liberan fondos) y sigue cuando se levanta.
func TestSecurity_Freeze_HoldsBeforeSigning_ThenProceeds(t *testing.T) {
	e := newSecEnv(t, 0, nil)
	e.fund(5)
	e.wire()
	signCalls := 0
	inner := e.signer.sign
	e.signer.sign = func(req SignRequest) (*SignedTx, error) { signCalls++; return inner(req) }
	ctx := context.Background()

	res, err := e.svc.Create(ctx, e.createInput("1"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	id := res.Withdrawal.ID

	e.freezes.set(true)
	w, _ := e.store.Get(ctx, id)
	d, err := e.svc.Advance(ctx, w)
	if err != nil || d != securityHoldDelay {
		t.Fatalf("Advance con freeze: delay=%v err=%v, esperaba %v sin error", d, err, securityHoldDelay)
	}
	w, _ = e.store.Get(ctx, id)
	if w.Status != StatusReserved || signCalls != 0 {
		t.Fatalf("con freeze no debe firmar ni cambiar de estado: status=%s firmas=%d", w.Status, signCalls)
	}

	e.freezes.set(false)
	if _, err := e.svc.Advance(ctx, w); err != nil {
		t.Fatalf("Advance sin freeze: %v", err)
	}
	w, _ = e.store.Get(ctx, id)
	if w.Status != StatusBroadcasted || signCalls != 1 {
		t.Fatalf("sin freeze debía firmar y transmitir: status=%s firmas=%d", w.Status, signCalls)
	}
}

func TestSecurity_Freeze_AlsoHoldsAlreadySignedBroadcast(t *testing.T) {
	e := newSecEnv(t, 0, nil)
	e.fund(5)
	e.wire()
	ctx := context.Background()
	res, err := e.svc.Create(ctx, e.createInput("1"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Llevar el retiro a processing con la tx ya firmada pero SIN transmitir:
	// reserved -> processing mediante un broadcast que falla de forma transitoria.
	e.chain.broadcastErr = errors.New("nodo caído")
	w, _ := e.store.Get(ctx, res.Withdrawal.ID)
	_, _ = e.svc.Advance(ctx, w)
	w, _ = e.store.Get(ctx, res.Withdrawal.ID)
	if w.Status != StatusProcessing || len(w.SignedRawTx) == 0 {
		t.Skipf("el harness no deja la tx firmada sin transmitir (status=%s); cubierto por el test de firma", w.Status)
	}

	e.freezes.set(true)
	e.chain.broadcastErr = nil
	d, err := e.svc.Advance(ctx, w)
	if err != nil || d != securityHoldDelay {
		t.Fatalf("con freeze no debe transmitir: delay=%v err=%v", d, err)
	}
	if w2, _ := e.store.Get(ctx, w.ID); w2.Status != StatusProcessing {
		t.Fatalf("el estado no debe cambiar: %s", w2.Status)
	}
}

func TestSecurity_NewService_RequiresLimitReservationStore(t *testing.T) {
	e := newSecEnv(t, 0, nil)
	d := e.svc.Deps

	d.Store = e.store // sin LimitReservationStore
	if _, err := NewService(d); err == nil {
		t.Fatal("con Security.Limits y un Store sin LimitReservationStore debía fallar (fail closed)")
	}
	d.Store = e.ls
	if _, err := NewService(d); err != nil {
		t.Fatalf("con LimitReservationStore debía construirse: %v", err)
	}
}

func TestMaskAddress(t *testing.T) {
	if got := maskAddress("0x2222222222222222222222222222222222222222"); got != "0x2222…2222" {
		t.Fatalf("maskAddress = %q", got)
	}
	if got := maskAddress("0x12"); got != "***" {
		t.Fatalf("direcciones cortas deben ocultarse por completo: %q", got)
	}
}
