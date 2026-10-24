package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/authjwt"
)

func AuthRequired(jwtMgr *authjwt.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if len(header) < 8 || header[:7] != "Bearer " {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "token faltante"})
			return
		}
		claims, err := jwtMgr.ValidateAccessToken(header[7:])
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "token inválido o expirado"})
			return
		}
		c.Set("user_id", claims.UserID)
		c.Next()
	}
}

func RequireRole(getRolesFn func(userID string) ([]string, error), allowed ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, _ := c.Get("user_id")
		roles, err := getRolesFn(userID.(string))
		if err != nil {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "no se pudo verificar rol"})
			return
		}
		for _, r := range roles {
			for _, a := range allowed {
				if r == a {
					c.Next()
					return
				}
			}
		}
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "permiso insuficiente"})
	}
}

type simpleRateLimiter struct {
	mu      sync.Mutex
	buckets map[string][]time.Time
	limit   int
	window  time.Duration
}

func NewRateLimiter(limit int, window time.Duration) *simpleRateLimiter {
	return &simpleRateLimiter{buckets: map[string][]time.Time{}, limit: limit, window: window}
}

func (rl *simpleRateLimiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		key := c.ClientIP() + ":" + c.FullPath()
		rl.mu.Lock()
		now := time.Now()
		var recent []time.Time
		for _, t := range rl.buckets[key] {
			if now.Sub(t) < rl.window {
				recent = append(recent, t)
			}
		}
		if len(recent) >= rl.limit {
			rl.mu.Unlock()
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "demasiadas solicitudes, intenta más tarde"})
			return
		}
		recent = append(recent, now)
		rl.buckets[key] = recent
		rl.mu.Unlock()
		c.Next()
	}
}

func IdempotencyKeyRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		key := c.GetHeader("Idempotency-Key")
		if key == "" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "header Idempotency-Key requerido"})
			return
		}
		// Longitud 8-128, charset [A-Za-z0-9._:-] y sin prefijos reservados
		// de claves internas del backend.
		if err := domain.ValidateUserIdempotencyKey(key); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"error": domain.ErrInvalidIdempotencyKey.Code, "message": domain.ErrInvalidIdempotencyKey.Message,
			})
			return
		}
		c.Set("idempotency_key", key)
		c.Next()
	}
}

func CORSConfig(allowedOrigins map[string]bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if allowedOrigins[origin] {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Allow-Credentials", "true")
		}
		c.Header("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Authorization,Content-Type,Idempotency-Key,X-2FA-Code")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
