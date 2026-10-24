// Package gasstation implements Bloque 7.2: the automated top-up of native
// gas (BNB, ETH, ...) from a custody GAS wallet (Bloque 7.1's
// custody_wallets, wallet_type = GAS) to whatever EVM address currently
// needs it to complete an operation — today, exclusively a withdrawal's
// own from-address, via blockchainwithdrawal.GasTopUp.
//
// This package deliberately does NOT:
//   - move any user-facing asset (USDT, etc.) — only the network's native
//     coin, and only ever FROM a GAS wallet TO a target address, never the
//     other direction and never a different asset;
//   - touch the Ledger — a Gas Station refill is infrastructure plumbing,
//     not a user balance change (spec: "El Gas Station NO es un Ledger de
//     usuarios");
//   - implement sweeps, treasury rebalancing, reconciliation, or recovery
//     (Bloques 7.3–7.6) — EnsureGasFor is the entire surface;
//   - support TRON. TRON withdrawals do not exist anywhere in this backend
//     today (internal/infrastructure/tronclient only reads TRC20 balances;
//     there is no TRON TransactionSigner, no TRON broadcast path, no TRON
//     NetworkRuntime), so there is no TRON operation that could ever call
//     EnsureGasFor in the first place. TRON's own resource model (Energy /
//     Bandwidth obtained by staking/freezing TRX, not "gas price × gas
//     used") is fundamentally different from the EVM gas model this
//     package automates, so even if a TRON withdrawal flow existed later,
//     forcing it through this same mechanism would be wrong. This package
//     is EVM-only, and only for whatever EVM networks are actually dialed
//     (main.go's evmProviders map) — never a hardcoded list.
package gasstation

import "errors"

var (
	// ErrNoGasWallet: no ACTIVE custody_wallets row of wallet_type=GAS
	// exists for this network/native asset. Same externally-visible
	// failure mode as today's "GAS_STATION_* not set" — the caller's own
	// gas_unavailable fail-closed path is unaffected.
	ErrNoGasWallet = errors.New("gasstation: no hay wallet GAS activa para esta red")

	// ErrDisabled: a GAS wallet exists but has no policy row, or its
	// policy has enabled=false. Refill never happens automatically.
	ErrDisabled = errors.New("gasstation: el refill automático no está habilitado para esta wallet GAS")

	// ErrNoNativeAsset: the network's native asset is not registered
	// (enabled) in the asset registry — refill amounts cannot be
	// converted to base units without knowing its decimals.
	ErrNoNativeAsset = errors.New("gasstation: el asset nativo de la red no está registrado/habilitado")

	// ErrInvalidRequest: caller-supplied arguments are malformed (empty
	// owner, nil/negative requiredWei, invalid target address).
	ErrInvalidRequest = errors.New("gasstation: solicitud de refill inválida")

	// ErrPolicyExceeded: the amount needed to satisfy requiredWei would
	// exceed max_refill or push the wallet past target_balance. The
	// station refuses rather than silently violating its own configured
	// caps — an operator must raise the policy, this is never auto-healed.
	ErrPolicyExceeded = errors.New("gasstation: el refill necesario excede max_refill/target_balance de la política")

	// ErrRefillInProgress: another concurrent call is already refilling
	// this exact target address; this call observed it and could not
	// confirm completion within its wait budget. Safe to retry later —
	// it will either find the other refill confirmed (and do nothing) or
	// find it failed (and proceed).
	ErrRefillInProgress = errors.New("gasstation: ya hay un refill en curso para esta dirección, reintenta")

	// ErrBroadcastAmbiguous: the broadcast RPC call itself failed in a way
	// that does not rule out the transaction having been accepted by the
	// network. Per Bloque 6B's own rule, this is NEVER auto-retried — the
	// refill row is left in 'broadcasted' (not 'failed') so a human or a
	// later reconciliation pass (Bloque 7.5, out of scope here) resolves
	// it, exactly the same posture blockchainwithdrawal already takes for
	// its own withdrawal broadcasts.
	ErrBroadcastAmbiguous = errors.New("gasstation: resultado del broadcast ambiguo, no se reintenta automáticamente")
)
