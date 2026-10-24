package tronclient

// FullNode is the TRON full-node HTTP client (the /wallet/* API) used by the
// deposit watcher, the withdrawal chain and the fee estimator. It REUSES
// NodeRealClient (same base URL / HTTP client, GetTRC20Balance, AccountExists)
// instead of duplicating it. It never touches private keys.
//
// BLOQUEADO (requiere un nodo real): la semántica exacta de algunos endpoints
// (visibilidad de txs pendientes, /wallet/getblock, forma de errores) solo se
// puede confirmar contra un nodo TRON real; aquí está probada contra un nodo
// simulado.

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type FullNode struct {
	*NodeRealClient
}

func NewFullNode(baseURL string) *FullNode {
	return &FullNode{NodeRealClient: NewNodeRealClient(baseURL)}
}

// NewFullNodeWithHTTP lets tests inject an *http.Client (httptest).
func NewFullNodeWithHTTP(baseURL string, hc *http.Client) *FullNode {
	n := NewNodeRealClient(baseURL)
	if hc != nil {
		n.httpClient = hc
	}
	return &FullNode{NodeRealClient: n}
}

const maxResponseBytes = 16 << 20

type tronHTTPStatusError struct {
	Path string
	Code int
	Body string
}

func (e *tronHTTPStatusError) Error() string {
	return fmt.Sprintf("tron %s: HTTP %d: %s", e.Path, e.Code, e.Body)
}

func (n *FullNode) post(ctx context.Context, path string, body any, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("tron %s: codificar petición: %w", path, err)
	}

	const maxAttempts = 3
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err := n.NodeRealClient.waitForRequest(ctx); err != nil {
			return fmt.Errorf("tron %s: espera de petición: %w", path, err)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.baseURL+path, bytes.NewReader(raw))
		if err != nil {
			return fmt.Errorf("tron %s: crear petición: %w", path, err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := n.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("tron %s: %w", path, err)
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
		resp.Body.Close()
		if readErr != nil {
			return fmt.Errorf("tron %s: leer respuesta: %w", path, readErr)
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			if attempt == maxAttempts-1 {
				return &tronHTTPStatusError{Path: path, Code: resp.StatusCode, Body: strings.TrimSpace(string(data))}
			}

			// TronGrid público puede suspender el servidor de consulta durante 2 s.
			delay := 2 * time.Second
			if retry := strings.TrimSpace(resp.Header.Get("Retry-After")); retry != "" {
				if seconds, parseErr := strconv.Atoi(retry); parseErr == nil && seconds > 0 && seconds <= 30 {
					delay = time.Duration(seconds) * time.Second
				}
			}
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return fmt.Errorf("tron %s: reintento cancelado: %w", path, ctx.Err())
			case <-timer.C:
			}
			continue
		}

		if resp.StatusCode != http.StatusOK {
			return &tronHTTPStatusError{Path: path, Code: resp.StatusCode, Body: strings.TrimSpace(string(data))}
		}
		if out == nil {
			return nil
		}
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("tron %s: respuesta inválida: %w", path, err)
		}
		return nil
	}
	return fmt.Errorf("tron %s: agotados los reintentos", path)
}

func hexAddr(b58 string) (string, error) { return tronAddressToHex(b58) }

// ---- blocks ----

type BlockRef struct {
	Number      uint64
	ID          string // 64 hex chars (no 0x)
	TimestampMs int64
}

type blockResp struct {
	BlockID     string `json:"blockID"`
	BlockHeader struct {
		RawData struct {
			Number    int64 `json:"number"`
			Timestamp int64 `json:"timestamp"`
		} `json:"raw_data"`
	} `json:"block_header"`
}

func (b blockResp) ref() (BlockRef, error) {
	if len(b.BlockID) != 64 {
		return BlockRef{}, errors.New("tron: bloque sin blockID válido")
	}
	if b.BlockHeader.RawData.Number < 0 {
		return BlockRef{}, errors.New("tron: número de bloque inválido")
	}
	return BlockRef{Number: uint64(b.BlockHeader.RawData.Number), ID: strings.ToLower(b.BlockID), TimestampMs: b.BlockHeader.RawData.Timestamp}, nil
}

// NowBlock returns the chain head.
func (n *FullNode) NowBlock(ctx context.Context) (BlockRef, error) {
	var r blockResp
	if err := n.post(ctx, "/wallet/getnowblock", map[string]any{}, &r); err != nil {
		return BlockRef{}, err
	}
	return r.ref()
}

// BlockByNum returns the CURRENT canonical block at `num` (header only).
func (n *FullNode) BlockByNum(ctx context.Context, num uint64) (BlockRef, error) {
	var r blockResp
	err := n.post(ctx, "/wallet/getblock", map[string]any{"id_or_num": strconv.FormatUint(num, 10), "detail": false}, &r)
	if err == nil {
		if ref, rerr := r.ref(); rerr == nil {
			return ref, nil
		}
	}
	// No usar el endpoint alternativo cuando el servidor está limitando peticiones.
	var statusErr *tronHTTPStatusError
	if errors.As(err, &statusErr) && statusErr.Code == http.StatusTooManyRequests {
		return BlockRef{}, err
	}

	// fallback for nodes without /wallet/getblock
	var r2 blockResp
	if err2 := n.post(ctx, "/wallet/getblockbynum", map[string]any{"num": num}, &r2); err2 != nil {
		return BlockRef{}, err2
	}
	return r2.ref()
}

// ---- transaction info / receipts ----

type TxLog struct {
	Address string   `json:"address"` // 20-byte hex, no 0x41 prefix
	Topics  []string `json:"topics"`
	Data    string   `json:"data"`
}

type TxInfo struct {
	ID             string `json:"id"`
	Fee            int64  `json:"fee"` // total TRX burned, in sun
	BlockNumber    int64  `json:"blockNumber"`
	BlockTimeStamp int64  `json:"blockTimeStamp"`
	Result         string `json:"result"` // "FAILED" on failure
	ResMessage     string `json:"resMessage"`
	Receipt        struct {
		EnergyFee        int64  `json:"energy_fee"`
		EnergyUsage      int64  `json:"energy_usage"`
		EnergyUsageTotal int64  `json:"energy_usage_total"`
		NetUsage         int64  `json:"net_usage"`
		NetFee           int64  `json:"net_fee"`
		Result           string `json:"result"`
	} `json:"receipt"`
	Log []TxLog `json:"log"`
}

// Success is strict: a TRC-20 call only counts as successful when the TVM
// reports SUCCESS.
func (i TxInfo) Success() bool {
	return i.Result != "FAILED" && i.Receipt.Result == "SUCCESS"
}

// SuccessNative is for plain TRX transfers, whose TransactionInfo carries no
// TVM result: success = not marked FAILED and no failing TVM result.
// BLOQUEADO (nodo real): confirmar la forma exacta de receipt en transferencias TRX.
func (i TxInfo) SuccessNative() bool {
	return i.Result != "FAILED" && (i.Receipt.Result == "" || i.Receipt.Result == "SUCCESS")
}

// TxInfoByID returns (nil, nil) when the node has no info for the tx yet.
func (n *FullNode) TxInfoByID(ctx context.Context, txid string) (*TxInfo, error) {
	var info TxInfo
	if err := n.post(ctx, "/wallet/gettransactioninfobyid", map[string]any{"value": txid}, &info); err != nil {
		return nil, err
	}
	if info.ID == "" {
		return nil, nil
	}
	return &info, nil
}

// TxInfosByBlock returns the TransactionInfo (incl. logs) of every tx in block `num`.
func (n *FullNode) TxInfosByBlock(ctx context.Context, num uint64) ([]TxInfo, error) {
	var raw json.RawMessage
	if err := n.post(ctx, "/wallet/gettransactioninfobyblocknum", map[string]any{"num": num}, &raw); err != nil {
		return nil, err
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, nil // empty block ("{}")
	}
	var infos []TxInfo
	if err := json.Unmarshal(trimmed, &infos); err != nil {
		return nil, fmt.Errorf("tron: infos del bloque %d inválidas: %w", num, err)
	}
	return infos, nil
}

// TxKnown reports whether the node knows the transaction (mined or, if the
// node exposes it, pending).
func (n *FullNode) TxKnown(ctx context.Context, txid string) (bool, error) {
	var r struct {
		TxID string `json:"txID"`
	}
	if err := n.post(ctx, "/wallet/gettransactionbyid", map[string]any{"value": txid}, &r); err != nil {
		return false, err
	}
	return r.TxID != "", nil
}

// ---- account / resources / prices ----

// AccountBalanceSun returns the TRX balance in sun (0 for a non-existent account).
func (n *FullNode) AccountBalanceSun(ctx context.Context, address string) (int64, error) {
	h, err := hexAddr(address)
	if err != nil {
		return 0, err
	}
	var r struct {
		Balance int64 `json:"balance"`
	}
	if err := n.post(ctx, "/wallet/getaccount", map[string]any{"address": h, "visible": false}, &r); err != nil {
		return 0, err
	}
	return r.Balance, nil
}

type AccountResources struct {
	FreeNetLimit      int64 `json:"freeNetLimit"`
	FreeNetUsed       int64 `json:"freeNetUsed"`
	NetLimit          int64 `json:"NetLimit"`
	NetUsed           int64 `json:"NetUsed"`
	EnergyLimit       int64 `json:"EnergyLimit"`
	EnergyUsed        int64 `json:"EnergyUsed"`
	TotalNetLimit     int64 `json:"TotalNetLimit"`
	TotalNetWeight    int64 `json:"TotalNetWeight"`
	TotalEnergyLimit  int64 `json:"TotalEnergyLimit"`
	TotalEnergyWeight int64 `json:"TotalEnergyWeight"`
}

func nonNeg(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}

func (r AccountResources) AvailableBandwidth() int64 {
	return nonNeg(r.FreeNetLimit-r.FreeNetUsed) + nonNeg(r.NetLimit-r.NetUsed)
}
func (r AccountResources) AvailableEnergy() int64 { return nonNeg(r.EnergyLimit - r.EnergyUsed) }

func (n *FullNode) AccountResources(ctx context.Context, address string) (AccountResources, error) {
	h, err := hexAddr(address)
	if err != nil {
		return AccountResources{}, err
	}
	var r AccountResources
	if err := n.post(ctx, "/wallet/getaccountresource", map[string]any{"address": h, "visible": false}, &r); err != nil {
		return AccountResources{}, err
	}
	return r, nil
}

// ChainParameters returns the network's dynamic parameters by key.
func (n *FullNode) ChainParameters(ctx context.Context) (map[string]int64, error) {
	var r struct {
		ChainParameter []struct {
			Key   string `json:"key"`
			Value int64  `json:"value"`
		} `json:"chainParameter"`
	}
	if err := n.post(ctx, "/wallet/getchainparameters", map[string]any{}, &r); err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(r.ChainParameter))
	for _, p := range r.ChainParameter {
		out[p.Key] = p.Value
	}
	return out, nil
}

// ---- constant calls ----

// TriggerConstant runs a read-only contract call and returns constant_result
// plus the energy it would consume.
func (n *FullNode) TriggerConstant(ctx context.Context, owner, contract, selector, parameter string) ([]string, int64, error) {
	oh, err := hexAddr(owner)
	if err != nil {
		return nil, 0, fmt.Errorf("owner: %w", err)
	}
	ch, err := hexAddr(contract)
	if err != nil {
		return nil, 0, fmt.Errorf("contrato: %w", err)
	}
	var r struct {
		Result struct {
			Result  bool   `json:"result"`
			Message string `json:"message"`
		} `json:"result"`
		ConstantResult []string `json:"constant_result"`
		EnergyUsed     int64    `json:"energy_used"`
	}
	err = n.post(ctx, "/wallet/triggerconstantcontract", map[string]any{
		"owner_address": oh, "contract_address": ch,
		"function_selector": selector, "parameter": parameter, "visible": false,
	}, &r)
	if err != nil {
		return nil, 0, err
	}
	if !r.Result.Result {
		return nil, 0, fmt.Errorf("triggerconstantcontract falló: %s", decodeNodeMessage(r.Result.Message))
	}
	return r.ConstantResult, r.EnergyUsed, nil
}

func decodeNodeMessage(m string) string {
	if d, err := hex.DecodeString(m); err == nil {
		return string(d)
	}
	return m
}

// ---- broadcast ----

// BroadcastError is a definitive answer from the node about a broadcast.
type BroadcastError struct {
	Code    string
	Message string
	TxID    string
}

func (e *BroadcastError) Error() string {
	return fmt.Sprintf("tron broadcast rechazado: %s: %s", e.Code, e.Message)
}

// BroadcastHex sends a serialized Transaction. On a definitive node answer it
// returns *BroadcastError; transport problems return a plain error (the caller
// must treat those as AMBIGUOUS: the node may have accepted the tx).
func (n *FullNode) BroadcastHex(ctx context.Context, txHex string) (string, error) {
	var r struct {
		Result  bool   `json:"result"`
		Code    string `json:"code"`
		TxID    string `json:"txid"`
		Message string `json:"message"`
	}
	if err := n.post(ctx, "/wallet/broadcasthex", map[string]any{"transaction": txHex}, &r); err != nil {
		return "", err
	}
	if r.Result {
		return r.TxID, nil
	}
	code := r.Code
	if code == "" {
		code = "OTHER_ERROR"
	}
	return "", &BroadcastError{Code: code, Message: decodeNodeMessage(r.Message), TxID: r.TxID}
}

// TRC20TransferParam builds the ABI parameter of transfer(address,uint256).
func TRC20TransferParam(toB58 string, amountRaw *big.Int) (string, error) {
	h, err := hexAddr(toB58)
	if err != nil {
		return "", err
	}
	if amountRaw == nil || amountRaw.Sign() < 0 || amountRaw.BitLen() > 256 {
		return "", errors.New("monto inválido")
	}
	return strings.Repeat("0", 24) + h[2:] + fmt.Sprintf("%064x", amountRaw), nil
}
