package security

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
)

// ---------------------------------------------------------------------
// fakes
// ---------------------------------------------------------------------

// fakeLimiter is an in-memory stand-in for postgres.SecurityLimitRepository.
// It reimplements the same reserve/commit/release contract (policy check,
// daily+monthly usage, replay protection) behind a single mutex, standing
// in for Postgres row locking — good enough to prove the CONTRACT
// (concurrent over-limit reservations cannot both succeed) without a real
// database. It is not a substitute for an integration test against
// Postgres itself, which should additionally verify the actual SQL.
type fakeLimiter struct {
	mu           sync.Mutex
	policies     map[OperationType]*Policy
	usage        map[string]decimal.Decimal // key: user|op|asset|periodType|periodKey
	count        map[string]int
	reservations map[uuid.UUID]*fakeReservation
	byReference  map[string]uuid.UUID // operation|referenceID -> active reservation ID
}

type fakeReservation struct {
	res          Reservation
	dailyKey     string
	monthlyKey   string
	status       string // active | committed | released
	referenceKey string
}

func newFakeLimiter() *fakeLimiter {
	return &fakeLimiter{
		policies:     map[OperationType]*Policy{},
		usage:        map[string]decimal.Decimal{},
		count:        map[string]int{},
		reservations: map[uuid.UUID]*fakeReservation{},
		byReference:  map[string]uuid.UUID{},
	}
}

func usageKey(userID uuid.UUID, op OperationType, asset, periodType, periodKey string) string {
	return fmt.Sprintf("%s|%s|%s|%s|%s", userID, op, asset, periodType, periodKey)
}

func (f *fakeLimiter) Reserve(ctx context.Context, in ReserveInput) (*Reservation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if !in.Amount.IsPositive() {
		return nil, fmt.Errorf("security: el monto a reservar debe ser positivo")
	}
	assetCode := in.AssetCode
	if assetCode == "" {
		assetCode = AnyAsset
	}

	if in.ReferenceID != "" {
		refKey := string(in.Operation) + "|" + in.ReferenceID
		if _, exists := f.byReference[refKey]; exists {
			return nil, ErrReplay
		}
	}

	policy := f.policies[in.Operation]
	if policy != nil {
		if !policy.Enabled {
			return nil, ErrOperationDisabled
		}
		if in.Amount.LessThan(policy.MinAmount) {
			return nil, ErrBelowMinimum
		}
		if policy.MaxAmount != nil && in.Amount.GreaterThan(*policy.MaxAmount) {
			return nil, ErrAboveMaximum
		}
	}

	dailyKey, monthlyKey := time.Now().UTC().Format("2006-01-02"), time.Now().UTC().Format("2006-01")
	dUK := usageKey(in.UserID, in.Operation, assetCode, "daily", dailyKey)
	mUK := usageKey(in.UserID, in.Operation, assetCode, "monthly", monthlyKey)

	if policy != nil {
		if policy.DailyLimit != nil && f.usage[dUK].Add(in.Amount).GreaterThan(*policy.DailyLimit) {
			return nil, ErrDailyLimitExceeded
		}
		if policy.DailyCountLimit != nil && f.count[dUK]+1 > *policy.DailyCountLimit {
			return nil, ErrDailyCountExceeded
		}
		if policy.MonthlyLimit != nil && f.usage[mUK].Add(in.Amount).GreaterThan(*policy.MonthlyLimit) {
			return nil, ErrMonthlyLimitExceeded
		}
		if policy.MonthlyCountLimit != nil && f.count[mUK]+1 > *policy.MonthlyCountLimit {
			return nil, ErrMonthlyCountExceeded
		}
	}

	f.usage[dUK] = f.usage[dUK].Add(in.Amount)
	f.count[dUK]++
	f.usage[mUK] = f.usage[mUK].Add(in.Amount)
	f.count[mUK]++

	id := uuid.New()
	fr := &fakeReservation{
		res:      Reservation{ID: id, UserID: in.UserID, Operation: in.Operation, AssetCode: assetCode, Amount: in.Amount},
		dailyKey: dailyKey, monthlyKey: monthlyKey, status: "active",
	}
	f.reservations[id] = fr
	if in.ReferenceID != "" {
		refKey := string(in.Operation) + "|" + in.ReferenceID
		fr.referenceKey = refKey
		f.byReference[refKey] = id
	}
	out := fr.res
	return &out, nil
}

func (f *fakeLimiter) Commit(ctx context.Context, reservationID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	fr, ok := f.reservations[reservationID]
	if !ok {
		return ErrReservationNotFound
	}
	if fr.status != "active" {
		return ErrReservationNotActive
	}
	fr.status = "committed"
	return nil
}

func (f *fakeLimiter) Release(ctx context.Context, reservationID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	fr, ok := f.reservations[reservationID]
	if !ok {
		return ErrReservationNotFound
	}
	if fr.status != "active" {
		return ErrReservationNotActive
	}
	dUK := usageKey(fr.res.UserID, fr.res.Operation, fr.res.AssetCode, "daily", fr.dailyKey)
	mUK := usageKey(fr.res.UserID, fr.res.Operation, fr.res.AssetCode, "monthly", fr.monthlyKey)
	f.usage[dUK] = f.usage[dUK].Sub(fr.res.Amount)
	f.count[dUK]--
	f.usage[mUK] = f.usage[mUK].Sub(fr.res.Amount)
	f.count[mUK]--
	fr.status = "released"
	if fr.referenceKey != "" {
		delete(f.byReference, fr.referenceKey)
	}
	return nil
}

type fakeRoles struct{ roles map[uuid.UUID][]string }

func (f *fakeRoles) GetRolesForUser(ctx context.Context, userID uuid.UUID) ([]string, error) {
	return f.roles[userID], nil
}

type fakeUsers struct{ users map[uuid.UUID]*domain.User }

func (f *fakeUsers) FindByID(ctx context.Context, id uuid.UUID) (*domain.User, error) {
	return f.users[id], nil
}

type fakeTwoFactor struct{ validCode string }

func (f *fakeTwoFactor) Verify(ctx context.Context, userID uuid.UUID, code string) (bool, error) {
	return code == f.validCode, nil
}

type fakeFreezeRepo struct {
	mu    sync.Mutex
	state map[OperationType]FreezeStatus
}

func newFakeFreezeRepo() *fakeFreezeRepo {
	return &fakeFreezeRepo{state: map[OperationType]FreezeStatus{}}
}

func (f *fakeFreezeRepo) Get(ctx context.Context, op OperationType) (FreezeStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state[op], nil
}
func (f *fakeFreezeRepo) List(ctx context.Context) ([]FreezeStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []FreezeStatus
	for _, s := range f.state {
		out = append(out, s)
	}
	return out, nil
}
func (f *fakeFreezeRepo) Set(ctx context.Context, op OperationType, frozen bool, reason string, actorID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state[op] = FreezeStatus{Operation: op, Frozen: frozen, Reason: reason, FrozenBy: &actorID}
	return nil
}

type fakeCounter struct {
	mu     sync.Mutex
	counts map[string]int64
}

func newFakeCounter() *fakeCounter { return &fakeCounter{counts: map[string]int64{}} }

func (f *fakeCounter) Incr(ctx context.Context, key string, window time.Duration) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counts[key]++
	return f.counts[key], nil
}

type fakeDestinationRepo struct {
	mu     sync.Mutex
	status map[string]DestinationStatus
}

func newFakeDestinationRepo() *fakeDestinationRepo {
	return &fakeDestinationRepo{status: map[string]DestinationStatus{}}
}
func destKey(userID uuid.UUID, assetCode, network, hash string) string {
	return fmt.Sprintf("%s|%s|%s|%s", userID, assetCode, network, hash)
}
func (f *fakeDestinationRepo) Lookup(ctx context.Context, userID uuid.UUID, assetCode, network, hash string) (DestinationStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.status[destKey(userID, assetCode, network, hash)]; ok {
		return s, nil
	}
	return DestinationNew, nil
}
func (f *fakeDestinationRepo) RecordUse(ctx context.Context, userID uuid.UUID, assetCode, network, hash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status[destKey(userID, assetCode, network, hash)] = DestinationKnown
	return nil
}
func (f *fakeDestinationRepo) SetStatus(ctx context.Context, userID uuid.UUID, assetCode, network, hash string, status DestinationStatus) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status[destKey(userID, assetCode, network, hash)] = status
	return nil
}

// ---------------------------------------------------------------------
// Limits — concurrency (spec §5, §26, §27)
// ---------------------------------------------------------------------

func TestLimitService_ConcurrentReservationsRespectDailyLimit(t *testing.T) {
	limiter := newFakeLimiter()
	dailyLimit := decimal.NewFromInt(1000)
	limiter.policies[OpTransfer] = &Policy{Operation: OpTransfer, MinAmount: decimal.Zero, DailyLimit: &dailyLimit, Enabled: true}
	svc := NewLimitService(limiter, nil)

	userID := uuid.New()
	amount := decimal.NewFromInt(700)

	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.Reserve(context.Background(), ReserveInput{
				UserID: userID, Operation: OpTransfer, AssetCode: "USDT", Amount: amount,
			}, "127.0.0.1")
			results <- err
		}()
	}
	wg.Wait()
	close(results)

	var succeeded, rejected int
	for err := range results {
		if err == nil {
			succeeded++
		} else if errors.Is(err, ErrDailyLimitExceeded) {
			rejected++
		} else {
			t.Fatalf("error inesperado: %v", err)
		}
	}
	if succeeded != 1 || rejected != 1 {
		t.Fatalf("esperaba exactamente 1 aprobada y 1 rechazada (daily_limit=1000, dos intentos de 700), obtuve %d aprobadas y %d rechazadas", succeeded, rejected)
	}
}

func TestLimitService_MinMaxEnforced(t *testing.T) {
	limiter := newFakeLimiter()
	minAmt := decimal.NewFromInt(10)
	maxAmt := decimal.NewFromInt(500)
	limiter.policies[OpWithdrawal] = &Policy{Operation: OpWithdrawal, MinAmount: minAmt, MaxAmount: &maxAmt, Enabled: true}
	svc := NewLimitService(limiter, nil)
	userID := uuid.New()

	cases := []struct {
		name   string
		amount decimal.Decimal
		want   error
	}{
		{"below_minimum", decimal.NewFromInt(1), ErrBelowMinimum},
		{"exactly_minimum", minAmt, nil},
		{"exactly_maximum", maxAmt, nil},
		{"above_maximum", decimal.NewFromInt(501), ErrAboveMaximum},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Reserve(context.Background(), ReserveInput{
				UserID: userID, Operation: OpWithdrawal, AssetCode: "USDT", Amount: tc.amount,
			}, "")
			if tc.want == nil && err != nil {
				t.Fatalf("esperaba éxito, obtuve %v", err)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("esperaba %v, obtuve %v", tc.want, err)
			}
		})
	}
}

func TestLimitService_ReplayProtection(t *testing.T) {
	limiter := newFakeLimiter()
	svc := NewLimitService(limiter, nil)
	userID := uuid.New()
	in := ReserveInput{UserID: userID, Operation: OpTransfer, AssetCode: "USDT", Amount: decimal.NewFromInt(10), ReferenceID: "idem-123"}

	if _, err := svc.Reserve(context.Background(), in, ""); err != nil {
		t.Fatalf("primera reserva no debería fallar: %v", err)
	}
	if _, err := svc.Reserve(context.Background(), in, ""); !errors.Is(err, ErrReplay) {
		t.Fatalf("esperaba ErrReplay en la segunda reserva con la misma reference_id, obtuve %v", err)
	}
}

func TestLimitService_ReleaseReturnsAllowance(t *testing.T) {
	limiter := newFakeLimiter()
	dailyLimit := decimal.NewFromInt(100)
	limiter.policies[OpTransfer] = &Policy{Operation: OpTransfer, DailyLimit: &dailyLimit, Enabled: true}
	svc := NewLimitService(limiter, nil)
	userID := uuid.New()

	res, err := svc.Reserve(context.Background(), ReserveInput{UserID: userID, Operation: OpTransfer, AssetCode: "USDT", Amount: decimal.NewFromInt(80)}, "")
	if err != nil {
		t.Fatalf("reserva inicial falló: %v", err)
	}
	// A second 80 should fail while the first is still active/committed.
	if _, err := svc.Reserve(context.Background(), ReserveInput{UserID: userID, Operation: OpTransfer, AssetCode: "USDT", Amount: decimal.NewFromInt(80)}, ""); !errors.Is(err, ErrDailyLimitExceeded) {
		t.Fatalf("esperaba ErrDailyLimitExceeded, obtuve %v", err)
	}
	if err := svc.Release(context.Background(), res.ID); err != nil {
		t.Fatalf("release falló: %v", err)
	}
	// After releasing, the allowance should be available again.
	if _, err := svc.Reserve(context.Background(), ReserveInput{UserID: userID, Operation: OpTransfer, AssetCode: "USDT", Amount: decimal.NewFromInt(80)}, ""); err != nil {
		t.Fatalf("esperaba éxito tras liberar la reserva anterior, obtuve %v", err)
	}
}

func TestLimitService_CommitThenReleaseFails(t *testing.T) {
	limiter := newFakeLimiter()
	svc := NewLimitService(limiter, nil)
	res, err := svc.Reserve(context.Background(), ReserveInput{UserID: uuid.New(), Operation: OpTransfer, AssetCode: "USDT", Amount: decimal.NewFromInt(1)}, "")
	if err != nil {
		t.Fatalf("reserva falló: %v", err)
	}
	if err := svc.Commit(context.Background(), res.ID); err != nil {
		t.Fatalf("commit falló: %v", err)
	}
	if err := svc.Release(context.Background(), res.ID); !errors.Is(err, ErrReservationNotActive) {
		t.Fatalf("esperaba ErrReservationNotActive al liberar una reserva ya confirmada, obtuve %v", err)
	}
}

// ---------------------------------------------------------------------
// Authorization (spec §10, §26, §27)
// ---------------------------------------------------------------------

func TestGuard_RequireRole(t *testing.T) {
	userID := uuid.New()
	roles := &fakeRoles{roles: map[uuid.UUID][]string{userID: {"user"}}}
	g := NewGuard(roles, &fakeUsers{users: map[uuid.UUID]*domain.User{}}, nil)

	if err := g.RequireRole(context.Background(), userID, "", "admin", "super_admin"); !errors.Is(err, ErrAuthorizationDenied) {
		t.Fatalf("un usuario sin rol admin no debería pasar RequireRole(admin), obtuve %v", err)
	}
	if err := g.RequireRole(context.Background(), userID, "", "user"); err != nil {
		t.Fatalf("un usuario con rol 'user' debería pasar RequireRole(user), obtuve %v", err)
	}
}

func TestGuard_RequireActiveAccount(t *testing.T) {
	activeID, blockedID, missingID := uuid.New(), uuid.New(), uuid.New()
	users := &fakeUsers{users: map[uuid.UUID]*domain.User{
		activeID:  {ID: activeID, Status: domain.UserStatusActive},
		blockedID: {ID: blockedID, Status: domain.UserStatus("blocked")},
	}}
	g := NewGuard(&fakeRoles{roles: map[uuid.UUID][]string{}}, users, nil)

	if err := g.RequireActiveAccount(context.Background(), activeID); err != nil {
		t.Fatalf("cuenta activa no debería fallar: %v", err)
	}
	if err := g.RequireActiveAccount(context.Background(), blockedID); err == nil {
		t.Fatal("cuenta bloqueada debería fallar")
	}
	if err := g.RequireActiveAccount(context.Background(), missingID); !errors.Is(err, domain.ErrUserNotFound) {
		t.Fatalf("usuario inexistente debería dar ErrUserNotFound, obtuve %v", err)
	}
}

func TestGuard_CheckOwnership_IDOR(t *testing.T) {
	g := NewGuard(&fakeRoles{roles: map[uuid.UUID][]string{}}, &fakeUsers{users: map[uuid.UUID]*domain.User{}}, nil)
	owner, requester := uuid.New(), uuid.New()

	if err := g.CheckOwnership(context.Background(), owner, owner, "transfer", nil, ""); err != nil {
		t.Fatalf("el propio dueño no debería ser rechazado: %v", err)
	}
	if err := g.CheckOwnership(context.Background(), requester, owner, "transfer", nil, ""); !errors.Is(err, ErrAuthorizationDenied) {
		t.Fatalf("un usuario distinto al dueño debería ser rechazado (IDOR), obtuve %v", err)
	}
}

// ---------------------------------------------------------------------
// Freeze / kill switch (spec §20, §21)
// ---------------------------------------------------------------------

func TestFreezeGuard_KillSwitchAndFreeze(t *testing.T) {
	freezeRepo := newFakeFreezeRepo()
	cfg := DefaultConfig()
	cfg.TransfersEnabled = false
	guard := NewFreezeGuard(freezeRepo, cfg, nil)
	uid := uuid.New()

	if err := guard.CheckEnabled(context.Background(), OpTransfer, &uid, ""); !errors.Is(err, ErrKillSwitchOff) {
		t.Fatalf("con SECURITY_TRANSFERS_ENABLED=false, esperaba ErrKillSwitchOff, obtuve %v", err)
	}

	cfg.TransfersEnabled = true
	guard = NewFreezeGuard(freezeRepo, cfg, nil)
	if err := guard.CheckEnabled(context.Background(), OpTransfer, &uid, ""); err != nil {
		t.Fatalf("sin freeze ni kill switch, no debería fallar: %v", err)
	}

	_ = freezeRepo.Set(context.Background(), OpTransfer, true, "incidente de seguridad", uuid.New())
	if err := guard.CheckEnabled(context.Background(), OpTransfer, &uid, ""); !errors.Is(err, ErrOperationFrozen) {
		t.Fatalf("con freeze activo, esperaba ErrOperationFrozen, obtuve %v", err)
	}

	_ = freezeRepo.Set(context.Background(), OpTransfer, false, "", uuid.New())
	if err := guard.CheckEnabled(context.Background(), OpTransfer, &uid, ""); err != nil {
		t.Fatalf("tras des-congelar, no debería fallar: %v", err)
	}
}

// ---------------------------------------------------------------------
// Step-up (spec §12, §13)
// ---------------------------------------------------------------------

func TestStepUpGuard_RequireForAmount(t *testing.T) {
	threshold := decimal.NewFromInt(1000)
	policy := &Policy{StepUpThreshold: &threshold}
	guard := NewStepUpGuard(&fakeTwoFactor{validCode: "123456"}, nil)
	userID := uuid.New()

	// Below threshold: no code needed at all.
	if err := guard.RequireForAmount(context.Background(), policy, decimal.NewFromInt(500), userID, "", ""); err != nil {
		t.Fatalf("monto bajo el umbral no debería requerir 2FA: %v", err)
	}
	// Above threshold, no code: must fail.
	if err := guard.RequireForAmount(context.Background(), policy, decimal.NewFromInt(1500), userID, "", ""); !errors.Is(err, domain.Err2FARequired) {
		t.Fatalf("monto sobre el umbral sin código debería exigir 2FA, obtuve %v", err)
	}
	// Above threshold, wrong code: must fail differently.
	if err := guard.RequireForAmount(context.Background(), policy, decimal.NewFromInt(1500), userID, "000000", ""); !errors.Is(err, ErrStepUpInvalid) {
		t.Fatalf("código incorrecto debería dar ErrStepUpInvalid, obtuve %v", err)
	}
	// Above threshold, correct code: must pass.
	if err := guard.RequireForAmount(context.Background(), policy, decimal.NewFromInt(1500), userID, "123456", ""); err != nil {
		t.Fatalf("código correcto sobre el umbral no debería fallar: %v", err)
	}
}

// ---------------------------------------------------------------------
// Config / secret validation (spec §19)
// ---------------------------------------------------------------------

func TestValidateSecrets_NeverLeaksValue(t *testing.T) {
	secretValue := "correct-horse-battery-staple-super-secret"
	err := ValidateSecrets(
		RequiredSecret{Name: "JWT_SECRET", Value: "", MinLength: 16},
		RequiredSecret{Name: "TOO_SHORT", Value: "abc", MinLength: 16},
		RequiredSecret{Name: "PLACEHOLDER", Value: "changeme"},
		RequiredSecret{Name: "GOOD_SECRET", Value: secretValue, MinLength: 16},
	)
	if err == nil {
		t.Fatal("esperaba error por secretos faltantes/inválidos")
	}
	msg := err.Error()
	if contains(msg, secretValue) {
		t.Fatalf("el error NUNCA debe contener el valor de un secreto: %q", msg)
	}
	for _, want := range []string{"JWT_SECRET", "TOO_SHORT", "PLACEHOLDER", "missing", "invalid", "misconfigured"} {
		if !contains(msg, want) {
			t.Fatalf("esperaba que el mensaje mencionara %q: %q", want, msg)
		}
	}
}

func TestValidateSecrets_AllGood(t *testing.T) {
	if err := ValidateSecrets(RequiredSecret{Name: "OK", Value: "a-fine-secret-value", MinLength: 8}); err != nil {
		t.Fatalf("no debería fallar: %v", err)
	}
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

// ---------------------------------------------------------------------
// Velocity (spec §14)
// ---------------------------------------------------------------------

func TestVelocityTracker_Exceeded(t *testing.T) {
	counter := newFakeCounter()
	rules := map[string]RateLimitRule{"withdrawals_per_user": {Limit: 2, Window: time.Minute}}
	v := NewVelocityTracker(counter, rules, nil)

	subject := "user-1"
	for i := 0; i < 2; i++ {
		exceeded, _, err := v.Record(context.Background(), "withdrawals_per_user", subject)
		if err != nil {
			t.Fatalf("Record falló: %v", err)
		}
		if exceeded {
			t.Fatalf("intento %d no debería exceder el límite de 2", i+1)
		}
	}
	exceeded, count, err := v.Record(context.Background(), "withdrawals_per_user", subject)
	if err != nil {
		t.Fatalf("Record falló: %v", err)
	}
	if !exceeded || count != 3 {
		t.Fatalf("el tercer intento debería exceder el límite (count=3), obtuve exceeded=%v count=%d", exceeded, count)
	}
}

func TestVelocityTracker_UnknownRuleNeverBlocks(t *testing.T) {
	v := NewVelocityTracker(newFakeCounter(), map[string]RateLimitRule{}, nil)
	exceeded, count, err := v.Record(context.Background(), "no_such_rule", "subject")
	if err != nil || exceeded || count != 0 {
		t.Fatalf("una regla desconocida nunca debe bloquear: exceeded=%v count=%d err=%v", exceeded, count, err)
	}
}

// ---------------------------------------------------------------------
// Destination controls (spec §15)
// ---------------------------------------------------------------------

func TestDestinationGuard_NewKnownBlocked(t *testing.T) {
	repo := newFakeDestinationRepo()
	guard := NewDestinationGuard(repo, nil)
	userID := uuid.New()

	status, err := guard.Check(context.Background(), userID, "USDT", "tron", "T-some-address", "")
	if err != nil || status != DestinationNew {
		t.Fatalf("primera vez debería ser 'new', obtuve status=%v err=%v", status, err)
	}

	if err := guard.RecordSuccessfulUse(context.Background(), userID, "USDT", "tron", "T-some-address"); err != nil {
		t.Fatalf("RecordSuccessfulUse falló: %v", err)
	}
	status, err = guard.Check(context.Background(), userID, "USDT", "tron", "T-some-address", "")
	if err != nil || status != DestinationKnown {
		t.Fatalf("tras usarla, debería ser 'known', obtuve status=%v err=%v", status, err)
	}

	_ = repo.SetStatus(context.Background(), userID, "USDT", "tron", HashDestination("T-bad-address"), DestinationBlocked)
	_, err = guard.Check(context.Background(), userID, "USDT", "tron", "T-bad-address", "")
	if !errors.Is(err, ErrDestinationBlocked) {
		t.Fatalf("destino bloqueado debería dar ErrDestinationBlocked, obtuve %v", err)
	}
}

// ---------------------------------------------------------------------
// Policy validation (spec §4)
// ---------------------------------------------------------------------

func TestValidatePolicyShape(t *testing.T) {
	maxAmt := decimal.NewFromInt(100)
	tooLowMax := decimal.NewFromInt(1) // below a min_amount of 5, should fail
	minAmt := decimal.NewFromInt(5)

	good := Policy{Operation: OpTransfer, MinAmount: decimal.Zero, MaxAmount: &maxAmt, Enabled: true}
	if err := ValidatePolicyShape(good); err != nil {
		t.Fatalf("política válida no debería fallar: %v", err)
	}

	bad := Policy{Operation: OpTransfer, MinAmount: minAmt, MaxAmount: &tooLowMax, Enabled: true}
	if err := ValidatePolicyShape(bad); err == nil {
		t.Fatal("min_amount > max_amount debería fallar la validación")
	}

	unknownOp := Policy{Operation: "NOT_A_REAL_OP", MinAmount: decimal.Zero}
	if err := ValidatePolicyShape(unknownOp); !errors.Is(err, ErrOperationUnknown) {
		t.Fatalf("operación desconocida debería dar ErrOperationUnknown, obtuve %v", err)
	}
}

// ---------------------------------------------------------------------
// Event metadata sanitization (spec §16, §17 — never store secrets)
// ---------------------------------------------------------------------

type fakeEventRepo struct {
	mu     sync.Mutex
	events []Event
}

func (f *fakeEventRepo) Insert(ctx context.Context, e Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, e)
	return nil
}
func (f *fakeEventRepo) List(ctx context.Context, userID *uuid.UUID, limit, offset int) ([]Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.events, nil
}

func TestEventLogger_StripsSecretLikeMetadata(t *testing.T) {
	repo := &fakeEventRepo{}
	logger := NewEventLogger(repo, nil)

	err := logger.Log(context.Background(), Event{
		Type: EventAdminAction,
		Metadata: map[string]any{
			"reason":      "cambio de límite",
			"private_key": "should-never-be-stored",
			"jwt":         "should-never-be-stored",
			"password":    "should-never-be-stored",
			"2fa_secret":  "should-never-be-stored",
			"seed":        "should-never-be-stored",
		},
	})
	if err != nil {
		t.Fatalf("Log falló: %v", err)
	}
	if len(repo.events) != 1 {
		t.Fatalf("esperaba 1 evento almacenado, obtuve %d", len(repo.events))
	}
	meta := repo.events[0].Metadata
	if _, ok := meta["reason"]; !ok {
		t.Fatal("el campo legítimo 'reason' no debería eliminarse")
	}
	for _, secretKey := range []string{"private_key", "jwt", "password", "2fa_secret", "seed"} {
		if _, ok := meta[secretKey]; ok {
			t.Fatalf("la clave %q nunca debe llegar a security_events", secretKey)
		}
	}
}
