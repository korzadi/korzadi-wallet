// Package evmwallet: transaction signing.
//
// CRITICAL SCOPE NOTE: everything in this file takes a plaintext private
// key as input. Callers are responsible for decrypting it immediately
// before use and never logging, persisting, or returning it. This file
// never itself touches storage or logs the key.
package evmwallet

import (
	"crypto/ecdsa"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// erc20TransferSelector is the first 4 bytes of keccak256("transfer(address,uint256)").
const erc20TransferSelector = "a9059cbb"

func privateKeyFromHex(privateKeyHex string) (*ecdsa.PrivateKey, error) {
	cleaned := strings.TrimPrefix(privateKeyHex, "0x")
	k, err := crypto.HexToECDSA(cleaned)
	if err != nil {
		// SECURITY: go-ethereum's error can echo the offending character of
		// the private key ("invalid hex character 'g' in private key").
		// Never propagate it; callers wrap and log this error.
		return nil, errors.New("formato de clave inválido")
	}
	return k, nil
}

// SignedTransaction is what callers need to broadcast plus a couple of
// fields useful for logging/persistence without re-parsing the raw tx.
type SignedTransaction struct {
	RawTx  []byte
	TxHash string
	Nonce  uint64
}

// BuildAndSignNativeTransfer signs a plain BNB transfer (used by the gas
// station to top up a user's deposit address with just enough native
// currency to pay for their withdrawal's gas — the user never needs to
// hold or manage BNB themselves).
func BuildAndSignNativeTransfer(privateKeyHex string, chainID *big.Int, nonce uint64, to string, amountWei *big.Int, gasLimit uint64, gasPriceWei *big.Int) (*SignedTransaction, error) {
	privKey, err := privateKeyFromHex(privateKeyHex)
	if err != nil {
		return nil, fmt.Errorf("evmwallet: clave privada inválida: %w", err)
	}
	if !common.IsHexAddress(to) {
		return nil, fmt.Errorf("evmwallet: dirección destino inválida: %s", to)
	}

	toAddr := common.HexToAddress(to)
	tx := types.NewTransaction(nonce, toAddr, amountWei, gasLimit, gasPriceWei, nil)

	return signLegacyTx(tx, privKey, chainID)
}

// BuildAndSignERC20Transfer signs a BEP-20/ERC-20 transfer(to, amount) call
// — this is what actually moves USDT out of a user's deposit address for a
// withdrawal.
func BuildAndSignERC20Transfer(privateKeyHex string, chainID *big.Int, nonce uint64, contractAddress, to string, amountRaw *big.Int, gasLimit uint64, gasPriceWei *big.Int) (*SignedTransaction, error) {
	privKey, err := privateKeyFromHex(privateKeyHex)
	if err != nil {
		return nil, fmt.Errorf("evmwallet: clave privada inválida: %w", err)
	}
	if !common.IsHexAddress(contractAddress) {
		return nil, fmt.Errorf("evmwallet: dirección de contrato inválida: %s", contractAddress)
	}
	if !common.IsHexAddress(to) {
		return nil, fmt.Errorf("evmwallet: dirección destino inválida: %s", to)
	}

	data, err := encodeERC20Transfer(to, amountRaw)
	if err != nil {
		return nil, err
	}

	contractAddr := common.HexToAddress(contractAddress)
	tx := types.NewTransaction(nonce, contractAddr, big.NewInt(0), gasLimit, gasPriceWei, data)

	return signLegacyTx(tx, privKey, chainID)
}

func BuildAndSignNativeTransfer1559(privateKeyHex string, chainID *big.Int, nonce uint64, to string, amountWei *big.Int, gasLimit uint64, maxFeePerGasWei, priorityFeePerGasWei *big.Int) (*SignedTransaction, error) {
	if chainID == nil || chainID.Sign() <= 0 || amountWei == nil || amountWei.Sign() <= 0 || maxFeePerGasWei == nil || maxFeePerGasWei.Sign() <= 0 || priorityFeePerGasWei == nil || priorityFeePerGasWei.Sign() <= 0 || priorityFeePerGasWei.Cmp(maxFeePerGasWei) > 0 || gasLimit == 0 {
		return nil, errors.New("evmwallet: parámetros EIP-1559 inválidos")
	}
	privKey, err := privateKeyFromHex(privateKeyHex)
	if err != nil {
		return nil, fmt.Errorf("evmwallet: clave privada inválida: %w", err)
	}
	if !common.IsHexAddress(to) {
		return nil, fmt.Errorf("evmwallet: dirección destino inválida: %s", to)
	}
	return signDynamicTx(types.NewTx(&types.DynamicFeeTx{ChainID: new(big.Int).Set(chainID), Nonce: nonce, GasTipCap: new(big.Int).Set(priorityFeePerGasWei), GasFeeCap: new(big.Int).Set(maxFeePerGasWei), Gas: gasLimit, To: ptrAddress(common.HexToAddress(to)), Value: new(big.Int).Set(amountWei)}), privKey, chainID)
}

func BuildAndSignERC20Transfer1559(privateKeyHex string, chainID *big.Int, nonce uint64, contractAddress, to string, amountRaw *big.Int, gasLimit uint64, maxFeePerGasWei, priorityFeePerGasWei *big.Int) (*SignedTransaction, error) {
	if chainID == nil || chainID.Sign() <= 0 || amountRaw == nil || amountRaw.Sign() <= 0 || maxFeePerGasWei == nil || maxFeePerGasWei.Sign() <= 0 || priorityFeePerGasWei == nil || priorityFeePerGasWei.Sign() <= 0 || priorityFeePerGasWei.Cmp(maxFeePerGasWei) > 0 || gasLimit == 0 {
		return nil, errors.New("evmwallet: parámetros EIP-1559 inválidos")
	}
	privKey, err := privateKeyFromHex(privateKeyHex)
	if err != nil {
		return nil, fmt.Errorf("evmwallet: clave privada inválida: %w", err)
	}
	if !common.IsHexAddress(contractAddress) {
		return nil, fmt.Errorf("evmwallet: dirección de contrato inválida: %s", contractAddress)
	}
	if !common.IsHexAddress(to) {
		return nil, fmt.Errorf("evmwallet: dirección destino inválida: %s", to)
	}
	data, err := encodeERC20Transfer(to, amountRaw)
	if err != nil {
		return nil, err
	}
	return signDynamicTx(types.NewTx(&types.DynamicFeeTx{ChainID: new(big.Int).Set(chainID), Nonce: nonce, GasTipCap: new(big.Int).Set(priorityFeePerGasWei), GasFeeCap: new(big.Int).Set(maxFeePerGasWei), Gas: gasLimit, To: ptrAddress(common.HexToAddress(contractAddress)), Value: big.NewInt(0), Data: data}), privKey, chainID)
}

func ptrAddress(address common.Address) *common.Address { return &address }

func encodeERC20Transfer(to string, amountRaw *big.Int) ([]byte, error) {
	selector, err := hex.DecodeString(erc20TransferSelector)
	if err != nil {
		return nil, fmt.Errorf("evmwallet: selector de función inválido: %w", err)
	}

	toAddr := common.HexToAddress(to)
	paddedTo := common.LeftPadBytes(toAddr.Bytes(), 32)
	paddedAmount := common.LeftPadBytes(amountRaw.Bytes(), 32)

	data := make([]byte, 0, 4+32+32)
	data = append(data, selector...)
	data = append(data, paddedTo...)
	data = append(data, paddedAmount...)
	return data, nil
}

func signLegacyTx(tx *types.Transaction, privKey *ecdsa.PrivateKey, chainID *big.Int) (*SignedTransaction, error) {
	signer := types.NewEIP155Signer(chainID)
	signedTx, err := types.SignTx(tx, signer, privKey)
	if err != nil {
		return nil, fmt.Errorf("evmwallet: firmar transacción: %w", err)
	}

	raw, err := signedTx.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("evmwallet: serializar transacción firmada: %w", err)
	}

	return &SignedTransaction{
		RawTx:  raw,
		TxHash: signedTx.Hash().Hex(),
		Nonce:  tx.Nonce(),
	}, nil
}

func signDynamicTx(tx *types.Transaction, privKey *ecdsa.PrivateKey, chainID *big.Int) (*SignedTransaction, error) {
	if tx.GasFeeCap().Sign() <= 0 || tx.GasTipCap().Sign() <= 0 || tx.GasTipCap().Cmp(tx.GasFeeCap()) > 0 {
		return nil, fmt.Errorf("evmwallet: fees EIP-1559 inválidas")
	}
	signedTx, err := types.SignTx(tx, types.LatestSignerForChainID(chainID), privKey)
	if err != nil {
		return nil, fmt.Errorf("evmwallet: firmar transacción EIP-1559: %w", err)
	}
	raw, err := signedTx.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("evmwallet: serializar transacción firmada: %w", err)
	}
	return &SignedTransaction{RawTx: raw, TxHash: signedTx.Hash().Hex(), Nonce: tx.Nonce()}, nil
}

// VerifySender decodes a signed raw transaction and checks that it was signed
// by wantAddress on chainID. AUDIT FIX (defense in depth): callers decrypt a
// key from storage and sign with it; if the stored key did not belong to the
// address the nonce was allocated for (corruption, or a tampered/swapped DB
// row), the transaction would silently be sent from a DIFFERENT account.
func VerifySender(rawTx []byte, chainID *big.Int, wantAddress string) error {
	tx := new(types.Transaction)
	if err := tx.UnmarshalBinary(rawTx); err != nil {
		return fmt.Errorf("evmwallet: tx firmada ilegible: %w", err)
	}
	if tx.ChainId().Cmp(chainID) != 0 {
		return fmt.Errorf("evmwallet: la tx firmada tiene otro chain id")
	}
	sender, err := types.Sender(types.LatestSignerForChainID(chainID), tx)
	if err != nil {
		return fmt.Errorf("evmwallet: no se pudo recuperar el remitente: %w", err)
	}
	if !common.IsHexAddress(wantAddress) || sender != common.HexToAddress(wantAddress) {
		return fmt.Errorf("evmwallet: el remitente de la tx firmada no es la dirección esperada")
	}
	return nil
}
