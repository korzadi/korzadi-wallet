package fee

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestServiceFee_Normal(t *testing.T) {
	cfg := Config{ServiceFeeFlat: decimal.NewFromInt(1), ServiceFeePercentBps: 0, ServiceFeeMin: decimal.NewFromFloat(0.5), ServiceFeeMax: decimal.NewFromInt(50)}
	policy := NewServiceFeePolicy(cfg)

	got := policy.ComputeServiceFee(decimal.Zero, "ETH")
	if !got.Equal(decimal.NewFromInt(1)) {
		t.Errorf("service fee = %s, esperaba 1 (flat)", got)
	}
}

func TestServiceFee_ClampedToMinimum(t *testing.T) {
	cfg := Config{ServiceFeeFlat: decimal.NewFromFloat(0.10), ServiceFeeMin: decimal.NewFromFloat(0.50), ServiceFeeMax: decimal.NewFromInt(50)}
	policy := NewServiceFeePolicy(cfg)

	got := policy.ComputeServiceFee(decimal.Zero, "ETH")
	if !got.Equal(decimal.NewFromFloat(0.50)) {
		t.Errorf("service fee = %s, esperaba el mínimo 0.50", got)
	}
}

func TestServiceFee_ClampedToMaximum(t *testing.T) {
	cfg := Config{ServiceFeeFlat: decimal.NewFromInt(1), ServiceFeePercentBps: 500, ServiceFeeMin: decimal.NewFromFloat(0.5), ServiceFeeMax: decimal.NewFromInt(10)}
	policy := NewServiceFeePolicy(cfg)

	got := policy.ComputeServiceFee(decimal.NewFromInt(10000), "ETH") // 5% of 10000 = 500, way above max
	if !got.Equal(decimal.NewFromInt(10)) {
		t.Errorf("service fee = %s, esperaba el máximo 10", got)
	}
}

func TestServiceFee_FlatPlusPercent(t *testing.T) {
	cfg := Config{ServiceFeeFlat: decimal.NewFromInt(1), ServiceFeePercentBps: 100, ServiceFeeMin: decimal.Zero, ServiceFeeMax: decimal.NewFromInt(1000)} // 1%
	policy := NewServiceFeePolicy(cfg)

	got := policy.ComputeServiceFee(decimal.NewFromInt(100), "ETH") // 1 + 1% of 100 = 1 + 1 = 2
	if !got.Equal(decimal.NewFromInt(2)) {
		t.Errorf("service fee = %s, esperaba 2 (1 flat + 1 porcentual)", got)
	}
}
