package evmclient

import (
	"context"
	"fmt"

	"korzadi-wallet/backend/internal/domain"
)

// HealthCheck verifies, in order: RPC reachability, chain ID correctness,
// latest block availability, and — if tokenContract is non-empty — that
// the given contract responds to a real read call. This is what lets an
// operator answer "are we actually connected to this network?" rather than
// just "did ethclient.Dial not error." tokenContract is supplied by the
// caller (e.g. one enabled asset from the registry for this network); this
// package has no opinion about which token, or whether there is one.
func (p *Provider) HealthCheck(ctx context.Context, tokenContract string) (domain.ProviderHealth, error) {
	h := domain.ProviderHealth{
		Network:         p.cfg.Network,
		ExpectedChainID: p.cfg.ChainID,
	}

	chainID, err := p.client.ChainID(ctx)
	if err != nil {
		h.Err = fmt.Sprintf("rpc unreachable: %v", err)
		return h, err
	}
	h.RPCReachable = true
	h.ActualChainID = chainID
	h.ChainIDOK = chainID == p.cfg.ChainID
	if !h.ChainIDOK {
		h.Err = fmt.Sprintf("chain id mismatch: esperado %d, obtenido %d", p.cfg.ChainID, chainID)
		return h, ErrWrongChain
	}

	latest, err := p.client.LatestBlock(ctx)
	if err != nil {
		h.Err = fmt.Sprintf("no se pudo leer el último bloque: %v", err)
		return h, err
	}
	h.LatestBlock = latest

	if tokenContract == "" {
		// No token configured/enabled for this network to probe — RPC and
		// chain ID are already verified above, which is all HealthCheck
		// can meaningfully report without a contract.
		return h, nil
	}

	// Probe the contract with a real read (balanceOf(0x0)) which is always
	// well-defined for a compliant ERC-20 and proves the contract is
	// deployed and responding on this chain.
	_, err = p.client.BalanceOf(ctx, tokenContract, "0x0000000000000000000000000000000000000000")
	if err != nil {
		h.Err = fmt.Sprintf("contrato %s no responde: %v", tokenContract, err)
		return h, err
	}
	h.ContractOK = true

	return h, nil
}
