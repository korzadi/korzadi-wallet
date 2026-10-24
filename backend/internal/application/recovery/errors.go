package recovery

import "errors"

var (
	ErrRecoveryDisabled = errors.New("recovery: deshabilitado (kill switch) — solo detección activa")
	ErrIncidentNotFound = errors.New("recovery: incidente no encontrado")
	ErrAlreadyResolved  = errors.New("recovery: el incidente ya está resuelto")
	ErrResolutionNote   = errors.New("recovery: se requiere una nota de resolución")
	ErrConcurrentUpdate = errors.New("recovery: el incidente cambió concurrentemente, reintenta")
	ErrUnknownSubsystem = errors.New("recovery: subsistema desconocido")
)
