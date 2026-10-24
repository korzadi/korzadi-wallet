package evmclient

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"korzadi-wallet/backend/internal/domain"
)

func TestIsTransient(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{context.DeadlineExceeded, true},
		{errors.New("dial tcp: connection refused"), true},
		{errors.New("429 Too Many Requests"), true},
		{errors.New("execution reverted: insufficient balance"), false},
		{errors.New("invalid argument"), false},
	}
	for _, c := range cases {
		if got := isTransient(c.err); got != c.want {
			t.Errorf("isTransient(%v) = %v, esperaba %v", c.err, got, c.want)
		}
	}
}

func TestBlockRangeErrorForObservedHeadIsTyped(t *testing.T) {
	cause := errors.New("provider rejected requested range")
	err := blockRangeErrorForObservedHead(120, 117, cause)
	var rangeErr *domain.BlockRangeBeyondHeadError
	if !errors.As(err, &rangeErr) {
		t.Fatalf("error type = %T, esperaba *domain.BlockRangeBeyondHeadError", err)
	}
	if rangeErr.RequestedTo != 120 || rangeErr.AvailableHead != 117 {
		t.Fatalf("rango tipado = [%d, %d], esperaba to=120/head=117", rangeErr.RequestedTo, rangeErr.AvailableHead)
	}
	if !errors.Is(err, cause) {
		t.Fatal("el error tipado debe conservar la causa RPC")
	}

	if err := blockRangeErrorForObservedHead(117, 117, cause); err != nil {
		t.Fatalf("rango que no supera la altura debe conservar el error original, got %v", err)
	}
}

func TestBackoffDuration_RespectsMax(t *testing.T) {
	base := 100 * time.Millisecond
	max := 2 * time.Second
	for attempt := 0; attempt < 20; attempt++ {
		d := backoffDuration(base, max, attempt)
		if d > max+max/5 { // allow jitter margin
			t.Errorf("backoff en intento %d = %v, excede el máximo %v", attempt, d, max)
		}
		if d <= 0 {
			t.Errorf("backoff en intento %d = %v, debe ser positivo", attempt, d)
		}
	}
}

// TestClient_WithRetry_RetriesTransientThenSucceeds exercises the retry
// loop directly (without a live RPC connection). This works for any EVM
// network's Client since the retry logic is entirely network-agnostic.
func TestClient_WithRetry_RetriesTransientThenSucceeds(t *testing.T) {
	c := &Client{cfg: Config{
		RPCTimeout:     50 * time.Millisecond,
		RPCMaxRetries:  3,
		RPCBackoffBase: 1 * time.Millisecond,
		RPCBackoffMax:  5 * time.Millisecond,
	}}

	attempts := 0
	err := c.withRetry(context.Background(), "TestOp", func(ctx context.Context) error {
		attempts++
		if attempts < 3 {
			return errors.New("connection reset by peer")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("esperaba éxito tras reintentos, obtuve: %v", err)
	}
	if attempts != 3 {
		t.Errorf("intentos = %d, esperaba 3", attempts)
	}
}

func TestClient_WithRetry_GivesUpAfterMaxRetries(t *testing.T) {
	c := &Client{cfg: Config{
		RPCTimeout:     50 * time.Millisecond,
		RPCMaxRetries:  2,
		RPCBackoffBase: 1 * time.Millisecond,
		RPCBackoffMax:  5 * time.Millisecond,
	}}

	attempts := 0
	err := c.withRetry(context.Background(), "TestOp", func(ctx context.Context) error {
		attempts++
		return errors.New("timeout")
	})
	if err == nil {
		t.Fatal("esperaba error tras agotar reintentos")
	}
	if !errors.Is(err, ErrRPCUnavailable) {
		t.Errorf("error = %v, esperaba ErrRPCUnavailable", err)
	}
	if attempts != 3 { // initial + 2 retries
		t.Errorf("intentos = %d, esperaba 3 (1 inicial + 2 reintentos)", attempts)
	}
}

func TestClient_WithRetry_DoesNotRetryNonTransient(t *testing.T) {
	c := &Client{cfg: Config{
		RPCTimeout:     50 * time.Millisecond,
		RPCMaxRetries:  5,
		RPCBackoffBase: 1 * time.Millisecond,
		RPCBackoffMax:  5 * time.Millisecond,
	}}

	attempts := 0
	err := c.withRetry(context.Background(), "TestOp", func(ctx context.Context) error {
		attempts++
		return errors.New("execution reverted")
	})
	if err == nil {
		t.Fatal("esperaba error")
	}
	if attempts != 1 {
		t.Errorf("intentos = %d, esperaba 1 (error no transitorio no debe reintentarse)", attempts)
	}
}

// TestLoadConfigFromEnv_NotConfiguredIsNotAnError verifies that an unset
// {PREFIX}_RPC_URL means "network optional, not configured" (ok=false, no
// error) rather than a hard failure — main.go relies on this to bring up
// only the EVM networks that are actually configured.
func TestLoadConfigFromEnv_NotConfiguredIsNotAnError(t *testing.T) {
	_, ok, err := LoadConfigFromEnv(NetworkEnvDefaults{EnvPrefix: "KZD_TEST_UNSET_NETWORK", MainnetID: 1})
	if err != nil {
		t.Fatalf("no esperaba error, obtuve: %v", err)
	}
	if ok {
		t.Fatal("esperaba ok=false cuando la variable RPC_URL no está definida")
	}
}

func TestLoadConfigFromEnv_EthereumMinimumConfirmations(t *testing.T) {
	t.Setenv("ETH_RPC_URL", "http://rpc.invalid")
	t.Setenv("ETH_CONFIRMATIONS_REQUIRED", "63")
	if _, _, err := LoadConfigFromEnv(knownEVMNetworks[domain.NetworkETHEREUM]); err == nil {
		t.Fatal("Ethereum confirmation depth below 64 must fail")
	}
	t.Setenv("ETH_CONFIRMATIONS_REQUIRED", "64")
	cfg, ok, err := LoadConfigFromEnv(knownEVMNetworks[domain.NetworkETHEREUM])
	if err != nil || !ok || cfg.ConfirmationsRequired != 64 {
		t.Fatalf("cfg=%+v ok=%v err=%v", cfg, ok, err)
	}
	t.Setenv("ETH_CONFIRMATIONS_REQUIRED", "")
	cfg, ok, err = LoadConfigFromEnv(knownEVMNetworks[domain.NetworkETHEREUM])
	if err != nil || !ok || cfg.ConfirmationsRequired != 64 {
		t.Fatalf("default cfg=%+v ok=%v err=%v", cfg, ok, err)
	}
}

func TestBounded1559FeeCapsRespectRPCQuote(t *testing.T) {
	cap, tip, err := bounded1559FeeCaps(big.NewInt(100), big.NewInt(3), big.NewInt(210))
	if err != nil {
		t.Fatal(err)
	}
	if cap.Cmp(big.NewInt(203)) != 0 || tip.Cmp(big.NewInt(3)) != 0 {
		t.Fatalf("cap=%s tip=%s", cap, tip)
	}
	if _, _, err := bounded1559FeeCaps(big.NewInt(100), big.NewInt(3), big.NewInt(102)); err == nil {
		t.Fatal("must reject quote below baseFee + priorityFee")
	}
}
