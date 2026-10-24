package fee

import (
	"github.com/shopspring/decimal"
)

// ServiceFeePolicy computes Korzadi's own charge for an operation — spec
// §4: "fee fijo, fee porcentual, mínimo, máximo... implementar solamente
// lo necesario para una política profesional inicial." This is flat +
// percentage-of-amount, clamped to [min, max] — the simplest policy that
// is still genuinely configurable per the spec's list, without building
// out per-asset/per-network/per-user rule tables that nothing in this
// block yet needs (spec §42: don't scope-creep beyond what 6A requires).
// A future block can extend this into a full rule engine by replacing
// this one function without touching engine.go, since callers only ever
// see the ServiceFeePolicy interface below.
type ServiceFeePolicy interface {
	ComputeServiceFee(operationAmount decimal.Decimal, operationAssetCode string) decimal.Decimal
}

type flatPlusPercentPolicy struct {
	flat       decimal.Decimal
	percentBps int64 // basis points: 100 = 1%
	min        decimal.Decimal
	max        decimal.Decimal
}

// NewServiceFeePolicy builds the standard policy from Config. Percentage
// only applies when operationAmount is known and positive — a fee quote
// requested without a concrete withdrawal amount yet (e.g. "what would it
// roughly cost to withdraw ETH") still gets the flat component and the
// configured minimum.
func NewServiceFeePolicy(cfg Config) ServiceFeePolicy {
	return &flatPlusPercentPolicy{flat: cfg.ServiceFeeFlat, percentBps: cfg.ServiceFeePercentBps, min: cfg.ServiceFeeMin, max: cfg.ServiceFeeMax}
}

func (p *flatPlusPercentPolicy) ComputeServiceFee(operationAmount decimal.Decimal, operationAssetCode string) decimal.Decimal {
	fee := p.flat
	if p.percentBps > 0 && operationAmount.IsPositive() {
		// operationAmount is in the OPERATION asset (e.g. ETH), not USDT —
		// spec §4 lists "reglas por asset" as a future extension point;
		// today the percentage applies to the numeric amount directly,
		// which only makes sense once 6B supplies operationAmount already
		// converted or when the policy is asset-aware. Documented here
		// rather than silently assumed: callers passing a percent-based
		// policy today should treat this as USDT-equivalent operationAmount.
		percent := decimal.NewFromInt(p.percentBps).Div(decimal.NewFromInt(10000))
		fee = fee.Add(operationAmount.Mul(percent))
	}
	if fee.LessThan(p.min) {
		fee = p.min
	}
	if fee.GreaterThan(p.max) {
		fee = p.max
	}
	return fee
}
