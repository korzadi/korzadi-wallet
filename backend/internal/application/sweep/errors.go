package sweep

import "errors"

// Sentinel errors — one per failure a caller (worker, tests) needs to react
// to differently. Wrapped with %w wherever extra context is added, so
// callers branch with errors.Is. Mirrors the exact style of
// blockchainwithdrawal/errors.go.
var (
	ErrSweepsDisabled       = errors.New("sweeps: los sweeps están deshabilitados")
	ErrUnsupportedNetwork   = errors.New("sweeps: red no soportada para sweeps (familia no EVM)")
	ErrNetworkNotConfigured = errors.New("sweeps: red no habilitada para sweeps en este entorno")
	ErrAssetNotEnabled      = errors.New("sweeps: asset no habilitado en el registro para esta red")

	// ErrNoCustodyDestination: no hay una custody wallet TREASURY activa
	// para (network, asset). Fail closed — spec: "Si no existe una custody
	// wallet válida: NO ejecutar sweep."
	ErrNoCustodyDestination = errors.New("sweeps: no hay custody wallet TREASURY activa para este network/asset")

	ErrBelowDustThreshold  = errors.New("sweeps: balance por debajo del umbral mínimo (dust)")
	ErrInsufficientOnChain = errors.New("sweeps: fondos on-chain insuficientes para cubrir el monto y el gas")
	ErrGasUnavailable      = errors.New("sweeps: no hay gas suficiente y no hay gas station para esta red")
	ErrNoSigner            = errors.New("sweeps: no hay firmante configurado")
	ErrNotFound            = errors.New("sweeps: sweep no encontrado")
	ErrBroadcastRejected   = errors.New("sweeps: la red rechazó definitivamente la transacción")
	ErrNonceStuck          = errors.New("sweeps: el nonce fue consumido por otra transacción")

	// ErrInFlight: ya existe un sweep no terminal para este
	// (network, asset, from_address) — la garantía de idempotencia /
	// no-doble-sweep, traducida desde uq_sweeps_inflight_source.
	ErrInFlight = errors.New("sweeps: ya hay un sweep en curso para esta dirección/asset")
)
