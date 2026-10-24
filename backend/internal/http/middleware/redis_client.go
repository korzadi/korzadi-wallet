package middleware

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

func NewRedisClient(addr, password string) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       0,
	})
}

func PingRedis(ctx context.Context, client *redis.Client) error {
	if client == nil {
		return fmt.Errorf("cliente Redis nulo")
	}

	return client.Ping(ctx).Err()
}
