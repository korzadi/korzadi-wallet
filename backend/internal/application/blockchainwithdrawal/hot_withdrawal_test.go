package blockchainwithdrawal

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/application/locking"
)

const (
	testHotAddr  = "0x3333333333333333333333333333333333333333"
	testUserAddr = "0x1111111111111111111111111111111111111111"
)

func hotHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.svc.HotWallets = &fakeHotBook{addrs: map[string]string{string(testNetwork) + "|ETH": testHotAddr}}
	return h
}

func TestHot_Create_UsesHotWalletNotUserDepositAddress(t *testing.T) {
	h := hotHarness(t)
	userAcc, _ := h.accounts.FindOrCreateUserAccount(context.Background(), h.userID, "ETH")
	h.ledger.fund(userAcc, decimal.NewFromInt(5))

	res, err := h.svc.Create(context.Background(), h.createInput("1"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	from := res.Withdrawal.FromAddress
	if !strings.EqualFold(from, testHotAddr) {
		t.Fatalf("el origen debe ser la wallet HOT, es %s", from)
	}
	if strings.EqualFold(from, testUserAddr) {
		t.Fatal("un retiro nunca debe salir de la dirección de depósito del usuario")
	}
}

func TestHot_Create_NoHotWallet_FailsClosedBeforeReserving(t *testing.T) {
	h := newHarness(t)
	h.svc.HotWallets = &fakeHotBook{addrs: map[string]string{}} // none registered
	userAcc, _ := h.accounts.FindOrCreateUserAccount(context.Background(), h.userID, "ETH")
	h.ledger.fund(userAcc, decimal.NewFromInt(5))

	_, err := h.svc.Create(context.Background(), h.createInput("1"))
	if !errors.Is(err, ErrNoHotWallet) {
		t.Fatalf("esperaba ErrNoHotWallet, obtuve %v", err)
	}
	if len(h.store.byID) != 0 {
		t.Fatal("no debe crearse ningún retiro sin wallet HOT")
	}
}

// The core Camino B guarantee: two withdrawals must never be approved against
// the same on-chain balance. HOT holds 3 ETH; the first withdrawal (2 ETH) is
// signed and in flight; the second (2 ETH) must NOT be approved because only
// 1 ETH is left once the first is accounted for.
func TestHot_InFlightWithdrawalIsSubtractedFromOnChainBalance(t *testing.T) {
	h := hotHarness(t)
	h.chain.nativeBalance = decimal.NewFromInt(3)
	userAcc, _ := h.accounts.FindOrCreateUserAccount(context.Background(), h.userID, "ETH")
	h.ledger.fund(userAcc, decimal.NewFromInt(10))
	h.chain.broadcastHash = "0xaaaa"
	h.signer.sign = func(req SignRequest) (*SignedTx, error) {
		return &SignedTx{Raw: []byte{0x01, 0x02}, TxHash: h.chain.broadcastHash, Nonce: req.Nonce}, nil
	}

	r1, err := h.svc.Create(context.Background(), h.createInput("2"))
	if err != nil {
		t.Fatalf("create 1: %v", err)
	}
	w1, _ := h.store.Get(context.Background(), r1.Withdrawal.ID)
	if _, err := h.svc.Advance(context.Background(), w1); err != nil {
		t.Fatalf("advance 1: %v", err)
	}
	w1, _ = h.store.Get(context.Background(), r1.Withdrawal.ID)
	if w1.Status != StatusBroadcasted {
		t.Fatalf("el primer retiro debía quedar broadcasted, está %s (%s)", w1.Status, w1.ErrorDetail)
	}

	r2, err := h.svc.Create(context.Background(), h.createInput("2"))
	if err != nil {
		t.Fatalf("create 2: %v", err)
	}
	w2, _ := h.store.Get(context.Background(), r2.Withdrawal.ID)
	if _, err := h.svc.Advance(context.Background(), w2); err != nil {
		t.Fatalf("advance 2: %v", err)
	}
	w2, _ = h.store.Get(context.Background(), r2.Withdrawal.ID)
	if w2.Status != StatusFailed || !strings.Contains(w2.ErrorDetail, "insufficient_onchain_funds") {
		t.Fatalf("el segundo retiro debía fallar por fondos on-chain insuficientes, está %s (%s)", w2.Status, w2.ErrorDetail)
	}
	if len(w2.SignedRawTx) > 0 {
		t.Fatal("no debe firmarse una transacción sin fondos")
	}
}

func TestHot_SecondWithdrawalFitsWhenBalanceCoversBoth(t *testing.T) {
	h := hotHarness(t)
	h.chain.nativeBalance = decimal.NewFromInt(10)
	userAcc, _ := h.accounts.FindOrCreateUserAccount(context.Background(), h.userID, "ETH")
	h.ledger.fund(userAcc, decimal.NewFromInt(10))
	n := 0
	h.signer.sign = func(req SignRequest) (*SignedTx, error) {
		n++
		return &SignedTx{Raw: []byte{0x01, byte(n)}, TxHash: "0xhash" + string(rune('a'+n)), Nonce: req.Nonce}, nil
	}
	for i := 0; i < 2; i++ {
		r, err := h.svc.Create(context.Background(), h.createInput("2"))
		if err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		w, _ := h.store.Get(context.Background(), r.Withdrawal.ID)
		h.chain.broadcastHash = "0xhash" + string(rune('a'+i+1))
		if _, err := h.svc.Advance(context.Background(), w); err != nil {
			t.Fatalf("advance %d: %v", i, err)
		}
		w, _ = h.store.Get(context.Background(), r.Withdrawal.ID)
		if w.Status != StatusBroadcasted {
			t.Fatalf("retiro %d: esperaba broadcasted, está %s (%s)", i, w.Status, w.ErrorDetail)
		}
	}
}

type bareStore struct{ Store } // hides SumInFlight on purpose

func TestNewService_HotWalletsRequireInFlightReader(t *testing.T) {
	h := newHarness(t)
	d := h.svc.Deps
	d.Store = bareStore{h.store}
	d.HotWallets = &fakeHotBook{}
	if _, err := NewService(d); err == nil {
		t.Fatal("con HotWallets, un Store sin InFlightReader debe rechazarse al arrancar")
	}
}

func TestLockHot_SerialisesTheSameAddressCaseInsensitively(t *testing.T) {
	h := newHarness(t)
	var inside, maxInside int32
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		addr := "0xABCDEF"
		if i%2 == 0 {
			addr = "0xabcdef"
		}
		go func() {
			defer wg.Done()
			unlock, err := h.svc.lockHot(context.Background(), testNetwork, addr)
			if err != nil {
				t.Errorf("lockHot: %v", err)
				return
			}
			n := atomic.AddInt32(&inside, 1)
			if n > atomic.LoadInt32(&maxInside) {
				atomic.StoreInt32(&maxInside, n)
			}
			time.Sleep(time.Millisecond)
			atomic.AddInt32(&inside, -1)
			unlock()
		}()
	}
	wg.Wait()
	if maxInside != 1 {
		t.Fatalf("la sección crítica debe ser exclusiva, se vieron %d a la vez", maxInside)
	}
}

func TestNewService_HotWalletsRequireLocker(t *testing.T) {
	h := newHarness(t)
	d := h.svc.Deps
	d.HotWallets = &fakeHotBook{}
	d.Locks = nil
	if _, err := NewService(d); err == nil {
		t.Fatal("con HotWallets y sin Locks debe rechazarse al arrancar (fail closed)")
	}
}

func TestLockHot_NoLockerFailsClosed(t *testing.T) {
	h := newHarness(t)
	h.svc.Locks = nil
	if _, err := h.svc.lockHot(context.Background(), testNetwork, testHotAddr); !errors.Is(err, ErrNoLocker) {
		t.Fatalf("esperaba ErrNoLocker, obtuve %v", err)
	}
}

func TestLockHot_BusyWhenAnotherInstanceHoldsIt(t *testing.T) {
	h := newHarness(t)
	h.svc.HotLockWait = 30 * time.Millisecond
	unlockOther, err := h.svc.Locks.Lock(context.Background(), locking.HotKey(string(testNetwork), testHotAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer unlockOther()
	if _, err := h.svc.lockHot(context.Background(), testNetwork, strings.ToUpper(testHotAddr)); !errors.Is(err, ErrHotLockBusy) {
		t.Fatalf("esperaba ErrHotLockBusy, obtuve %v", err)
	}
}

// Dos instancias simuladas (dos Service con distinto InstanceID) comparten la
// misma "base de datos" (store, nonces, ledger) y el mismo Locker (= el
// pg_advisory_lock compartido). Seis retiros de 1 ETH contra una HOT con 3.5
// ETH: solo tres pueden firmarse; nunca se firma dos veces el mismo retiro ni
// se reparte el mismo nonce dos veces.
func TestTwoInstances_ConcurrentWorkers_NeverOverCommitHotWallet(t *testing.T) {
	h := hotHarness(t)
	h.chain.nativeBalance = decimal.RequireFromString("3.5")
	h.chain.echoHash = true
	userAcc, _ := h.accounts.FindOrCreateUserAccount(context.Background(), h.userID, "ETH")
	h.ledger.fund(userAcc, decimal.NewFromInt(50))

	var signCalls int32
	h.signer.sign = func(req SignRequest) (*SignedTx, error) {
		n := atomic.AddInt32(&signCalls, 1)
		raw := []byte{0xAA, byte(n), byte(req.Nonce)}
		return &SignedTx{Raw: raw, TxHash: "0x" + hex.EncodeToString(raw), Nonce: req.Nonce}, nil
	}

	const total = 6
	ids := make([]uuid.UUID, 0, total)
	for i := 0; i < total; i++ {
		r, err := h.svc.Create(context.Background(), h.createInput("1"))
		if err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		ids = append(ids, r.Withdrawal.ID)
	}
	// Create deja el lease a nombre del creador: liberarlo para que los
	// workers de ambas instancias compitan por reclamarlos.
	h.store.mu.Lock()
	for _, w := range h.store.byID {
		w.LockedBy = ""
	}
	h.store.mu.Unlock()

	// Lotes pequeños: cada instancia reclama la mitad y ambas firman a la vez,
	// compitiendo por el lock de la misma HOT.
	h.svc.Config.BatchSize = 3
	depsB := h.svc.Deps
	depsB.InstanceID = "wd-instance-B"
	svcB, err := NewService(depsB)
	if err != nil {
		t.Fatalf("segunda instancia: %v", err)
	}
	workerA, workerB := NewWorker(h.svc), NewWorker(svcB)

	for round := 0; round < 10; round++ {
		var wg sync.WaitGroup
		for _, wk := range []*Worker{workerA, workerB} {
			wg.Add(1)
			go func(wk *Worker) {
				defer wg.Done()
				if _, err := wk.RunOnce(context.Background()); err != nil {
					t.Errorf("RunOnce: %v", err)
				}
			}(wk)
		}
		wg.Wait()
	}

	signed, failedFunds := 0, 0
	nonces := map[int64]bool{}
	for _, id := range ids {
		w, _ := h.store.Get(context.Background(), id)
		if len(w.SignedRawTx) > 0 {
			signed++
			if w.Nonce == nil {
				t.Fatalf("retiro %s firmado sin nonce", id)
			}
			if nonces[*w.Nonce] {
				t.Fatalf("el nonce %d se asignó a dos retiros", *w.Nonce)
			}
			nonces[*w.Nonce] = true
		}
		if w.Status == StatusFailed && strings.Contains(w.ErrorDetail, "insufficient_onchain_funds") {
			failedFunds++
		}
	}
	if signed != 3 {
		t.Fatalf("con 3.5 ETH en la HOT solo 3 retiros de 1 ETH pueden firmarse, se firmaron %d", signed)
	}
	if failedFunds != total-3 {
		t.Fatalf("los otros %d debían fallar por fondos on-chain insuficientes, fallaron %d", total-3, failedFunds)
	}
	if got := atomic.LoadInt32(&signCalls); got != 3 {
		t.Fatalf("el firmante debía invocarse una vez por retiro firmado (3), se invocó %d", got)
	}
}
