package domain

import (
	"regexp"
	"strings"

	"github.com/google/uuid"
)

// Prefijos con los que el ledger namespacea las Idempotency-Key que llegan
// de usuarios, para que NUNCA puedan coincidir con claves internas.
const (
	IdemPrefixTransfer    = "transfer:"
	IdemPrefixFiatDeposit = "fiat-deposit:"
)

const (
	MinUserIdempotencyKeyLen = 8
	MaxUserIdempotencyKeyLen = 128
)

var userIdempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._:\-]+$`)

// reservedIdempotencyPrefixes son prefijos de claves generadas por el
// backend (depósitos on-chain, settlement de retiros, VPN, fees, sweeps...).
// Un cliente no puede mandar una Idempotency-Key que empiece por ellos
// (defensa en profundidad: además el ledger namespacea las claves de usuario).
var reservedIdempotencyPrefixes = []string{
	"blockchain-deposit:", "withdrawal-settle:", "withdrawal:", "korzadivpn:",
	"fee_quote:", "transfer:", "fiat-deposit:", "sweep:", "treasury:",
	"gas:", "gas-station:", "commission:", "affiliate:", "settle:",
	"adjustment:", "system:",
}

var ErrInvalidIdempotencyKey = &DomainError{
	Code:    "invalid_idempotency_key",
	Message: "Idempotency-Key inválida: 8-128 caracteres [A-Za-z0-9._:-] y sin prefijos reservados",
}

// ErrIdempotencyPayloadMismatch: la idempotency_key ya existe en el ledger
// pero con otro tipo o con otros entries. Nunca es un replay legítimo.
var ErrIdempotencyPayloadMismatch = &DomainError{
	Code:    "idempotency_payload_mismatch",
	Message: "la idempotency_key ya fue usada por otra operación distinta",
}

var ErrAdjustmentRequiresService = &DomainError{
	Code:    "adjustment_requires_privileged_service",
	Message: "los ajustes del ledger requieren el servicio administrativo privilegiado",
}

// ValidateUserIdempotencyKey valida la clave que envía un cliente.
func ValidateUserIdempotencyKey(key string) error {
	if len(key) < MinUserIdempotencyKeyLen || len(key) > MaxUserIdempotencyKeyLen ||
		!userIdempotencyKeyPattern.MatchString(key) {
		return ErrInvalidIdempotencyKey
	}
	lower := strings.ToLower(key)
	for _, p := range reservedIdempotencyPrefixes {
		if strings.HasPrefix(lower, p) {
			return ErrInvalidIdempotencyKey
		}
	}
	return nil
}

// NamespacedIdempotencyKey construye la clave que se usa en el ledger:
// "<prefix><userID>:<clave del usuario>".
func NamespacedIdempotencyKey(prefix string, userID uuid.UUID, key string) string {
	return prefix + userID.String() + ":" + key
}
