// Package fee implements the Universal Fee Engine (Bloque 6A): given a
// future operation (today, only "withdrawal") on some network/asset, it
// calculates how much the user must pay, split internally into:
//
//	Service Fee (Korzadi's own charge)
//	+ Network Fee (the blockchain's real/estimated execution cost)
//	= Total Fee
//
// The user always pays the Total Fee in a single asset — USDT — no matter
// which network or asset the underlying operation touches. This package
// only calculates, quotes, and (via the existing Reservation system)
// reserves that USDT amount. It does not move money on its own: every
// actual balance change still goes through domain.LedgerRepository /
// application/ledger.Service exactly as before — this package is a
// calculator and a thin reservation wrapper, never a second accounting
// system.
//
// Bloque 6A explicitly does NOT implement withdrawals: no transaction is
// ever built, signed, or broadcast here. See each type's doc comment for
// how it's meant to be consumed by the future withdrawal service
// (Bloque 6B).
package fee

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
)

// OperationType is the kind of future operation a quote is for. Only
// withdrawal exists today — the type exists so a quote's purpose is
// explicit and so a second operation type (should one ever need fees)
// doesn't require restructuring this package.
type OperationType string

const (
	OperationWithdrawal OperationType = "withdrawal"
)

// QuoteStatus tracks a fee quote's lifecycle. A quote is not money moving
// by itself — these states describe the QUOTE, not a ledger transaction.
type QuoteStatus string

const (
	QuoteStatusActive    QuoteStatus = "active"    // usable, not yet reserved against
	QuoteStatusReserved  QuoteStatus = "reserved"  // a reservation exists for this quote's Total Fee
	QuoteStatusConsumed  QuoteStatus = "consumed"  // the reservation was consumed (by a future withdrawal completing)
	QuoteStatusExpired   QuoteStatus = "expired"   // past expires_at, no longer usable for a new reservation
	QuoteStatusCancelled QuoteStatus = "cancelled" // reservation was released and the quote abandoned
)

// FeeQuote is a durable, auditable record of one fee calculation — spec
// §17 and §39 ("¿qué fee se calculó? ¿cuándo? ¿con qué precio? ¿qué
// buffer?..."). Every field a future audit or a future withdrawal service
// needs to reconstruct or validate the calculation lives here; nothing is
// collapsed into a single opaque total.
type FeeQuote struct {
	ID                 uuid.UUID
	UserID             uuid.UUID
	OperationType      OperationType
	Network            domain.Network
	OperationAssetCode string // the asset actually being withdrawn (e.g. "ETH") — NEVER confused with FeeAssetCode
	FeeAssetCode       string // always "USDT" today (spec §2) — kept as a field, not a constant, so this is the one place that would change if that policy ever did

	ServiceFee decimal.Decimal // USDT
	NetworkFee decimal.Decimal // USDT
	TotalFee   decimal.Decimal // USDT — ServiceFee + NetworkFee, exactly (never independently rounded)

	NetworkFeeNative decimal.Decimal // the network fee in its native asset before conversion (e.g. ETH)
	NativeAssetCode  string          // e.g. "ETH", "BNB", "BTC" — the asset NetworkFeeNative is denominated in

	ExchangeRate        decimal.Decimal // NativeAssetCode -> USDT, as used for this quote
	PriceSource         string
	PriceAsOf           time.Time
	VolatilityBufferBps int32 // basis points applied on top of the raw converted network fee (spec §11)

	Status        QuoteStatus
	ReservationID *uuid.UUID // set once ReserveFee succeeds for this quote — nil until then

	CreatedAt time.Time
	ExpiresAt time.Time
}

// IsExpired reports whether the quote is past its expiry as of `now` —
// callers should always pass the current time explicitly rather than have
// this reach for the clock itself, so tests can exercise expiry
// deterministically (spec §18).
func (q *FeeQuote) IsExpired(now time.Time) bool {
	return now.After(q.ExpiresAt)
}
