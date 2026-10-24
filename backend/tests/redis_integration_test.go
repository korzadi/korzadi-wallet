//go:build integration

package tests

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func TestRedisIntegration_PingAndIsolatedKeyLifecycle(t *testing.T) {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		t.Skip("REDIS_ADDR no definido, saltando test Redis")
	}
	client := redis.NewClient(&redis.Options{Addr: addr, Password: os.Getenv("REDIS_PASSWORD"), DB: 0})
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("Redis ping: %v", err)
	}
	key := "integration:test:" + uuid.NewString()
	if err := client.Set(ctx, key, "ok", time.Minute).Err(); err != nil {
		t.Fatalf("Redis SET: %v", err)
	}
	got, err := client.Get(ctx, key).Result()
	if err != nil || got != "ok" {
		t.Fatalf("Redis GET = %q, %v", got, err)
	}
	if err := client.Del(ctx, key).Err(); err != nil {
		t.Fatalf("Redis DEL: %v", err)
	}
}
