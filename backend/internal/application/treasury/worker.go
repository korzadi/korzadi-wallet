package treasury

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

// Worker is the persistent process that evaluates treasury policies and
// advances every non-final rebalance. It holds NO state of its own:
// everything it needs is claimed from PostgreSQL with a time-limited lease
// (FOR UPDATE SKIP LOCKED — see RebalanceStore.ClaimDue), so it survives a
// restart or crash and several instances can run side by side without ever
// processing the same rebalance twice — same design as
// sweep.Worker / blockchainwithdrawal.Worker.
//
// There is deliberately no HTTP trigger for a rebalance (same posture as
// sweep: "NO crear endpoint público"): this worker is the only thing that
// ever creates or advances a treasury_rebalances row. Policy CRUD
// (Service.CreatePolicy / SetPolicyEnabled) is a separate, safe-to-expose
// concern that never itself moves funds.
type Worker struct {
	svc   *Service
	locks locking.Locker
}

func NewWorker(svc *Service) *Worker { return &Worker{svc: svc} }

// WithLocker hace que la EVALUACIÓN de políticas (lecturas de saldo por RPC)
// corra en una sola réplica a la vez. Crear rebalanceos ya es seguro con N
// réplicas (índice único por política en vuelo) y reclamar/avanzar usa lease
// + SKIP LOCKED.
func (w *Worker) WithLocker(l locking.Locker) *Worker { w.locks = l; return w }

// Run ticks until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	log.Printf("treasury worker: iniciado (instancia=%s, intervalo=%s, enabled=%v, mainnet_enabled=%v)",
		w.svc.deps.InstanceID, w.svc.deps.Config.PollInterval, w.svc.deps.Config.Enabled, w.svc.deps.Config.MainnetEnabled)
	ticker := time.NewTicker(w.svc.deps.Config.PollInterval)
	defer ticker.Stop()

	for {
		if _, err := w.RunOnce(ctx); err != nil && ctx.Err() == nil {
			log.Printf("treasury worker: error en el ciclo: %v", err)
		}
		select {
		case <-ctx.Done():
			log.Printf("treasury worker: detenido")
			return
		case <-ticker.C:
		}
	}
}

// RunOnce evaluates policies for new deficit/excess (only while the kill
// switch is on — TREASURY_REBALANCE_ENABLED=false never starts a new
// rebalance) and then claims and advances the due rebalances one step
// each, in parallel. Returns how many rebalances it advanced. Exposed for
// tests.
func (w *Worker) RunOnce(ctx context.Context) (int, error) {
	if w.svc.deps.Config.Enabled {
		if _, err := locking.RunExclusive(ctx, w.locks, locking.SingletonKey("treasury-evaluate"), func(c context.Context) error {
			if evaluated, created, eerr := w.svc.EvaluatePolicies(c); eerr != nil {
				log.Printf("treasury worker: error evaluando políticas: %v", eerr)
			} else if created > 0 {
				log.Printf("treasury worker: %d/%d políticas generaron un rebalanceo nuevo", created, evaluated)
			}
			return nil
		}); err != nil && ctx.Err() == nil {
			log.Printf("treasury worker: no se pudo tomar el lock de evaluación: %v", err)
		}
	}

	statuses := ActiveStatuses
	if !w.svc.deps.Config.Enabled {
		// Kill switch: keep OBSERVING what is already on-chain (so a
		// rebalance already broadcast still gets followed to completion),
		// but never start signing/broadcasting anything new — same
		// posture sweep.Worker already documents for its own kill switch.
		statuses = TrackingStatuses
	}
	owner := w.svc.newOwner()
	items, err := w.svc.deps.Rebalances.ClaimDue(ctx, owner, w.svc.deps.Config.LeaseDuration, w.svc.deps.Config.BatchSize, statuses)
	if err != nil {
		return 0, fmt.Errorf("reclamar rebalanceos: %w", err)
	}

	// Apagado ordenado: lo ya reclamado termina aunque ctx se cancele.
	workCtx, cancelWork := lifecycle.WorkContext(ctx)
	defer cancelWork()

	var wg sync.WaitGroup
	for _, item := range items {
		wg.Add(1)
		go func(item *Rebalance) {
			defer wg.Done()
			w.svc.processClaimed(workCtx, item, owner)
		}(item)
	}
	wg.Wait()
	return len(items), nil
}

// processClaimed advances one leased rebalance and ALWAYS frees the lease
// (even on panic) with the appropriate backoff.
func (s *Service) processClaimed(ctx context.Context, r *Rebalance, owner string) {
	delay := time.Duration(0)
	bump := false
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("treasury worker: PANIC id=%s status=%s: %v\n%s", r.ID, r.Status, rec, debug.Stack())
			delay, bump = Backoff(r.Attempts+1), true
		}
		s.unlease(ctx, r.ID, owner, delay, bump)
	}()

	d, err := s.Advance(ctx, r)
	if err != nil {
		bump = true
		delay = Backoff(r.Attempts + 1)
		log.Printf("treasury worker: reintento id=%s status=%s network=%s asset=%s direction=%s attempt=%d: %v",
			r.ID, r.Status, r.Network, r.AssetCode, r.Direction, r.Attempts+1, sanitizeCause(err))
		return
	}
	delay = d
}
