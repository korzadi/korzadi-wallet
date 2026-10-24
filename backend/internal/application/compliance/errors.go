package compliance

import "errors"

// All errors are safe to map to HTTP responses: no PII, no provider detail.
var (
	ErrKYCDisabled          = errors.New("compliance: KYC no está habilitado")
	ErrAMLDisabled          = errors.New("compliance: AML no está habilitado")
	ErrScreeningUnavailable = errors.New("compliance: screening no disponible")
	ErrUnknownProvider      = errors.New("compliance: proveedor desconocido")
	ErrProviderUnavailable  = errors.New("compliance: proveedor no disponible")

	ErrInvalidTransition = errors.New("compliance: transición de estado KYC inválida")
	ErrCaseNotFound      = errors.New("compliance: caso KYC no encontrado")
	ErrActiveCaseExists  = errors.New("compliance: ya existe un caso KYC activo")
	ErrConcurrentUpdate  = errors.New("compliance: el recurso cambió concurrentemente, reintente")
	ErrRenewalNotAllowed = errors.New("compliance: renovación KYC no permitida en el estado actual")

	ErrReasonRequired  = errors.New("compliance: se requiere un motivo")
	ErrSelfAction      = errors.New("compliance: no se permite actuar sobre el propio caso")
	ErrForbidden       = errors.New("compliance: acción no permitida para el rol")
	ErrInvalidArgument = errors.New("compliance: argumento inválido")

	ErrIdempotencyKeyRequired = errors.New("compliance: se requiere Idempotency-Key")
	ErrIdempotencyKeyReuse    = errors.New("compliance: Idempotency-Key reutilizada con otra solicitud")

	ErrWebhookSignature = errors.New("compliance: firma de webhook inválida")
	ErrWebhookReplay    = errors.New("compliance: webhook fuera de la ventana de tiempo permitida")
	ErrWebhookMalformed = errors.New("compliance: webhook mal formado")

	ErrFlagNotFound            = errors.New("compliance: flag no encontrado")
	ErrFlagNotOpen             = errors.New("compliance: el flag ya fue resuelto")
	ErrHoldNotFound            = errors.New("compliance: hold no encontrado")
	ErrHoldAlreadyReleased     = errors.New("compliance: el hold ya fue liberado")
	ErrRiskDowngradeManualOnly = errors.New("compliance: bajar el nivel de riesgo requiere revisión manual")
	ErrScreeningNotFound       = errors.New("compliance: resultado de screening no encontrado")
	ErrScreeningReviewed       = errors.New("compliance: el resultado de screening ya fue revisado")
)
