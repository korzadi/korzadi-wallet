package custody

import "errors"

// Sentinel errors — one per failure a caller (HTTP handler, admin tool)
// needs to react to differently. Wrapped with %w wherever extra context
// is added, so callers branch with errors.Is.
var (
	ErrInvalidWalletType  = errors.New("custody: wallet_type inválido")
	ErrInvalidStatus      = errors.New("custody: status inválido")
	ErrInvalidAddressType = errors.New("custody: address_type inválido")
	ErrInvalidAddress     = errors.New("custody: dirección inválida")
	ErrEmptyPrivateKey    = errors.New("custody: private key vacía")

	ErrUnknownNetwork           = errors.New("custody: red no registrada")
	ErrUnknownAsset             = errors.New("custody: asset no registrado")
	ErrAssetNotEnabledOnNetwork = errors.New("custody: el asset no está habilitado en esa red")

	ErrDuplicateAddress = errors.New("custody: la dirección ya está registrada como custody wallet")

	ErrNotFound                = errors.New("custody: custody wallet no encontrada")
	ErrInvalidStatusTransition = errors.New("custody: transición de estado inválida")
	ErrConcurrentStatusChange  = errors.New("custody: el status cambió concurrentemente, reintenta")
)
