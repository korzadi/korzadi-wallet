package blockchainwithdrawal

import (
	"bytes"
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/cryptutil"
	"korzadi-wallet/backend/internal/infrastructure/evmwallet"
)

// SignRequest is the family-agnostic-by-construction input of a Signer. The
// withdrawal business logic builds it and never sees a private key: keys are
// resolved, decrypted, used and discarded INSIDE the Signer implementation.
//
// GasPriceWei selects a legacy transaction; MaxFeePerGasWei and
// MaxPriorityFeePerGasWei select a dynamic-fee EIP-1559 transaction.
type SignRequest struct {
	Network domain.Network
	UserID  uuid.UUID

	FromAddress string // the address whose key signs (must be the user's own)
	ToAddress   string

	AssetKind       domain.AssetKind // native | token
	ContractAddress string           // token only — ALWAYS from the Asset Registry
	AmountRaw       *big.Int         // base units

	ChainID                 *big.Int
	Nonce                   uint64
	GasLimit                uint64
	GasPriceWei             *big.Int
	MaxFeePerGasWei         *big.Int
	MaxPriorityFeePerGasWei *big.Int

	// Tron carries the TRON-only inputs (reference block, expiration, fee_limit).
	Tron *TronSignParams
}

// SignedTx is the result of signing: a raw transaction ready to broadcast.
type SignedTx struct {
	Raw    []byte
	TxHash string
	Nonce  uint64
}

// Signer is the signing seam: EVMSigner today; a BitcoinSigner (currently
// the always-refusing DisabledBitcoinSigner) later. Implementations must
// never log or return key material, and must discard plaintext keys as soon
// as signing completes.
type Signer interface {
	Sign(ctx context.Context, req SignRequest) (*SignedTx, error)
}

// KeyResolver finds the ENCRYPTED private key of (network, address) scoped
// to userID — a withdrawal can never be signed with another user's key.
// Satisfied by postgres.CryptoAddressKeyRepository.
type KeyResolver interface {
	FindEncryptedPrivateKey(ctx context.Context, network domain.Network, address string, userID uuid.UUID) (string, error)
}

// KeySourceResolver is optionally implemented by a KeyResolver that can tell
// WHICH stored column the blob came from, so the signer authenticates it with
// the matching AAD (custody_wallets vs crypto_addresses). A KeyResolver that
// does not implement it is assumed to read crypto_addresses.
type KeySourceResolver interface {
	FindEncryptedKeyWithAAD(ctx context.Context, network domain.Network, address string, userID uuid.UUID) (string, cryptutil.AAD, error)
}

// Decryptor decrypts a custody blob (cryptutil.Encryptor). The AAD binds the
// blob to the row it was read from; ctx lets the Encryptor lazily upgrade a
// legacy/rotated blob in place.
type Decryptor interface {
	Decrypt(ctx context.Context, ciphertext string, aad cryptutil.AAD) (string, error)
}

// EVMSigner signs legacy EVM transactions with the user's own custody key.
type EVMSigner struct {
	keys KeyResolver
	dec  Decryptor
}

func NewEVMSigner(keys KeyResolver, dec Decryptor) *EVMSigner {
	return &EVMSigner{keys: keys, dec: dec}
}

// Sign decrypts the key ONLY for the duration of this call, signs, verifies
// that what was signed is exactly what was requested, and drops the
// plaintext reference before returning. (Go strings are immutable, so the
// plaintext cannot be zeroed in place; the reference is dropped immediately
// so it becomes collectible — the same limitation the rest of the codebase's
// custody code documents.)
func (s *EVMSigner) Sign(ctx context.Context, req SignRequest) (*SignedTx, error) {
	if err := validateSignRequest(req); err != nil {
		return nil, err
	}

	var (
		encrypted string
		aad       cryptutil.AAD
		err       error
	)
	if ks, ok := s.keys.(KeySourceResolver); ok {
		encrypted, aad, err = ks.FindEncryptedKeyWithAAD(ctx, req.Network, req.FromAddress, req.UserID)
	} else {
		encrypted, err = s.keys.FindEncryptedPrivateKey(ctx, req.Network, req.FromAddress, req.UserID)
		aad = cryptutil.ForCryptoAddress(req.FromAddress)
	}
	if err != nil {
		return nil, fmt.Errorf("signer: resolver llave de custodia: %w", err)
	}
	keyHex, err := s.dec.Decrypt(ctx, encrypted, aad)
	if err != nil {
		return nil, fmt.Errorf("signer: descifrar llave de custodia")
	}
	defer func() { keyHex = "" }()

	var signed *evmwallet.SignedTransaction
	switch req.AssetKind {
	case domain.AssetKindNative:
		if req.MaxFeePerGasWei != nil {
			signed, err = evmwallet.BuildAndSignNativeTransfer1559(keyHex, req.ChainID, req.Nonce, req.ToAddress, req.AmountRaw, req.GasLimit, req.MaxFeePerGasWei, req.MaxPriorityFeePerGasWei)
		} else {
			signed, err = evmwallet.BuildAndSignNativeTransfer(keyHex, req.ChainID, req.Nonce, req.ToAddress, req.AmountRaw, req.GasLimit, req.GasPriceWei)
		}
	case domain.AssetKindToken:
		if req.MaxFeePerGasWei != nil {
			signed, err = evmwallet.BuildAndSignERC20Transfer1559(keyHex, req.ChainID, req.Nonce, req.ContractAddress, req.ToAddress, req.AmountRaw, req.GasLimit, req.MaxFeePerGasWei, req.MaxPriorityFeePerGasWei)
		} else {
			signed, err = evmwallet.BuildAndSignERC20Transfer(keyHex, req.ChainID, req.Nonce, req.ContractAddress, req.ToAddress, req.AmountRaw, req.GasLimit, req.GasPriceWei)
		}
	default:
		return nil, fmt.Errorf("signer: tipo de asset no soportado: %q", req.AssetKind)
	}
	if err != nil {
		// evmwallet errors never contain the key, but wrap without %v of any key-bearing value anyway.
		return nil, fmt.Errorf("signer: firmar transacción: %w", err)
	}

	if err := verifySignedEVM(req, signed.RawTx); err != nil {
		return nil, err
	}
	return &SignedTx{Raw: signed.RawTx, TxHash: strings.ToLower(signed.TxHash), Nonce: signed.Nonce}, nil
}

func validateSignRequest(req SignRequest) error {
	if req.Network.Family() != domain.FamilyEVM {
		return fmt.Errorf("signer: red no EVM: %s", req.Network)
	}
	if req.ChainID == nil || req.ChainID.Sign() <= 0 {
		return fmt.Errorf("signer: chain id requerido")
	}
	if req.AmountRaw == nil || req.AmountRaw.Sign() <= 0 {
		return fmt.Errorf("signer: monto inválido")
	}
	legacy := req.GasPriceWei != nil && req.GasPriceWei.Sign() > 0 && req.MaxFeePerGasWei == nil && req.MaxPriorityFeePerGasWei == nil
	dynamic := req.GasPriceWei == nil && req.MaxFeePerGasWei != nil && req.MaxPriorityFeePerGasWei != nil && req.MaxFeePerGasWei.Sign() > 0 && req.MaxPriorityFeePerGasWei.Sign() > 0 && req.MaxPriorityFeePerGasWei.Cmp(req.MaxFeePerGasWei) <= 0
	if (!legacy && !dynamic) || req.GasLimit == 0 {
		return fmt.Errorf("signer: gas inválido")
	}
	if !common.IsHexAddress(req.FromAddress) || !common.IsHexAddress(req.ToAddress) {
		return fmt.Errorf("signer: direcciones inválidas")
	}
	if req.AssetKind == domain.AssetKindToken && !common.IsHexAddress(req.ContractAddress) {
		return fmt.Errorf("signer: contrato de token inválido")
	}
	if req.AssetKind == domain.AssetKindNative && req.ContractAddress != "" {
		return fmt.Errorf("signer: un asset nativo no lleva contrato")
	}
	return nil
}

// verifySignedEVM decodes the freshly signed transaction and checks it
// matches the request field by field (chain, nonce, gas, recipient, value /
// calldata, and that the recovered sender is FromAddress) — defense in depth
// against a signing-path bug ever producing a different transfer than the
// one the ledger reserved funds for.
func verifySignedEVM(req SignRequest, raw []byte) error {
	tx := new(types.Transaction)
	if err := tx.UnmarshalBinary(raw); err != nil {
		return fmt.Errorf("signer: tx firmada ilegible: %w", err)
	}
	if tx.ChainId().Cmp(req.ChainID) != 0 {
		return fmt.Errorf("signer: la tx firmada tiene otro chain id")
	}
	if tx.Nonce() != req.Nonce || tx.Gas() != req.GasLimit {
		return fmt.Errorf("signer: la tx firmada no coincide (nonce/gas)")
	}
	if req.MaxFeePerGasWei != nil {
		if tx.Type() != types.DynamicFeeTxType || tx.GasFeeCap().Cmp(req.MaxFeePerGasWei) != 0 || tx.GasTipCap().Cmp(req.MaxPriorityFeePerGasWei) != 0 {
			return fmt.Errorf("signer: fees EIP-1559 firmadas no coinciden")
		}
	} else if tx.Type() == types.DynamicFeeTxType || tx.GasPrice().Cmp(req.GasPriceWei) != 0 {
		return fmt.Errorf("signer: gas price firmado no coincide")
	}
	sender, err := types.Sender(types.LatestSignerForChainID(req.ChainID), tx)
	if err != nil {
		return fmt.Errorf("signer: no se pudo recuperar el remitente: %w", err)
	}
	if sender != common.HexToAddress(req.FromAddress) {
		return fmt.Errorf("signer: el remitente de la tx firmada no es la dirección de origen")
	}

	switch req.AssetKind {
	case domain.AssetKindNative:
		if tx.To() == nil || *tx.To() != common.HexToAddress(req.ToAddress) || tx.Value().Cmp(req.AmountRaw) != 0 || len(tx.Data()) != 0 {
			return fmt.Errorf("signer: la tx nativa firmada no coincide con lo solicitado")
		}
	case domain.AssetKindToken:
		if tx.To() == nil || *tx.To() != common.HexToAddress(req.ContractAddress) || tx.Value().Sign() != 0 {
			return fmt.Errorf("signer: la tx de token firmada apunta a otro contrato")
		}
		want := append([]byte{0xa9, 0x05, 0x9c, 0xbb}, common.LeftPadBytes(common.HexToAddress(req.ToAddress).Bytes(), 32)...)
		want = append(want, common.LeftPadBytes(req.AmountRaw.Bytes(), 32)...)
		if !bytes.Equal(tx.Data(), want) {
			return fmt.Errorf("signer: el calldata firmado no coincide con transfer(to, amount)")
		}
	}
	return nil
}

// DisabledBitcoinSigner is the Bitcoin half of the Signer abstraction in
// Bloque 6B: Bitcoin withdrawals are DISABLED (no UTXO discovery, no P2WPKH
// signing exists), so it refuses unconditionally. It exists so the wiring
// point for a real BitcoinSigner is explicit — it is never a stub that
// pretends to sign.
type DisabledBitcoinSigner struct{}

func (DisabledBitcoinSigner) Sign(ctx context.Context, req SignRequest) (*SignedTx, error) {
	return nil, ErrBitcoinDisabled
}
