package reconciliation

import "errors"

var (
	ErrReconciliationDisabled = errors.New("reconciliation: deshabilitada (kill switch)")
	ErrNetworkNotConfigured   = errors.New("reconciliation: red no configurada")
	ErrAssetNotEnabled        = errors.New("reconciliation: asset no habilitado en el registry")
	ErrWalletNotFound         = errors.New("reconciliation: custody wallet no encontrada")

	ErrCheckNotFound      = errors.New("reconciliation: check no encontrado")
	ErrNotMismatched      = errors.New("reconciliation: el check no está en mismatch/pending_review")
	ErrStillMismatched    = errors.New("reconciliation: los números actuales todavía no coinciden — no se puede resolver sin evidencia")
	ErrResolutionRequired = errors.New("reconciliation: se requiere una nota de resolución")
	ErrConcurrentUpdate   = errors.New("reconciliation: el check cambió concurrentemente, reintenta")
)
