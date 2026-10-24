package domain

import "errors"

// ErrResourceFeeLimitExceeded: a transfer would need a fee_limit above the hard cap.
var ErrResourceFeeLimitExceeded = errors.New("domain: el fee_limit requerido supera el máximo configurado")

// Types shared by chains priced in RESOURCES (TRON: Energy + Bandwidth,
// paid by staking or by burning TRX) instead of EVM gas. They live in domain
// so the withdrawal flow, the fee engine and the infrastructure client agree
// on them without importing each other.

// ChainRef is a recent block used as TaPoS reference (ref_block_bytes /
// ref_block_hash) of a transaction.
type ChainRef struct {
	ID          []byte // 32-byte blockID of the reference block
	Number      uint64
	TimestampMs int64 // timestamp of the reference block
	// HeadTimestampMs is the CHAIN time (latest block). Transaction timestamp
	// and expiration are derived from it, not from the local clock.
	HeadTimestampMs int64
}

// ResourceQuote is the cost of one TRC-20 transfer in TRON resources.
// All money amounts are in sun (1 TRX = 1_000_000 sun).
type ResourceQuote struct {
	EnergyRequired     int64 // energy the call consumes (estimated, with headroom)
	EnergyAvailable    int64 // sender's remaining staked energy
	BandwidthBytes     int64 // estimated transaction size in bytes
	BandwidthAvailable int64 // sender's remaining free+staked bandwidth
	EnergyPriceSun     int64 // sun burned per missing energy unit
	BandwidthPriceSun  int64 // sun burned per bandwidth byte when bandwidth is missing

	// BurnSun is the TRX the sender must hold to cover ONLY the shortfall
	// (what is not covered by staked resources), with safety margin.
	BurnSun int64
	// WorstCaseSun is the cost if NOTHING were staked: used for user quotes so
	// the platform never undercharges.
	WorstCaseSun int64
	// FeeLimitSun caps what the transaction may burn in energy (fee_limit).
	FeeLimitSun int64
}
