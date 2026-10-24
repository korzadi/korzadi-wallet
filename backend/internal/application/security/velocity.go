package security

import (
	"context"
	"fmt"
	"time"
)

// Counter is the minimal counting primitive velocity controls need:
// increment a key and report its new value, expiring after window if this
// was the first increment. redis.Counter (INCR + EXPIRE) implements this
// directly; nothing here imports Redis.
type Counter interface {
	// Incr increments key and returns the new count. On the increment
	// that takes the count from 0 to 1, the key's TTL is (re)set to
	// window.
	Incr(ctx context.Context, key string, window time.Duration) (int64, error)
}

// VelocityTracker implements spec §14's operational (non-fraud-ML)
// pattern counters: "many withdrawals in a short time", "many
// consecutive transfers", "many failed attempts", "repeated security
// changes", "many new destinations". It only counts and flags — it does
// not block by itself; callers decide what to do with Exceeded (e.g. add
// friction, alert, or feed VelocityExceeded into RequireForAmount as if
// it were a high-value operation).
type VelocityTracker struct {
	counter Counter
	rules   map[string]RateLimitRule
	events  *EventLogger
}

func NewVelocityTracker(counter Counter, rules map[string]RateLimitRule, events *EventLogger) *VelocityTracker {
	if rules == nil {
		rules = defaultVelocity()
	}
	return &VelocityTracker{counter: counter, rules: rules, events: events}
}

// Record increments the named velocity rule for subject (typically a user
// ID or IP) and reports whether it now exceeds the configured threshold.
// An unknown rule name is treated as "not tracked" (count=0, exceeded
// false) rather than an error — velocity controls are advisory, a missing
// rule must never block an otherwise-valid operation.
func (v *VelocityTracker) Record(ctx context.Context, rule, subject string) (exceeded bool, count int64, err error) {
	r, ok := v.rules[rule]
	if !ok {
		return false, 0, nil
	}
	key := fmt.Sprintf("security:velocity:%s:%s", rule, subject)
	count, err = v.counter.Incr(ctx, key, r.Window)
	if err != nil {
		return false, 0, err
	}
	return count > int64(r.Limit), count, nil
}

// RecordAndLog is Record plus a RATE_LIMIT_TRIGGERED security event when
// the threshold is exceeded — used at call sites that want the audit
// trail without duplicating the same six lines everywhere.
func (v *VelocityTracker) RecordAndLog(ctx context.Context, rule, subject, ip string) (exceeded bool, err error) {
	exceeded, count, err := v.Record(ctx, rule, subject)
	if err != nil || !exceeded || v.events == nil {
		return exceeded, err
	}
	_ = v.events.Log(ctx, Event{
		Type:     EventRateLimitTriggered,
		Severity: "warning",
		IP:       ip,
		Metadata: map[string]any{"rule": rule, "subject": subject, "count": count},
	})
	return exceeded, nil
}
