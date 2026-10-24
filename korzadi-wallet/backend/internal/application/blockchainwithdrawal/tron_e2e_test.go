package blockchainwithdrawal

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	appfee "korzadi-wallet/backend/internal/application/fee"
	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/cryptutil"
	"korzadi-wallet/backend/internal/infrastructure/tronclient"
	"korzadi-wallet/backend/internal/infrastructure/tronclient/tronfake"
	"korzadi-wallet/backend/internal/infrastructure/tronwallet"
)

// End-to-end TRON withdrawals: the REAL state machine (Service/Advance, ledger
// fakes of this package) + the REAL TronSigner + the REAL tronclient chain,
// talking HTTP to an in-memory node. Nothing here touches a real network.

type tronFees struct{ *fakeFeeEngine }

// EstimateFee quotes in TRX like TronFeeEstimator does (the base fake quotes ETH).
func (f tronFees) EstimateFee(ctx context.Context, in appfee.EstimateFeeInput) (*appfee.FeeQuote, error) {
	q, err := f.fakeFeeEngine.EstimateFee(ctx, in)
	if err == nil {
		q.NativeAssetCode = "TRX"
		q.NetworkFeeNative = decimal.RequireFromString(tronQuotedTRX)
	}
	return q, err
}

var tronQuotedTRX = "40"

type tronKeys struct{ hot string }

func (k tronKeys) FindEncryptedPrivateKey(_ context.Context, _ domain.Network, address string, _ uuid.UUID) (string, error) {
	if address != k.hot {
		return "", errors.New("llave no encontrada")
	}
	return "encrypted:hot", nil
}

type tronDec struct{ plaintext string }

func (d tronDec) Decrypt(_ context.Context, _ string, _ cryptutil.AAD) (string, error) {
	return d.plaintext, nil
}

// gasStub records EnsureGasFor calls; fund (optional) simulates the top-up landing.
type gasStub struct {
	calls []*big.Int
	err   error
	fund  func()
}

func (g *gasStub) EnsureGasFor(_ context.Context, _, _ string, required *big.Int) error {
	g.calls = append(g.calls, new(big.Int).Set(required))
	if g.err != nil {
		return g.err
	}
	if g.fund != nil {
		g.fund()
	}
	return nil
}

type tronHarness struct {
	*harness
	node     *tronfake.Chain
	hot      *tronwallet.KeyPair
	dest     *tronwallet.KeyPair
	contract string
	gas      *gasStub
}

func newTronHarness(t *testing.T) *tronHarness {
	t.Helper()
	tronQuotedTRX = "40"
	h := newHarness(t)

	node := tronfake.New()
	t.Cleanup(node.Close)
	hot, _ := tronwallet.GenerateKeyPair()
	dest, _ := tronwallet.GenerateKeyPair()
	contractKP, _ := tronwallet.GenerateKeyPair()
	node.SetToken(contractKP.Address, hot.Address, big.NewInt(1_000_000_000)) // 1000 USDT
	node.SetBalanceSun(hot.Address, 100_000_000)                              // 100 TRX

	// short windows so expiry is reachable by mining a few blocks (3s each)
	chain := tronclient.NewWithdrawalChain(tronclient.NewFullNode(node.URL()), tronclient.Config{
		ConfirmationsRequired: 3, ExpirationWindow: 30 * time.Second, ExpirySafetyMargin: 6 * time.Second,
	})
	gas := &gasStub{}

	h.svc.Networks = map[domain.Network]NetworkRuntime{domain.NetworkTRON: {Resource: chain, Gas: gas}}
	h.svc.Signer = FamilySigner{Tron: NewTronSigner(tronKeys{hot: hot.Address}, tronDec{plaintext: hot.PrivateKeyHex})}
	h.svc.HotWallets = &fakeHotBook{addrs: map[string]string{string(domain.NetworkTRON) + "|USDT": hot.Address}}
	h.svc.Fees = tronFees{h.fees}
	h.registry.add(domain.NetworkAsset{Network: domain.NetworkTRON, AssetCode: "USDT", Kind: domain.AssetKindToken,
		ContractAddress: contractKP.Address, Standard: domain.TokenStandardTRC20, Decimals: 6, Enabled: true})
	h.registry.add(domain.NetworkAsset{Network: domain.NetworkTRON, AssetCode: "TRX", Kind: domain.AssetKindNative, Decimals: 6, Enabled: true})
	h.svc.LedgerAssets.(fakeLedgerAssetsAdapter)["USDT"] = true
	h.svc.LedgerAssets.(fakeLedgerAssetsAdapter)["TRX"] = true
	h.svc.Config.Limits["USDT"] = AssetLimits{Max: decimal.NewFromInt(500), DailyMax: decimal.NewFromInt(5000)}
	h.svc.Config.Limits["TRX"] = AssetLimits{Max: decimal.NewFromInt(500), DailyMax: decimal.NewFromInt(5000)}

	userUSDT, _ := h.accounts.FindOrCreateUserAccount(context.Background(), h.userID, "USDT")
	h.ledger.fund(userUSDT, decimal.NewFromInt(100))

	return &tronHarness{harness: h, node: node, hot: hot, dest: dest, contract: contractKP.Address, gas: gas}
}

func (h *tronHarness) create(t *testing.T, amount string) uuid.UUID {
	t.Helper()
	res, err := h.svc.Create(context.Background(), CreateInput{
		UserID: h.userID, AssetCode: "USDT", Network: string(domain.NetworkTRON),
		Amount: decimal.RequireFromString(amount), DestinationAddress: h.dest.Address,
		IdempotencyKey: "wd-" + uuid.NewString(), TwoFAVerified: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return res.Withdrawal.ID
}

// tick advances the withdrawal once and returns its fresh state.
func (h *tronHarness) tick(t *testing.T, id uuid.UUID) *Withdrawal {
	t.Helper()
	w, err := h.store.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = h.svc.Advance(context.Background(), w) // transient errors are retried by the worker; state is what matters
	w, _ = h.store.Get(context.Background(), id)
	return w
}

func (h *tronHarness) mustReach(t *testing.T, id uuid.UUID, want Status, maxTicks int, mineEach int) *Withdrawal {
	t.Helper()
	var w *Withdrawal
	for i := 0; i < maxTicks; i++ {
		w = h.tick(t, id)
		if w.Status == want {
			return w
		}
		if mineEach > 0 {
			h.node.Mine(mineEach)
		}
	}
	t.Fatalf("no alcanzó %s en %d ticks; estado=%s detalle=%q", want, maxTicks, w.Status, w.ErrorDetail)
	return nil
}

func (h *tronHarness) userRaw(t *testing.T) *big.Int { return h.node.Token(h.contract, h.dest.Address) }

func reservationStatus(t *testing.T, h *tronHarness, w *Withdrawal) domain.ReservationStatus {
	t.Helper()
	r, err := h.ledger.FindReservation(context.Background(), *w.AmountReservationID)
	if err != nil {
		t.Fatal(err)
	}
	return r.Status
}

func TestTron_HappyPath_ReservedToCompleted(t *testing.T) {
	h := newTronHarness(t)
	id := h.create(t, "25")

	w := h.tick(t, id)
	if w.Status != StatusBroadcasted {
		t.Fatalf("esperaba broadcasted, got %s (%s)", w.Status, w.ErrorDetail)
	}
	if len(w.TxHash) != 64 || strings.HasPrefix(w.TxHash, "0x") || len(w.SignedRawTx) == 0 {
		t.Fatalf("hash/tx firmada TRON inválidos: %q", w.TxHash)
	}
	if w.Nonce == nil {
		t.Fatal("la columna nonce guarda el bloque de referencia TaPoS")
	}
	if len(h.nonces.live) != 0 || len(h.nonces.next) != 0 {
		t.Fatal("TRON no usa el lease de nonces EVM")
	}
	if h.node.Broadcasts() != 1 {
		t.Fatalf("broadcasts = %d", h.node.Broadcasts())
	}

	// minada pero sin confirmaciones suficientes → confirming
	h.node.Mine(1)
	if w = h.tick(t, id); w.Status != StatusConfirming {
		t.Fatalf("esperaba confirming, got %s (%s)", w.Status, w.ErrorDetail)
	}
	// nunca completa antes de tiempo
	if h.ledger.settledCount() != 0 {
		t.Fatal("no se liquida antes de las confirmaciones")
	}
	h.node.Mine(4)
	w = h.tick(t, id)
	if w.Status != StatusCompleted {
		t.Fatalf("esperaba completed, got %s (%s)", w.Status, w.ErrorDetail)
	}
	if w.LedgerTransactionID == nil || !w.ActualNetworkFeeUSDT.Valid {
		t.Fatal("debe liquidar en el ledger y registrar la comisión real")
	}
	if h.ledger.settledCount() != 1 {
		t.Fatalf("exactamente una liquidación, hubo %d", h.ledger.settledCount())
	}
	if h.userRaw(t).Cmp(big.NewInt(25_000_000)) != 0 {
		t.Fatalf("el destino debía recibir 25 USDT, got %s", h.userRaw(t))
	}
	// reintentar tras completar no duplica nada
	h.tick(t, id)
	if h.ledger.settledCount() != 1 || h.node.Broadcasts() != 1 {
		t.Fatal("un retiro completado es inmutable")
	}
}

func TestTron_AmbiguousBroadcast_AcceptedByNode_NeverSentTwice(t *testing.T) {
	h := newTronHarness(t)
	id := h.create(t, "10")
	h.node.QueueBroadcast("ambiguous") // el nodo lo aceptó pero perdimos la respuesta

	w := h.tick(t, id)
	if w.Status != StatusProcessing || w.TxHash == "" || len(w.SignedRawTx) == 0 {
		t.Fatalf("tras un broadcast ambiguo la tx firmada queda persistida en processing: %s", w.Status)
	}
	hash := w.TxHash
	w = h.mustReach(t, id, StatusBroadcasted, 3, 0)
	if w.TxHash != hash {
		t.Fatal("el hash no puede cambiar mientras la tx pueda estar viva")
	}
	if h.node.Broadcasts() != 1 {
		t.Fatalf("una tx que el nodo ya conoce jamás se reenvía (broadcasts=%d)", h.node.Broadcasts())
	}
	h.node.Mine(1)
	h.mustReach(t, id, StatusCompleted, 6, 2)
	if h.userRaw(t).Cmp(big.NewInt(10_000_000)) != 0 {
		t.Fatal("el destino debe recibir el monto exactamente una vez")
	}
}

func TestTron_AmbiguousBroadcast_Lost_ResendsSameBytesBeforeExpiry(t *testing.T) {
	h := newTronHarness(t)
	id := h.create(t, "10")
	h.node.QueueBroadcast("ambiguous_lost") // el nodo NO lo recibió

	w := h.tick(t, id)
	hash := w.TxHash
	if w.Status != StatusProcessing || hash == "" {
		t.Fatalf("estado tras envío perdido: %s", w.Status)
	}
	// aún no expira: se reenvían EXACTAMENTE los mismos bytes (mismo txid)
	w = h.mustReach(t, id, StatusBroadcasted, 3, 0)
	if w.TxHash != hash {
		t.Fatal("antes de expirar se reenvía la misma tx, no una nueva")
	}
	if h.node.Broadcasts() != 2 {
		t.Fatalf("un reenvío de la misma tx (broadcasts=%d)", h.node.Broadcasts())
	}
	h.mustReach(t, id, StatusCompleted, 8, 2)
	if h.userRaw(t).Cmp(big.NewInt(10_000_000)) != 0 {
		t.Fatal("monto duplicado o perdido")
	}
}

func TestTron_LostTxExpires_IsResignedWithFreshTxid_PaidOnce(t *testing.T) {
	h := newTronHarness(t)
	id := h.create(t, "10")
	// los envíos se pierden mientras la tx pueda estar viva
	h.node.QueueBroadcast("ambiguous_lost", "ambiguous_lost")

	w := h.tick(t, id)
	oldHash := w.TxHash
	if oldHash == "" {
		t.Fatal("debía firmar")
	}
	// La cadena avanza más allá de expiración + margen (30s+6s = 12 bloques de 3s)
	h.node.Mine(20)
	w = h.tick(t, id) // desconocida y expirada → se descarta, vuelve a firmar
	if w.Status != StatusProcessing || w.TxHash != "" {
		t.Fatalf("una tx expirada y desconocida debe descartarse (status=%s hash=%q)", w.Status, w.TxHash)
	}
	if h.node.PendingCount() != 0 {
		t.Fatal("la tx perdida nunca llegó al nodo")
	}
	w = h.mustReach(t, id, StatusBroadcasted, 3, 0)
	if w.TxHash == oldHash {
		t.Fatal("la nueva firma usa otro bloque de referencia → otro txid")
	}
	if !hasEvent(h.store, "expired_resign") {
		t.Fatal("debe quedar el evento expired_resign en la auditoría")
	}
	h.node.Mine(1)
	h.mustReach(t, id, StatusCompleted, 8, 2)
	if h.userRaw(t).Cmp(big.NewInt(10_000_000)) != 0 {
		t.Fatalf("se debe pagar exactamente una vez, destino=%s", h.userRaw(t))
	}
	if h.node.TxMined(oldHash) {
		t.Fatal("la tx expirada jamás puede minarse")
	}
}

func TestTron_ReorgDropsReceipt_NeverCompletesPrematurely(t *testing.T) {
	h := newTronHarness(t)
	id := h.create(t, "10")
	h.tick(t, id)
	h.node.Mine(1)
	if w := h.tick(t, id); w.Status != StatusConfirming {
		t.Fatalf("esperaba confirming, got %s", w.Status)
	}

	h.node.Reorg(2) // el bloque con la tx desaparece de la cadena canónica
	h.node.Mine(1)
	w := h.tick(t, id)
	if w.Status == StatusCompleted || h.ledger.settledCount() != 0 {
		t.Fatal("tras un reorg no se puede completar ni liquidar")
	}
	// se reenvía la MISMA tx (aún no expira) y termina bien
	hash := w.TxHash
	h.mustReach(t, id, StatusCompleted, 12, 2)
	w, _ = h.store.Get(context.Background(), id)
	if w.TxHash != hash && hash != "" {
		t.Fatal("el reenvío tras el reorg usa la misma tx")
	}
	if h.ledger.settledCount() != 1 {
		t.Fatalf("liquidaciones = %d", h.ledger.settledCount())
	}
}

func TestTron_OutOfEnergy_OnChainFailure_ReleasesFunds(t *testing.T) {
	h := newTronHarness(t)
	id := h.create(t, "10")
	h.node.FailNextTransfer()
	h.tick(t, id)
	h.node.Mine(1)
	w := h.mustReach(t, id, StatusFailed, 6, 1)
	if h.ledger.settledCount() != 0 {
		t.Fatal("una tx fallida on-chain no liquida el monto")
	}
	if got := reservationStatus(t, h, w); got != domain.ReservationReleased {
		t.Fatalf("las reservas deben liberarse, están %s", got)
	}
	if h.userRaw(t).Sign() != 0 {
		t.Fatal("el destino no recibe nada si la tx falló")
	}
}

func TestTron_NoEnergy_GasStationTopsUpThenWithdrawalProceeds(t *testing.T) {
	h := newTronHarness(t)
	h.node.SetBalanceSun(h.hot.Address, 0) // sin TRX y sin recursos en stake
	h.gas.fund = func() { h.node.SetBalanceSun(h.hot.Address, 100_000_000) }
	id := h.create(t, "10")

	w := h.tick(t, id)
	if len(h.gas.calls) != 1 {
		t.Fatalf("debía pedir gas exactamente una vez, got %d", len(h.gas.calls))
	}
	if h.gas.calls[0].Sign() <= 0 || h.gas.calls[0].Cmp(big.NewInt(100_000_000)) > 0 {
		t.Fatalf("el top-up pedido (sun) debe ser solo el faltante: %s", h.gas.calls[0])
	}
	if w.Status != StatusBroadcasted {
		t.Fatalf("con el gas ya repuesto debe transmitir, got %s (%s)", w.Status, w.ErrorDetail)
	}
	h.node.Mine(1)
	h.mustReach(t, id, StatusCompleted, 8, 2)
}

func TestTron_NoEnergy_NoStakeNoGasStation_FailsClosedAndReleases(t *testing.T) {
	h := newTronHarness(t)
	h.node.SetBalanceSun(h.hot.Address, 0)
	h.svc.Networks[domain.NetworkTRON] = NetworkRuntime{Resource: h.svc.Networks[domain.NetworkTRON].Resource} // sin Gas
	id := h.create(t, "10")

	w := h.tick(t, id)
	if w.Status != StatusFailed || w.FailureCode() != "gas_unavailable" {
		t.Fatalf("sin energía ni gas station debe fallar cerrado: %s %q", w.Status, w.ErrorDetail)
	}
	if h.node.Broadcasts() != 0 {
		t.Fatal("no se transmite nada sin recursos")
	}
	if got := reservationStatus(t, h, w); got != domain.ReservationReleased {
		t.Fatalf("reservas liberadas, están %s", got)
	}
}

func TestTron_NoEnergy_GasStationPolicyExceeded_KeepsRetryingNeverSigns(t *testing.T) {
	h := newTronHarness(t)
	h.node.SetBalanceSun(h.hot.Address, 0)
	h.gas.err = errors.New("gasstation: monto fuera de la política")
	id := h.create(t, "10")

	for i := 0; i < 3; i++ {
		w := h.tick(t, id)
		if w.Status != StatusProcessing || len(w.SignedRawTx) != 0 {
			t.Fatalf("sin gas no se firma: status=%s", w.Status)
		}
	}
	if h.node.Broadcasts() != 0 {
		t.Fatal("no se transmite nada")
	}
}

func TestTron_StakedResources_NoGasNeeded(t *testing.T) {
	h := newTronHarness(t)
	h.node.SetBalanceSun(h.hot.Address, 0)
	h.node.SetStaked(h.hot.Address, 1_000_000, 100_000) // energía y bandwidth en stake
	id := h.create(t, "10")
	w := h.tick(t, id)
	if w.Status != StatusBroadcasted || len(h.gas.calls) != 0 {
		t.Fatalf("con recursos en stake no hay que quemar TRX: status=%s gas calls=%d", w.Status, len(h.gas.calls))
	}
}

func TestTron_InsufficientOnChainUSDT_FailsAndReleases(t *testing.T) {
	h := newTronHarness(t)
	h.node.SetToken(h.contract, h.hot.Address, big.NewInt(1_000_000)) // solo 1 USDT en la HOT
	id := h.create(t, "10")
	w := h.tick(t, id)
	if w.Status != StatusFailed || w.FailureCode() != "insufficient_onchain_funds" {
		t.Fatalf("got %s %q", w.Status, w.ErrorDetail)
	}
	if got := reservationStatus(t, h, w); got != domain.ReservationReleased {
		t.Fatalf("reservas: %s", got)
	}
}

func TestTron_FeeSpikeBeyondQuote_FailsBeforeSigning(t *testing.T) {
	h := newTronHarness(t)
	tronQuotedTRX = "5" // se cotizó 5 TRX; el costo real del peor caso es ~16 TRX
	id := h.create(t, "10")
	w := h.tick(t, id)
	if w.Status != StatusFailed || w.FailureCode() != "network_fee_overrun" {
		t.Fatalf("got %s %q", w.Status, w.ErrorDetail)
	}
	if h.node.Broadcasts() != 0 {
		t.Fatal("no se firma ni transmite fuera de lo cotizado")
	}
}

func TestTron_NativeTRXWithdrawalIsRefused(t *testing.T) {
	h := newTronHarness(t)
	userTRX, _ := h.accounts.FindOrCreateUserAccount(context.Background(), h.userID, "TRX")
	h.ledger.fund(userTRX, decimal.NewFromInt(100))
	_, err := h.svc.Create(context.Background(), CreateInput{
		UserID: h.userID, AssetCode: "TRX", Network: string(domain.NetworkTRON),
		Amount: decimal.NewFromInt(10), DestinationAddress: h.dest.Address,
		IdempotencyKey: "wd-" + uuid.NewString(), TwoFAVerified: true,
	})
	if !errors.Is(err, ErrAssetNotEnabled) {
		t.Fatalf("TRX es combustible, no un producto: %v", err)
	}
}

func TestTron_NoKeyMaterialInStateOrEvents(t *testing.T) {
	h := newTronHarness(t)
	id := h.create(t, "10")
	h.tick(t, id)
	h.node.Mine(1)
	h.mustReach(t, id, StatusCompleted, 8, 2)

	key := h.hot.PrivateKeyHex
	w, _ := h.store.Get(context.Background(), id)
	if strings.Contains(w.ErrorDetail, key) || strings.Contains(string(w.SignedRawTx), key) {
		t.Fatal("la clave privada no puede aparecer en el retiro")
	}
	for _, ev := range h.store.events {
		for _, v := range ev.Detail {
			if s, ok := v.(string); ok && strings.Contains(s, key) {
				t.Fatalf("la clave privada apareció en el evento %s", ev.Type)
			}
		}
	}
}

func TestTronSigner_RefusesWrongSourceKey(t *testing.T) {
	hot, other, dest, contract := mustTronKP(t), mustTronKP(t), mustTronKP(t), mustTronKP(t)
	// el descifrador devuelve la clave de OTRA wallet: la tx saldría firmada por un firmante distinto del origen
	s := NewTronSigner(tronKeys{hot: hot.Address}, tronDec{plaintext: other.PrivateKeyHex})
	ref := make([]byte, 32)
	_, err := s.Sign(context.Background(), SignRequest{
		Network: domain.NetworkTRON, FromAddress: hot.Address, ToAddress: dest.Address,
		AssetKind: domain.AssetKindToken, ContractAddress: contract.Address, AmountRaw: big.NewInt(1),
		Tron: &TronSignParams{RefBlockID: ref, TimestampMs: 1000, ExpirationMs: 2000, FeeLimitSun: 1},
	})
	if err == nil {
		t.Fatal("firmar con una clave que no es la del origen debe fallar")
	}
	if strings.Contains(err.Error(), other.PrivateKeyHex) {
		t.Fatal("el error filtra la clave")
	}
}

func TestTronSigner_RefusesNativeAndEVMRequests(t *testing.T) {
	hot, dest := mustTronKP(t), mustTronKP(t)
	s := NewTronSigner(tronKeys{hot: hot.Address}, tronDec{plaintext: hot.PrivateKeyHex})
	base := SignRequest{Network: domain.NetworkTRON, FromAddress: hot.Address, ToAddress: dest.Address, AmountRaw: big.NewInt(1),
		Tron: &TronSignParams{RefBlockID: make([]byte, 32), TimestampMs: 1, ExpirationMs: 2, FeeLimitSun: 1}}
	native := base
	native.AssetKind = domain.AssetKindNative
	if _, err := s.Sign(context.Background(), native); err == nil {
		t.Fatal("TRON solo firma TRC-20")
	}
	evm := base
	evm.Network = domain.NetworkBSC
	evm.AssetKind = domain.AssetKindToken
	if _, err := s.Sign(context.Background(), evm); err == nil {
		t.Fatal("una red EVM no se firma con el firmante TRON")
	}
	if _, err := (FamilySigner{}).Sign(context.Background(), base); !errors.Is(err, ErrNoSigner) {
		t.Fatalf("sin firmante para la familia debe ser ErrNoSigner, got %v", err)
	}
}

func TestValidateTronDestination(t *testing.T) {
	kp, contract := mustTronKP(t), mustTronKP(t)
	if got, err := ValidateDestination(domain.NetworkTRON, contract.Address, kp.Address); err != nil || got != kp.Address {
		t.Fatalf("dirección válida: %v", err)
	}
	for name, addr := range map[string]string{
		"vacía":          "",
		"EVM":            "0x2222222222222222222222222222222222222222",
		"checksum roto":  kp.Address[:len(kp.Address)-1] + "x",
		"con espacios":   " " + kp.Address,
		"el contrato":    contract.Address,
		"dirección cero": tronZeroAddress,
	} {
		if _, err := ValidateDestination(domain.NetworkTRON, contract.Address, addr); !errors.Is(err, ErrInvalidAddress) {
			t.Errorf("%s debía ser inválida, got %v", name, err)
		}
	}
}

func TestClassifyTronBroadcastErrors(t *testing.T) {
	cases := map[string]broadcastClass{
		"tron broadcast rechazado: DUP_TRANSACTION_ERROR: dup transaction":         broadcastAlreadyKnown,
		"tron broadcast rechazado: SIGERROR: validate signature error":              broadcastRejected,
		"tron broadcast rechazado: CONTRACT_VALIDATE_ERROR: x":                      broadcastRejected,
		"tron broadcast rechazado: TRANSACTION_EXPIRATION_ERROR: transaction expired": broadcastReprice,
		"tron broadcast rechazado: TAPOS_ERROR: x":                                  broadcastReprice,
		"tron broadcast rechazado: BANDWITH_ERROR: x":                               broadcastReprice,
		"tron /wallet/broadcasthex: HTTP 504":                                        broadcastAmbiguous,
		"context deadline exceeded":                                                  broadcastAmbiguous,
	}
	for msg, want := range cases {
		if got := classifyBroadcastError(errors.New(msg)); got != want {
			t.Errorf("%q → %v, esperaba %v", msg, got, want)
		}
	}
}

func hasEvent(s *fakeStore, typ string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.events {
		if e.Type == typ {
			return true
		}
	}
	return false
}

func mustTronKP(t *testing.T) *tronwallet.KeyPair {
	t.Helper()
	kp, err := tronwallet.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	return kp
}

func (l *fakeLedger) settledCount() int { return len(l.settled) }
