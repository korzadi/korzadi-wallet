// Package lifecycle coordina el arranque y el apagado ordenado de los workers
// en segundo plano.
//
// Dos contextos:
//   - el contexto "suave" que recibe cada worker: se cancela al iniciar el
//     apagado y significa "no tomes trabajo nuevo";
//   - el contexto "duro": solo se cancela cuando vence el timeout de apagado.
//     El trabajo ya reclamado (firmar, difundir, liquidar) corre con
//     WorkContext, que ignora la cancelación suave y solo obedece al duro, de
//     modo que una firma o un broadcast en curso termina en vez de cortarse a
//     mitad. Si el timeout vence se cancela todo: el estado ya está persistido
//     en PostgreSQL y los leases expiran solos.
package lifecycle

import (
	"context"
	"log"
	"sync"
	"time"
)

type hardKey struct{}

// WorkContext devuelve un contexto para procesar UN ítem ya reclamado: conserva
// los valores de ctx pero NO se cancela cuando ctx se cancela (apagado suave);
// solo se cancela si el contexto duro del Supervisor vence o al llamar cancel.
// Fuera de un Supervisor (tests, herramientas) equivale a ctx sin cancelación.
func WorkContext(ctx context.Context) (context.Context, context.CancelFunc) {
	wctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	if hard, ok := ctx.Value(hardKey{}).(context.Context); ok {
		stop := context.AfterFunc(hard, cancel)
		return wctx, func() { stop(); cancel() }
	}
	return wctx, cancel
}

// Supervisor lanza goroutines con nombre y espera a que terminen.
type Supervisor struct {
	soft, hard context.Context
	softCancel context.CancelFunc
	hardCancel context.CancelFunc
	wg         sync.WaitGroup

	mu    sync.Mutex
	names map[string]int
}

// New crea un Supervisor cuyos workers heredan parent.
func New(parent context.Context) *Supervisor {
	hard, hardCancel := context.WithCancel(context.WithoutCancel(parent))
	soft, softCancel := context.WithCancel(context.WithValue(parent, hardKey{}, hard))
	return &Supervisor{soft: soft, hard: hard, softCancel: softCancel, hardCancel: hardCancel, names: map[string]int{}}
}

// Context es el contexto suave que se pasa a los workers.
func (s *Supervisor) Context() context.Context { return s.soft }

// Go ejecuta fn(ctx) en una goroutine registrada; un panic se registra en el
// log y no tumba el proceso.
func (s *Supervisor) Go(name string, fn func(ctx context.Context)) {
	s.mu.Lock()
	s.names[name]++
	s.mu.Unlock()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				log.Printf("lifecycle: PANIC en worker %q: %v", name, r)
			}
			s.mu.Lock()
			s.names[name]--
			if s.names[name] <= 0 {
				delete(s.names, name)
			}
			s.mu.Unlock()
		}()
		fn(s.soft)
	}()
}

// Shutdown pide parar (no más trabajo nuevo), espera a que los workers
// terminen su trabajo en curso hasta timeout y, si vence, cancela el contexto
// duro y espera un margen corto adicional. Devuelve true si todos terminaron a
// tiempo (sin forzar).
func (s *Supervisor) Shutdown(timeout time.Duration) bool {
	s.softCancel()
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()

	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		s.hardCancel()
		return true
	case <-timer.C:
	}

	s.mu.Lock()
	pending := make([]string, 0, len(s.names))
	for n := range s.names {
		pending = append(pending, n)
	}
	s.mu.Unlock()
	log.Printf("lifecycle: timeout de apagado (%s) — forzando cancelación; workers pendientes: %v", timeout, pending)
	s.hardCancel()

	grace := time.NewTimer(5 * time.Second)
	defer grace.Stop()
	select {
	case <-done:
	case <-grace.C:
		log.Printf("lifecycle: hay workers que no respondieron a la cancelación; se continúa el apagado")
	}
	return false
}
