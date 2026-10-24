package lifecycle

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// Un trabajo en curso (WorkContext) debe terminar aunque se inicie el apagado,
// y Shutdown debe esperarlo.
func TestShutdown_WaitsForInFlightWork(t *testing.T) {
	sup := New(context.Background())
	var finished atomic.Bool
	started := make(chan struct{})

	sup.Go("w", func(ctx context.Context) {
		wctx, cancel := WorkContext(ctx)
		defer cancel()
		close(started)
		select {
		case <-time.After(150 * time.Millisecond): // "firma + broadcast"
			finished.Store(true)
		case <-wctx.Done():
		}
	})
	<-started
	if !sup.Shutdown(2 * time.Second) {
		t.Fatal("el apagado debía terminar sin forzar")
	}
	if !finished.Load() {
		t.Fatal("el trabajo en curso fue cortado por el apagado")
	}
}

func TestShutdown_SoftContextStopsLoop(t *testing.T) {
	sup := New(context.Background())
	var stopped atomic.Bool
	sup.Go("loop", func(ctx context.Context) {
		<-ctx.Done()
		stopped.Store(true)
	})
	if !sup.Shutdown(time.Second) || !stopped.Load() {
		t.Fatal("el bucle debía detenerse con el contexto suave")
	}
}

// Si el trabajo no termina dentro del timeout, el contexto duro lo cancela.
func TestShutdown_TimeoutForcesHardCancel(t *testing.T) {
	sup := New(context.Background())
	var hardCancelled atomic.Bool
	started := make(chan struct{})
	sup.Go("stuck", func(ctx context.Context) {
		wctx, cancel := WorkContext(ctx)
		defer cancel()
		close(started)
		<-wctx.Done() // solo termina por cancelación dura
		hardCancelled.Store(true)
	})
	<-started
	if sup.Shutdown(100 * time.Millisecond) {
		t.Fatal("debía reportar que tuvo que forzar")
	}
	if !hardCancelled.Load() {
		t.Fatal("el contexto duro debía cancelar el trabajo atascado")
	}
}

func TestWorkContext_WithoutSupervisorIgnoresCancel(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	wctx, cancel := WorkContext(parent)
	defer cancel()
	cancelParent()
	select {
	case <-wctx.Done():
		t.Fatal("WorkContext no debe heredar la cancelación del padre")
	case <-time.After(20 * time.Millisecond):
	}
}

func TestSupervisor_PanicDoesNotCrash(t *testing.T) {
	sup := New(context.Background())
	sup.Go("boom", func(context.Context) { panic("x") })
	if !sup.Shutdown(time.Second) {
		t.Fatal("el apagado debía completarse tras un panic")
	}
}
