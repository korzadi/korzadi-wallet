package fee

import "errors"

// Sentinel errors (spec §30) — one per distinct failure a caller (the
// future withdrawal service, or an HTTP handler) needs to react to
// differently. Kept flat and specific rather than a single generic
// "fee_error" so callers can branch with errors.Is.
var (
	ErrUnsupportedNetwork = errors.New("fee: red no soportada")
	ErrUnsupportedAsset   = errors.New("fee: asset no soportado")
	ErrPriceUnavailable   = errors.New("fee: no hay precio disponible para este asset")
	ErrPriceStale         = errors.New("fee: el precio disponible está desactualizado")
	ErrFeeTooHigh         = errors.New("fee: el fee calculado excede el máximo permitido")
	ErrFeeTooLow          = errors.New("fee: el fee calculado es menor al mínimo permitido")
	ErrInvalidAmount      = errors.New("fee: monto inválido")
	ErrQuoteExpired       = errors.New("fee: la cotización expiró")
	ErrQuoteNotFound      = errors.New("fee: cotización no encontrada")
	ErrQuoteNotActive     = errors.New("fee: la cotización ya fue reservada, consumida o cancelada")
	ErrReservationFailed  = errors.New("fee: no se pudo reservar el fee")
	ErrInvalidConfig      = errors.New("fee: configuración inválida")
)
