package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"korzadi-wallet/backend/internal/application/security"
)

// deadRedis simula una caída de Redis: nada escucha en ese puerto.
func deadRedis() *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr: "127.0.0.1:1", DialTimeout: 200 * time.Millisecond, MaxRetries: -1,
	})
}

func doGET(h gin.HandlerFunc) int {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/x", h, func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	return w.Code
}

func TestRedisRateLimiter_FailClosed_WhenRedisDown(t *testing.T) {
	rl := NewRedisRateLimiter(deadRedis(), 10, time.Minute).FailClosed()
	if code := doGET(rl.Middleware()); code != http.StatusServiceUnavailable {
		t.Fatalf("fail-closed: esperaba 503 con Redis caído, obtuve %d", code)
	}
}

func TestRedisRateLimiter_DefaultFailOpen_WhenRedisDown(t *testing.T) {
	rl := NewRedisRateLimiter(deadRedis(), 10, time.Minute)
	if code := doGET(rl.Middleware()); code != http.StatusOK {
		t.Fatalf("por defecto (fail-open) esperaba 200, obtuve %d", code)
	}
}

type errCounter struct{}

func (errCounter) Incr(_ context.Context, _ string, _ time.Duration) (int64, error) {
	return 0, http.ErrServerClosed
}

func TestSecurityRateLimit_FailClosedOperations(t *testing.T) {
	cfg := security.Config{
		RateLimitEnabled: true,
		RateLimits: map[string]security.RateLimitRule{
			"login": {Limit: 5, Window: time.Minute}, "2fa": {Limit: 5, Window: time.Minute},
			"withdrawal": {Limit: 5, Window: time.Minute}, "transfer": {Limit: 5, Window: time.Minute},
		},
	}
	for _, op := range []string{"login", "2fa", "withdrawal"} {
		if code := doGET(SecurityRateLimit(errCounter{}, cfg, op)); code != http.StatusServiceUnavailable {
			t.Fatalf("%s debe fallar cerrado con Redis caído: %d", op, code)
		}
	}
	if code := doGET(SecurityRateLimit(errCounter{}, cfg, "transfer")); code != http.StatusOK {
		t.Fatalf("transfer conserva fail-open: %d", code)
	}
}
