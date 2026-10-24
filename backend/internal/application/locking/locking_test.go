package locking

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHotKey_CaseInsensitiveAddress(t *testing.T) {
	if HotKey("BSC", "0xABCDEF") != HotKey("bsc", "0xabcdef") {
		t.Fatal("la clave debe ignorar mayúsculas/minúsculas")
	}
	if HotKey("BSC", "0xabc") == HotKey("ETHEREUM", "0xabc") {
		t.Fatal("redes distintas no deben compartir lock")
	}
}

func TestMemory_LockIsExclusiveAcrossInstances(t *testing.T) {
	shared := NewMemory() // "la base de datos" compartida por dos instancias
	var inside, maxInside int32
	var wg sync.WaitGroup
	for instance := 0; instance < 2; instance++ {
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				unlock, err := shared.Lock(context.Background(), HotKey("bsc", "0xabc"))
				if err != nil {
					t.Errorf("lock: %v", err)
					return
				}
				n := atomic.AddInt32(&inside, 1)
				for {
					m := atomic.LoadInt32(&maxInside)
					if n <= m || atomic.CompareAndSwapInt32(&maxInside, m, n) {
						break
					}
				}
				time.Sleep(time.Millisecond)
				atomic.AddInt32(&inside, -1)
				unlock()
			}()
		}
	}
	wg.Wait()
	if maxInside != 1 {
		t.Fatalf("sección crítica no exclusiva: %d a la vez", maxInside)
	}
}

func TestMemory_TryLockAndRelease(t *testing.T) {
	m := NewMemory()
	unlock, ok, err := m.TryLock(context.Background(), "k")
	if err != nil || !ok {
		t.Fatalf("primer TryLock debe adquirir: ok=%v err=%v", ok, err)
	}
	if _, ok2, _ := m.TryLock(context.Background(), "k"); ok2 {
		t.Fatal("segundo TryLock no debe adquirir mientras está tomado")
	}
	unlock()
	unlock() // idempotente
	if _, ok3, _ := m.TryLock(context.Background(), "k"); !ok3 {
		t.Fatal("tras liberar debe poder adquirirse")
	}
}

func TestMemory_LockHonoursContext(t *testing.T) {
	m := NewMemory()
	unlock, _ := m.Lock(context.Background(), "k")
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := m.Lock(ctx, "k"); err == nil {
		t.Fatal("Lock debe fallar al vencer el contexto")
	}
}

func TestRunExclusive_OnlyOneInstanceRuns(t *testing.T) {
	shared := NewMemory()
	var runs int32
	started := make(chan struct{})
	release := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = RunExclusive(context.Background(), shared, SingletonKey("job"), func(context.Context) error {
			atomic.AddInt32(&runs, 1)
			close(started)
			<-release
			return nil
		})
	}()
	<-started
	ran, err := RunExclusive(context.Background(), shared, SingletonKey("job"), func(context.Context) error {
		atomic.AddInt32(&runs, 1)
		return nil
	})
	if err != nil || ran {
		t.Fatalf("la segunda instancia no debe ejecutar: ran=%v err=%v", ran, err)
	}
	close(release)
	wg.Wait()
	if runs != 1 {
		t.Fatalf("debía ejecutarse una sola vez, fueron %d", runs)
	}
}

func TestRunExclusive_NilLockerRunsDirectly(t *testing.T) {
	ran, err := RunExclusive(context.Background(), nil, "x", func(context.Context) error { return nil })
	if !ran || err != nil {
		t.Fatalf("sin Locker debe ejecutar: ran=%v err=%v", ran, err)
	}
}
