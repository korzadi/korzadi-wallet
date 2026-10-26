// Package redis holds thin Redis-backed adapters for Bloque 8. The actual
// *redis.Client construction/ping helpers already live in
// internal/http/middleware/redis_ratelimit.go (Bloque 1) and are reused
// as-is — this file only adds the counting primitive security.Counter
// needs, on top of the same client.
package redis

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// SecurityCounter implements security.Counter (INCR + EXPIRE-on-first-hit)
// — the same pattern middleware.RedisRateLimiter already uses for
// per-path rate limiting, reused here for velocity counters and
// per-operation/per-user rate limits (spec §8/§14), which need a
// different key shape (per user+operation, not per IP+route) than the
// existing middleware provides.
type SecurityCounter struct {
	client *redis.Client
}

func NewSecurityCounter(client *redis.Client) *SecurityCounter {
	return &SecurityCounter{client: client}
}

// Incr es atómico (script Lua INCR+PEXPIRE): no puede quedar una clave sin TTL.
// Los errores de Redis se propagan; cada llamador decide si falla cerrado
// (login, 2FA, retiros) o abierto (contadores solo informativos).
func (c *SecurityCounter) Incr(ctx context.Context, key string, window time.Duration) (int64, error) {
	return AtomicIncr(ctx, c.client, key, window)
}

// Revoke elimina inmediatamente una clave del contador (revocación de sesión
// o bloqueo de usuario en tiempo real). No requiere reinicio del servicio.
func (c *SecurityCounter) Revoke(ctx context.Context, key string) error {
	return c.client.Del(ctx, key).Err()
}
