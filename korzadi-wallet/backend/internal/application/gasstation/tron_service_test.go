package gasstation

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/postgres"
	"korzadi-wallet/backend/internal/infrastructure/tronclient"
	"korzadi-wallet/backend/internal/infrastructure/tronclient/tronfake"
	"korzadi-wallet/backend/internal/infrastructure/tronwallet"
)

// TRON gas station against the in-memory node: real tx building/signing, real
// WithdrawalChain, the existing policy/refill fakes. No real network.

type tronGasEnv struct {
	chain   *tronfake.Chain
	svc     *TronService
	refills *fakeRefills
	audit   *fakeAudit
	gas     *tronwallet.KeyPair
	hot     *tronwallet.KeyPair
	offset  time.Duration // simulated wall-clock advance
}

func newTronGasEnv(t *testing.T, minimum, target, maxRefill string, enabled bool) *tronGasEnv {
	t.Helper()
	chain := tronfake.New()
	t.Cleanup(chain.Close)
	gas, _ := tronwallet.GenerateKeyPair()
	hot, _ := tronwallet.GenerateKeyPair()
	chain.SetBalanceSun(gas.Address, 1_000_000_000) // 1000 TRX in the GAS wallet

	e := &tronGasEnv{chain: chain, refills: newFakeRefills(), audit: &fakeAudit{}, gas: gas, hot: hot}
	wallets, policies := newFakeWallets(), newFakePolicies()
	walletID := uuid.New()
	wallets.add(&postgres.CustodyWallet{
		ID: walletID, Network: string(domain.NetworkTRON), AssetCode: "TRX", WalletType: "GAS",
		Address: gas.Address, AddressType: "tron", EncryptedPrivateKey: "encrypted:x", Status: "active",
	})
	policies.set(&postgres.GasStationPolicy{
		CustodyWalletID: walletID, Enabled: enabled,
		MinimumBalance: decimal.RequireFromString(minimum), TargetBalance: decimal.RequireFromString(target), MaxRefill: decimal.RequireFromString(maxRefill),
	})
	assets := &fakeAssets{assets: []domain.NetworkAsset{{Network: domain.NetworkTRON, AssetCode: "TRX", Kind: domain.AssetKindNative, Decimals: 6, Enabled: true}}}

	base := time.Now()
	svc, err := NewTronService(TronDeps{
		Network: domain.NetworkTRON, Wallets: wallets, Policies: policies, Refills: e.refills, Assets: assets,
		Chain: tronclient.NewWithdrawalChain(tronclient.NewFullNode(chain.URL()), tronclient.Config{}),
		Decryptor: &fakeDecryptor{plaintext: gas.PrivateKeyHex}, Audit: e.audit,
		Now:   func() time.Time { return base.Add(e.offset) },
		Sleep: func(ctx context.Context, d time.Duration) error { chain.Mine(1); return nil }, // time passes = blocks get mined
	})
	if err != nil {
		t.Fatal(err)
	}
	e.svc = svc
	return e
}

func trx(n int64) *big.Int { return big.NewInt(n * 1_000_000) }

func TestTronGas_TopsUpHotWithinPolicy(t *testing.T) {
	e := newTronGasEnv(t, "5", "50", "100", true)
	ctx := context.Background()

	if err := e.svc.EnsureGasFor(ctx, "withdrawal:1", e.hot.Address, trx(20)); err != nil {
		t.Fatal(err)
	}
	if got := e.chain.SunBalance(e.hot.Address); got != 50_000_000 {
		t.Fatalf("HOT debía quedar en el target de la política (50 TRX), got %d sun", got)
	}
	if e.refills.nonFailedCount() != 1 {
		t.Fatal("debía quedar exactamente un refill")
	}
	// idempotente: el mismo owner no vuelve a enviar
	before := e.chain.Broadcasts()
	if err := e.svc.EnsureGasFor(ctx, "withdrawal:1", e.hot.Address, trx(20)); err != nil {
		t.Fatal(err)
	}
	if e.chain.Broadcasts() != before {
		t.Fatal("con saldo suficiente no se debe enviar otro top-up")
	}
}

func TestTronGas_NoRefillWhenBalanceIsEnough(t *testing.T) {
	e := newTronGasEnv(t, "5", "50", "100", true)
	e.chain.SetBalanceSun(e.hot.Address, 80_000_000)
	if err := e.svc.EnsureGasFor(context.Background(), "w", e.hot.Address, trx(20)); err != nil {
		t.Fatal(err)
	}
	if e.chain.Broadcasts() != 0 {
		t.Fatal("no debía enviar nada")
	}
}

func TestTronGas_PolicyLimitsAreHard(t *testing.T) {
	// max_refill = 10 TRX no alcanza para los 30 TRX que exige la operación
	e := newTronGasEnv(t, "5", "50", "10", true)
	err := e.svc.EnsureGasFor(context.Background(), "w", e.hot.Address, trx(30))
	if !errors.Is(err, ErrPolicyExceeded) {
		t.Fatalf("esperaba ErrPolicyExceeded, got %v", err)
	}
	if e.chain.Broadcasts() != 0 || e.refills.count() != 0 {
		t.Fatal("fuera de política no se firma ni se registra nada")
	}
}

func TestTronGas_DisabledAndMissingWallet(t *testing.T) {
	e := newTronGasEnv(t, "5", "50", "100", false)
	if err := e.svc.EnsureGasFor(context.Background(), "w", e.hot.Address, trx(20)); !errors.Is(err, ErrDisabled) {
		t.Fatalf("política deshabilitada: %v", err)
	}
	e2 := newTronGasEnv(t, "5", "50", "100", true)
	e2.svc.Wallets = newFakeWallets() // sin wallet GAS
	if err := e2.svc.EnsureGasFor(context.Background(), "w", e2.hot.Address, trx(20)); !errors.Is(err, ErrNoGasWallet) {
		t.Fatalf("sin wallet GAS: %v", err)
	}
	if err := e2.svc.EnsureGasFor(context.Background(), "w", "no-es-tron", trx(1)); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("destino inválido: %v", err)
	}
}

func TestTronGas_AmbiguousBroadcastIsReconciledNotResent(t *testing.T) {
	e := newTronGasEnv(t, "5", "50", "100", true)
	ctx := context.Background()
	e.chain.QueueBroadcast("ambiguous") // el nodo aceptó pero la respuesta se perdió

	err := e.svc.EnsureGasFor(ctx, "w", e.hot.Address, trx(20))
	// El top-up se mina mientras se espera la confirmación, así que puede cerrar bien
	// o reportar ambiguo; lo que NUNCA debe pasar es un segundo envío.
	if err != nil && !errors.Is(err, ErrBroadcastAmbiguous) {
		t.Fatalf("error inesperado: %v", err)
	}
	e.chain.Mine(2)
	if err := e.svc.EnsureGasFor(ctx, "w", e.hot.Address, trx(20)); err != nil {
		t.Fatalf("la segunda llamada debía reconciliar: %v", err)
	}
	if e.chain.Broadcasts() != 1 {
		t.Fatalf("un broadcast ambiguo jamás se reenvía a ciegas (broadcasts=%d)", e.chain.Broadcasts())
	}
	if e.chain.SunBalance(e.hot.Address) != 50_000_000 {
		t.Fatalf("el HOT debía tener el top-up, got %d", e.chain.SunBalance(e.hot.Address))
	}
}

func TestTronGas_LostTxExpiresAndIsPlannedAgain(t *testing.T) {
	e := newTronGasEnv(t, "5", "50", "100", true)
	ctx := context.Background()
	e.chain.QueueBroadcast("ambiguous_lost") // el nodo NO lo recibió

	if err := e.svc.EnsureGasFor(ctx, "w", e.hot.Address, trx(20)); err == nil || !errors.Is(err, ErrBroadcastAmbiguous) {
		t.Fatalf("esperaba ambiguo, got %v", err)
	}
	// Aún no expiró: sigue ambiguo, sin reenviar
	if err := e.svc.EnsureGasFor(ctx, "w", e.hot.Address, trx(20)); !errors.Is(err, ErrBroadcastAmbiguous) {
		t.Fatalf("antes de expirar debe seguir ambiguo: %v", err)
	}
	if e.chain.Broadcasts() != 1 {
		t.Fatal("no se reenvía mientras puede estar viva")
	}
	// Pasa el tiempo más allá de expiración + margen
	e.offset = 30 * time.Minute
	e.chain.Mine(200)
	if err := e.svc.EnsureGasFor(ctx, "w", e.hot.Address, trx(20)); err == nil {
		t.Fatal("la llamada que cierra el refill expirado debe pedir reintento")
	}
	if err := e.svc.EnsureGasFor(ctx, "w", e.hot.Address, trx(20)); err != nil {
		t.Fatalf("el nuevo refill debía completarse: %v", err)
	}
	if e.chain.SunBalance(e.hot.Address) != 50_000_000 {
		t.Fatalf("HOT = %d", e.chain.SunBalance(e.hot.Address))
	}
}

func TestTronGas_DefinitiveRejectionMarksFailed(t *testing.T) {
	e := newTronGasEnv(t, "5", "50", "100", true)
	e.chain.QueueBroadcast("reject")
	e.chain.SetRejectCode("SIGERROR")
	err := e.svc.EnsureGasFor(context.Background(), "w", e.hot.Address, trx(20))
	if err == nil || errors.Is(err, ErrBroadcastAmbiguous) {
		t.Fatalf("un rechazo definitivo no es ambiguo: %v", err)
	}
	if e.refills.nonFailedCount() != 0 {
		t.Fatal("el refill rechazado debe quedar fallido (no bloquea reintentos)")
	}
}

func TestTronGas_NeverLeaksPrivateKey(t *testing.T) {
	e := newTronGasEnv(t, "5", "50", "100", true)
	e.chain.QueueBroadcast("reject")
	e.chain.SetRejectCode("SIGERROR")
	err := e.svc.EnsureGasFor(context.Background(), "w", e.hot.Address, trx(20))
	_ = e.svc.EnsureGasFor(context.Background(), "w2", e.hot.Address, trx(20))
	key := e.gas.PrivateKeyHex
	if err != nil && strings.Contains(err.Error(), key) {
		t.Fatal("el error filtra la clave privada")
	}
	for _, blob := range e.audit.blobs {
		for _, v := range blob {
			if s, ok := v.(string); ok && strings.Contains(s, key) {
				t.Fatal("la auditoría filtra la clave privada")
			}
		}
	}
}
