package evmclient

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
)

// PendingNonceAt returns the next nonce to use for `address`, including
// pending (not-yet-mined) transactions — required so consecutive
// withdrawals/gas-topups from the same address don't reuse a nonce.
func (c *Client) PendingNonceAt(ctx context.Context, address string) (uint64, error) {
	if !common.IsHexAddress(address) {
		return 0, fmt.Errorf("evmclient: dirección inválida: %s", address)
	}
	var nonce uint64
	err := c.withRetry(ctx, "PendingNonceAt", func(ctx context.Context) error {
		v, err := c.eth.PendingNonceAt(ctx, common.HexToAddress(address))
		if err != nil {
			return err
		}
		nonce = v
		return nil
	})
	return nonce, err
}

// NativeBalanceWei returns the native-coin balance of `address` in wei
// (unscaled) — ETH on Ethereum/L2s, BNB on BSC, MATIC/POL on Polygon.
func (c *Client) NativeBalanceWei(ctx context.Context, address string) (*big.Int, error) {
	if !common.IsHexAddress(address) {
		return nil, fmt.Errorf("evmclient: dirección inválida: %s", address)
	}
	var balance *big.Int
	err := c.withRetry(ctx, "NativeBalanceWei", func(ctx context.Context) error {
		v, err := c.eth.BalanceAt(ctx, common.HexToAddress(address), nil)
		if err != nil {
			return err
		}
		balance = v
		return nil
	})
	return balance, err
}

// EstimateGasERC20Transfer estimates the gas units needed for a
// transfer(to, amount) call on `contract`, called as if `from` were the
// sender — this must match the actual sender at broadcast time, since gas
// cost can depend on the sender's existing token balance/allowances.
func (c *Client) EstimateGasERC20Transfer(ctx context.Context, contract, from, to string, amountRaw *big.Int) (uint64, error) {
	data, err := erc20TransferCalldata(to, amountRaw)
	if err != nil {
		return 0, err
	}

	fromAddr := common.HexToAddress(from)
	contractAddr := common.HexToAddress(contract)

	var gas uint64
	err = c.withRetry(ctx, "EstimateGasERC20Transfer", func(ctx context.Context) error {
		v, err := c.eth.EstimateGas(ctx, ethereum.CallMsg{
			From: fromAddr,
			To:   &contractAddr,
			Data: data,
		})
		if err != nil {
			return err
		}
		gas = v
		return nil
	})
	return gas, err
}

// erc20TransferCalldata mirrors evmwallet's encoding so gas estimation
// calls the exact same calldata that will actually be broadcast.
func erc20TransferCalldata(to string, amountRaw *big.Int) ([]byte, error) {
	if !common.IsHexAddress(to) {
		return nil, fmt.Errorf("evmclient: dirección destino inválida: %s", to)
	}
	selector := []byte{0xa9, 0x05, 0x9c, 0xbb} // transfer(address,uint256)
	toAddr := common.HexToAddress(to)
	paddedTo := common.LeftPadBytes(toAddr.Bytes(), 32)
	paddedAmount := common.LeftPadBytes(amountRaw.Bytes(), 32)

	data := make([]byte, 0, 4+32+32)
	data = append(data, selector...)
	data = append(data, paddedTo...)
	data = append(data, paddedAmount...)
	return data, nil
}

// NonceAt returns the account nonce as of the LATEST block — only
// transactions already mined are counted (unlike PendingNonceAt, which also
// counts the mempool). Bloque 6B uses the pair to reconcile an ambiguous
// broadcast: pending > latest means something is still in the mempool.
func (c *Client) NonceAt(ctx context.Context, address string) (uint64, error) {
	if !common.IsHexAddress(address) {
		return 0, fmt.Errorf("evmclient: dirección inválida: %s", address)
	}
	var nonce uint64
	err := c.withRetry(ctx, "NonceAt", func(ctx context.Context) error {
		v, err := c.eth.NonceAt(ctx, common.HexToAddress(address), nil)
		if err != nil {
			return err
		}
		nonce = v
		return nil
	})
	return nonce, err
}

// EstimateGasNative estimates the gas units for a plain native-coin transfer
// of valueWei from `from` to `to` (21000 for an EOA recipient, more if the
// recipient is a contract with a receive hook).
func (c *Client) EstimateGasNative(ctx context.Context, from, to string, valueWei *big.Int) (uint64, error) {
	if !common.IsHexAddress(from) || !common.IsHexAddress(to) {
		return 0, fmt.Errorf("evmclient: dirección inválida en estimación de gas nativo")
	}
	if valueWei == nil || valueWei.Sign() < 0 {
		return 0, fmt.Errorf("evmclient: valor inválido en estimación de gas nativo")
	}
	fromAddr := common.HexToAddress(from)
	toAddr := common.HexToAddress(to)

	var gas uint64
	err := c.withRetry(ctx, "EstimateGasNative", func(ctx context.Context) error {
		v, err := c.eth.EstimateGas(ctx, ethereum.CallMsg{
			From:  fromAddr,
			To:    &toAddr,
			Value: valueWei,
		})
		if err != nil {
			return err
		}
		gas = v
		return nil
	})
	return gas, err
}

// TransactionKnown reports whether the node knows the transaction at all —
// either mined or waiting in its mempool. A transaction the node has never
// seen (or already dropped) returns (false, nil); only a genuine RPC failure
// returns an error.
func (c *Client) TransactionKnown(ctx context.Context, txHash [32]byte) (bool, error) {
	var known bool
	err := c.withRetry(ctx, "TransactionKnown", func(ctx context.Context) error {
		tx, _, err := c.eth.TransactionByHash(ctx, common.Hash(txHash))
		if err != nil {
			if errors.Is(err, ethereum.NotFound) {
				known = false
				return nil
			}
			return err
		}
		known = tx != nil
		return nil
	})
	return known, err
}
