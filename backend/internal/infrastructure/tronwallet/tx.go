package tronwallet

// Construcción LOCAL, firma y análisis de transacciones TRC-20 de TRON.
//
// Por qué local y no "pedirle la tx al nodo" (/wallet/triggersmartcontract):
// un nodo comprometido o malicioso podría devolver una transacción que
// mueva fondos a otra dirección y nosotros la firmaríamos. Construyendo
// raw_data nosotros mismos, lo único que se firma es lo que construimos.
//
// Formato (protobuf de java-tron):
//
//	Transaction { raw raw_data = 1; repeated bytes signature = 2; }
//	raw         { bytes ref_block_bytes = 1; bytes ref_block_hash = 4;
//	              int64 expiration = 8; repeated Contract contract = 11;
//	              int64 timestamp = 14; int64 fee_limit = 18; }
//	Contract    { ContractType type = 1; Any parameter = 2; }
//	Any         { string type_url = 1; bytes value = 2; }
//	TriggerSmartContract { bytes owner_address = 1; bytes contract_address = 2;
//	              int64 call_value = 3; bytes data = 4; }
//
// El txID es sha256(raw_data) y la firma es r||s||v (v = 0/1) sobre ese hash.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/btcsuite/btcutil/base58"
	"github.com/ethereum/go-ethereum/crypto"
)

const (
	contractTypeTransfer     = 1
	contractTypeTriggerSmart = 31
	transferTypeURL          = "type.googleapis.com/protocol.TransferContract"
	triggerTypeURL           = "type.googleapis.com/protocol.TriggerSmartContract"
)

// transferSelector = keccak256("transfer(address,uint256)")[:4].
var transferSelector = []byte{0xa9, 0x05, 0x9c, 0xbb}

// AddressToBytes21 decodes a base58check TRON address into its 21 bytes
// (0x41 || 20-byte hash), validating version and checksum.
func AddressToBytes21(address string) ([]byte, error) {
	if err := ValidateAddress(address); err != nil {
		return nil, err
	}
	return base58.Decode(address)[:21], nil
}

// AddressFromBytes21 is the inverse of AddressToBytes21.
func AddressFromBytes21(b []byte) (string, error) {
	if len(b) != 21 || b[0] != tronAddressVersion {
		return "", errors.New("tronwallet: dirección binaria inválida")
	}
	return base58CheckEncode(append([]byte(nil), b...)), nil
}

// TRC20TransferParams are the only inputs of a TRC-20 transfer transaction.
type TRC20TransferParams struct {
	Owner        string // HOT address (base58)
	Contract     string // TRC-20 contract (base58)
	To           string // recipient (base58)
	AmountRaw    *big.Int
	RefBlockID   []byte // 32-byte blockID of a RECENT block (ref_block_bytes/hash derive from it)
	TimestampMs  int64
	ExpirationMs int64
	FeeLimitSun  int64
}

// BuildTRC20TransferRaw returns the serialized raw_data of the transaction.
func BuildTRC20TransferRaw(p TRC20TransferParams) ([]byte, error) {
	owner, err := AddressToBytes21(p.Owner)
	if err != nil {
		return nil, fmt.Errorf("owner: %w", err)
	}
	contract, err := AddressToBytes21(p.Contract)
	if err != nil {
		return nil, fmt.Errorf("contrato: %w", err)
	}
	to, err := AddressToBytes21(p.To)
	if err != nil {
		return nil, fmt.Errorf("destino: %w", err)
	}
	if p.AmountRaw == nil || p.AmountRaw.Sign() <= 0 || p.AmountRaw.BitLen() > 256 {
		return nil, errors.New("tronwallet: monto inválido")
	}
	if len(p.RefBlockID) != 32 {
		return nil, errors.New("tronwallet: blockID de referencia inválido")
	}
	if p.TimestampMs <= 0 || p.ExpirationMs <= p.TimestampMs {
		return nil, errors.New("tronwallet: timestamp/expiración inválidos")
	}
	if p.FeeLimitSun <= 0 {
		return nil, errors.New("tronwallet: fee_limit inválido")
	}

	// transfer(address,uint256) calldata
	data := make([]byte, 0, 68)
	data = append(data, transferSelector...)
	data = append(data, make([]byte, 12)...)
	data = append(data, to[1:]...)
	amt := make([]byte, 32)
	p.AmountRaw.FillBytes(amt)
	data = append(data, amt...)

	var trig []byte
	trig = pbBytes(trig, 1, owner)
	trig = pbBytes(trig, 2, contract)
	trig = pbBytes(trig, 4, data)

	var anyMsg []byte
	anyMsg = pbBytes(anyMsg, 1, []byte(triggerTypeURL))
	anyMsg = pbBytes(anyMsg, 2, trig)

	return rawEnvelope(contractTypeTriggerSmart, anyMsg, p.RefBlockID, p.TimestampMs, p.ExpirationMs, p.FeeLimitSun), nil
}

// rawEnvelope serializes raw_data around an already-built Any(parameter).
// ref_block_bytes = last 2 bytes of the 8-byte block number (blockID[6:8]);
// ref_block_hash  = blockID[8:16]. feeLimit <= 0 omits fee_limit (TRX transfers).
func rawEnvelope(contractType uint64, anyMsg []byte, refBlockID []byte, tsMs, expMs, feeLimitSun int64) []byte {
	var ctr []byte
	ctr = pbVarint(ctr, 1, contractType)
	ctr = pbBytes(ctr, 2, anyMsg)

	var raw []byte
	raw = pbBytes(raw, 1, refBlockID[6:8])
	raw = pbBytes(raw, 4, refBlockID[8:16])
	raw = pbVarint(raw, 8, uint64(expMs))
	raw = pbBytes(raw, 11, ctr)
	raw = pbVarint(raw, 14, uint64(tsMs))
	if feeLimitSun > 0 {
		raw = pbVarint(raw, 18, uint64(feeLimitSun))
	}
	return raw
}

// TRXTransferParams are the inputs of a native TRX transfer (TransferContract):
// used by the gas station to top up a HOT wallet's TRX (burned for Energy/Bandwidth).
type TRXTransferParams struct {
	Owner        string
	To           string
	AmountSun    int64
	RefBlockID   []byte
	TimestampMs  int64
	ExpirationMs int64
}

// BuildTRXTransferRaw returns the serialized raw_data of a TRX transfer.
func BuildTRXTransferRaw(p TRXTransferParams) ([]byte, error) {
	owner, err := AddressToBytes21(p.Owner)
	if err != nil {
		return nil, fmt.Errorf("owner: %w", err)
	}
	to, err := AddressToBytes21(p.To)
	if err != nil {
		return nil, fmt.Errorf("destino: %w", err)
	}
	if p.AmountSun <= 0 {
		return nil, errors.New("tronwallet: monto inválido")
	}
	if len(p.RefBlockID) != 32 {
		return nil, errors.New("tronwallet: blockID de referencia inválido")
	}
	if p.TimestampMs <= 0 || p.ExpirationMs <= p.TimestampMs {
		return nil, errors.New("tronwallet: timestamp/expiración inválidos")
	}
	var param []byte
	param = pbBytes(param, 1, owner)
	param = pbBytes(param, 2, to)
	param = pbVarint(param, 3, uint64(p.AmountSun))

	var anyMsg []byte
	anyMsg = pbBytes(anyMsg, 1, []byte(transferTypeURL))
	anyMsg = pbBytes(anyMsg, 2, param)
	return rawEnvelope(contractTypeTransfer, anyMsg, p.RefBlockID, p.TimestampMs, p.ExpirationMs, 0), nil
}

// TxID = hex(sha256(raw_data)).
func TxID(raw []byte) string {
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}

// SignRaw signs raw_data with the secp256k1 key and returns the txID and the
// serialized Transaction (raw_data + signature), ready for /wallet/broadcasthex.
// Errors never echo key material.
func SignRaw(privateKeyHex string, raw []byte) (txID string, signedTx []byte, err error) {
	key, kerr := crypto.HexToECDSA(strings.TrimPrefix(privateKeyHex, "0x"))
	if kerr != nil {
		// go-ethereum's error can echo the offending key character: never propagate it.
		return "", nil, errors.New("tronwallet: formato de clave inválido")
	}
	digest := sha256.Sum256(raw)
	sig, serr := crypto.Sign(digest[:], key) // r||s||v, v in {0,1} — TRON's format
	if serr != nil {
		return "", nil, errors.New("tronwallet: no se pudo firmar")
	}
	var tx []byte
	tx = pbBytes(tx, 1, raw)
	tx = pbBytes(tx, 2, sig)
	return hex.EncodeToString(digest[:]), tx, nil
}

// ParsedTx is what we can read back from a serialized Transaction.
type ParsedTx struct {
	TxID          string
	Raw           []byte
	Signature     []byte
	ContractType  int
	Owner         string // base58
	Contract      string // base58
	CallValue     int64
	To            string // base58 (from transfer calldata)
	AmountRaw     *big.Int
	IsTransfer    bool  // calldata is exactly transfer(address,uint256)
	IsTRXTransfer bool  // native TRX TransferContract
	TRXAmountSun  int64 // amount of a native TRX transfer
	TimestampMs   int64
	ExpirationMs  int64
	FeeLimitSun   int64
	RefBlockBytes []byte
	RefBlockHash  []byte
}

// ParseSignedTx decodes a serialized Transaction (as persisted in
// blockchain_withdrawals.signed_raw_tx). It never panics on malformed input.
func ParseSignedTx(signed []byte) (*ParsedTx, error) {
	out := &ParsedTx{}
	err := pbWalk(signed, func(field int, wire int, v uint64, b []byte) error {
		switch {
		case field == 1 && wire == 2:
			out.Raw = b
		case field == 2 && wire == 2:
			if out.Signature == nil {
				out.Signature = b
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(out.Raw) == 0 {
		return nil, errors.New("tronwallet: transacción sin raw_data")
	}
	out.TxID = TxID(out.Raw)

	var ctrBytes []byte
	err = pbWalk(out.Raw, func(field int, wire int, v uint64, b []byte) error {
		switch {
		case field == 1 && wire == 2:
			out.RefBlockBytes = b
		case field == 4 && wire == 2:
			out.RefBlockHash = b
		case field == 8 && wire == 0:
			out.ExpirationMs = int64(v)
		case field == 11 && wire == 2 && ctrBytes == nil:
			ctrBytes = b
		case field == 14 && wire == 0:
			out.TimestampMs = int64(v)
		case field == 18 && wire == 0:
			out.FeeLimitSun = int64(v)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if ctrBytes == nil {
		return nil, errors.New("tronwallet: transacción sin contrato")
	}

	var anyValue []byte
	var typeURL string
	err = pbWalk(ctrBytes, func(field int, wire int, v uint64, b []byte) error {
		switch {
		case field == 1 && wire == 0:
			out.ContractType = int(v)
		case field == 2 && wire == 2:
			return pbWalk(b, func(f2 int, w2 int, _ uint64, b2 []byte) error {
				switch {
				case f2 == 1 && w2 == 2:
					typeURL = string(b2)
				case f2 == 2 && w2 == 2:
					anyValue = b2
				}
				return nil
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if out.ContractType == contractTypeTransfer && typeURL == transferTypeURL {
		var owner, to []byte
		err = pbWalk(anyValue, func(field int, wire int, v uint64, b []byte) error {
			switch {
			case field == 1 && wire == 2:
				owner = b
			case field == 2 && wire == 2:
				to = b
			case field == 3 && wire == 0:
				out.TRXAmountSun = int64(v)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		if out.Owner, err = AddressFromBytes21(owner); err != nil {
			return nil, err
		}
		if out.To, err = AddressFromBytes21(to); err != nil {
			return nil, err
		}
		out.IsTRXTransfer = true
		return out, nil
	}
	if out.ContractType != contractTypeTriggerSmart || typeURL != triggerTypeURL {
		return out, nil // not a TRC-20 call: IsTransfer stays false
	}

	var owner, contract, data []byte
	err = pbWalk(anyValue, func(field int, wire int, v uint64, b []byte) error {
		switch {
		case field == 1 && wire == 2:
			owner = b
		case field == 2 && wire == 2:
			contract = b
		case field == 3 && wire == 0:
			out.CallValue = int64(v)
		case field == 4 && wire == 2:
			data = b
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if out.Owner, err = AddressFromBytes21(owner); err != nil {
		return nil, err
	}
	if out.Contract, err = AddressFromBytes21(contract); err != nil {
		return nil, err
	}
	if len(data) == 68 && bytes.Equal(data[:4], transferSelector) && allZero(data[4:16]) {
		to, terr := AddressFromBytes21(append([]byte{tronAddressVersion}, data[16:36]...))
		if terr == nil {
			out.To = to
			out.AmountRaw = new(big.Int).SetBytes(data[36:68])
			out.IsTransfer = true
		}
	}
	return out, nil
}

// RecoverSigner returns the base58 address that produced the transaction's
// signature (sanity check that we signed with the intended HOT key).
func (p *ParsedTx) RecoverSigner() (string, error) {
	if len(p.Signature) != 65 {
		return "", errors.New("tronwallet: firma inválida")
	}
	digest := sha256.Sum256(p.Raw)
	pub, err := crypto.SigToPub(digest[:], p.Signature)
	if err != nil {
		return "", errors.New("tronwallet: no se pudo recuperar la firma")
	}
	return addressFromPublicKeyBytes(crypto.FromECDSAPub(pub))
}

func allZero(b []byte) bool {
	for _, x := range b {
		if x != 0 {
			return false
		}
	}
	return true
}

// ---- minimal protobuf helpers ----

func pbVarintRaw(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

func pbVarint(b []byte, field int, v uint64) []byte {
	b = pbVarintRaw(b, uint64(field)<<3)
	return pbVarintRaw(b, v)
}

func pbBytes(b []byte, field int, v []byte) []byte {
	b = pbVarintRaw(b, uint64(field)<<3|2)
	b = pbVarintRaw(b, uint64(len(v)))
	return append(b, v...)
}

var errPB = errors.New("tronwallet: protobuf inválido")

func pbReadVarint(b []byte) (uint64, int, error) {
	var v uint64
	for i := 0; i < len(b) && i < 10; i++ {
		v |= uint64(b[i]&0x7f) << (7 * uint(i))
		if b[i]&0x80 == 0 {
			return v, i + 1, nil
		}
	}
	return 0, 0, errPB
}

// pbWalk iterates over the fields of a message. For wire type 0 `v` holds the
// value; for wire type 2 `b` holds the payload. Fixed 32/64-bit fields are skipped.
func pbWalk(msg []byte, fn func(field int, wire int, v uint64, b []byte) error) error {
	for len(msg) > 0 {
		tag, n, err := pbReadVarint(msg)
		if err != nil {
			return err
		}
		msg = msg[n:]
		field, wire := int(tag>>3), int(tag&7)
		switch wire {
		case 0:
			v, n, err := pbReadVarint(msg)
			if err != nil {
				return err
			}
			msg = msg[n:]
			if err := fn(field, wire, v, nil); err != nil {
				return err
			}
		case 2:
			l, n, err := pbReadVarint(msg)
			if err != nil {
				return err
			}
			msg = msg[n:]
			if l > uint64(len(msg)) {
				return errPB
			}
			if err := fn(field, wire, 0, msg[:l]); err != nil {
				return err
			}
			msg = msg[l:]
		case 1:
			if len(msg) < 8 {
				return errPB
			}
			msg = msg[8:]
		case 5:
			if len(msg) < 4 {
				return errPB
			}
			msg = msg[4:]
		default:
			return errPB
		}
	}
	return nil
}
