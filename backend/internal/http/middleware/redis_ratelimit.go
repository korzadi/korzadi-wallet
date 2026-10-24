package middleware

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	securityredis "korzadi-wallet/backend/internal/infrastructure/redis"
)

// ErrBodyRateLimitUnavailable es el cuerpo de la respuesta 503 cuando un
// limitador FAIL-CLOSED no puede consultar Redis.
const rateLimitUnavailableMsg = "servicio temporalmente no disponible, intenta más tarde"

type RedisRateLimiter struct {
	client   *redis.Client
	limit    int
	window   time.Duration
	ipLimit  int
	ipWindow time.Duration
	// failClosed: si Redis falla, RECHAZA (503) en vez de dejar pasar.
	failClosed bool
}

func NewRedisRateLimiter(client *redis.Client, limit int, window time.Duration) *RedisRateLimiter {
	// The normal limit is per authenticated user.
	// The IP limit is a broader anti-abuse ceiling so users behind
	// the same NAT/public IP do not block each other.
	ipLimit := limit
	if limit == 120 {
		ipLimit = 600
	}

	return &RedisRateLimiter{
		client:   client,
		limit:    limit,
		window:   window,
		ipLimit:  ipLimit,
		ipWindow: window,
	}
}

// FailClosed hace que el limitador rechace con 503 cuando Redis no responde.
// Se usa en login, 2FA y retiros: sin contador no hay forma de frenar fuerza
// bruta, así que es preferible negar el servicio a dejarlo sin freno.
func (rl *RedisRateLimiter) FailClosed() *RedisRateLimiter {
	rl.failClosed = true
	return rl
}

func (rl *RedisRateLimiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()

		// Broad IP anti-abuse bucket (INCR+PEXPIRE atómico).
		ipKey := "ratelimit:ip:" + c.ClientIP() + ":" + c.FullPath()
		ipCount, err := securityredis.AtomicIncr(ctx, rl.client, ipKey, rl.ipWindow)
		if err != nil {
			if rl.failClosed {
				c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": rateLimitUnavailableMsg})
				return
			}
			c.Next()
			return
		}
		if ipCount > int64(rl.ipLimit) {
			c.AbortWithStatusJSON(
				http.StatusTooManyRequests,
				gin.H{"error": "demasiadas solicitudes, intenta más tarde"},
			)
			return
		}

		// AuthRequired runs before route middleware on authenticated routes.
		// Therefore user_id is available here.
		if userID, exists := c.Get("user_id"); exists {
			if uid, ok := userID.(string); ok && uid != "" {
				userKey := "ratelimit:user:" + uid + ":" + c.FullPath()

				userCount, userErr := securityredis.AtomicIncr(ctx, rl.client, userKey, rl.window)
				if userErr != nil {
					if rl.failClosed {
						c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": rateLimitUnavailableMsg})
						return
					}
					c.Next()
					return
				}
				if userCount > int64(rl.limit) {
					c.AbortWithStatusJSON(
						http.StatusTooManyRequests,
						gin.H{"error": "demasiadas solicitudes, intenta más tarde"},
					)
					return
				}
			}
		}

		c.Next()
	}
}
