package blockchainwithdrawal

import "errors"

// Sentinel errors — one per failure a caller (HTTP handler, worker) needs to
// react to differently. Wrapped with %w wherever extra context is added, so
// callers branch with errors.Is.
var (
	// ErrBitcoinDisabled: Bitcoin withdrawals are explicitly DISABLED in
	// Bloque 6B (no UTXO discovery/selection/locking, no P2WPKH signing).
	// Returned BEFORE any quote, reservation, ledger write, signing or
	// broadcast.
	ErrBitcoinDisabled = errors.New("bitcoin withdrawals are currently disabled")

	ErrWithdrawalsDisabled  = errors.New("withdrawals: los retiros están deshabilitados")
	ErrUnsupportedNetwork   = errors.New("withdrawals: red no soportada para retiros")
	ErrNetworkNotConfigured = errors.New("withdrawals: red no habilitada para retiros en este entorno")
	ErrAssetNotEnabled      = errors.New("withdrawals: asset no habilitado para retiros en esta red")
	ErrAccountInactive      = errors.New("withdrawals: la cuenta o wallet no está activa")
	ErrNoDepositAddress     = errors.New("withdrawals: el usuario no tiene una dirección en esta red")
	ErrNoHotWallet          = errors.New("withdrawals: no hay una wallet HOT activa para esta red y activo")

	ErrInvalidAddress        = errors.New("withdrawals: dirección de destino inválida")
	ErrDestinationIsContract = errors.New("withdrawals: la dirección de destino es el contrato del token")
	ErrDestinationIsSelf     = errors.New("withdrawals: la dirección de destino es tu propia dirección de depósito")
	ErrInvalidAmount         = errors.New("withdrawals: monto inválido")
	ErrAmountBelowMinimum    = errors.New("withdrawals: monto por debajo del mínimo")
	ErrAmountAboveMaximum    = errors.New("withdrawals: monto por encima del máximo")
	ErrDailyLimitExceeded    = errors.New("withdrawals: límite diario excedido")
	ErrInvalidIdempotencyKey = errors.New("withdrawals: idempotency key inválida")
	ErrIdempotencyKeyReused  = errors.New("withdrawals: la idempotency key ya fue usada con una solicitud distinta")
	ErrQuoteMismatch         = errors.New("withdrawals: la cotización no corresponde a esta operación")
	ErrQuoteAlreadyUsed      = errors.New("withdrawals: la cotización ya respalda otro retiro")
	ErrInsufficientFunds     = errors.New("withdrawals: saldo insuficiente")
	ErrNotFound              = errors.New("withdrawals: retiro no encontrado")
	ErrInvalidTransition     = errors.New("withdrawals: transición de estado inválida")
	ErrCannotCancel          = errors.New("withdrawals: el retiro ya no puede cancelarse")
	ErrStaleState            = errors.New("withdrawals: el retiro cambió de estado concurrentemente")
	ErrReservationLost       = errors.New("withdrawals: una reserva del retiro ya no está activa")
	ErrNoSigner              = errors.New("withdrawals: no hay firmante configurado")
	ErrInsufficientOnChain   = errors.New("withdrawals: fondos on-chain insuficientes en la dirección de origen")
	ErrGasUnavailable        = errors.New("withdrawals: no hay gas suficiente y no hay gas station para esta red")
	ErrNetworkFeeOverrun     = errors.New("withdrawals: el costo de red actual excede lo cotizado")
	ErrBroadcastRejected     = errors.New("withdrawals: la red rechazó definitivamente la transacción")
	ErrNonceStuck            = errors.New("withdrawals: el nonce fue consumido por otra transacción")

	// ErrNoLocker: falta el Locker entre procesos; se falla cerrado.
	ErrNoLocker = errors.New("withdrawals: no hay Locker configurado para la wallet HOT")
	// ErrHotLockBusy: otra instancia retiene el lock de la HOT más allá del
	// tiempo de espera; es transitorio y no consume intentos.
	ErrHotLockBusy = errors.New("withdrawals: la wallet HOT está siendo usada por otra instancia")

	// ErrSecurityUnavailable: un control de seguridad (destino, velocidad) no pudo
	// evaluarse. Los retiros fallan CERRADOS: sin control no hay retiro.
	ErrSecurityUnavailable = errors.New("withdrawals: los controles de seguridad no están disponibles")
)
