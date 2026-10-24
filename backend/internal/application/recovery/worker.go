package recovery

import (
	"context"
	"log"
	"time"

	"korzadi-wallet/backend/internal/application/locking"
)

// Worker ticks RunDetection on an interval. Detection always runs; the
// Enabled kill switch only gates the write actions inside it (see
// Config/Service docs).
type Worker struct {
	svc   *Service
	locks locking.Locker
}

func NewWorker(svc *Service) *Worker { return &Worker{svc: svc} }

// WithLocker hace que la pasada de detección corra en UNA réplica a la vez
// (evita incidentes/auditorías duplicados y acciones de unstick simultáneas).
func (w *Worker) WithLocker(l locking.Locker) *Worker { w.locks = l; return w }

func (w *Worker) Run(ctx context.Context) {
	log.Printf("recovery worker: iniciado (intervalo=%s, enabled=%v — detección siempre activa, acciones de escritura solo si enabled)",
		w.svc.deps.Config.PollInterval, w.svc.deps.Config.Enabled)
	ticker := time.NewTicker(w.svc.deps.Config.PollInterval)
	defer ticker.Stop()

	for {
		w.RunOnce(ctx)
		select {
		case <-ctx.Done():
			log.Printf("recovery worker: detenido")
			return
		case <-ticker.C:
		}
	}
}

func (w *Worker) RunOnce(ctx context.Context) {
	ran, err := locking.RunExclusive(ctx, w.locks, locking.SingletonKey("recovery-detection"), func(c context.Context) error {
		summary, derr := w.svc.RunDetection(c)
		if derr != nil {
			return derr
		}
		if summary.Detected > 0 || summary.Unstuck > 0 || summary.Escalated > 0 {
			log.Printf("recovery worker: scanned=%d detected=%d unstuck=%d escalated=%d errors=%d",
				summary.Scanned, summary.Detected, summary.Unstuck, summary.Escalated, summary.Errors)
		}
		return nil
	})
	if err != nil && ctx.Err() == nil {
		log.Printf("recovery worker: error: %v", err)
	}
	_ = ran // ran=false: otra réplica está haciendo la pasada
}
