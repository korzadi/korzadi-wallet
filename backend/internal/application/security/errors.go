package security

import "errors"

// Every rejection below must be safe to return straight to an HTTP handler:
// no secrets, no internal policy details beyond what the caller already
// knew (their own amount, their own operation).
var (
	// ErrOperationUnknown means the caller passed an OperationType this
	// package does not recognize — a programming error upstream, never
	// something a request body should be able to trigger.
	ErrOperationUnknown = errors.New("security: tipo de operación desconocido")

	// ErrOperationDisabled: no hay una security_policy habilitada para
	// esta combinación de operación/asset/network, o la política
	// existente tiene enabled=false.
	ErrOperationDisabled = errors.New("security: operación deshabilitada por política")

	// ErrOperationFrozen: activo un Emergency Freeze (spec §21) sobre
	// esta operación.
	ErrOperationFrozen = errors.New("security: operación congelada temporalmente (emergency freeze)")

	// ErrKillSwitchOff: el kill switch de configuración (spec §20) para
	// esta operación está apagado.
	ErrKillSwitchOff = errors.New("security: operación deshabilitada por configuración")

	ErrBelowMinimum         = errors.New("security: el monto es menor al mínimo permitido")
	ErrAboveMaximum         = errors.New("security: el monto excede el máximo permitido por operación")
	ErrDailyLimitExceeded   = errors.New("security: se excede el límite diario")
	ErrDailyCountExceeded   = errors.New("security: se excede la cantidad máxima de operaciones diarias")
	ErrMonthlyLimitExceeded = errors.New("security: se excede el límite mensual")
	ErrMonthlyCountExceeded = errors.New("security: se excede la cantidad máxima de operaciones mensuales")

	// ErrReservationNotFound / ErrReservationNotActive mirror the naming
	// of domain.ErrReservationNotFound / domain.ErrReservationNotActive
	// on purpose — same shape of primitive, different subject (limit
	// usage instead of account balance).
	ErrReservationNotFound  = errors.New("security: reserva de límite no encontrada")
	ErrReservationNotActive = errors.New("security: la reserva de límite ya fue liberada o confirmada")

	// ErrReplay: se intentó reservar de nuevo la misma reference_id
	// mientras una reserva activa para esa misma operación ya existe
	// (spec §9 — protección contra replay de una autorización).
	ErrReplay = errors.New("security: esta operación ya fue autorizada (replay detectado)")

	ErrRateLimited = errors.New("security: demasiadas solicitudes para esta operación")

	ErrStepUpRequired         = errors.New("security: se requiere verificación 2FA (step-up) para este monto")
	ErrStepUpInvalid          = errors.New("security: código de verificación 2FA (step-up) inválido")
	ErrManualApprovalRequired = errors.New("security: este monto requiere aprobación manual")

	// ErrComplianceRestricted (Bloque 9): KYC/AML state (status, hold)
	// forbids this operation. Deliberately generic: callers/users never
	// learn which compliance rule fired.
	ErrComplianceRestricted = errors.New("security: operación no disponible por restricción de cumplimiento")

	ErrAuthorizationDenied = errors.New("security: autorización denegada")
	ErrDestinationBlocked  = errors.New("security: destino bloqueado")

	// ErrConfigMissingSecret / ErrConfigInvalidSecret are returned by
	// ValidateSecrets. Never wrap the offending value into these — only
	// the config KEY name, never printed alongside its value (spec §19).
	ErrConfigMissingSecret = errors.New("security: configuración incompleta (secreto faltante)")
	ErrConfigInvalidSecret = errors.New("security: configuración inválida (secreto no cumple el formato esperado)")
)

// HTTPStatusFor maps a security package error (possibly wrapped with
// fmt.Errorf("%w: ...")) to the HTTP status and a short machine-readable
// code an HTTP handler should return — so a limit rejection surfaces as
// 400/403/429, never as a generic 500 "internal_error" (which would also
// be misleading: nothing actually broke).  ok is false for any error this
// package didn't originate, so callers can fall back to their own
// generic error handling.
func HTTPStatusFor(err error) (status int, code string, ok bool) {
	switch {
	case errors.Is(err, ErrBelowMinimum):
		return 400, "below_minimum", true
	case errors.Is(err, ErrAboveMaximum):
		return 400, "above_maximum", true
	case errors.Is(err, ErrDailyLimitExceeded):
		return 400, "daily_limit_exceeded", true
	case errors.Is(err, ErrDailyCountExceeded):
		return 400, "daily_count_exceeded", true
	case errors.Is(err, ErrMonthlyLimitExceeded):
		return 400, "monthly_limit_exceeded", true
	case errors.Is(err, ErrMonthlyCountExceeded):
		return 400, "monthly_count_exceeded", true
	case errors.Is(err, ErrReplay):
		return 409, "duplicate_operation", true
	case errors.Is(err, ErrOperationFrozen), errors.Is(err, ErrKillSwitchOff), errors.Is(err, ErrOperationDisabled):
		return 503, "operation_unavailable", true
	case errors.Is(err, ErrRateLimited):
		return 429, "rate_limited", true
	case errors.Is(err, ErrStepUpRequired):
		return 401, "step_up_required", true
	case errors.Is(err, ErrStepUpInvalid):
		return 401, "step_up_invalid", true
	case errors.Is(err, ErrManualApprovalRequired):
		return 202, "manual_approval_required", true
	case errors.Is(err, ErrComplianceRestricted):
		return 403, "compliance_restricted", true
	case errors.Is(err, ErrDestinationCooldown):
		return 403, "new_destination_cooldown", true
	case errors.Is(err, ErrAuthorizationDenied), errors.Is(err, ErrDestinationBlocked):
		return 403, "authorization_denied", true
	default:
		return 0, "", false
	}
}
