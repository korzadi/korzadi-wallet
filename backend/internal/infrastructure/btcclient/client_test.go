package btcclient

import (
	"testing"
)

func TestBtcToSats_ExactConversion(t *testing.T) {
	cases := []struct {
		btc  float64
		want int64
	}{
		{1.0, 100_000_000},
		{0.00000001, 1}, // smallest unit, 1 satoshi
		{0.5, 50_000_000},
		{21_000_000, 2_100_000_000_000_000}, // max BTC supply, sanity check for overflow
	}
	for _, c := range cases {
		got := btcToSats(c.btc)
		if got != c.want {
			t.Errorf("btcToSats(%v) = %d, esperaba %d", c.btc, got, c.want)
		}
	}
}

func TestIsTransient_RPCWarmupIsRetried(t *testing.T) {
	warmupErr := &rpcError{Code: -28, Message: "Loading block index..."}
	if !isTransient(warmupErr) {
		t.Error("un nodo calentando (-28) debe tratarse como transitorio y reintentarse")
	}
}

func TestIsTransient_NotFoundIsNotRetried(t *testing.T) {
	notFoundErr := &rpcError{Code: -5, Message: "No such mempool or blockchain transaction"}
	if isTransient(notFoundErr) {
		t.Error("'no encontrado' (-5) no es un error transitorio, no debe reintentarse")
	}
}

func TestLoadConfigFromEnv_OptionalWhenUnset(t *testing.T) {
	t.Setenv("BTC_RPC_URL", "")
	_, ok, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}
	if ok {
		t.Fatal("esperaba ok=false cuando BTC_RPC_URL no está definido — Bitcoin es opcional, igual que cada red EVM")
	}
}

func TestLoadConfigFromEnv_Configured(t *testing.T) {
	t.Setenv("BTC_RPC_URL", "http://127.0.0.1:8332")
	t.Setenv("BTC_RPC_USER", "korzadi")
	t.Setenv("BTC_RPC_PASS", "secret")
	t.Setenv("BTC_CONFIRMATIONS_REQUIRED", "3")

	cfg, ok, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}
	if !ok {
		t.Fatal("esperaba ok=true")
	}
	if cfg.ConfirmationsRequired != 3 {
		t.Errorf("confirmations = %d, esperaba 3", cfg.ConfirmationsRequired)
	}
	if cfg.RPCPass != "secret" {
		t.Error("la contraseña debe leerse del entorno, nunca hardcodeada")
	}
}
