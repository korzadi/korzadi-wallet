package evmclient

import "math/big"

func newBigInt(v uint64) *big.Int {
	return new(big.Int).SetUint64(v)
}
