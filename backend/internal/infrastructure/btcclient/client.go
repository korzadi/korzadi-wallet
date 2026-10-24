package btcclient

import (
	"context"
	"fmt"
)

// Client is the typed Bitcoin Core RPC surface this backend needs. Safe
// for concurrent use — construct one per process and share it, same as
// evmclient.Client.
type Client struct {
	cfg Config
	rpc *rpcClient
}

// Dial does not actually open a persistent connection (Bitcoin Core RPC is
// plain HTTP, one request per call) but is named to match evmclient.Dial
// and performs the same fail-fast verification: a real RPC call against
// the configured node before returning, so a misconfigured endpoint is
// caught at boot.
func Dial(ctx context.Context, cfg Config) (*Client, error) {
	c := &Client{cfg: cfg, rpc: newRPCClient(cfg)}
	if _, err := c.GetBlockCount(ctx); err != nil {
		return nil, fmt.Errorf("btc rpc: verificación inicial falló: %w", err)
	}
	return c, nil
}

func (c *Client) Config() Config { return c.cfg }

func (c *Client) GetBlockCount(ctx context.Context) (uint64, error) {
	var height uint64
	err := c.rpc.call(ctx, "getblockcount", nil, &height)
	return height, err
}

func (c *Client) GetBlockHash(ctx context.Context, height uint64) (string, error) {
	var hash string
	err := c.rpc.call(ctx, "getblockhash", []interface{}{height}, &hash)
	return hash, err
}

// blockScriptPubKey mirrors the relevant subset of Bitcoin Core's decoded
// scriptPubKey JSON — specifically the `address` field it fills in for any
// standard (non-multisig, non-OP_RETURN) output.
type blockScriptPubKey struct {
	Address string `json:"address"`
}

type blockVout struct {
	Value        float64           `json:"value"` // BTC, not satoshis — converted immediately, never stored as float
	N            uint32            `json:"n"`
	ScriptPubKey blockScriptPubKey `json:"scriptPubKey"`
}

type blockTx struct {
	TxID string      `json:"txid"`
	Vout []blockVout `json:"vout"`
}

// verboseBlock is the shape returned by `getblock <hash> 2` — verbosity 2
// includes full decoded transaction data (including scriptPubKey.address),
// which is exactly what lets this client avoid any local script-parsing
// dependency.
type verboseBlock struct {
	Hash   string    `json:"hash"`
	Height uint64    `json:"height"`
	Tx     []blockTx `json:"tx"`
}

// GetBlockOutputs returns every output of every transaction in the block
// identified by blockHash, with addresses already decoded by the node.
// Outputs whose scriptPubKey doesn't resolve to a single address (bare
// multisig, OP_RETURN, ...) are omitted — they can never be a deposit to a
// single managed address.
func (c *Client) GetBlockOutputs(ctx context.Context, blockHash string) (height uint64, hash string, outputs []rawOutput, err error) {
	var block verboseBlock
	if err := c.rpc.call(ctx, "getblock", []interface{}{blockHash, 2}, &block); err != nil {
		return 0, "", nil, err
	}

	for _, tx := range block.Tx {
		for _, vout := range tx.Vout {
			if vout.ScriptPubKey.Address == "" {
				continue
			}
			outputs = append(outputs, rawOutput{
				TxHash:     tx.TxID,
				VoutIndex:  vout.N,
				Address:    vout.ScriptPubKey.Address,
				AmountSats: btcToSats(vout.Value),
			})
		}
	}
	return block.Height, block.Hash, outputs, nil
}

type rawOutput struct {
	TxHash     string
	VoutIndex  uint32
	Address    string
	AmountSats int64
}

// btcToSats converts a BTC amount as returned by Bitcoin Core (a float64
// in its JSON, unfortunately — that's the node's wire format, not a choice
// made here) into an exact integer satoshi count. This is the ONLY place
// in the Bitcoin adapter a float ever appears, and it is immediately
// converted to an exact int64 — no monetary value is ever stored, compared,
// or summed as a float anywhere else in this codebase (spec: never
// float64 for money).
func btcToSats(btc float64) int64 {
	return int64(btc*1e8 + 0.5*sign(btc))
}

func sign(v float64) float64 {
	if v < 0 {
		return -1
	}
	return 1
}

type rawTxVerbose struct {
	TxID          string      `json:"txid"`
	Confirmations uint64      `json:"confirmations"`
	BlockHash     string      `json:"blockhash"`
	Vout          []blockVout `json:"vout"`
}

// GetRawTransaction returns the confirmation status and outputs of txHash,
// or found=false if the node doesn't know about it (never mined and not in
// the mempool, or -txindex is disabled and it's neither recent nor
// relevant to a wallet the node tracks).
func (c *Client) GetRawTransaction(ctx context.Context, txHash string) (found bool, confirmations uint64, blockHash string, err error) {
	var tx rawTxVerbose
	callErr := c.rpc.call(ctx, "getrawtransaction", []interface{}{txHash, true}, &tx)
	if callErr != nil {
		if rpcErr, ok := asRPCError(callErr); ok && (rpcErr.Code == -5) {
			// -5 = "No such mempool or blockchain transaction" — this is
			// Bitcoin Core's normal "not found" response, not a failure.
			return false, 0, "", nil
		}
		return false, 0, "", callErr
	}
	return true, tx.Confirmations, tx.BlockHash, nil
}

func asRPCError(err error) (*rpcError, bool) {
	rpcErr, ok := err.(*rpcError)
	return rpcErr, ok
}

// EstimateSmartFee returns the estimated fee rate, in satoshis per virtual
// byte, to confirm within targetBlocks blocks.
func (c *Client) EstimateSmartFee(ctx context.Context, targetBlocks int) (satsPerVByte int64, err error) {
	var result struct {
		FeeRate float64  `json:"feerate"` // BTC per kvB
		Errors  []string `json:"errors"`
	}
	if err := c.rpc.call(ctx, "estimatesmartfee", []interface{}{targetBlocks}, &result); err != nil {
		return 0, err
	}
	if len(result.Errors) > 0 {
		return 0, fmt.Errorf("btc rpc: estimatesmartfee: %v", result.Errors)
	}
	// BTC/kvB -> sats/vB : (BTC/kvB * 1e8 sats/BTC) / 1000 vB/kvB
	satsPerKvB := btcToSats(result.FeeRate)
	return satsPerKvB / 1000, nil
}

func (c *Client) SendRawTransaction(ctx context.Context, rawTxHex string) (txHash string, err error) {
	err = c.rpc.call(ctx, "sendrawtransaction", []interface{}{rawTxHex}, &txHash)
	return txHash, err
}
