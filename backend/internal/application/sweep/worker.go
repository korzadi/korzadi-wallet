package sweep

import (
	"context"
	"fmt"
	"log"
	"runtime/debug"
	"sync"
	"time"

	"korzadi-wallet/backend/internal/application/lifecycle"
	"korzadi-wallet/backend/internal/application/locking"
)

// Worker is the persistent process that discovers sweep candidates and
// advances every non-final sweep. It holds NO state of its own: everything
// it needs is claimed from PostgreSQL with a time-limited lease (FOR UPDATE
// SKIP LOCKED — see Store.ClaimDue), so it survives a restart or crash and
// several instances can run side by side without ever processing the same
// sweep twice — same design as blockchainwithdrawal.Worker.
//
// There is deliberately no HTTP trigger for a sweep (spec: "NO crear
// endpoint público para que un usuario dispare un sweep"): this worker is
// the only thing that ever creates or advances a sweeps row.
type Worker struct {
	svc   *Service
	locks locking.Locker
}

func NewWorker(svc *Service) *Worker { return &Worker{svc: svc} }

// WithLocker hace que el DESCUBRIMIENTO de candidatos (lecturas de saldo por
// RPC) corra en una sola réplica a la vez. Reclamar y avanzar sweeps ya usa
// lease + SKIP LOCKED y es seguro con N réplicas aunque no se configure.
func (w *Worker) WithLocker(l locking.Locker) *Worker { w.locks = l; return w }

// Run ticks until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	log.Printf("sweep worker: iniciado (instancia=%s, intervalo=%s, enabled=%v)",
		w.svc.deps.InstanceID, w.svc.deps.Config.PollInterval, w.svc.deps.Config.Enabled)
	ticker := time.NewTicker(w.svc.deps.Config.PollInterval)
	defer ticker.Stop()

	for {
		if _, err := w.RunOnce(ctx); err != nil && ctx.Err() == nil {
			log.Printf("sweep worker: error en el ciclo: %v", err)
		}
		select {
		case <-ctx.Done():
			log.Printf("sweep worker: detenido")
			return
		case <-ticker.C:
		}
	}
}

// RunOnce discovers new candidates (only while sweeps are enabled — the
// kill switch never starts a new sweep) and then claims and advances the
// due sweeps one step each, in parallel. Returns how many sweeps it
// advanced. Exposed for tests.
func (w *Worker) RunOnce(ctx context.Context) (int, error) {
	if w.svc.deps.Config.Enabled {
		if _, err := locking.RunExclusive(ctx, w.locks, locking.SingletonKey("sweep-discover"), func(c context.Context) error {
			if _, _, derr := w.svc.DiscoverAndClaim(c); derr != nil {
				log.Printf("sweep worker: error descubriendo candidatos: %v", derr)
			}
			return nil
		}); err != nil && ctx.Err() == nil {
			log.Printf("sweep worker: no se pudo tomar el lock de descubrimiento: %v", err)
		}
	}

	statuses := activeStatuses
	if !w.svc.deps.Config.Enabled {
		// Kill switch: keep OBSERVING what is already on-chain (so a sweep
		// already broadcast still gets followed to completion), but never
		// start signing/broadcasting anything new.
		statuses = trackingStatuses
	}
	owner := w.svc.newOwner()
	items, err := w.svc.deps.Store.ClaimDue(ctx, owner, w.svc.deps.Config.LeaseDuration, w.svc.deps.Config.BatchSize, statuses)
	if err != nil {
		return 0, fmt.Errorf("reclamar sweeps: %w", err)
	}

	// Apagado ordenado: lo ya reclamado termina aunque ctx se cancele.
	workCtx, cancelWork := lifecycle.WorkContext(ctx)
	defer cancelWork()

	var wg sync.WaitGroup
	for _, item := range items {
		wg.Add(1)
		go func(item *Sweep) {
			defer wg.Done()
			w.svc.processClaimed(workCtx, item, owner)
		}(item)
	}
	wg.Wait()
	return len(items), nil
}

// processClaimed advances one leased sweep and ALWAYS frees the lease
// (even on panic) with the appropriate backoff.
func (s *Service) processClaimed(ctx context.Context, sw *Sweep, owner string) {
	delay := time.Duration(0)
	bump := false
	defer func() {
		if r := recover(); r != nil {
			log.Printf("sweep worker: PANIC id=%s status=%s: %v\n%s", sw.ID, sw.Status, r, debug.Stack())
			delay, bump = Backoff(sw.Attempts+1), true
		}
		s.unlease(ctx, sw.ID, owner, delay, bump)
	}()

	d, err := s.Advance(ctx, sw)
	if err != nil {
		bump = true
		delay = Backoff(sw.Attempts + 1)
		log.Printf("sweep worker: reintento id=%s status=%s network=%s asset=%s from=%s attempt=%d: %v",
			sw.ID, sw.Status, sw.Network, sw.AssetCode, sw.FromAddress, sw.Attempts+1, sanitizeCause(err))
		return
	}
	delay = d
}
