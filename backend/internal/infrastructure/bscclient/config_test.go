package bscclient

import "os"
import "testing"

func clearBSCEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"BSC_RPC_URL", "BSC_CHAIN_ID", "USDT_BSC_CONTRACT", "USDT_DECIMALS",
		"BSC_CONFIRMATIONS_REQUIRED", "BSC_RPC_TIMEOUT", "BSC_RPC_MAX_RETRIES",
		"BSC_RPC_BACKOFF_BASE", "BSC_RPC_BACKOFF_MAX",
	} {
		os.Unsetenv(k)
	}
}

func TestLoadConfigFromEnv_MissingRPCURL(t *testing.T) {
	clearBSCEnv(t)
	_, err := LoadConfigFromEnv()
	if err == nil {
		t.Fatal("esperaba error por BSC_RPC_URL ausente")
	}
}

func TestLoadConfigFromEnv_MissingContract(t *testing.T) {
	clearBSCEnv(t)
	os.Setenv("BSC_RPC_URL", "https://bsc-dataseed.example.com")
	defer clearBSCEnv(t)

	_, err := LoadConfigFromEnv()
	if err == nil {
		t.Fatal("esperaba error por USDT_BSC_CONTRACT ausente — nunca debe asumirse")
	}
}

func TestLoadConfigFromEnv_InvalidContractFormat(t *testing.T) {
	clearBSCEnv(t)
	os.Setenv("BSC_RPC_URL", "https://bsc-dataseed.example.com")
	os.Setenv("USDT_BSC_CONTRACT", "not-an-address")
	defer clearBSCEnv(t)

	_, err := LoadConfigFromEnv()
	if err == nil {
		t.Fatal("esperaba error por formato de dirección inválido")
	}
}

func TestLoadConfigFromEnv_Valid(t *testing.T) {
	clearBSCEnv(t)
	os.Setenv("BSC_RPC_URL", "https://bsc-dataseed.example.com")
	os.Setenv("BSC_CHAIN_ID", "56")
	os.Setenv("USDT_BSC_CONTRACT", "0x55d398326f99059fF775485246999027B3197955")
	os.Setenv("USDT_DECIMALS", "18")
	os.Setenv("BSC_CONFIRMATIONS_REQUIRED", "15")
	defer clearBSCEnv(t)

	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}
	if cfg.ChainID != 56 {
		t.Errorf("chain id = %d, esperaba 56", cfg.ChainID)
	}
	if cfg.ConfirmationsRequired != 15 {
		t.Errorf("confirmations = %d, esperaba 15", cfg.ConfirmationsRequired)
	}
	if cfg.USDTDecimals != 18 {
		t.Errorf("usdt decimals = %d, esperaba 18", cfg.USDTDecimals)
	}
}
