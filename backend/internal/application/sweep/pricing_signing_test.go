package sweep

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
)

// ---- 6. Balance insuficiente (no alcanza ni para su propio gas) ----

func TestPriceCandidate_NativeInsufficientAfterGas(t *testing.T) {
	h := newHarness(t, nil)
	h.chain.nativeBalance = decimal.NewFromFloat(0.0000000000001) // menor que el costo de gas

	_, err := h.svc.evaluateCandidate(ctx(), h.candidate("BNB"))
	if !errors.Is(err, ErrInsufficientOnChain) && !errors.Is(err, ErrBelowDustThreshold) {
		t.Fatalf("esperaba ErrInsufficientOnChain o ErrBelowDustThreshold, obtuve %v", err)
	}
}

// ---- 7. Dust / monto por debajo del mínimo ----

func TestPriceCandidate_TokenBelowDustThreshold(t *testing.T) {
	h := newHarness(t, nil)
	h.chain.tokenBalance["0xCONTRACT00000000000000000000000000000CC"] = decimal.NewFromInt(1) // dust configurado en 5

	_, err := h.svc.evaluateCandidate(ctx(), h.candidate("USDT"))
	if !errors.Is(err, ErrBelowDustThreshold) {
		t.Fatalf("esperaba ErrBelowDustThreshold, obtuve %v", err)
	}
}

func TestPriceCandidate_TokenAboveDust_Eligible(t *testing.T) {
	h := newHarness(t, nil)
	h.chain.tokenBalance["0xCONTRACT00000000000000000000000000000CC"] = decimal.NewFromInt(100)

	sw, err := h.svc.evaluateCandidate(ctx(), h.candidate("USDT"))
	if err != nil {
		t.Fatalf("evaluateCandidate: %v", err)
	}
	if !sw.Amount.Equal(decimal.NewFromInt(100)) {
		t.Fatalf("un sweep de token debe barrer el balance completo, obtuve %s", sw.Amount)
	}
	if sw.AssetKind != domain.AssetKindToken {
		t.Fatalf("esperaba asset_kind=token, obtuve %s", sw.AssetKind)
	}
}

// ---- 23. El monto nunca proviene del historico de depositos, solo del balance vivo ----

func TestPriceCandidate_AmountReflectsLiveChainBalance_NotDepositHistory(t *testing.T) {
	h := newHarness(t, nil)
	h.chain.tokenBalance["0xCONTRACT00000000000000000000000000000CC"] = decimal.NewFromInt(50)
	sw1, err := h.svc.evaluateCandidate(ctx(), h.candidate("USDT"))
	if err != nil {
		t.Fatalf("evaluateCandidate (50): %v", err)
	}
	if !sw1.Amount.Equal(decimal.NewFromInt(50)) {
		t.Fatalf("esperaba 50, obtuve %s", sw1.Amount)
	}

	// Llega otro deposito a la misma direccion: el balance on-chain sube. El
	// proximo candidato debe reflejar el balance ACTUAL, no una suma de
	// depositos historicos que este paquete nunca consulta.
	h.chain.tokenBalance["0xCONTRACT00000000000000000000000000000CC"] = decimal.NewFromInt(150)
	sw2, err := h.svc.evaluateCandidate(ctx(), h.candidate("USDT"))
	if err != nil {
		t.Fatalf("evaluateCandidate (150): %v", err)
	}
	if !sw2.Amount.Equal(decimal.NewFromInt(150)) {
		t.Fatalf("esperaba 150 (balance vivo), obtuve %s", sw2.Amount)
	}
}

// ---- 10 & 11. Firma correcta + nonce correcto ----

func TestPrepareAndSign_SignsAndPersistsBeforeBroadcast(t *testing.T) {
	h := newHarness(t, nil)
	sw := h.seedSweep(&Sweep{
		Network: h.network, AssetCode: "BNB", AssetKind: domain.AssetKindNative,
		FromAddress: h.fromAddr, FromUserID: h.userID, FromCryptoAddressID: h.addressID,
		ToCustodyWalletID: h.wallets.byKey[walletKey("TREASURY", string(h.network), "BNB")].ID,
		ToAddress:         h.toAddr, Amount: decimal.NewFromInt(1), Status: StatusProcessing,
	})
	h.chain.pendingNonce = 7

	var capturedReq SignRequest
	h.signer.sign = func(req SignRequest) (*SignedTx, error) {
		capturedReq = req
		return &SignedTx{Raw: []byte{0xDE, 0xAD}, TxHash: "0xsigned", Nonce: req.Nonce}, nil
	}

	rt, _ := h.svc.runtimeFor(h.network)
	updated, err := h.svc.prepareAndSign(ctx(), rt, sw)
	if err != nil {
		t.Fatalf("prepareAndSign: %v", err)
	}
	if len(updated.SignedRawTx) == 0 || updated.TxHash != "0xsigned" {
		t.Fatalf("debe persistir la tx firmada y su hash: %+v", updated)
	}
	if updated.Nonce == nil || *updated.Nonce != 7 {
		t.Fatalf("el nonce debe salir del nonce pendiente de la cadena (7), obtuve %v", updated.Nonce)
	}
	if capturedReq.FromAddress != h.fromAddr || capturedReq.ToAddress != h.toAddr {
		t.Fatalf("la firma debe usar from=direccion de deposito, to=custody wallet: %+v", capturedReq)
	}
	if capturedReq.UserID != h.userID {
		t.Fatalf("debe firmar con la clave del usuario dueno de la direccion de deposito, no la de la custody wallet")
	}
	if updated.SignedAt == nil {
		t.Fatal("debe registrar signed_at")
	}
}

// ---- 18. Error de firma ----

func TestPrepareAndSign_SignerError_NeverPersistsPartialState(t *testing.T) {
	h := newHarness(t, nil)
	sw := h.seedSweep(&Sweep{
		Network: h.network, AssetCode: "BNB", AssetKind: domain.AssetKindNative,
		FromAddress: h.fromAddr, FromUserID: h.userID, FromCryptoAddressID: h.addressID,
		ToCustodyWalletID: h.wallets.byKey[walletKey("TREASURY", string(h.network), "BNB")].ID,
		ToAddress:         h.toAddr, Amount: decimal.NewFromInt(1), Status: StatusProcessing,
	})
	h.signer.fail = true

	rt, _ := h.svc.runtimeFor(h.network)
	_, err := h.svc.prepareAndSign(ctx(), rt, sw)
	if err == nil {
		t.Fatal("esperaba un error del firmante")
	}
	stored, _ := h.store.Get(ctx(), sw.ID)
	if len(stored.SignedRawTx) != 0 || stored.TxHash != "" {
		t.Fatalf("un error de firma no debe dejar una tx parcial persistida: %+v", stored)
	}
	if stored.Status != StatusProcessing {
		t.Fatalf("debe permanecer en processing para reintentar, quedo %s", stored.Status)
	}
}

// ---- 8. Gas insuficiente (sin Gas Station disponible) ----

func TestPrepareAndSign_TokenGasUnavailable_FailsClosed(t *testing.T) {
	h := newHarness(t, nil)
	h.chain.nativeBalance = decimal.Zero // sin gas nativo
	rt := h.svc.deps.Networks[h.network]
	rt.Gas = nil // sin Gas Station configurada para esta red
	h.svc.deps.Networks[h.network] = rt

	sw := h.seedSweep(&Sweep{
		Network: h.network, AssetCode: "USDT", AssetKind: domain.AssetKindToken,
		FromAddress: h.fromAddr, FromUserID: h.userID, FromCryptoAddressID: h.addressID,
		ToCustodyWalletID: h.wallets.byKey[walletKey("TREASURY", string(h.network), "USDT")].ID,
		ToAddress:         h.toAddr, Amount: decimal.NewFromInt(10), Status: StatusProcessing,
	})
	h.chain.tokenBalance["0xCONTRACT00000000000000000000000000000CC"] = decimal.NewFromInt(10)

	_, err := h.svc.prepareAndSign(ctx(), h.svc.deps.Networks[h.network], sw)
	// failClosed succeeds -> prepareAndSign returns (sweep, nil): the
	// failure is signaled by the persisted status, not by a returned error
	// (mirrors blockchainwithdrawal.failWithRelease's exact contract).
	if err != nil {
		t.Fatalf("failClosed exitoso no debe propagar error: %v", err)
	}
	stored, _ := h.store.Get(ctx(), sw.ID)
	if stored.Status != StatusFailed {
		t.Fatalf("sin Gas Station y sin gas propio, debe fallar cerrado (failed), quedo %s", stored.Status)
	}
	if stored.NeedsReview {
		t.Fatal("nunca se transmitio nada: no requiere revision manual, es seguro reintentar mas tarde")
	}
	if stored.FailureCode() != "gas_unavailable" {
		t.Fatalf("codigo de fallo inesperado: %s", stored.ErrorDetail)
	}
}

// ---- 9. Gas Station integrada correctamente ----

func TestPrepareAndSign_TokenSweep_RequestsGasTopUpThenSignsOnceFunded(t *testing.T) {
	h := newHarness(t, nil)
	h.chain.nativeBalance = decimal.Zero
	h.chain.tokenBalance["0xCONTRACT00000000000000000000000000000CC"] = decimal.NewFromInt(20)

	sw := h.seedSweep(&Sweep{
		Network: h.network, AssetCode: "USDT", AssetKind: domain.AssetKindToken,
		FromAddress: h.fromAddr, FromUserID: h.userID, FromCryptoAddressID: h.addressID,
		ToCustodyWalletID: h.wallets.byKey[walletKey("TREASURY", string(h.network), "USDT")].ID,
		ToAddress:         h.toAddr, Amount: decimal.NewFromInt(20), Status: StatusProcessing,
	})

	rt := h.svc.deps.Networks[h.network]
	_, err := h.svc.prepareAndSign(ctx(), rt, sw)
	if err == nil {
		t.Fatal("la primera pasada debe esperar la confirmacion del top-up, no firmar todavia")
	}
	if h.gas.calls != 1 {
		t.Fatalf("debe haber pedido exactamente 1 top-up a Gas Station, hubo %d", h.gas.calls)
	}
	stored, _ := h.store.Get(ctx(), sw.ID)
	if stored.Status != StatusProcessing || len(stored.SignedRawTx) != 0 {
		t.Fatalf("mientras espera el gas no debe firmar ni fallar: %+v", stored)
	}

	h.chain.nativeBalance = decimal.NewFromFloat(0.01)
	updated, err := h.svc.prepareAndSign(ctx(), rt, stored)
	if err != nil {
		t.Fatalf("tras el top-up debe poder firmar: %v", err)
	}
	if len(updated.SignedRawTx) == 0 {
		t.Fatal("debe haber firmado en la segunda pasada")
	}
	if h.gas.calls != 1 {
		t.Fatalf("no debe pedir un segundo top-up si ya alcanza, hubo %d llamadas", h.gas.calls)
	}
}
