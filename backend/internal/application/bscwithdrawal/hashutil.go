package bscwithdrawal

import (
	"encoding/hex"
	"fmt"
	"strings"
)

func hexToHash32ForWithdrawal(s string) ([32]byte, error) {
	var out [32]byte
	s = strings.TrimPrefix(s, "0x")
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 32 {
		return out, fmt.Errorf("hash inválido: %s", s)
	}
	copy(out[:], b)
	return out, nil
}
