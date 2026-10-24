package btcclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrRPCUnavailable is returned when all retries against the node have
// been exhausted.
var ErrRPCUnavailable = errors.New("btc rpc: endpoint unavailable after retries")

// rpcClient is the minimal Bitcoin Core JSON-RPC 1.0 transport: HTTP POST
// with basic auth, one call per request, no batching. Deliberately
// hand-rolled with only net/http + encoding/json — no rpcclient library
// dependency needed for the small surface this backend uses.
type rpcClient struct {
	cfg        Config
	httpClient *http.Client
}

func newRPCClient(cfg Config) *rpcClient {
	return &rpcClient{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: cfg.RPCTimeout},
	}
}

type rpcRequest struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      string        `json:"id"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("btc rpc error %d: %s", e.Code, e.Message) }

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
	ID     string          `json:"id"`
}

// call performs a single JSON-RPC call with retry/backoff on transient
// (network/timeout) failures, mirroring evmclient's retry policy so both
// families behave consistently under RPC flakiness.
func (c *rpcClient) call(parent context.Context, method string, params []interface{}, out interface{}) error {
	if params == nil {
		params = []interface{}{}
	}
	reqBody, err := json.Marshal(rpcRequest{JSONRPC: "1.0", ID: "korzadi", Method: method, Params: params})
	if err != nil {
		return fmt.Errorf("btc rpc: no se pudo serializar la petición: %w", err)
	}

	attempts := c.cfg.RPCMaxRetries + 1
	var lastErr error

	for attempt := 0; attempt < attempts; attempt++ {
		callCtx, cancel := context.WithTimeout(parent, c.cfg.RPCTimeout)
		err := c.doOnce(callCtx, method, reqBody, out)
		cancel()

		if err == nil {
			return nil
		}
		lastErr = err

		if !isTransient(err) {
			log.Printf("btc rpc: %s falló (no reintentable): %v", method, err)
			return err
		}
		if attempt == attempts-1 {
			break
		}

		wait := backoffDuration(c.cfg.RPCBackoffBase, c.cfg.RPCBackoffMax, attempt)
		log.Printf("btc rpc: %s falló (intento %d/%d), reintentando en %s: %v", method, attempt+1, attempts, wait, err)

		select {
		case <-time.After(wait):
		case <-parent.Done():
			return parent.Err()
		}
	}

	log.Printf("btc rpc: %s agotó reintentos: %v", method, lastErr)
	return fmt.Errorf("%w: %s: %v", ErrRPCUnavailable, method, lastErr)
}

func (c *rpcClient) doOnce(ctx context.Context, method string, reqBody []byte, out interface{}) error {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.RPCURL, bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("construir petición: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.cfg.RPCUser != "" {
		httpReq.SetBasicAuth(c.cfg.RPCUser, c.cfg.RPCPass)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return redactURLErr(err, c.cfg.RPCURL)
	}
	defer resp.Body.Close()

	var rpcResp rpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return fmt.Errorf("respuesta inválida (http %d): %w", resp.StatusCode, err)
	}
	if rpcResp.Error != nil {
		return rpcResp.Error
	}
	if resp.StatusCode >= 500 {
		return fmt.Errorf("http %d del nodo", resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(rpcResp.Result, out); err != nil {
		return fmt.Errorf("no se pudo interpretar el resultado de %s: %w", method, err)
	}
	return nil
}

func backoffDuration(base, max time.Duration, attempt int) time.Duration {
	d := base * time.Duration(1<<uint(attempt))
	if d > max || d <= 0 {
		d = max
	}
	jitter := time.Duration(rand.Int63n(int64(d)/5 + 1))
	if rand.Intn(2) == 0 {
		return d + jitter
	}
	return d - jitter
}

func isTransient(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var rpcErr *rpcError
	if errors.As(err, &rpcErr) {
		// -28 = "Loading block index..." / warming up; treat as transient.
		return rpcErr.Code == -28
	}
	msg := strings.ToLower(err.Error())
	transientSubstrings := []string{
		"timeout", "connection refused", "connection reset", "eof",
		"too many requests", "rate limit", "429",
		"temporarily unavailable", "no route to host", "broken pipe",
	}
	for _, s := range transientSubstrings {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

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
