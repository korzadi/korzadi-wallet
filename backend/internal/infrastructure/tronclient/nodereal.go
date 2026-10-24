// NodeReal exposes TRON's native full-node HTTP API (/wallet/...), which is
// a different protocol from TronGrid's convenient /v1/accounts/{address}
// REST wrapper that Client (in client.go) uses. This file implements only
// what's needed to read a TRC20 balance via that native API:
// /wallet/triggerconstantcontract, calling balanceOf(address) as a
// read-only ("constant") contract call — no private key, no state change,
// no signing involved.
//
// Scope note: GetTRC20Transfers (transfers.go) is NOT reimplemented here.
// NodeReal's native API does not expose an equivalent of TronGrid's
// convenient "transactions/trc20" history endpoint; doing this properly
// would require polling getcontractevents or building a local indexer,
// which is out of scope until a TRON deposit watcher is actually built
// (none exists in this codebase today — TRON currently only exposes an
// on-demand balance check, not automated deposit detection).
package tronclient

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/shopspring/decimal"
)

const USDTContractMainnet = "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"

// NodeRealClient implements TRC20BalanceReader against NodeReal's native
// TRON full-node HTTP API.
type NodeRealClient struct {
	baseURL    string
	httpClient *http.Client

	requestMu   sync.Mutex
	nextRequest time.Time
}

const tronMinRequestInterval = 400 * time.Millisecond

// waitForRequest serializa las solicitudes que pasan por FullNode.post.
func (c *NodeRealClient) waitForRequest(ctx context.Context) error {
	c.requestMu.Lock()
	now := time.Now()
	slot := now
	if c.nextRequest.After(slot) {
		slot = c.nextRequest
	}
	c.nextRequest = slot.Add(tronMinRequestInterval)
	c.requestMu.Unlock()

	delay := time.Until(slot)
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func NewNodeRealClient(baseURL string) *NodeRealClient {
	return &NodeRealClient{
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

type triggerConstantContractRequest struct {
	OwnerAddress     string `json:"owner_address"`
	ContractAddress  string `json:"contract_address"`
	FunctionSelector string `json:"function_selector"`
	Parameter        string `json:"parameter"`
	Visible          bool   `json:"visible"`
}

type triggerConstantContractResponse struct {
	Result struct {
		Result  bool   `json:"result"`
		Message string `json:"message"` // hex-encoded, present on failure
	} `json:"result"`
	ConstantResult []string `json:"constant_result"`
	EnergyUsed     int64    `json:"energy_used"`
}

// GetTRC20Balance reads balanceOf(walletAddress) on contractAddress via a
// read-only ("constant") contract call — no gas spent, no signature needed,
// nothing that touches custody. Both addresses are given in normal TRON
// base58 form (e.g. "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"); this function
// converts them to the hex form NodeReal's native API requires.
func (c *NodeRealClient) GetTRC20Balance(ctx context.Context, walletAddress, contractAddress string, decimals int32) (decimal.Decimal, error) {
	ownerHex, err := tronAddressToHex(walletAddress)
	if err != nil {
		return decimal.Zero, fmt.Errorf("dirección de wallet inválida: %w", err)
	}
	contractHex, err := tronAddressToHex(contractAddress)
	if err != nil {
		return decimal.Zero, fmt.Errorf("dirección de contrato inválida: %w", err)
	}

	// balanceOf(address) parameter: the 20-byte address hash (without the
	// 0x41 prefix), left-padded with zeros to 32 bytes (64 hex chars) —
	// standard EVM/TVM ABI calling convention.
	addressBytes := ownerHex[2:] // drop the "41" prefix, keep the 20-byte hash as hex
	parameter := strings.Repeat("0", 64-len(addressBytes)) + addressBytes

	reqBody := triggerConstantContractRequest{
		OwnerAddress:     ownerHex,
		ContractAddress:  contractHex,
		FunctionSelector: "balanceOf(address)",
		Parameter:        parameter,
		Visible:          false,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return decimal.Zero, fmt.Errorf("codificar petición NodeReal: %w", err)
	}

	url := c.baseURL + "/wallet/triggerconstantcontract"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return decimal.Zero, fmt.Errorf("crear petición NodeReal: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return decimal.Zero, fmt.Errorf("consultar NodeReal (TRON): %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return decimal.Zero, fmt.Errorf("leer respuesta NodeReal: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return decimal.Zero, fmt.Errorf("NodeReal (TRON) respondió %d: %s", resp.StatusCode, string(respBytes))
	}

	var parsed triggerConstantContractResponse
	if err := json.Unmarshal(respBytes, &parsed); err != nil {
		return decimal.Zero, fmt.Errorf("decodificar respuesta NodeReal: %w (cuerpo: %s)", err, string(respBytes))
	}

	if !parsed.Result.Result {
		msg := parsed.Result.Message
		if decoded, decErr := hex.DecodeString(msg); decErr == nil {
			msg = string(decoded)
		}
		return decimal.Zero, fmt.Errorf("triggerconstantcontract falló: %s", msg)
	}
	if len(parsed.ConstantResult) == 0 || parsed.ConstantResult[0] == "" {
		return decimal.Zero, fmt.Errorf("NodeReal no devolvió constant_result para balanceOf")
	}

	rawHex := parsed.ConstantResult[0]
	rawInt, ok := new(big.Int).SetString(rawHex, 16)
	if !ok {
		return decimal.Zero, fmt.Errorf("no se pudo interpretar el resultado hexadecimal de balanceOf: %q", rawHex)
	}

	divisor := decimal.New(1, decimals)
	return decimal.NewFromBigInt(rawInt, 0).Div(divisor), nil
}

// AccountExists checks account presence via the native /wallet/getaccount
// endpoint. An account with zero activity returns an empty object ({}),
// which we treat as "does not exist yet" — matching AccountExists'
// existing TronGrid-backed semantics in client.go.
func (c *NodeRealClient) AccountExists(ctx context.Context, walletAddress string) (bool, error) {
	hexAddr, err := tronAddressToHex(walletAddress)
	if err != nil {
		return false, fmt.Errorf("dirección inválida: %w", err)
	}

	reqBody := map[string]any{"address": hexAddr, "visible": false}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return false, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/wallet/getaccount", bytes.NewReader(bodyBytes))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("NodeReal (TRON) respondió %d: %s", resp.StatusCode, string(respBytes))
	}

	var parsed map[string]any
	if err := json.Unmarshal(respBytes, &parsed); err != nil {
		return false, fmt.Errorf("decodificar respuesta getaccount: %w", err)
	}
	return len(parsed) > 0, nil
}
