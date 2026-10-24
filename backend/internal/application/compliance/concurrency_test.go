package compliance_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/application/compliance"
)

func TestConcurrency_DoubleStart(t *testing.T) {
	e := newEnv(t)
	u := uuid.New()
	var wg sync.WaitGroup
	ids := make(chan uuid.UUID, 16)
	var failures atomic.Int32
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := e.svc.StartVerification(context.Background(), compliance.StartInput{UserID: u, IdempotencyKey: key(), Country: "AR"})
			if err != nil {
				failures.Add(1)
				return
			}
			ids <- r.Case.ID
		}()
	}
	wg.Wait()
	close(ids)
	if failures.Load() != 0 {
		t.Fatalf("fallos=%d", failures.Load())
	}
	var first uuid.UUID
	for id := range ids {
		if first == uuid.Nil {
			first = id
		}
		if id != first {
			t.Fatal("todas las solicitudes deben resolver al mismo caso")
		}
	}
	all, _ := e.store.AllCases(context.Background(), u)
	if len(all) != 1 || e.prov.Created != 1 {
		t.Fatalf("casos=%d verificaciones en proveedor=%d", len(all), e.prov.Created)
	}
}

func TestConcurrency_DuplicateWebhookSameEvent(t *testing.T) {
	e := newEnv(t)
	u := uuid.New()
	c := e.start(u)
	var wg sync.WaitGroup
	var applied, dup atomic.Int32
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := e.hook("same", compliance.EvtInReview, c.ProviderReference, e.clock.Now(), "", nil)
			if err != nil {
				t.Errorf("err: %v", err)
				return
			}
			if res.Duplicate {
				dup.Add(1)
			} else {
				applied.Add(1)
			}
		}()
	}
	wg.Wait()
	if applied.Load() != 1 || dup.Load() != 11 {
		t.Fatalf("applied=%d dup=%d", applied.Load(), dup.Load())
	}
	if e.status(u) != compliance.KYCInReview {
		t.Fatal("estado")
	}
}

func TestConcurrency_TwoConflictingWebhooks(t *testing.T) {
	for i := 0; i < 25; i++ {
		e := newEnv(t)
		u := uuid.New()
		c := e.start(u)
		var wg sync.WaitGroup
		outcomes := make([]compliance.WebhookProcessing, 2)
		types := []string{compliance.EvtApproved, compliance.EvtRejected}
		for j := range types {
			wg.Add(1)
			go func(j int) {
				defer wg.Done()
				res, err := e.hook("evt-"+types[j], types[j], c.ProviderReference, time.Time{}, "R", nil)
				if err != nil {
					t.Errorf("err: %v", err)
					return
				}
				outcomes[j] = res.Processing
			}(j)
		}
		wg.Wait()
		applied := 0
		for _, o := range outcomes {
			if o == compliance.WebhookApplied {
				applied++
			}
		}
		if applied != 1 {
			t.Fatalf("iter %d: exactamente uno debe aplicarse, outcomes=%v", i, outcomes)
		}
		st := e.status(u)
		if st != compliance.KYCVerified && st != compliance.KYCRejected {
			t.Fatalf("estado final inconsistente: %s", st)
		}
		if n := e.store.HistoryCount(c.ID); n != 3 { // NOT_STARTED, PENDING, + exactly one decision
			t.Fatalf("history=%d", n)
		}
	}
}

func TestConcurrency_ApproveRejectRace(t *testing.T) {
	for i := 0; i < 25; i++ {
		e := newEnv(t)
		u := uuid.New()
		e.start(u)
		_, _ = e.svc.Review(context.Background(), reviewer(), u, nil, "D", "r", key())
		var wg sync.WaitGroup
		errs := make([]error, 2)
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, errs[0] = e.svc.Approve(context.Background(), cAdmin(), u, nil, "OK", "aprobar", key())
		}()
		go func() {
			defer wg.Done()
			_, errs[1] = e.svc.Reject(context.Background(), cAdmin(), u, nil, "NO", "rechazar", key())
		}()
		wg.Wait()
		ok := 0
		for _, err := range errs {
			if err == nil {
				ok++
			} else if !isErr(err, compliance.ErrInvalidTransition) {
				t.Fatalf("error inesperado: %v", err)
			}
		}
		if ok != 1 {
			t.Fatalf("iter %d: solo un admin puede ganar: %v", i, errs)
		}
	}
}

func TestConcurrency_ExpirationVsManualSuspend(t *testing.T) {
	for i := 0; i < 25; i++ {
		e := newEnv(t)
		u := uuid.New()
		c := e.start(u)
		exp := e.clock.Now().Add(time.Hour)
		if _, err := e.hook("a", compliance.EvtApproved, c.ProviderReference, e.clock.Now(), "", &exp); err != nil {
			t.Fatal(err)
		}
		e.clock.Advance(2 * time.Hour)
		var wg sync.WaitGroup
		var expired atomic.Int32
		var suspendErr error
		for w := 0; w < 4; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				n, err := e.svc.ExpireDue(context.Background(), 10)
				if err != nil {
					t.Errorf("expire: %v", err)
				}
				expired.Add(int32(n))
			}()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, suspendErr = e.svc.Suspend(context.Background(), cAdmin(), u, nil, "C", "suspender", key())
		}()
		wg.Wait()
		suspended := suspendErr == nil
		if suspendErr != nil && !isErr(suspendErr, compliance.ErrInvalidTransition) {
			t.Fatalf("error inesperado: %v", suspendErr)
		}
		if expired.Load() > 1 {
			t.Fatalf("expiró %d veces", expired.Load())
		}
		if suspended == (expired.Load() == 1) {
			t.Fatalf("iter %d: exactamente uno (expiración XOR suspensión) debe ganar: suspended=%v expired=%d", i, suspended, expired.Load())
		}
		st := e.status(u)
		if suspended && st != compliance.KYCSuspended {
			t.Fatalf("estado=%s", st)
		}
		if !suspended && st != compliance.KYCExpired {
			t.Fatalf("estado=%s", st)
		}
	}
}

func TestConcurrency_HoldPlaceAndRelease(t *testing.T) {
	e := newEnv(t)
	u := uuid.New()
	adm := cAdmin()
	var wg sync.WaitGroup
	var holdID atomic.Value
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h, err := e.svc.PlaceHold(context.Background(), compliance.PlaceHoldInput{Actor: adm, UserID: u, Type: compliance.HoldCompliance, ReasonCode: "R", Reason: "x", IdempotencyKey: key()})
			if err != nil {
				t.Errorf("place: %v", err)
				return
			}
			holdID.Store(h.ID)
		}()
	}
	wg.Wait()
	if n := len(e.store.AllHolds(u)); n != 1 {
		t.Fatalf("holds=%d", n)
	}
	id := holdID.Load().(uuid.UUID)
	var released atomic.Int32
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.svc.ReleaseHold(context.Background(), cAdmin(), id, "fin", key()); err == nil {
				released.Add(1)
			} else if !isErr(err, compliance.ErrHoldAlreadyReleased) {
				t.Errorf("release: %v", err)
			}
		}()
	}
	wg.Wait()
	if released.Load() != 1 {
		t.Fatalf("released=%d", released.Load())
	}
}
