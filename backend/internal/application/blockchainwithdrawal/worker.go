package blockchainwithdrawal

import (
	"context"
	"errors"
	"fmt"
	"log"
	"runtime/debug"
	"sync"
	"time"

	"korzadi-wallet/backend/internal/application/lifecycle"
)

// Worker is the persistent process that advances every non-final withdrawal.
// It holds NO state of its own: everything it needs is claimed from
// PostgreSQL with a time-limited lease (FOR UPDATE SKIP LOCKED), so it
// survives — and is safely re-runnable after — a restart, crash or deploy,
// and several instances can run side by side without processing the same
// withdrawal twice.
//
// It replaces "a goroutine tied to an HTTP request" entirely: an HTTP
// request only creates the withdrawal and reserves funds.
type Worker struct {
	svc *Service
}

func NewWorker(svc *Service) *Worker { return &Worker{svc: svc} }

// Run ticks until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	log.Printf("withdrawal worker: iniciado (instancia=%s, intervalo=%s, enabled=%v)",
		w.svc.InstanceID, w.svc.Config.PollInterval, w.svc.Config.Enabled)
	ticker := time.NewTicker(w.svc.Config.PollInterval)
	defer ticker.Stop()

	for {
		if _, err := w.RunOnce(ctx); err != nil && ctx.Err() == nil {
			log.Printf("withdrawal worker: error en el ciclo: %v", err)
		}
		select {
		case <-ctx.Done():
			log.Printf("withdrawal worker: detenido")
			return
		case <-ticker.C:
		}
	}
}

// RunOnce claims the due withdrawals and advances each one step, in
// parallel, returning how many it processed. Exposed for tests.
func (w *Worker) RunOnce(ctx context.Context) (int, error) {
	statuses := activeStatuses
	if !w.svc.Config.Enabled {
		// Kill switch: keep OBSERVING what is already on-chain, but never
		// start (reserve/sign/send) anything new.
		statuses = trackingStatuses
	}
	owner := w.svc.newOwner()
	items, err := w.svc.Store.ClaimDue(ctx, owner, w.svc.Config.LeaseDuration, w.svc.Config.BatchSize, statuses)
	if err != nil {
		return 0, fmt.Errorf("reclamar retiros: %w", err)
	}

	// Apagado ordenado: lo ya reclamado se procesa hasta terminar (firma,
	// broadcast, liquidación) aunque ctx se cancele; ver lifecycle.WorkContext.
	workCtx, cancelWork := lifecycle.WorkContext(ctx)
	defer cancelWork()

	var wg sync.WaitGroup
	for _, item := range items {
		wg.Add(1)
		go func(item *Withdrawal) {
			defer wg.Done()
			w.svc.processClaimed(workCtx, item, owner)
		}(item)
	}
	wg.Wait()
	return len(items), nil
}

// hotLockBusyDelay es la espera antes de reintentar cuando la HOT está ocupada.
const hotLockBusyDelay = 2 * time.Second

// processClaimed advances one leased withdrawal and ALWAYS frees the lease
// (even on panic) with the appropriate backoff.
func (s *Service) processClaimed(ctx context.Context, w *Withdrawal, owner string) {
	delay := time.Duration(0)
	bump := false
	defer func() {
		if r := recover(); r != nil {
			log.Printf("withdrawal worker: PANIC id=%s status=%s: %v\n%s", w.ID, w.Status, r, debug.Stack())
			delay, bump = backoff(w.Attempts+1), true
		}
		s.unlease(ctx, w.ID, owner, delay, bump)
	}()

	d, err := s.Advance(ctx, w)
	if err != nil && errors.Is(err, ErrHotLockBusy) {
		// Otra instancia está firmando con la misma HOT: no es un fallo del
		// retiro, reintentar pronto SIN consumir intentos.
		delay = hotLockBusyDelay
		log.Printf("withdrawal worker: HOT ocupada por otra instancia id=%s network=%s, reintento en %s", w.ID, w.Network, delay)
		return
	}
	if err != nil {
		bump = true
		delay = backoff(w.Attempts + 1)
		log.Printf("withdrawal worker: reintento id=%s status=%s network=%s asset=%s attempt=%d: %v",
			w.ID, w.Status, w.Network, w.AssetCode, w.Attempts+1, sanitizeCause(err))
		return
	}
	delay = d
}
