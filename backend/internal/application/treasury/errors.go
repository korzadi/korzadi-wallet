package treasury

import "errors"

// Sentinel errors — one per failure a caller (admin tool, worker) needs to
// react to differently. Wrapped with %w wherever extra context is added,
// so callers branch with errors.Is — same discipline as
// custody/gasstation/sweep.
var (
	// ---- policy validation (7.4.1) ----
	ErrInvalidNetwork       = errors.New("treasury: red inválida")
	ErrInvalidAsset         = errors.New("treasury: asset inválido")
	ErrNegativeAmount       = errors.New("treasury: monto negativo")
	ErrMinExceedsTarget     = errors.New("treasury: min_balance debe ser <= target_balance")
	ErrTargetExceedsMax     = errors.New("treasury: target_balance debe ser <= max_balance")
	ErrThresholdNotPositive = errors.New("treasury: rebalance_threshold debe ser > 0")
	ErrMaxAmountNotPositive = errors.New("treasury: max_rebalance_amount debe ser > 0")
	ErrHotEqualsTreasury    = errors.New("treasury: la wallet HOT y TREASURY deben ser distintas")

	ErrWalletNotFound             = errors.New("treasury: custody wallet no encontrada")
	ErrWalletWrongType            = errors.New("treasury: wallet_type incorrecto para el rol solicitado")
	ErrWalletNotActive            = errors.New("treasury: la custody wallet no está activa")
	ErrWalletNetworkAssetMismatch = errors.New("treasury: la custody wallet no coincide con network/asset de la política")

	ErrPolicyNotFound         = errors.New("treasury: política no encontrada")
	ErrDuplicatePolicy        = errors.New("treasury: ya existe una política para esa combinación de wallets")
	ErrPolicyAlreadyInState   = errors.New("treasury: la política ya está en ese estado")
	ErrConcurrentPolicyChange = errors.New("treasury: la política cambió concurrentemente, reintenta")

	// ---- rebalancing (7.4.2) ----
	ErrPolicyDisabled       = errors.New("treasury: política deshabilitada")
	ErrWithinRange          = errors.New("treasury: el balance HOT está dentro del rango, no se requiere rebalanceo")
	ErrBelowThreshold       = errors.New("treasury: la diferencia no supera rebalance_threshold")
	ErrInsufficientTreasury = errors.New("treasury: fondos insuficientes en TREASURY para cubrir el déficit")
	ErrInsufficientHot      = errors.New("treasury: fondos insuficientes en HOT para cubrir el exceso")
	ErrRebalanceInFlight    = errors.New("treasury: ya hay un rebalanceo en curso para esta política")
	ErrNoNativeAsset        = errors.New("treasury: no se encontró el asset nativo de la red en el registry")
	ErrInvalidRequest       = errors.New("treasury: solicitud inválida")

	// ---- broadcast / confirmation (shared vocabulary with sweep/gasstation) ----
	ErrBroadcastAmbiguous   = errors.New("treasury: resultado de broadcast ambiguo, requiere revisión antes de reintentar")
	ErrBroadcastRejected    = errors.New("treasury: broadcast rechazado definitivamente por la red")
	ErrNetworkNotConfigured = errors.New("treasury: red no configurada para rebalanceo")
	ErrAssetNotEnabled      = errors.New("treasury: asset no habilitado en el registry")
	ErrGasUnavailable       = errors.New("treasury: gas no disponible para la wallet origen")
	ErrNonceStuck           = errors.New("treasury: nonce usado en la red pero la tx es desconocida")
	ErrRebalancingDisabled  = errors.New("treasury: rebalanceo automático deshabilitado (kill switch)")

	ErrNotFound = errors.New("treasury: rebalanceo no encontrado")
)
