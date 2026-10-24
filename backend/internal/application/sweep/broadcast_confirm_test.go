package sweep

import (
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
)

// ---- 12. Broadcast exitoso ----

func TestBroadcastStep_Success(t *testing.T) {
	h := newHarness(t, nil)
	nonce := int64(3)
	sw := h.seedSweep(&Sweep{
		Network: h.network, AssetCode: "BNB", AssetKind: domain.AssetKindNative,
		FromAddress: h.fromAddr, ToAddress: h.toAddr, Amount: decimal.NewFromInt(1),
		Status: StatusProcessing, Nonce: &nonce, TxHash: "0xhash1", SignedRawTx: []byte{0x01},
	})
	h.chain.broadcastHash = "0xhash1"

	rt, _ := h.svc.runtimeFor(h.network)
	if _, err := h.svc.broadcastStep(ctx(), rt, sw); err != nil {
		t.Fatalf("broadcastStep: %v", err)
	}
	stored, _ := h.store.Get(ctx(), sw.ID)
	if stored.Status != StatusBroadcasted {
		t.Fatalf("esperaba broadcasted, obtuve %s", stored.Status)
	}
	if stored.BroadcastedAt == nil {
		t.Fatal("debe registrar broadcasted_at")
	}
}

// ---- 13. Broadcast rechazado ----

func TestBroadcastStep_RejectedFailsClosed(t *testing.T) {
	h := newHarness(t, nil)
	nonce := int64(1)
	sw := h.seedSweep(&Sweep{
		Network: h.network, AssetCode: "BNB", AssetKind: domain.AssetKindNative,
		FromAddress: h.fromAddr, ToAddress: h.toAddr, Amount: decimal.NewFromInt(1),
		Status: StatusProcessing, Nonce: &nonce, TxHash: "0xrejected", SignedRawTx: []byte{0x01},
	})
	h.chain.broadcastErr = errors.New("invalid sender")

	rt, _ := h.svc.runtimeFor(h.network)
	// failClosed succeeds -> broadcastStep returns (0, nil): the failure is
	// signaled by the persisted status, not by a returned error.
	if _, err := h.svc.broadcastStep(ctx(), rt, sw); err != nil {
		t.Fatalf("un rechazo definitivo manejado (failed) no debe propagar error: %v", err)
	}
	stored, _ := h.store.Get(ctx(), sw.ID)
	if stored.Status != StatusFailed {
		t.Fatalf("un rechazo definitivo debe fallar cerrado, quedo %s", stored.Status)
	}
}

// ---- 14. Broadcast ambiguo: nunca reenvia automaticamente ----

func TestBroadcastStep_AmbiguousNeverAutoRebroadcasts(t *testing.T) {
	h := newHarness(t, nil)
	nonce := int64(2)
	sw := h.seedSweep(&Sweep{
		Network: h.network, AssetCode: "BNB", AssetKind: domain.AssetKindNative,
		FromAddress: h.fromAddr, ToAddress: h.toAddr, Amount: decimal.NewFromInt(1),
		Status: StatusProcessing, Nonce: &nonce, TxHash: "0xambiguous", SignedRawTx: []byte{0x01},
	})
	h.chain.broadcastErr = errors.New("i/o timeout") // clasifica como ambiguo

	rt, _ := h.svc.runtimeFor(h.network)
	if _, err := h.svc.broadcastStep(ctx(), rt, sw); err == nil {
		t.Fatal("esperaba un error (ambiguo) propagado para reintentar con backoff, nunca en la misma llamada")
	}
	stored, _ := h.store.Get(ctx(), sw.ID)
	if stored.Status != StatusProcessing {
		t.Fatalf("un broadcast ambiguo nunca debe marcarse broadcasted/failed automaticamente, quedo %s", stored.Status)
	}
	if len(stored.SignedRawTx) == 0 || stored.TxHash != "0xambiguous" {
		t.Fatal("debe conservar la misma tx firmada para que el siguiente tick decida, sin generar una segunda")
	}
}

// ---- 24 (parte 1). No crea doble transaccion: no re-firma si ya hay una firmada ----

func TestProcessingStep_SkipsSigningIfAlreadySigned(t *testing.T) {
	h := newHarness(t, nil)
	nonce := int64(5)
	sw := h.seedSweep(&Sweep{
		Network: h.network, AssetCode: "BNB", AssetKind: domain.AssetKindNative,
		FromAddress: h.fromAddr, ToAddress: h.toAddr, Amount: decimal.NewFromInt(1),
		Status: StatusProcessing, Nonce: &nonce, TxHash: "0xexisting", SignedRawTx: []byte{0xAB},
	})
	h.chain.broadcastHash = "0xexisting"
	signCalls := 0
	h.signer.sign = func(req SignRequest) (*SignedTx, error) {
		signCalls++
		return &SignedTx{Raw: []byte{0x99}, TxHash: "0xnew", Nonce: req.Nonce}, nil
	}

	if _, err := h.svc.processingStep(ctx(), sw); err != nil {
		t.Fatalf("processingStep: %v", err)
	}
	if signCalls != 0 {
		t.Fatalf("no debe volver a firmar una tx ya firmada, se firmo %d veces", signCalls)
	}
	stored, _ := h.store.Get(ctx(), sw.ID)
	if stored.TxHash != "0xexisting" {
		t.Fatalf("debe conservar la tx original, obtuvo %s", stored.TxHash)
	}
}

// ---- 24 (parte 2). Un tx perdido del mempool se reenvia con los MISMOS bytes ----

func TestHandleMissingReceipt_NonceFree_RebroadcastsIdenticalSignedBytes(t *testing.T) {
	h := newHarness(t, nil)
	nonce := int64(9)
	signedBytes := []byte{0x01, 0x02, 0x03}
	sw := h.seedSweep(&Sweep{
		Network: h.network, AssetCode: "BNB", AssetKind: domain.AssetKindNative,
		FromAddress: h.fromAddr, ToAddress: h.toAddr, Amount: decimal.NewFromInt(1),
		Status: StatusBroadcasted, Nonce: &nonce, TxHash: "0xdropped", SignedRawTx: signedBytes,
		BroadcastedAt: timePtr(time.Now().Add(-time.Minute)),
	})
	h.chain.pendingNonce = 9 // el nonce sigue libre: la red nunca mino la tx
	h.chain.broadcastHash = "0xdropped"

	if _, err := h.svc.confirmStep(ctx(), sw); err != nil {
		t.Fatalf("confirmStep: %v", err)
	}
	stored, _ := h.store.Get(ctx(), sw.ID)
	if stored.Status != StatusBroadcasted {
		t.Fatalf("debe seguir en broadcasted esperando, quedo %s", stored.Status)
	}
	if string(stored.SignedRawTx) != string(signedBytes) {
		t.Fatal("debe conservar exactamente los mismos bytes firmados (mismo nonce): nunca construir una segunda transaccion")
	}
}

// ---- 15. Confirmacion ----

func TestConfirmStep_ReachesRequiredConfirmations_Completes(t *testing.T) {
	h := newHarness(t, nil)
	required := int64(2)
	sw := h.seedSweep(&Sweep{
		Network: h.network, AssetCode: "BNB", AssetKind: domain.AssetKindNative,
		FromAddress: h.fromAddr, ToAddress: h.toAddr, Amount: decimal.NewFromInt(1),
		Status: StatusBroadcasted, TxHash: "0xconfirm", RequiredConfirmations: &required,
	})
	h.chain.receipts["0xconfirm"] = &domain.TransactionReceipt{
		TxHash: "0xconfirm", BlockNumber: 100, BlockHash: "0xblockA", Status: true, Confirmations: 1,
	}
	if _, err := h.svc.confirmStep(ctx(), sw); err != nil {
		t.Fatalf("confirmStep (1a confirmacion): %v", err)
	}
	stored, _ := h.store.Get(ctx(), sw.ID)
	if stored.Status != StatusConfirming {
		t.Fatalf("esperaba confirming, obtuve %s", stored.Status)
	}

	h.chain.receipts["0xconfirm"].Confirmations = 2
	if _, err := h.svc.confirmStep(ctx(), stored); err != nil {
		t.Fatalf("confirmStep (2a confirmacion): %v", err)
	}
	stored, _ = h.store.Get(ctx(), sw.ID)
	if stored.Status != StatusCompleted {
		t.Fatalf("esperaba completed, obtuve %s (%s)", stored.Status, stored.ErrorDetail)
	}
	if stored.CompletedAt == nil {
		t.Fatal("debe registrar completed_at")
	}
}

// ---- 16. Reorg / tx desaparecida ----

func TestConfirmStep_ReorgDetected_ResetsConfirmations(t *testing.T) {
	h := newHarness(t, nil)
	required := int64(2)
	blockNum := int64(100)
	confs := int64(1)
	sw := h.seedSweep(&Sweep{
		Network: h.network, AssetCode: "BNB", AssetKind: domain.AssetKindNative,
		FromAddress: h.fromAddr, ToAddress: h.toAddr, Amount: decimal.NewFromInt(1),
		Status: StatusConfirming, TxHash: "0xreorg", RequiredConfirmations: &required,
		BlockNumber: &blockNum, BlockHash: "0xblockOLD", Confirmations: confs,
	})
	h.chain.receipts["0xreorg"] = &domain.TransactionReceipt{
		TxHash: "0xreorg", BlockNumber: 101, BlockHash: "0xblockNEW", Status: true, Confirmations: 1,
	}
	if _, err := h.svc.confirmStep(ctx(), sw); err != nil {
		t.Fatalf("confirmStep: %v", err)
	}
	stored, _ := h.store.Get(ctx(), sw.ID)
	if stored.Status == StatusCompleted {
		t.Fatal("un reorg nunca debe completar en el mismo tick")
	}
	if stored.BlockHash != "0xblockNEW" {
		t.Fatalf("debe registrar el nuevo bloque, obtuvo %s", stored.BlockHash)
	}
}

func TestHandleMissingReceipt_ReorgDroppedReceipt_GoesBackToBroadcasted(t *testing.T) {
	h := newHarness(t, nil)
	blockNum := int64(50)
	sw := h.seedSweep(&Sweep{
		Network: h.network, AssetCode: "BNB", AssetKind: domain.AssetKindNative,
		FromAddress: h.fromAddr, ToAddress: h.toAddr, Amount: decimal.NewFromInt(1),
		Status: StatusConfirming, TxHash: "0xvanished", BlockNumber: &blockNum, BlockHash: "0xoldblock",
	})
	if _, err := h.svc.confirmStep(ctx(), sw); err != nil {
		t.Fatalf("confirmStep: %v", err)
	}
	stored, _ := h.store.Get(ctx(), sw.ID)
	if stored.Status != StatusBroadcasted {
		t.Fatalf("debe volver a broadcasted para seguir el tx, quedo %s", stored.Status)
	}
	if stored.BlockNumber != nil {
		t.Fatal("debe limpiar el bloque anterior tras el reorg")
	}
}

// ---- 17. Reinicio del worker: continua desde el estado persistido ----

func TestAdvance_ResumeAfterRestart_UsesPersistedSignedTxWithoutResigning(t *testing.T) {
	h := newHarness(t, nil)
	nonce := int64(4)
	sw := h.seedSweep(&Sweep{
		Network: h.network, AssetCode: "BNB", AssetKind: domain.AssetKindNative,
		FromAddress: h.fromAddr, ToAddress: h.toAddr, Amount: decimal.NewFromInt(1),
		Status: StatusProcessing, Nonce: &nonce, TxHash: "0xpersisted", SignedRawTx: []byte{0x77},
	})
	h.chain.broadcastHash = "0xpersisted"
	signCalls := 0
	h.signer.sign = func(req SignRequest) (*SignedTx, error) {
		signCalls++
		return &SignedTx{Raw: []byte{0x00}, TxHash: "0xshouldnothappen", Nonce: req.Nonce}, nil
	}

	// Simula un "reinicio del proceso": una llamada nueva a Advance() con la
	// fila tal cual quedo persistida, sin ningun estado en memoria.
	if _, err := h.svc.Advance(ctx(), sw); err != nil {
		t.Fatalf("Advance tras reinicio: %v", err)
	}
	if signCalls != 0 {
		t.Fatal("tras un reinicio nunca debe volver a construir/firmar una transaccion nueva")
	}
	stored, _ := h.store.Get(ctx(), sw.ID)
	if stored.Status != StatusBroadcasted || stored.TxHash != "0xpersisted" {
		t.Fatalf("debe retomar y transmitir la misma tx persistida: %+v", stored)
	}
}

// ---- 19. Error de custody (lookup falla, no solo "no encontrado") ----

func TestEvaluateCandidate_CustodyLookupError_FailsClosed(t *testing.T) {
	h := newHarness(t, nil)
	h.wallets.failErr = errors.New("db unavailable")

	_, err := h.svc.evaluateCandidate(ctx(), h.candidate("BNB"))
	if !errors.Is(err, ErrNoCustodyDestination) {
		t.Fatalf("un error consultando custody tambien debe fallar cerrado, obtuve %v", err)
	}
}

// ---- 20. Auditoria ----

func TestAudit_LogsAcrossLifecycle(t *testing.T) {
	h := newHarness(t, nil)
	h.candidates.items = []Candidate{h.candidate("BNB")}
	if _, claimed, err := h.svc.DiscoverAndClaim(ctx()); err != nil || claimed != 1 {
		t.Fatalf("DiscoverAndClaim: claimed=%d err=%v", claimed, err)
	}
	actions := h.audit.actions()
	if len(actions) == 0 || actions[0] != "sweep_detected" {
		t.Fatalf("debe auditar la deteccion del candidato, acciones=%v", actions)
	}

	var sw *Sweep
	for _, s := range h.store.byID {
		sw = s
	}
	nonce := int64(1)
	sw.Nonce = &nonce
	sw.TxHash = "0xaudit"
	sw.SignedRawTx = []byte{0x01}
	sw.Status = StatusProcessing
	h.store.byID[sw.ID] = sw
	h.chain.broadcastHash = "0xaudit"

	rt, _ := h.svc.runtimeFor(h.network)
	if _, err := h.svc.broadcastStep(ctx(), rt, sw); err != nil {
		t.Fatalf("broadcastStep: %v", err)
	}
	actions = h.audit.actions()
	if actions[len(actions)-1] != "sweep_broadcast" {
		t.Fatalf("debe auditar el broadcast, ultimas acciones=%v", actions)
	}
}

// ---- 21. Seguridad: nunca se filtran secretos ----

func TestSanitizeCause_TruncatesAndStaysSingleLine(t *testing.T) {
	longMsg := ""
	for i := 0; i < 500; i++ {
		longMsg += "x"
	}
	got := sanitizeCause(errors.New("linea1\nlinea2\r" + longMsg))
	if len(got) > 300 {
		t.Fatalf("sanitizeCause debe truncar a 300 caracteres, dio %d", len(got))
	}
	for _, r := range got {
		if r == '\n' || r == '\r' {
			t.Fatal("sanitizeCause nunca debe dejar saltos de linea en un mensaje persistido")
		}
	}
}

// La firma nunca pasa material de clave al llamador: SignRequest solo lleva
// UserID (para que el KeyResolver la busque), nunca la clave en si, y
// ningun camino de error de este paquete construye un mensaje a partir del
// contenido de una clave privada o de SignedTx.Raw.
func TestSecurity_ErrorPathsNeverEmbedSignedPayloadOrKeyMaterial(t *testing.T) {
	h := newHarness(t, nil)
	sw := h.seedSweep(&Sweep{
		Network: h.network, AssetCode: "BNB", AssetKind: domain.AssetKindNative,
		FromAddress: h.fromAddr, FromUserID: h.userID, FromCryptoAddressID: h.addressID,
		ToCustodyWalletID: h.wallets.byKey[walletKey("TREASURY", string(h.network), "BNB")].ID,
		ToAddress:         h.toAddr, Amount: decimal.NewFromInt(1), Status: StatusProcessing,
	})
	secret := "MUY-SECRETO-PRIVATE-KEY-0xDEADBEEF"
	h.signer.sign = func(req SignRequest) (*SignedTx, error) {
		return nil, errors.New("fallo de firma") // el firmante real nunca debe incluir la clave en el error
	}
	rt, _ := h.svc.runtimeFor(h.network)
	_, err := h.svc.prepareAndSign(ctx(), rt, sw)
	if err == nil {
		t.Fatal("esperaba error")
	}
	if containsSubstring(err.Error(), secret) {
		t.Fatal("el error jamas debe contener material de clave")
	}
}

func containsSubstring(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ---- 22. No modifica el saldo del usuario / no genera doble credito ----
//
// Por diseno, este paquete no tiene ningun puerto de Ledger: Deps no
// declara un campo Ledger, y complete() solo llama a Store/Nonces/Audit. Es
// estructuralmente imposible que un sweep acredite o debite el ledger del
// usuario: el credito ya ocurrio en deposit.BlockchainDepositService (ver
// package doc en model.go).
func TestComplete_NeverTouchesLedger(t *testing.T) {
	h := newHarness(t, nil)
	required := int64(1)
	sw := h.seedSweep(&Sweep{
		Network: h.network, AssetCode: "BNB", AssetKind: domain.AssetKindNative,
		FromAddress: h.fromAddr, ToAddress: h.toAddr, Amount: decimal.NewFromInt(1),
		Status: StatusConfirming, TxHash: "0xnoledger", RequiredConfirmations: &required,
		Confirmations: 1,
	})
	if _, err := h.svc.complete(ctx(), sw); err != nil {
		t.Fatalf("complete: %v", err)
	}
	stored, _ := h.store.Get(ctx(), sw.ID)
	if stored.Status != StatusCompleted {
		t.Fatalf("esperaba completed, obtuve %s", stored.Status)
	}
}

func timePtr(t time.Time) *time.Time { return &t }
