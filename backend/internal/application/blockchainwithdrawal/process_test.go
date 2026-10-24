package blockchainwithdrawal

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
)

func TestFullLifecycle_NativeWithdrawal_CompletesAndSettles(t *testing.T) {
	h := newHarness(t)
	userAcc, _ := h.accounts.FindOrCreateUserAccount(context.Background(), h.userID, "ETH")
	h.ledger.fund(userAcc, decimal.NewFromInt(5))

	h.chain.broadcastHash = "0xaaaa"
	h.signer.sign = func(req SignRequest) (*SignedTx, error) {
		return &SignedTx{Raw: []byte{0x01, 0x02}, TxHash: h.chain.broadcastHash, Nonce: req.Nonce}, nil
	}

	result, err := h.svc.Create(context.Background(), h.createInput("1"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := result.Withdrawal.ID

	// reserved -> processing -> signed -> broadcasted
	w, err := h.store.Get(context.Background(), id)
	if err != nil || w.Status != StatusReserved {
		t.Fatalf("precondición: esperaba reserved, obtuve %+v err=%v", w, err)
	}
	if _, err := h.svc.Advance(context.Background(), w); err != nil {
		t.Fatalf("advance (reserved): %v", err)
	}
	w, _ = h.store.Get(context.Background(), id)
	if w.Status != StatusBroadcasted {
		t.Fatalf("esperaba broadcasted tras firmar+transmitir, obtuve %s (err_detail=%s)", w.Status, w.ErrorDetail)
	}
	if w.TxHash != "0xaaaa" || len(w.SignedRawTx) == 0 {
		t.Fatalf("debe persistir tx_hash y la tx firmada: %+v", w)
	}

	// broadcasted -> confirming (mined, aún sin confirmaciones suficientes)
	h.chain.receipts[w.TxHash] = &domain.TransactionReceipt{
		TxHash: w.TxHash, BlockNumber: 100, BlockHash: "0xblock1", Status: true, Confirmations: 1,
		GasUsed: 21000, EffectiveGasPriceWei: bigFromInt(10),
	}
	if _, err := h.svc.Advance(context.Background(), w); err != nil {
		t.Fatalf("advance (broadcasted, 1 confirmación): %v", err)
	}
	w, _ = h.store.Get(context.Background(), id)
	if w.Status != StatusConfirming {
		t.Fatalf("esperaba confirming, obtuve %s", w.Status)
	}
	if w.Confirmations != 1 {
		t.Fatalf("debe registrar 1 confirmación, tiene %d", w.Confirmations)
	}

	// confirming -> completed (alcanza las 2 confirmaciones requeridas)
	h.chain.receipts[w.TxHash].Confirmations = 2
	if _, err := h.svc.Advance(context.Background(), w); err != nil {
		t.Fatalf("advance (confirming -> completed): %v", err)
	}
	w, _ = h.store.Get(context.Background(), id)
	if w.Status != StatusCompleted {
		t.Fatalf("esperaba completed, obtuve %s (%s)", w.Status, w.ErrorDetail)
	}
	if w.LedgerTransactionID == nil {
		t.Fatal("debe registrar la transacción de liquidación del ledger")
	}
	if !w.ActualNetworkFeeUSDT.Valid {
		t.Fatal("debe registrar el network fee real")
	}

	// El ledger quedó liquidado: reservas consumidas, exactamente una liquidación.
	if len(h.ledger.settled) != 1 {
		t.Fatalf("esperaba exactamente 1 liquidación, hubo %d", len(h.ledger.settled))
	}
	amtRes, err := h.ledger.FindReservation(context.Background(), *w.AmountReservationID)
	if err != nil || amtRes.Status != domain.ReservationConsumed {
		t.Fatalf("la reserva del monto debe quedar consumed: %+v err=%v", amtRes, err)
	}
}

func TestReorgDropsReceipt_NeverCompletesPrematurely(t *testing.T) {
	h := newHarness(t)
	userAcc, _ := h.accounts.FindOrCreateUserAccount(context.Background(), h.userID, "ETH")
	h.ledger.fund(userAcc, decimal.NewFromInt(5))
	h.chain.broadcastHash = "0xbbbb"
	h.signer.sign = func(req SignRequest) (*SignedTx, error) {
		return &SignedTx{Raw: []byte{0x01}, TxHash: h.chain.broadcastHash, Nonce: req.Nonce}, nil
	}

	result, err := h.svc.Create(context.Background(), h.createInput("1"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	w, _ := h.store.Get(context.Background(), result.Withdrawal.ID)
	if _, err := h.svc.Advance(context.Background(), w); err != nil {
		t.Fatalf("advance: %v", err)
	}
	w, _ = h.store.Get(context.Background(), result.Withdrawal.ID)

	// Se mina con 1 confirmación...
	h.chain.receipts[w.TxHash] = &domain.TransactionReceipt{TxHash: w.TxHash, BlockNumber: 50, BlockHash: "0xblockA", Status: true, Confirmations: 1}
	if _, err := h.svc.Advance(context.Background(), w); err != nil {
		t.Fatalf("advance: %v", err)
	}
	w, _ = h.store.Get(context.Background(), result.Withdrawal.ID)
	if w.Status != StatusConfirming {
		t.Fatalf("esperaba confirming, obtuve %s", w.Status)
	}

	// ...pero el bloque desaparece (reorg): el receipt ya no está.
	delete(h.chain.receipts, w.TxHash)
	h.chain.knownHashes[w.TxHash] = true // sigue en el mempool tras el reorg
	if _, err := h.svc.Advance(context.Background(), w); err != nil {
		t.Fatalf("advance tras reorg: %v", err)
	}
	w, _ = h.store.Get(context.Background(), result.Withdrawal.ID)
	if w.Status != StatusBroadcasted {
		t.Fatalf("un reorg debe volver a broadcasted (nunca completed), obtuve %s", w.Status)
	}
	if w.Confirmations != 0 {
		t.Fatalf("las confirmaciones deben resetear a 0 tras un reorg, tiene %d", w.Confirmations)
	}
}

func TestBroadcastRejected_ReleasesFunds(t *testing.T) {
	h := newHarness(t)
	userAcc, _ := h.accounts.FindOrCreateUserAccount(context.Background(), h.userID, "ETH")
	h.ledger.fund(userAcc, decimal.NewFromInt(5))
	h.chain.broadcastErr = &simpleErr{"intrinsic gas too low"}

	result, err := h.svc.Create(context.Background(), h.createInput("1"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	w, _ := h.store.Get(context.Background(), result.Withdrawal.ID)
	// Un rechazo DEFINITIVO de la red se resuelve por completo aquí mismo
	// (falla + libera fondos): Advance no debe devolver error, porque no
	// queda nada transitorio que el worker deba reintentar.
	if _, err := h.svc.Advance(context.Background(), w); err != nil {
		t.Fatalf("un rechazo definitivo ya manejado no debe devolver error transitorio: %v", err)
	}
	w, _ = h.store.Get(context.Background(), result.Withdrawal.ID)
	if w.Status != StatusFailed {
		t.Fatalf("un rechazo definitivo de la red debe fallar el retiro, está %s", w.Status)
	}
	amtRes, _ := h.ledger.FindReservation(context.Background(), *w.AmountReservationID)
	if amtRes.Status != domain.ReservationReleased {
		t.Fatalf("las reservas deben liberarse cuando el broadcast es rechazado (nunca se envió nada), está %s", amtRes.Status)
	}
}

func TestAmbiguousBroadcast_NeverSendsTwice(t *testing.T) {
	h := newHarness(t)
	userAcc, _ := h.accounts.FindOrCreateUserAccount(context.Background(), h.userID, "ETH")
	h.ledger.fund(userAcc, decimal.NewFromInt(5))
	h.chain.broadcastErr = &simpleErr{"context deadline exceeded"} // ambiguo: no sabemos si llegó a la red

	result, err := h.svc.Create(context.Background(), h.createInput("1"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	w, _ := h.store.Get(context.Background(), result.Withdrawal.ID)
	if _, err := h.svc.Advance(context.Background(), w); err == nil {
		t.Fatal("un broadcast ambiguo debe reportarse como error transitorio, no como éxito")
	}
	w, _ = h.store.Get(context.Background(), result.Withdrawal.ID)
	if w.Status != StatusProcessing {
		t.Fatalf("debe quedarse en processing (ni failed ni broadcasted) hasta reconciliar, está %s", w.Status)
	}
	if len(w.SignedRawTx) == 0 {
		t.Fatal("la tx firmada debe seguir persistida para poder reconciliar/reenviar la MISMA transacción")
	}

	// El siguiente tick descubre que la red SÍ la conoce (era ambigua, no perdida):
	// debe reconciliar a broadcasted sin volver a llamar Broadcast.
	h.chain.broadcastErr = &simpleErr{"no debería llamarse de nuevo"}
	h.chain.knownHashes[w.TxHash] = true
	if _, err := h.svc.Advance(context.Background(), w); err != nil {
		t.Fatalf("reconciliar: %v", err)
	}
	w, _ = h.store.Get(context.Background(), result.Withdrawal.ID)
	if w.Status != StatusBroadcasted {
		t.Fatalf("tras reconciliar, debe quedar broadcasted, está %s", w.Status)
	}
}

// Una liquidación con fallo TRANSITORIO (p. ej. un problema puntual de BD)
// debe reintentarse con backoff — nunca liberar fondos que ya salieron de
// la cadena, y nunca requerir revisión manual por algo que probablemente
// se resuelva solo en el siguiente intento. needs_review se reserva para
// un fallo que prueba un problema estructural (ver los sentinels de estado
// de reserva verificados en Service.complete), no para un error genérico.
func TestSettlementTransientFailureAfterBroadcast_RetriesFundsNotReleased(t *testing.T) {
	h := newHarness(t)
	userAcc, _ := h.accounts.FindOrCreateUserAccount(context.Background(), h.userID, "ETH")
	h.ledger.fund(userAcc, decimal.NewFromInt(5))
	h.chain.broadcastHash = "0xcccc"
	h.signer.sign = func(req SignRequest) (*SignedTx, error) {
		return &SignedTx{Raw: []byte{0x01}, TxHash: h.chain.broadcastHash, Nonce: req.Nonce}, nil
	}

	result, err := h.svc.Create(context.Background(), h.createInput("1"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	w, _ := h.store.Get(context.Background(), result.Withdrawal.ID)
	if _, err := h.svc.Advance(context.Background(), w); err != nil {
		t.Fatalf("advance (broadcast): %v", err)
	}
	w, _ = h.store.Get(context.Background(), result.Withdrawal.ID)
	h.chain.receipts[w.TxHash] = &domain.TransactionReceipt{TxHash: w.TxHash, BlockNumber: 10, BlockHash: "0xb", Status: true, Confirmations: 2}

	h.ledger.failSettle = errors.New("fallo simulado de base de datos")
	if _, err := h.svc.Advance(context.Background(), w); err == nil {
		t.Fatal("esperaba error transitorio al no poder liquidar")
	}
	amtRes, _ := h.ledger.FindReservation(context.Background(), *w.AmountReservationID)
	if amtRes.Status != domain.ReservationActive {
		t.Fatalf("las reservas NO deben liberarse cuando los fondos ya salieron on-chain, está %s", amtRes.Status)
	}
	wAfter, _ := h.store.Get(context.Background(), result.Withdrawal.ID)
	if wAfter.Status != StatusConfirming || wAfter.LedgerTransactionID != nil {
		t.Fatalf("no debe completarse ni fallar: debe reintentar en el siguiente tick, está %s", wAfter.Status)
	}

	// El siguiente tick, con el ledger sano de nuevo, sí debe completar.
	h.ledger.failSettle = nil
	if _, err := h.svc.Advance(context.Background(), wAfter); err != nil {
		t.Fatalf("advance tras recuperarse: %v", err)
	}
	final, _ := h.store.Get(context.Background(), result.Withdrawal.ID)
	if final.Status != StatusCompleted {
		t.Fatalf("debe completar en el reintento, está %s (%s)", final.Status, final.ErrorDetail)
	}
}

func bigFromInt(n int64) *big.Int { return big.NewInt(n) }
