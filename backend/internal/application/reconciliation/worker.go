package reconciliation

import (
	"context"
	"log"
	"time"

	"korzadi-wallet/backend/internal/application/locking"
)

// Worker ticks both reconciliation legs on an interval. Unlike
// sweep/treasury's worker, there is no per-item lease to claim: each pass
// recomputes the full picture and Upsert's the same row idempotently, so
// two instances running the same pass concurrently is safe (last write
// wins on a read-only recomputation, never a fund movement) — this is
// exactly the property spec 7.7 asks to verify ("repeated reconciliation,
// idempotent result").
type Worker struct {
	svc   *Service
	locks locking.Locker
}

func NewWorker(svc *Service) *Worker { return &Worker{svc: svc} }

// WithLocker hace que la conciliación corra en UNA réplica a la vez: el
// resultado ya era idempotente, pero así no se duplican lecturas de saldo por
// RPC, escalados ni alertas.
func (w *Worker) WithLocker(l locking.Locker) *Worker { w.locks = l; return w }

func (w *Worker) Run(ctx context.Context) {
	log.Printf("reconciliation worker: iniciado (intervalo=%s, enabled=%v)", w.svc.deps.Config.PollInterval, w.svc.deps.Config.Enabled)
	ticker := time.NewTicker(w.svc.deps.Config.PollInterval)
	defer ticker.Stop()

	for {
		w.RunOnce(ctx)
		select {
		case <-ctx.Done():
			log.Printf("reconciliation worker: detenido")
			return
		case <-ticker.C:
		}
	}
}

// RunOnce runs both legs once. It is a no-op (returns immediately) while
// the kill switch is off — spec: "No activar automáticamente" — but is
// exposed for tests and for an eventual manual-trigger admin endpoint.
func (w *Worker) RunOnce(ctx context.Context) {
	if !w.svc.deps.Config.Enabled {
		return
	}
	if _, err := locking.RunExclusive(ctx, w.locks, locking.SingletonKey("reconciliation"), func(c context.Context) error {
		w.runLegs(c)
		return nil
	}); err != nil && ctx.Err() == nil {
		log.Printf("reconciliation worker: no se pudo tomar el lock: %v", err)
	}
}

func (w *Worker) runLegs(ctx context.Context) {
	if _, ok := w.svc.deps.Ledger.(LedgerIntegrityReader); ok {
		mismatches, err := w.svc.ReconcileLedgerIntegrity(ctx)
		if err != nil {
			log.Printf("reconciliation worker: ledger integrity: %v", err)
		} else {
			for _, m := range mismatches {
				log.Printf("reconciliation worker: ALERT ledger integrity account=%s asset=%s balance_cache=%s ledger_sum=%s locked_balance=%s active_reservations=%s",
					m.WalletAccountID, m.AssetCode, m.BalanceCache, m.LedgerBalance, m.LockedBalance, m.ActiveReservations)
			}
			if len(mismatches) == 0 {
				log.Printf("reconciliation worker: ledger integrity matched")
			}
		}
	}
	bc, err := w.svc.ReconcileBlockchainCustody(ctx)
	if err != nil {
		log.Printf("reconciliation worker: blockchain→custody: %v", err)
	} else {
		log.Printf("reconciliation worker: blockchain→custody checked=%d matched=%d mismatch=%d escalated=%d errors=%d",
			bc.Checked, bc.Matched, bc.Mismatch, bc.Escalated, bc.Errors)
	}

	cl, err := w.svc.ReconcileCustodyLedger(ctx)
	if err != nil {
		log.Printf("reconciliation worker: custody→ledger: %v", err)
	} else {
		log.Printf("reconciliation worker: custody→ledger checked=%d matched=%d mismatch=%d escalated=%d errors=%d",
			cl.Checked, cl.Matched, cl.Mismatch, cl.Escalated, cl.Errors)
		if cl.Mismatch > 0 {
			log.Printf("reconciliation worker: ALERTA — %d asset(s) con posible déficit custody<ledger, requiere revisión", cl.Mismatch)
		}
	}
}
