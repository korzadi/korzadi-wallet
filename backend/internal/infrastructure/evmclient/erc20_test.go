package evmclient

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/shopspring/decimal"
)

func TestScaleDown(t *testing.T) {
	raw := big.NewInt(1500000000000000000) // 1.5 * 10^18
	got := scaleDown(raw, 18)
	want := decimal.NewFromFloat(1.5)
	if !got.Equal(want) {
		t.Errorf("scaleDown = %s, esperaba %s", got, want)
	}
}

func buildTransferLog(t *testing.T, contract, from, to common.Address, value *big.Int, logIndex uint) types.Log {
	t.Helper()
	packedValue, err := erc20ABI.Events["Transfer"].Inputs.NonIndexed().Pack(value)
	if err != nil {
		t.Fatalf("no se pudo empaquetar value: %v", err)
	}
	return types.Log{
		Address: contract,
		Topics: []common.Hash{
			transferEventSig,
			common.BytesToHash(from.Bytes()),
			common.BytesToHash(to.Bytes()),
		},
		Data:        packedValue,
		BlockNumber: 1000,
		TxHash:      common.HexToHash("0xabc123"),
		Index:       logIndex,
	}
}

// TestDecodeTransferLog_Valid uses an Ethereum mainnet USDC contract
// address only as an arbitrary example — this decoder has no
// network/token-specific logic and works identically for any
// ERC-20/BEP-20-compatible contract on any EVM network.
func TestDecodeTransferLog_Valid(t *testing.T) {
	contract := common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48")
	from := common.HexToAddress("0x1111111111111111111111111111111111111111")
	to := common.HexToAddress("0x2222222222222222222222222222222222222222")
	value := big.NewInt(42000000) // 42 tokens if decimals=6

	l := buildTransferLog(t, contract, from, to, value, 3)
	tl, err := decodeTransferLog(l)
	if err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}
	if tl.From != from.Hex() {
		t.Errorf("from = %s, esperaba %s", tl.From, from.Hex())
	}
	if tl.To != to.Hex() {
		t.Errorf("to = %s, esperaba %s", tl.To, to.Hex())
	}
	if tl.Value.Cmp(value) != 0 {
		t.Errorf("value = %s, esperaba %s", tl.Value, value)
	}
	if tl.LogIndex != 3 {
		t.Errorf("log index = %d, esperaba 3 — necesario para idempotencia (network,contract,tx_hash,log_index)", tl.LogIndex)
	}
}

func TestDecodeTransferLog_WrongTopicCount(t *testing.T) {
	l := types.Log{
		Topics: []common.Hash{transferEventSig}, // missing from/to topics
		Data:   []byte{},
	}
	_, err := decodeTransferLog(l)
	if err == nil {
		t.Fatal("esperaba error por log con topics insuficientes (posible contrato no estándar)")
	}
}

func TestTransferTopicsFilterRecipientAndBatchSize(t *testing.T) {
	to := common.HexToAddress("0x2222222222222222222222222222222222222222")
	topics := transferTopics([]common.Address{to})
	if len(topics) != 3 || len(topics[0]) != 1 || topics[0][0] != transferEventSig || topics[1] != nil || len(topics[2]) != 1 {
		t.Fatalf("unexpected Transfer topic filter: %#v", topics)
	}
	if topics[2][0] != common.BytesToHash(to.Bytes()) {
		t.Fatal("recipient must be encoded as indexed topic 2")
	}
	addresses := make([]common.Address, 205)
	for i := range addresses {
		addresses[i] = common.BigToAddress(big.NewInt(int64(i + 1)))
	}
	batches := chunkRecipientAddresses(addresses)
	if len(batches) != 3 || len(batches[0]) != 100 || len(batches[1]) != 100 || len(batches[2]) != 5 {
		t.Fatalf("recipient batches: lengths unexpected (%d batches)", len(batches))
	}
}

// TestDecodeTransferLog_SharedAcrossNetworks documents the key
// architectural property this test file exists to protect: the same
// decoder is reused, unmodified, whether the log came from Ethereum, BSC,
// Polygon, Arbitrum, Optimism or Base — there is no per-network Transfer
// decoding logic anywhere in this package.
func TestDecodeTransferLog_SharedAcrossNetworks(t *testing.T) {
	from := common.HexToAddress("0x3333333333333333333333333333333333333333")
	to := common.HexToAddress("0x4444444444444444444444444444444444444444")
	value := big.NewInt(1000000)

	contracts := []common.Address{
		common.HexToAddress("0x55d398326f99059fF775485246999027B3197955"), // BEP-20 USDT
		common.HexToAddress("0xdAC17F958D2ee523a2206206994597C13D831ec7"), // ERC-20 USDT
	}
	for _, contract := range contracts {
		l := buildTransferLog(t, contract, from, to, value, 0)
		tl, err := decodeTransferLog(l)
		if err != nil {
			t.Fatalf("contrato %s: no esperaba error: %v", contract.Hex(), err)
		}
		if tl.ContractAddress != contract.Hex() {
			t.Errorf("contract = %s, esperaba %s", tl.ContractAddress, contract.Hex())
		}
	}
}
