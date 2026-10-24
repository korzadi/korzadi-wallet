package middleware

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/application/security"
)

// failClosedOperations: operaciones donde un fallo de Redis NIEGA la petición
// (503) en vez de dejarla pasar sin límite.
var failClosedOperations = map[string]bool{"login": true, "2fa": true, "withdrawal": true}

// SecurityRateLimit implements spec §8's per-user AND per-IP rate
// limiting for one named sensitive operation (login, 2fa, withdrawal,
// transfer, deposit, admin, security). It complements — does not replace
// — the coarser per-route RedisRateLimiter already wired in router.go:
// that one limits "how many requests hit this path from this IP"; this
// one limits "how many times has THIS user attempted THIS sensitive
// operation", which stays meaningful even behind a shared NAT/proxy IP.
//
// enabled is read once per request from cfg so an operator can flip
// SECURITY_RATE_LIMIT_ENABLED off (e.g. temporarily, while debugging)
// without restarting every other rate limiter in the process.
func SecurityRateLimit(counter security.Counter, cfg security.Config, operation string) gin.HandlerFunc {
	rule, hasRule := cfg.RateLimits[operation]
	failClosed := failClosedOperations[operation]
	return func(c *gin.Context) {
		if !cfg.RateLimitEnabled || !hasRule || counter == nil {
			c.Next()
			return
		}

		ctx := c.Request.Context()
		ip := c.ClientIP()

		count, err := counter.Incr(ctx, "security:ratelimit:ip:"+operation+":"+ip, rule.Window)
		if err != nil && failClosed {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": rateLimitUnavailableMsg})
			return
		}
		if err == nil && count > int64(rule.Limit) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "demasiadas solicitudes, intenta más tarde"})
			return
		}

		if uidVal, ok := c.Get("user_id"); ok {
			if uid, _ := uidVal.(string); uid != "" {
				key := "security:ratelimit:user:" + operation + ":" + uid
				ucount, uerr := counter.Incr(ctx, key, rule.Window)
				if uerr != nil && failClosed {
					c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": rateLimitUnavailableMsg})
					return
				}
				if uerr == nil && ucount > int64(rule.Limit) {
					c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "demasiadas solicitudes, intenta más tarde"})
					return
				}
			}
		}

		c.Next()
	}
}

// OperationGuard runs security.Service.PreAuthorize (active-account check
// + kill switch + emergency freeze — spec §6/§20/§21) before the handler
// for a sensitive operation runs. svc may be nil (Bloque 8 not wired for
// this deployment yet) — in that case the middleware is a no-op, so
// router wiring can adopt it incrementally without a hard dependency.
func OperationGuard(svc *security.Service, op security.OperationType) gin.HandlerFunc {
	return func(c *gin.Context) {
		if svc == nil {
			c.Next()
			return
		}
		uidVal, _ := c.Get("user_id")
		uidStr, _ := uidVal.(string)
		userID, err := uuid.Parse(uidStr)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "token faltante o inválido"})
			return
		}
		if err := svc.PreAuthorize(c.Request.Context(), op, userID, c.ClientIP()); err != nil {
			status := http.StatusForbidden
			if errors.Is(err, security.ErrOperationFrozen) || errors.Is(err, security.ErrKillSwitchOff) {
				status = http.StatusServiceUnavailable
			}
			c.AbortWithStatusJSON(status, gin.H{"error": "operación no disponible temporalmente"})
			return
		}
		c.Next()
	}
}

// BodyLimit caps every request body. AUDIT FIX (MEDIUM): only the KYC webhook
// had a size limit; the HMAC middleware (unauthenticated) did io.ReadAll and the
// JSON handlers bound unbounded bodies, so a single client could exhaust memory.
// Reads past the limit fail with an error, which handlers already treat as a
// 400 invalid request.
func BodyLimit(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		}
		c.Next()
	}
}
