package pglock

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestKeyID_DeterministicAndDistinct(t *testing.T) {
	if KeyID("hot:bsc:0xabc") != KeyID("hot:bsc:0xabc") {
		t.Fatal("KeyID debe ser determinista")
	}
	if KeyID("hot:bsc:0xabc") == KeyID("hot:bsc:0xabd") {
		t.Fatal("claves distintas deben dar ids distintos")
	}
}

// Prueba de integración contra un PostgreSQL REAL: dos Locker con pools
// independientes (= dos réplicas) se excluyen mutuamente. Se omite si no hay
// PGLOCK_TEST_DATABASE_URL (p. ej. postgres://user:pass@localhost:5432/db).
func TestPG_TwoInstancesMutuallyExclusive(t *testing.T) {
	url := os.Getenv("PGLOCK_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("PGLOCK_TEST_DATABASE_URL no definido")
	}
	ctx := context.Background()
	poolA, err := NewPool(ctx, url, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer poolA.Close()
	poolB, err := NewPool(ctx, url, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer poolB.Close()
	a, b := New(poolA), New(poolB)

	var inside, maxInside int32
	var wg sync.WaitGroup
	for _, l := range []*Locker{a, b} {
		for i := 0; i < 6; i++ {
			wg.Add(1)
			go func(l *Locker) {
				defer wg.Done()
				lctx, cancel := context.WithTimeout(ctx, 20*time.Second)
				defer cancel()
				unlock, err := l.Lock(lctx, "test:pglock:hot")
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
				time.Sleep(5 * time.Millisecond)
				atomic.AddInt32(&inside, -1)
				unlock()
			}(l)
		}
	}
	wg.Wait()
	if maxInside != 1 {
		t.Fatalf("exclusión rota entre instancias: %d a la vez", maxInside)
	}
}

func TestPG_TryLockBusyThenFree(t *testing.T) {
	url := os.Getenv("PGLOCK_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("PGLOCK_TEST_DATABASE_URL no definido")
	}
	ctx := context.Background()
	pa, _ := NewPool(ctx, url, 2)
	defer pa.Close()
	pb, _ := NewPool(ctx, url, 2)
	defer pb.Close()
	a, b := New(pa), New(pb)

	unlock, ok, err := a.TryLock(ctx, "test:pglock:try")
	if err != nil || !ok {
		t.Fatalf("A debe adquirir: ok=%v err=%v", ok, err)
	}
	if _, ok2, _ := b.TryLock(ctx, "test:pglock:try"); ok2 {
		t.Fatal("B no debe adquirir mientras A lo retiene")
	}
	unlock()
	u2, ok3, err := b.TryLock(ctx, "test:pglock:try")
	if err != nil || !ok3 {
		t.Fatalf("B debe adquirir tras liberar A: ok=%v err=%v", ok3, err)
	}
	u2()
}
