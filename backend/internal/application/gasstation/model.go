package gasstation

import "math/big"

// refillPlan is the internal, already-validated decision of how much to
// send and to whom, computed once and then executed unchanged — nothing
// downstream of planRefill re-derives the amount, so what gets audited is
// exactly what was decided.
type refillPlan struct {
	amountWei *big.Int // > 0, already capped by policy.max_refill and policy.target_balance
}
