package security

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// FreezeStatus is the current freeze state of one operation.
type FreezeStatus struct {
	Operation OperationType
	Frozen    bool
	Reason    string
	FrozenBy  *uuid.UUID
	FrozenAt  *time.Time
}

// FreezeRepository persists security_freezes — a runtime, no-restart
// mechanism (spec §21), distinct from the compile-time-ish kill switches
// in Config/each subsystem's own env vars (spec §20). It never touches
// balances or the Ledger; it only records/reports a boolean per
// operation.
type FreezeRepository interface {
	Get(ctx context.Context, op OperationType) (FreezeStatus, error)
	List(ctx context.Context) ([]FreezeStatus, error)
	Set(ctx context.Context, op OperationType, frozen bool, reason string, actorID uuid.UUID) error
}

// FreezeGuard answers "is this operation allowed to proceed right now",
// combining the runtime emergency freeze with the static, per-operation
// kill switches this block adds (transfers, admin) — the pre-existing
// per-subsystem kill switches (withdrawals, sweeps, treasury, gas) are
// deliberately left to their own Config.Enabled checks (never duplicated
// here), but ARE still subject to the runtime FreezeRepository, since an
// emergency freeze must be able to stop them without a redeploy even
// though Bloque 8 doesn't own their day-to-day enable flag.
type FreezeGuard struct {
	freezes FreezeRepository
	cfg     Config
	events  *EventLogger
}

func NewFreezeGuard(freezes FreezeRepository, cfg Config, events *EventLogger) *FreezeGuard {
	return &FreezeGuard{freezes: freezes, cfg: cfg, events: events}
}

// CheckEnabled returns nil if op may proceed, or a wrapped
// ErrOperationFrozen / ErrKillSwitchOff otherwise. It logs a
// KILL_SWITCH_ACTIVATED security event on rejection so a blocked
// operation is always visible in security_events, not just a silent 403.
func (g *FreezeGuard) CheckEnabled(ctx context.Context, op OperationType, userID *uuid.UUID, ip string) error {
	if !op.Valid() {
		return ErrOperationUnknown
	}

	// 1) Static kill switches this block introduces (spec §20).
	switch op {
	case OpTransfer:
		if !g.cfg.TransfersEnabled {
			g.logBlocked(ctx, op, userID, ip, "kill_switch: SECURITY_TRANSFERS_ENABLED=false")
			return fmt.Errorf("%w: %s", ErrKillSwitchOff, op)
		}
	case OpAdminOperation:
		if !g.cfg.AdminOperationsEnabled {
			g.logBlocked(ctx, op, userID, ip, "kill_switch: SECURITY_ADMIN_OPERATIONS_ENABLED=false")
			return fmt.Errorf("%w: %s", ErrKillSwitchOff, op)
		}
	}
	// WITHDRAWAL / TREASURY_REBALANCE / GAS_REFILL / SWEEP keep their own
	// pre-existing kill switches (WITHDRAWALS_ENABLED, etc.) — checked by
	// their own services, not duplicated here.

	// 2) Runtime emergency freeze (spec §21) — applies to every operation.
	if g.freezes == nil {
		return nil
	}
	status, err := g.freezes.Get(ctx, op)
	if err != nil {
		// Fail closed: if we cannot determine freeze status, do not allow
		// a sensitive operation to silently proceed.
		return fmt.Errorf("security: no se pudo verificar el estado de freeze de %s: %w", op, err)
	}
	if status.Frozen {
		g.logBlocked(ctx, op, userID, ip, "emergency_freeze: "+status.Reason)
		return fmt.Errorf("%w: %s (%s)", ErrOperationFrozen, op, status.Reason)
	}
	return nil
}

func (g *FreezeGuard) logBlocked(ctx context.Context, op OperationType, userID *uuid.UUID, ip, reason string) {
	if g.events == nil {
		return
	}
	_ = g.events.Log(ctx, Event{
		Type:      EventKillSwitchActivated,
		Severity:  "warning",
		UserID:    userID,
		Operation: op,
		IP:        ip,
		Metadata:  map[string]any{"reason": reason},
	})
}
