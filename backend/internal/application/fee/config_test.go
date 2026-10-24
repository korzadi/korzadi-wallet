package fee

import (
	"os"
	"testing"

	"github.com/shopspring/decimal"
)

func clearFeeEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		envServiceFeeFlat, envServiceFeePercentBps, envServiceFeeMin, envServiceFeeMax,
		envNetworkFeeMin, envNetworkFeeMax, envTotalFeeMin, envTotalFeeMax,
		envVolatilityBufferBps, envMaxPriceAge, envQuoteTTL, envBitcoinTxVBytes,
		"FEE_GAS_LIMIT_ETH", "FEE_GAS_LIMIT_BSC", "FEE_GAS_LIMIT_POLYGON",
		"FEE_GAS_LIMIT_ARBITRUM", "FEE_GAS_LIMIT_OPTIMISM", "FEE_GAS_LIMIT_BASE",
	} {
		os.Unsetenv(k)
	}
}

func TestLoadConfigFromEnv_SafeDefaults(t *testing.T) {
	clearFeeEnv(t)
	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}
	if !cfg.TotalFeeMin.IsPositive() {
		t.Error("el default de TotalFeeMin debe ser positivo — nunca cero por defecto en un sistema que cobra dinero")
	}
	if cfg.VolatilityBufferBps <= 0 {
		t.Error("el buffer de volatilidad por defecto debe ser positivo (spec §11: no ilimitado, pero tampoco cero)")
	}
	if cfg.GasLimitByNetwork["ETHEREUM"] == 0 {
		t.Error("el gas limit por defecto de ETHEREUM no debe ser cero")
	}
}

func TestLoadConfigFromEnv_RejectsMinGreaterThanMax(t *testing.T) {
	clearFeeEnv(t)
	os.Setenv(envServiceFeeMin, "10")
	os.Setenv(envServiceFeeMax, "5")
	defer clearFeeEnv(t)

	_, err := LoadConfigFromEnv()
	if err == nil {
		t.Fatal("esperaba error: ServiceFeeMin > ServiceFeeMax es una configuración inválida")
	}
}

func TestLoadConfigFromEnv_RejectsExcessiveVolatilityBuffer(t *testing.T) {
	clearFeeEnv(t)
	os.Setenv(envVolatilityBufferBps, "20000") // 200%
	defer clearFeeEnv(t)

	_, err := LoadConfigFromEnv()
	if err == nil {
		t.Fatal("esperaba error: un buffer de 200% no es una protección razonable (spec §11: no absurdo/ilimitado)")
	}
}

func TestLoadConfigFromEnv_RejectsNegativeAmount(t *testing.T) {
	clearFeeEnv(t)
	os.Setenv(envServiceFeeFlat, "-1")
	defer clearFeeEnv(t)

	_, err := LoadConfigFromEnv()
	if err == nil {
		t.Fatal("esperaba error: un fee negativo no tiene sentido")
	}
}

func TestLoadConfigFromEnv_CustomValues(t *testing.T) {
	clearFeeEnv(t)
	os.Setenv(envServiceFeeFlat, "2.50")
	os.Setenv(envVolatilityBufferBps, "1000")
	defer clearFeeEnv(t)

	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}
	if !cfg.ServiceFeeFlat.Equal(decimal.NewFromFloat(2.50)) {
		t.Errorf("ServiceFeeFlat = %s, esperaba 2.50", cfg.ServiceFeeFlat)
	}
	if cfg.VolatilityBufferBps != 1000 {
		t.Errorf("VolatilityBufferBps = %d, esperaba 1000", cfg.VolatilityBufferBps)
	}
}
