package evmclient

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/big"
	"math/rand"
	"net/url"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"

	"korzadi-wallet/backend/internal/domain"
)

// ErrRPCUnavailable is returned when all retries against the RPC endpoint
// have been exhausted.
var ErrRPCUnavailable = errors.New("evm rpc: endpoint unavailable after retries")

// ErrWrongChain is returned when the RPC endpoint reports a chain ID that
// does not match configuration — i.e. we are not actually talking to the
// network we think we are.
var ErrWrongChain = errors.New("evm rpc: chain id mismatch, endpoint is not the configured network")

// Client wraps a single ethclient.Client with retry/backoff/timeout
// policy. It is network-agnostic: the same Client type serves Ethereum,
// BSC, Polygon, Arbitrum, Optimism and Base — only Config differs. Safe
// for concurrent use — construct one per (process, network) and share it.
type Client struct {
	cfg Config
	eth *ethclient.Client
}

// Dial establishes the single, reusable RPC connection for the process
// lifetime. Call once at startup per configured network, not per request.
func Dial(ctx context.Context, cfg Config) (*Client, error) {
	dialCtx, cancel := context.WithTimeout(ctx, cfg.RPCTimeout)
	defer cancel()

	eth, err := ethclient.DialContext(dialCtx, cfg.RPCURL)
	if err != nil {
		return nil, fmt.Errorf("evm rpc(%s): dial failed: %w", cfg.Network, redactURLErr(err, cfg.RPCURL))
	}

	c := &Client{cfg: cfg, eth: eth}

	// Fail fast at startup if the endpoint is on the wrong chain — better
	// to crash boot than silently monitor deposits on the wrong network.
	if err := c.verifyChainID(ctx); err != nil {
		eth.Close()
		return nil, err
	}

	return c, nil
}

func (c *Client) Network() domain.Network { return c.cfg.Network }
func (c *Client) Config() Config          { return c.cfg }

func (c *Client) Close() {
	if c.eth != nil {
		c.eth.Close()
	}
}

func (c *Client) verifyChainID(ctx context.Context) error {
	id, err := c.ChainID(ctx)
	if err != nil {
		return err
	}
	if id != c.cfg.ChainID {
		return fmt.Errorf("%w (%s): esperado %d, RPC reportó %d", ErrWrongChain, c.cfg.Network, c.cfg.ChainID, id)
	}
	return nil
}

func (c *Client) ChainID(ctx context.Context) (uint64, error) {
	var id uint64
	err := c.withRetry(ctx, "ChainID", func(ctx context.Context) error {
		v, err := c.eth.ChainID(ctx)
		if err != nil {
			return err
		}
		id = v.Uint64()
		return nil
	})
	return id, err
}

func (c *Client) LatestBlock(ctx context.Context) (uint64, error) {
	var block uint64
	err := c.withRetry(ctx, "LatestBlock", func(ctx context.Context) error {
		v, err := c.eth.BlockNumber(ctx)
		if err != nil {
			return err
		}
		block = v
		return nil
	})
	return block, err
}

func (c *Client) CallContract(ctx context.Context, msg ethereum.CallMsg) ([]byte, error) {
	var out []byte
	err := c.withRetry(ctx, "CallContract", func(ctx context.Context) error {
		v, err := c.eth.CallContract(ctx, msg, nil)
		if err != nil {
			return err
		}
		out = v
		return nil
	})
	return out, err
}

func (c *Client) FilterLogs(ctx context.Context, q ethereum.FilterQuery) ([]types.Log, error) {
	var out []types.Log
	err := c.withRetry(ctx, "FilterLogs", func(ctx context.Context) error {
		v, err := c.eth.FilterLogs(ctx, q)
		if err != nil {
			return err
		}
		out = v
		return nil
	})
	return out, err
}

func (c *Client) TransactionReceipt(ctx context.Context, txHash [32]byte) (*types.Receipt, error) {
	var out *types.Receipt
	err := c.withRetry(ctx, "TransactionReceipt", func(ctx context.Context) error {
		v, err := c.eth.TransactionReceipt(ctx, txHash)
		if err != nil {
			return err
		}
		out = v
		return nil
	})
	return out, err
}

// BlockByNumber fetches a full block including transactions — used to scan
// for native-coin transfers, which (unlike ERC-20 Transfer events) aren't
// indexed as logs and so can't be pulled with FilterLogs.
func (c *Client) BlockByNumber(ctx context.Context, number uint64) (*types.Block, error) {
	var out *types.Block
	err := c.withRetry(ctx, "BlockByNumber", func(ctx context.Context) error {
		v, err := c.eth.BlockByNumber(ctx, newBigInt(number))
		if err != nil {
			return err
		}
		out = v
		return nil
	})
	return out, err
}

func (c *Client) SendRawTransaction(ctx context.Context, raw []byte) (string, error) {
	tx := new(types.Transaction)
	if err := tx.UnmarshalBinary(raw); err != nil {
		return "", fmt.Errorf("evm rpc(%s): raw tx inválida: %w", c.cfg.Network, err)
	}
	err := c.withRetry(ctx, "SendRawTransaction", func(ctx context.Context) error {
		return c.eth.SendTransaction(ctx, tx)
	})
	if err != nil {
		return "", err
	}
	return tx.Hash().Hex(), nil
}

func (c *Client) SuggestGasPrice(ctx context.Context) (uint64, error) {
	var out uint64
	err := c.withRetry(ctx, "SuggestGasPrice", func(ctx context.Context) error {
		v, err := c.eth.SuggestGasPrice(ctx)
		if err != nil {
			return err
		}
		out = v.Uint64()
		return nil
	})
	return out, err
}

// Suggest1559Fees returns a bounded EIP-1559 fee quote. maxFeePerGas never
// exceeds the eth_gasPrice quote, and the priority fee never exceeds the cap. supported
// is false on pre-London chains, which must continue using legacy gasPrice.
func (c *Client) Suggest1559Fees(ctx context.Context) (maxFeePerGas, priorityFeePerGas *big.Int, supported bool, err error) {
	var head *types.Header
	err = c.withRetry(ctx, "HeaderByNumber", func(ctx context.Context) error {
		var e error
		head, e = c.eth.HeaderByNumber(ctx, nil)
		return e
	})
	if err != nil {
		return nil, nil, false, err
	}
	if head == nil || head.BaseFee == nil {
		return nil, nil, false, nil
	}
	var tip, quote *big.Int
	err = c.withRetry(ctx, "Suggest1559Fees", func(ctx context.Context) error {
		var e error
		tip, e = c.eth.SuggestGasTipCap(ctx)
		if e != nil {
			return e
		}
		quote, e = c.eth.SuggestGasPrice(ctx)
		return e
	})
	if err != nil {
		return nil, nil, true, err
	}
	if tip == nil || tip.Sign() <= 0 || quote == nil || quote.Sign() <= 0 {
		return nil, nil, true, fmt.Errorf("evm rpc(%s): quote EIP-1559 inválido", c.cfg.Network)
	}
	maxFeePerGas, priorityFeePerGas, err = bounded1559FeeCaps(head.BaseFee, tip, quote)
	if err != nil {
		return nil, nil, true, fmt.Errorf("evm rpc(%s): %w", c.cfg.Network, err)
	}
	return maxFeePerGas, priorityFeePerGas, true, nil
}

func bounded1559FeeCaps(baseFee, tip, quote *big.Int) (*big.Int, *big.Int, error) {
	if baseFee == nil || baseFee.Sign() <= 0 || tip == nil || tip.Sign() <= 0 || quote == nil || quote.Sign() <= 0 {
		return nil, nil, fmt.Errorf("quote EIP-1559 inválido")
	}
	maxFee := new(big.Int).Add(new(big.Int).Mul(baseFee, big.NewInt(2)), tip)
	if maxFee.Cmp(quote) > 0 {
		maxFee.Set(quote)
	}
	minimum := new(big.Int).Add(baseFee, tip)
	if maxFee.Cmp(minimum) < 0 {
		return nil, nil, fmt.Errorf("fee EIP-1559 supera el tope del quote RPC")
	}
	return maxFee, new(big.Int).Set(tip), nil
}

// withRetry runs op under a per-attempt timeout, retrying transient errors
// with exponential backoff + jitter, up to cfg.RPCMaxRetries times.
// Non-transient errors (e.g. malformed calldata) are returned immediately.
func (c *Client) withRetry(parent context.Context, op string, fn func(ctx context.Context) error) error {
	var lastErr error
	attempts := c.cfg.RPCMaxRetries + 1

	for attempt := 0; attempt < attempts; attempt++ {
		callCtx, cancel := context.WithTimeout(parent, c.cfg.RPCTimeout)
		err := fn(callCtx)
		cancel()

		if err == nil {
			return nil
		}
		lastErr = err

		if !isTransient(err) {
			// AUDIT FIX: never log the raw error — it can embed the RPC URL (API key).
			log.Printf("evm rpc(%s): %s falló (no reintentable): %v", c.cfg.Network, op, redactURLErr(err, c.cfg.RPCURL))
			return err
		}

		if attempt == attempts-1 {
			break
		}

		wait := backoffDuration(c.cfg.RPCBackoffBase, c.cfg.RPCBackoffMax, attempt)
		log.Printf("evm rpc(%s): %s falló (intento %d/%d), reintentando en %s: %v", c.cfg.Network, op, attempt+1, attempts, wait, redactURLErr(err, c.cfg.RPCURL))

		select {
		case <-time.After(wait):
		case <-parent.Done():
			return parent.Err()
		}
	}

	redacted := redactURLErr(lastErr, c.cfg.RPCURL)
	log.Printf("evm rpc(%s): %s agotó reintentos: %v", c.cfg.Network, op, redacted)
	return fmt.Errorf("%w (%s): %v", ErrRPCUnavailable, c.cfg.Network, redacted)
}

func backoffDuration(base, max time.Duration, attempt int) time.Duration {
	d := base * time.Duration(1<<uint(attempt))
	if d > max || d <= 0 {
		d = max
	}
	// jitter of ±20% to avoid thundering-herd retries across workers
	jitter := time.Duration(rand.Int63n(int64(d)/5 + 1))
	if rand.Intn(2) == 0 {
		return d + jitter
	}
	return d - jitter
}

// isTransient classifies whether an RPC error is worth retrying: network
// errors, timeouts, and rate limiting are transient; everything else
// (bad request, execution revert, etc.) is not.
func isTransient(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	msg := strings.ToLower(err.Error())
	transientSubstrings := []string{
		"timeout",
		"connection refused",
		"connection reset",
		"eof",
		"too many requests",
		"rate limit",
		"429",
		"temporarily unavailable",
		"no route to host",
		"broken pipe",
	}
	for _, s := range transientSubstrings {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// redactURLErr rewrites an error so that any embedded RPC URL (which may
// contain an API key in its path/query, e.g. an Infura/QuickNode style
// endpoint) never reaches logs verbatim.
func redactURLErr(err error, rawURL string) error {
	if err == nil {
		return nil
	}
	u, parseErr := url.Parse(rawURL)
	if parseErr != nil {
		return err
	}
	redacted := u.Scheme + "://" + u.Host + "/[redacted]"
	return fmt.Errorf("%s", strings.ReplaceAll(err.Error(), rawURL, redacted))
}
