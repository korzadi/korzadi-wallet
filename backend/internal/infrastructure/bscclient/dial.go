package bscclient

import (
	"context"

	"korzadi-wallet/backend/internal/infrastructure/evmclient"
)

// Dial and NewProvider delegate straight to evmclient — kept here only so
// existing call sites (main.go) can keep writing bscclient.Dial(...) /
// bscclient.NewProvider(...) unchanged.
func Dial(ctx context.Context, cfg Config) (*Client, error) {
	return evmclient.Dial(ctx, cfg.Config)
}

func NewProvider(client *Client, cfg Config) *Provider {
	return evmclient.NewProvider(client, cfg.Config)
}
