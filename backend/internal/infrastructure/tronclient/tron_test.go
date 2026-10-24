package tronclient

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/tronclient/tronfake"
	"korzadi-wallet/backend/internal/infrastructure/tronwallet"
)

type env struct {
	t        *testing.T
	chain    *tronfake.Chain
	node     *FullNode
	prov     *Provider
	wc       *WithdrawalChain
	contract string
	hot      *tronwallet.KeyPair
	user     *tronwallet.KeyPair
}

func newEnv(t *testing.T) *env {
	t.Helper()
	c := tronfake.New()
	t.Cleanup(c.Close)
	node := NewFullNode(c.URL())
	cfg := Config{ConfirmationsRequired: 5, ExpirationWindow: 0}
	contract, _ := tronwallet.GenerateKeyPair()
	hot, _ := tronwallet.GenerateKeyPair()
	user, _ := tronwallet.GenerateKeyPair()
	return &env{t: t, chain: c, node: node, prov: NewProvider(node, cfg), wc: NewWithdrawalChain(node, cfg),
		contract: contract.Address, hot: hot, user: user}
}

func (e *env) signTransfer(to string, amount int64) (string, []byte) {
	e.t.Helper()
	ref, err := e.wc.RefBlock(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	raw, err := tronwallet.BuildTRC20TransferRaw(tronwallet.TRC20TransferParams{
		Owner: e.hot.Address, Contract: e.contract, To: to, AmountRaw: big.NewInt(amount),
		RefBlockID: ref.ID, TimestampMs: ref.HeadTimestampMs, ExpirationMs: ref.HeadTimestampMs + 300_000, FeeLimitSun: 50_000_000,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	id, signed, err := tronwallet.SignRaw(e.hot.PrivateKeyHex, raw)
	if err != nil {
		e.t.Fatal(err)
	}
	return id, signed
}

func TestTransferEventsInRange(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	from := e.hot.Address
	before := e.chain.Head()
	txid := e.chain.Deposit(e.contract, from, e.user.Address, big.NewInt(25_500_000))
	otherContract, _ := tronwallet.GenerateKeyPair()
	e.chain.Deposit(otherContract.Address, from, e.user.Address, big.NewInt(1))
	e.chain.Mine(1)

	evs, err := e.prov.TransferEventsInRange(ctx, e.contract, 6, before+1, e.chain.Head())
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 {
		t.Fatalf("esperaba 1 evento del contrato configurado, got %d", len(evs))
	}
	ev := evs[0]
	if ev.TxHash != txid || ev.To != e.user.Address || ev.From != from || ev.ContractAddress != e.contract {
		t.Fatalf("evento incorrecto: %+v", ev)
	}
	if ev.RawAmount.String() != "25.5" {
		t.Fatalf("decimales 6: %s", ev.RawAmount)
	}
	blk, _ := e.node.BlockByNum(ctx, ev.BlockNumber)
	if ev.BlockHash != blk.ID {
		t.Fatal("el hash del bloque del evento debe ser el canónico")
	}
}

func TestFailedTransfersProduceNoEvents(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.chain.SetToken(e.contract, e.hot.Address, big.NewInt(10_000_000))
	e.chain.SetBalanceSun(e.hot.Address, 100_000_000)
	e.chain.FailNextTransfer()
	_, signed := e.signTransfer(e.user.Address, 1_000_000)
	if _, err := e.wc.Broadcast(ctx, signed); err != nil {
		t.Fatal(err)
	}
	from := e.chain.Head()
	e.chain.Mine(1)
	evs, _ := e.prov.TransferEventsInRange(ctx, e.contract, 6, from+1, e.chain.Head())
	if len(evs) != 0 {
		t.Fatal("una tx fallida no debe generar eventos de depósito")
	}
}

func TestReceiptLifecycleAndConfirmations(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.chain.SetToken(e.contract, e.hot.Address, big.NewInt(50_000_000))
	e.chain.SetBalanceSun(e.hot.Address, 100_000_000)
	id, signed := e.signTransfer(e.user.Address, 2_000_000)

	if r, err := e.wc.Receipt(ctx, id); err != nil || r != nil {
		t.Fatalf("antes de emitir no hay receipt: %v %v", r, err)
	}
	got, err := e.wc.Broadcast(ctx, signed)
	if err != nil || got != id {
		t.Fatalf("broadcast: %v %s", err, got)
	}
	if known, _ := e.wc.TxKnown(ctx, id); !known {
		t.Fatal("una tx pendiente visible debe ser conocida")
	}
	if r, _ := e.wc.Receipt(ctx, id); r != nil {
		t.Fatal("pendiente: aún sin receipt")
	}
	e.chain.Mine(1)
	r, err := e.wc.Receipt(ctx, id)
	if err != nil || r == nil || !r.Status || r.Confirmations != 0 {
		t.Fatalf("receipt: %+v %v", r, err)
	}
	if r.NativeFee == nil || r.NativeFee.IsNegative() {
		t.Fatal("el receipt debe traer la comisión nativa real")
	}
	e.chain.Mine(5)
	r, _ = e.wc.Receipt(ctx, id)
	if r.Confirmations != 5 {
		t.Fatalf("confirmaciones = %d, esperaba 5", r.Confirmations)
	}
	if e.chain.Token(e.contract, e.user.Address).Cmp(big.NewInt(2_000_000)) != 0 {
		t.Fatal("el destino debía recibir el monto")
	}
}

func TestReorgChangesReceiptBlockHash(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.chain.SetToken(e.contract, e.hot.Address, big.NewInt(50_000_000))
	e.chain.SetBalanceSun(e.hot.Address, 100_000_000)
	id, signed := e.signTransfer(e.user.Address, 1_000_000)
	_, _ = e.wc.Broadcast(ctx, signed)
	e.chain.Mine(1)
	r1, _ := e.wc.Receipt(ctx, id)
	e.chain.Mine(3)
	e.chain.Reorg(5)
	r2, err := e.wc.Receipt(ctx, id)
	if err != nil || r2 != nil {
		t.Fatalf("tras el reorg el nodo ya no conoce la tx: %+v %v", r2, err)
	}
	if r1 == nil || r1.BlockHash == "" {
		t.Fatal("el receipt original debía tener hash de bloque")
	}
}

func TestBroadcastErrorKinds(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.chain.SetToken(e.contract, e.hot.Address, big.NewInt(50_000_000))
	e.chain.SetBalanceSun(e.hot.Address, 100_000_000)

	_, s1 := e.signTransfer(e.user.Address, 1_000_000)
	e.chain.QueueBroadcast("reject")
	e.chain.SetRejectCode("SIGERROR")
	_, err := e.wc.Broadcast(ctx, s1)
	var be *BroadcastError
	if !errors.As(err, &be) || be.Code != "SIGERROR" {
		t.Fatalf("un rechazo definitivo debe ser *BroadcastError: %v", err)
	}

	e.chain.QueueBroadcast("ambiguous_lost")
	_, err = e.wc.Broadcast(ctx, s1)
	if err == nil || errors.As(err, &be) {
		t.Fatalf("un timeout debe ser un error plano (ambiguo): %v", err)
	}

	// el broadcast duplicado se reconoce
	if _, err := e.wc.Broadcast(ctx, s1); err != nil {
		t.Fatal(err)
	}
	_, err = e.wc.Broadcast(ctx, s1)
	if !errors.As(err, &be) || be.Code != "DUP_TRANSACTION_ERROR" {
		t.Fatalf("duplicado: %v", err)
	}
}

func TestQuoteWithAndWithoutStakedResources(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.chain.SetToken(e.contract, e.hot.Address, big.NewInt(50_000_000))

	q, err := e.wc.Quote(ctx, e.hot.Address, e.contract, e.user.Address, big.NewInt(1_000_000))
	if err != nil {
		t.Fatal(err)
	}
	if q.EnergyAvailable != 0 || q.BurnSun <= 0 || q.BurnSun > q.WorstCaseSun*2 {
		t.Fatalf("sin recursos hay que quemar TRX: %+v", q)
	}
	e.chain.SetStaked(e.hot.Address, 1_000_000, 100_000)
	q2, _ := e.wc.Quote(ctx, e.hot.Address, e.contract, e.user.Address, big.NewInt(1_000_000))
	if q2.BurnSun != 0 {
		t.Fatalf("con energía y bandwidth en stake no hay que quemar: %+v", q2)
	}
	if q2.WorstCaseSun != q.WorstCaseSun {
		t.Fatal("el peor caso (cotización al usuario) no debe depender del stake")
	}
	// saldo de USDT insuficiente: el nodo revierte la estimación
	if _, err := e.wc.Quote(ctx, e.hot.Address, e.contract, e.user.Address, big.NewInt(900_000_000)); err == nil {
		t.Fatal("estimar un envío sin saldo debe fallar")
	}
}

func TestComputeQuoteLimits(t *testing.T) {
	cfg := Config{}.withDefaults()
	if _, err := computeQuote(0, AccountResources{}, 420, 1000, cfg); err == nil {
		t.Fatal("energía 0 debía fallar")
	}
	if _, err := computeQuote(30000, AccountResources{}, 0, 1000, cfg); err == nil {
		t.Fatal("precio 0 debía fallar")
	}
	cfg.MaxFeeLimitSun = 1_000_000 // 1 TRX
	if _, err := computeQuote(65_000, AccountResources{}, 420, 1000, cfg); !errors.Is(err, ErrFeeLimitExceeded) {
		t.Fatalf("esperaba ErrFeeLimitExceeded, got %v", err)
	}
	cfg = Config{}.withDefaults()
	q, err := computeQuote(65_000, AccountResources{FreeNetLimit: 600}, 420, 1000, cfg)
	if err != nil {
		t.Fatal(err)
	}
	// 65_000 × 1.2 = 78_000 energía → 78_000×420 = 32_760_000 sun; bandwidth 400B > 600? no: libre alcanza
	if q.EnergyRequired != 78_000 || q.WorstCaseSun != 78_000*420+400*1000 {
		t.Fatalf("cálculo inesperado: %+v", q)
	}
	if q.BurnSun != ceilPct(78_000*420, 115) {
		t.Fatalf("solo se quema la energía faltante: %d", q.BurnSun)
	}
}

func TestVerifyUSDTContract(t *testing.T) {
	if err := VerifyUSDTContract("mainnet", OfficialUSDTMainnet, nil); err != nil {
		t.Fatalf("el contrato oficial debe pasar: %v", err)
	}
	fake, _ := tronwallet.GenerateKeyPair()
	if err := VerifyUSDTContract("mainnet", fake.Address, []string{fake.Address}); err == nil {
		t.Fatal("en mainnet NINGÚN otro contrato debe pasar, ni con lista extra")
	}
	if err := VerifyUSDTContract("mainnet", "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6x", nil); err == nil {
		t.Fatal("una dirección con checksum roto debe fallar")
	}
	if err := VerifyUSDTContract("testnet", fake.Address, nil); err == nil {
		t.Fatal("testnet sin lista blanca debe fallar")
	}
	if err := VerifyUSDTContract("testnet", fake.Address, []string{" " + fake.Address}); err != nil {
		t.Fatalf("testnet en lista blanca: %v", err)
	}
	if err := VerifyUSDTContract("", OfficialUSDTMainnet, nil); err == nil {
		t.Fatal("modo vacío debe fallar")
	}
}

func TestHealthAndBalances(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.chain.SetBalanceSun(e.hot.Address, 12_345_678)
	e.chain.SetToken(e.contract, e.hot.Address, big.NewInt(7_000_000))
	h, err := e.prov.HealthCheck(ctx, e.contract)
	if err != nil || !h.RPCReachable || !h.ContractOK || h.LatestBlock == 0 {
		t.Fatalf("health: %+v %v", h, err)
	}
	if b, _ := e.prov.NativeBalance(ctx, e.hot.Address); b.String() != "12.345678" {
		t.Fatalf("TRX: %s", b)
	}
	if b, _ := e.prov.TokenBalance(ctx, e.hot.Address, e.contract, 6); b.String() != "7" {
		t.Fatalf("USDT: %s", b)
	}
	if raw, _ := e.wc.TokenBalanceRaw(ctx, e.contract, e.hot.Address); raw.Cmp(big.NewInt(7_000_000)) != 0 {
		t.Fatalf("raw: %s", raw)
	}
	var _ domain.BlockchainProvider = e.prov
}
