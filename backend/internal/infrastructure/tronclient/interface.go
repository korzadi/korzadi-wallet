package tronclient

import (
	"context"

	"github.com/shopspring/decimal"
)

// TRC20BalanceReader is the minimal contract application code needs to
// read an on-chain TRC20 balance. Currently satisfied only by
// NodeRealClient (nodereal.go) — TronGrid support was removed once
// NodeReal covered the only balance-reading use case in the app.
type TRC20BalanceReader interface {
	GetTRC20Balance(ctx context.Context, walletAddress, contractAddress string, decimals int32) (decimal.Decimal, error)
}
