package blockchainwithdrawal

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
)

// toReview deja un retiro recién creado como lo dejaría failNeedsReview.
func (h *harness) toReview(t *testing.T, txHash string) uuid.UUID {
	t.Helper()
	userAcc, _ := h.accounts.FindOrCreateUserAccount(context.Background(), h.userID, "ETH")
	h.ledger.fund(userAcc, decimal.NewFromInt(5))
	r, err := h.svc.Create(context.Background(), h.createInput("1"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	w, _ := h.store.Get(context.Background(), r.Withdrawal.ID)
	review := true
	detail := "tx_lost: prueba"
	u := Update{NeedsReview: &review, ErrorDetail: &detail}
	if txHash != "" {
		u.TxHash = &txHash
	}
	if _, err := h.store.Apply(context.Background(), w.ID, w.Status, StatusFailed, u, Event{}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	return w.ID
}

func TestReview_ReasonRequired(t *testing.T) {
	h := newHarness(t)
	id := h.toReview(t, "")
	_, err := h.svc.ResolveNeedsReview(context.Background(), ResolveInput{ID: id, Action: ReviewRelease, Reason: "corto", Actor: "a"})
	if !errors.Is(err, ErrReviewReasonInvalid) {
		t.Fatalf("esperaba ErrReviewReasonInvalid, obtuve %v", err)
	}
}

func TestReview_NotInReview(t *testing.T) {
	h := newHarness(t)
	userAcc, _ := h.accounts.FindOrCreateUserAccount(context.Background(), h.userID, "ETH")
	h.ledger.fund(userAcc, decimal.NewFromInt(5))
	r, _ := h.svc.Create(context.Background(), h.createInput("1"))
	_, err := h.svc.ResolveNeedsReview(context.Background(), ResolveInput{ID: r.Withdrawal.ID, Action: ReviewRelease, Reason: "motivo suficiente", Actor: "a"})
	if !errors.Is(err, ErrNotInReview) {
		t.Fatalf("esperaba ErrNotInReview, obtuve %v", err)
	}
}

func TestReview_Release_NeverSent_ReleasesAndClearsFlag(t *testing.T) {
	h := newHarness(t)
	id := h.toReview(t, "")
	w, err := h.svc.ResolveNeedsReview(context.Background(), ResolveInput{ID: id, Action: ReviewRelease, Reason: "nunca se transmitió, verificado", Actor: "admin"})
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if w.NeedsReview || w.Status != StatusFailed || !strings.HasPrefix(w.ErrorDetail, "released_by_operator") {
		t.Fatalf("estado inesperado: status=%s review=%v detail=%q", w.Status, w.NeedsReview, w.ErrorDetail)
	}
}

// Si la tx salió con éxito, liberar sería regalar el dinero: debe rechazarse.
func TestReview_Release_RefusedWhenTxSucceededOnChain(t *testing.T) {
	h := newHarness(t)
	id := h.toReview(t, "0xdone")
	h.chain.receipts["0xdone"] = &domain.TransactionReceipt{TxHash: "0xdone", Status: true, Confirmations: 10}
	_, err := h.svc.ResolveNeedsReview(context.Background(), ResolveInput{ID: id, Action: ReviewRelease, Reason: "motivo suficiente", Actor: "a"})
	if !errors.Is(err, ErrReviewTxOnChain) {
		t.Fatalf("esperaba ErrReviewTxOnChain, obtuve %v", err)
	}
	w, _ := h.store.Get(context.Background(), id)
	if !w.NeedsReview {
		t.Fatal("el retiro debe seguir en revisión")
	}
}

func TestReview_Release_RefusedWhenTxStillInMempool(t *testing.T) {
	h := newHarness(t)
	id := h.toReview(t, "0xpending")
	h.chain.knownHashes["0xpending"] = true
	_, err := h.svc.ResolveNeedsReview(context.Background(), ResolveInput{ID: id, Action: ReviewRelease, Reason: "motivo suficiente", Actor: "a"})
	if !errors.Is(err, ErrReviewTxMaybeLive) {
		t.Fatalf("esperaba ErrReviewTxMaybeLive, obtuve %v", err)
	}
}

func TestReview_RetrySettlement_RefusedWithoutConfirmedTx(t *testing.T) {
	h := newHarness(t)
	id := h.toReview(t, "0xnone")
	_, err := h.svc.ResolveNeedsReview(context.Background(), ResolveInput{ID: id, Action: ReviewRetrySettlement, Reason: "motivo suficiente", Actor: "a"})
	if !errors.Is(err, ErrReviewTxNotFinal) {
		t.Fatalf("esperaba ErrReviewTxNotFinal, obtuve %v", err)
	}
}

func TestReview_RetrySettlement_RefusedWhenNeverSent(t *testing.T) {
	h := newHarness(t)
	id := h.toReview(t, "")
	_, err := h.svc.ResolveNeedsReview(context.Background(), ResolveInput{ID: id, Action: ReviewRetrySettlement, Reason: "motivo suficiente", Actor: "a"})
	if !errors.Is(err, ErrReviewActionInvalid) {
		t.Fatalf("esperaba ErrReviewActionInvalid, obtuve %v", err)
	}
}

func TestReview_InvalidAction(t *testing.T) {
	h := newHarness(t)
	id := h.toReview(t, "")
	_, err := h.svc.ResolveNeedsReview(context.Background(), ResolveInput{ID: id, Action: "borrar", Reason: "motivo suficiente", Actor: "a"})
	if !errors.Is(err, ErrReviewActionInvalid) {
		t.Fatalf("esperaba ErrReviewActionInvalid, obtuve %v", err)
	}
}
