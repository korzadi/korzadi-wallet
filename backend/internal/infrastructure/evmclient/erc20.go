package evmclient

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
)

const transferRecipientBatchSize = 100

// We deliberately use a minimal, hand-written ABI fragment instead of a
// full generated ERC-20 binding — no need for a heavy dependency just to
// read balanceOf/decimals/Transfer, and this fragment is identical for
// every ERC-20/BEP-20 token regardless of network, which is exactly the
// point: this file has no USDT-specific (or any other token-specific)
// code anywhere in it.
const erc20MinimalABI = `[
	{"constant":true,"inputs":[{"name":"_owner","type":"address"}],"name":"balanceOf","outputs":[{"name":"balance","type":"uint256"}],"type":"function"},
	{"constant":true,"inputs":[],"name":"decimals","outputs":[{"name":"","type":"uint8"}],"type":"function"},
	{"constant":true,"inputs":[],"name":"totalSupply","outputs":[{"name":"","type":"uint256"}],"type":"function"},
	{"anonymous":false,"inputs":[{"indexed":true,"name":"from","type":"address"},{"indexed":true,"name":"to","type":"address"},{"indexed":false,"name":"value","type":"uint256"}],"name":"Transfer","type":"event"}
]`

var erc20ABI abi.ABI

// transferEventSig is the keccak256 topic0 for Transfer(address,address,uint256).
var transferEventSig = crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))

func init() {
	parsed, err := abi.JSON(strings.NewReader(erc20MinimalABI))
	if err != nil {
		panic(fmt.Sprintf("evmclient: ABI mínima inválida: %v", err))
	}
	erc20ABI = parsed
}

// BalanceOf returns the raw (un-scaled) token balance of address on contract.
func (c *Client) BalanceOf(ctx context.Context, contract, address string) (*big.Int, error) {
	if !common.IsHexAddress(contract) {
		return nil, fmt.Errorf("evmclient: dirección de contrato inválida: %s", contract)
	}
	if !common.IsHexAddress(address) {
		return nil, fmt.Errorf("evmclient: dirección de wallet inválida: %s", address)
	}

	data, err := erc20ABI.Pack("balanceOf", common.HexToAddress(address))
	if err != nil {
		return nil, fmt.Errorf("evmclient: empaquetar balanceOf: %w", err)
	}

	contractAddr := common.HexToAddress(contract)
	result, err := c.CallContract(ctx, ethereum.CallMsg{To: &contractAddr, Data: data})
	if err != nil {
		return nil, fmt.Errorf("evmclient: balanceOf falló: %w", err)
	}

	out, err := erc20ABI.Unpack("balanceOf", result)
	if err != nil || len(out) == 0 {
		return nil, fmt.Errorf("evmclient: no se pudo interpretar respuesta de balanceOf: %w", err)
	}
	bal, ok := out[0].(*big.Int)
	if !ok {
		return nil, fmt.Errorf("evmclient: tipo inesperado en respuesta de balanceOf")
	}
	return bal, nil
}

// ScaledBalanceOf returns BalanceOf already divided by 10^decimals.
func (c *Client) ScaledBalanceOf(ctx context.Context, contract, address string, decimals int32) (decimal.Decimal, error) {
	raw, err := c.BalanceOf(ctx, contract, address)
	if err != nil {
		return decimal.Zero, err
	}
	return scaleDown(raw, decimals), nil
}

func scaleDown(raw *big.Int, decimals int32) decimal.Decimal {
	return decimal.NewFromBigInt(raw, 0).Div(decimal.New(1, decimals))
}

// TransferLog is a decoded, not-yet-persisted Transfer event.
type TransferLog struct {
	ContractAddress string
	TxHash          string
	LogIndex        uint
	BlockNumber     uint64
	BlockHash       string
	From            string
	To              string
	Value           *big.Int
}

// TransferEventsInRange scans [fromBlock, toBlock] for Transfer events
// emitted by `contract`. It filters at the RPC level by topic0 (event
// signature) and contract address, so it never has to pull unrelated logs.
// This works identically for every ERC-20/BEP-20-compatible token on any
// EVM network — the token's identity (which contract, which decimals) is
// entirely a caller/asset-registry concern.
func (c *Client) TransferEventsInRange(ctx context.Context, contract string, fromBlock, toBlock uint64) ([]TransferLog, error) {
	return c.transferEventsInRange(ctx, contract, fromBlock, toBlock, nil)
}

// TransferEventsInRangeForRecipients limits Transfer logs to a set of
// indexed `to` addresses. Recipients are split into bounded OR-topic batches
// so providers never need to return transfers for every address of a busy
// token contract. Each block-range batch retains the same adaptive split.
func (c *Client) TransferEventsInRangeForRecipients(ctx context.Context, contract string, fromBlock, toBlock uint64, recipients []string) ([]TransferLog, error) {
	if len(recipients) == 0 {
		return []TransferLog{}, nil
	}
	addresses := make([]common.Address, 0, len(recipients))
	seen := make(map[common.Address]struct{}, len(recipients))
	for _, recipient := range recipients {
		if !common.IsHexAddress(recipient) {
			return nil, fmt.Errorf("evmclient: dirección vigilada inválida")
		}
		address := common.HexToAddress(recipient)
		if _, ok := seen[address]; ok {
			continue
		}
		seen[address] = struct{}{}
		addresses = append(addresses, address)
	}
	sort.Slice(addresses, func(i, j int) bool { return addresses[i].Hex() < addresses[j].Hex() })
	var all []TransferLog
	for _, batch := range chunkRecipientAddresses(addresses) {
		logs, err := c.transferEventsInRange(ctx, contract, fromBlock, toBlock, batch)
		if err != nil {
			return nil, err
		}
		all = append(all, logs...)
	}
	return all, nil
}

func chunkRecipientAddresses(addresses []common.Address) [][]common.Address {
	var batches [][]common.Address
	for start := 0; start < len(addresses); start += transferRecipientBatchSize {
		end := start + transferRecipientBatchSize
		if end > len(addresses) {
			end = len(addresses)
		}
		batches = append(batches, addresses[start:end])
	}
	return batches
}

func (c *Client) transferEventsInRange(ctx context.Context, contract string, fromBlock, toBlock uint64, recipients []common.Address) ([]TransferLog, error) {
	if !common.IsHexAddress(contract) {
		return nil, fmt.Errorf("evmclient: dirección de contrato inválida: %s", contract)
	}
	if fromBlock > toBlock {
		return nil, fmt.Errorf("evmclient: rango de bloques inválido: %d > %d", fromBlock, toBlock)
	}

	logs, err := c.transferEventsInRangeAdaptive(ctx, contract, fromBlock, toBlock, recipients)
	if err == nil {
		return logs, nil
	}

	// RPC providers may serve eth_getLogs from a node that is behind the node
	// answering eth_blockNumber. Avoid parsing provider-specific error text:
	// use a fresh numeric head observation to identify the future-range case.
	if availableHead, headErr := c.LatestBlock(ctx); headErr == nil {
		if typedErr := blockRangeErrorForObservedHead(toBlock, availableHead, err); typedErr != nil {
			return nil, typedErr
		}
	}
	return nil, err
}

func blockRangeErrorForObservedHead(requestedTo, availableHead uint64, cause error) error {
	if requestedTo <= availableHead {
		return nil
	}
	return &domain.BlockRangeBeyondHeadError{
		RequestedTo:   requestedTo,
		AvailableHead: availableHead,
		Cause:         cause,
	}
}

func (c *Client) transferEventsInRangeAdaptive(ctx context.Context, contract string, fromBlock, toBlock uint64, recipients []common.Address) ([]TransferLog, error) {
	q := ethereum.FilterQuery{
		FromBlock: newBigInt(fromBlock),
		ToBlock:   newBigInt(toBlock),
		Addresses: []common.Address{common.HexToAddress(contract)},
		Topics:    transferTopics(recipients),
	}

	logs, err := c.FilterLogs(ctx, q)
	if err != nil {
		if !isLogLimitError(err) {
			return nil, fmt.Errorf(
				"evmclient: FilterLogs falló en rango [%d,%d]: %w",
				fromBlock,
				toBlock,
				err,
			)
		}

		// El proveedor rechazó el rango porque contiene demasiados logs.
		// Dividimos el rango por la mitad y procesamos cada parte.
		if fromBlock == toBlock {
			return nil, fmt.Errorf(
				"evmclient: límite de logs alcanzado incluso en un solo bloque %d: %w",
				fromBlock,
				err,
			)
		}

		mid := fromBlock + (toBlock-fromBlock)/2

		left, err := c.transferEventsInRangeAdaptive(
			ctx,
			contract,
			fromBlock,
			mid,
			recipients,
		)
		if err != nil {
			return nil, err
		}

		right, err := c.transferEventsInRangeAdaptive(
			ctx,
			contract,
			mid+1,
			toBlock,
			recipients,
		)
		if err != nil {
			return nil, err
		}

		return append(left, right...), nil
	}

	out := make([]TransferLog, 0, len(logs))
	for _, l := range logs {
		tl, err := decodeTransferLog(l)
		if err != nil {
			// Un log malformado no debe detener todo el escaneo.
			continue
		}
		out = append(out, tl)
	}

	return out, nil
}

func transferTopics(recipients []common.Address) [][]common.Hash {
	topics := [][]common.Hash{{transferEventSig}, nil}
	if len(recipients) == 0 {
		return topics
	}
	toTopics := make([]common.Hash, 0, len(recipients))
	for _, recipient := range recipients {
		toTopics = append(toTopics, common.BytesToHash(recipient.Bytes()))
	}
	return append(topics, toTopics)
}

func isLogLimitError(err error) bool {
	if err == nil {
		return false
	}

	msg := strings.ToLower(err.Error())

	return strings.Contains(msg, "logs count exceeds the limit") ||
		strings.Contains(msg, "log count exceeds the limit") ||
		strings.Contains(msg, "too many logs") ||
		strings.Contains(msg, "query returned more than")
}

func decodeTransferLog(l types.Log) (TransferLog, error) {
	if len(l.Topics) != 3 {
		return TransferLog{}, fmt.Errorf("Transfer log con topics inesperados: %d", len(l.Topics))
	}
	unpacked := struct{ Value *big.Int }{}
	if err := erc20ABI.UnpackIntoInterface(&unpacked, "Transfer", l.Data); err != nil {
		return TransferLog{}, err
	}

	return TransferLog{
		ContractAddress: l.Address.Hex(),
		TxHash:          l.TxHash.Hex(),
		LogIndex:        l.Index,
		BlockNumber:     l.BlockNumber,
		BlockHash:       l.BlockHash.Hex(),
		From:            common.HexToAddress(l.Topics[1].Hex()).Hex(),
		To:              common.HexToAddress(l.Topics[2].Hex()).Hex(),
		Value:           unpacked.Value,
	}, nil
}
