package compliance

import (
	"context"
	"log"
	"time"
)

// Worker runs the periodic, idempotent jobs: KYC expiration and screening
// retry. Every tick is safe to repeat, to overlap across instances
// (SKIP LOCKED + state-machine validation) and to resume after a restart
// (state lives in the database, not in the process).
type Worker struct {
	svc      *Service
	interval time.Duration
}

func NewWorker(svc *Service, interval time.Duration) *Worker {
	if interval <= 0 {
		interval = time.Minute
	}
	return &Worker{svc: svc, interval: interval}
}

// Tick runs one pass and returns how many rows each job handled.
func (w *Worker) Tick(ctx context.Context) (expired, retried int, err error) {
	if w.svc.cfg.KYCEnabled {
		expired, err = w.svc.ExpireDue(ctx, 100)
		if err != nil {
			return expired, 0, err
		}
	}
	if w.svc.cfg.SanctionsEnabled {
		retried, err = w.svc.RetryFailedScreenings(ctx, 50)
	}
	return expired, retried, err
}

// Run loops until ctx is cancelled. Failures are logged (no payloads) and
// retried on the next tick — the retry policy is the tick interval plus the
// per-request backoff stored in screening_requests.
func (w *Worker) Run(ctx context.Context) {
	t := time.NewTicker(w.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			e, r, err := w.Tick(ctx)
			if err != nil {
				log.Printf("compliance worker: error en tick (expired=%d retried=%d)", e, r)
				continue
			}
			if e > 0 || r > 0 {
				log.Printf("compliance worker: expired=%d screening_retried=%d", e, r)
			}
		}
	}
}
