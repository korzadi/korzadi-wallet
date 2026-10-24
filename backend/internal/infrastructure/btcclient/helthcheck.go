package btcclient

import (
	"context"
	"fmt"

	"korzadi-wallet/backend/internal/domain"
)

// HealthCheck verifies RPC reachability and latest block availability.
// Bitcoin has no chain ID and no single "the token contract" to probe, so
// this is intentionally simpler than the EVM HealthCheck.
func (p *Provider) HealthCheck(ctx context.Context) (domain.ProviderHealth, error) {
	h := domain.ProviderHealth{Network: domain.NetworkBITCOIN}

	height, err := p.client.GetBlockCount(ctx)
	if err != nil {
		h.Err = fmt.Sprintf("rpc unreachable: %v", err)
		return h, err
	}
	h.RPCReachable = true
	h.ChainIDOK = true // not applicable to Bitcoin; true = "no mismatch possible"
	h.LatestBlock = height
	return h, nil
}
