package blockchainwithdrawal

import (
	"errors"
	"fmt"
	"log"
	"strings"

	"context"

	"github.com/google/uuid"

	appfee "korzadi-wallet/backend/internal/application/fee"
)

// Resolución manual de retiros en needs_review (status=failed + needs_review).
// Un retiro llega ahí cuando una transacción pudo salir a la red y el estado
// real no pudo determinarse solo (settlement_failed, tx_lost, nonce_stuck): sus
// fondos quedan retenidos hasta que un operador decida. Las dos acciones
// comprueban la cadena ANTES de tocar el ledger, porque equivocarse en
// cualquiera de las dos pierde dinero (liquidar sin pago, o liberar un pago
// que sí salió).
var (
	ErrNotInReview         = errors.New("withdrawals: el retiro no está pendiente de revisión manual")
	ErrReviewReasonInvalid = errors.New("withdrawals: el motivo es obligatorio (mínimo 10 caracteres)")
	ErrReviewActionInvalid = errors.New("withdrawals: acción de revisión no válida para este retiro")
	ErrReviewTxNotFinal    = errors.New("withdrawals: la transacción no está confirmada con éxito en la red")
	ErrReviewTxOnChain     = errors.New("withdrawals: la transacción salió a la red con éxito; use retry_settlement, no release")
	ErrReviewTxMaybeLive   = errors.New("withdrawals: la transacción aún podría minarse; espere o verifique en el explorador")
)

// ReviewAction es lo que el operador decide.
type ReviewAction string

const (
	// ReviewRetrySettlement: la tx salió y está confirmada; reintenta la
	// liquidación en el ledger y marca el retiro completed.
	ReviewRetrySettlement ReviewAction = "retry_settlement"
	// ReviewRelease: la tx NO salió (nunca existió, revertió o su nonce fue
	// consumido por otra); libera las reservas del usuario.
	ReviewRelease ReviewAction = "release"
)

const minReviewReason = 10

// ReviewLister lo implementa el Store de PostgreSQL (lectura para el panel).
type ReviewLister interface {
	ListNeedsReview(ctx context.Context, limit, offset int) ([]*Withdrawal, error)
}

// ResolveInput identifica la decisión del operador.
type ResolveInput struct {
	ID     uuid.UUID
	Action ReviewAction
	Reason string
	Actor  string // id del super_admin, para el evento de auditoría del retiro
}

// ListNeedsReview devuelve los retiros pendientes de revisión (más recientes primero).
func (s *Service) ListNeedsReview(ctx context.Context, limit, offset int) ([]*Withdrawal, error) {
	lister, ok := s.Store.(ReviewLister)
	if !ok {
		return nil, errors.New("withdrawals: el Store no soporta listar revisiones")
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	return lister.ListNeedsReview(ctx, limit, offset)
}

// ResolveNeedsReview aplica la decisión del operador. Es idempotente y segura
// ante concurrencia: todo cambio de estado es compare-and-set sobre
// (failed + needs_review), así que dos administradores no pueden resolver dos
// veces el mismo retiro.
func (s *Service) ResolveNeedsReview(ctx context.Context, in ResolveInput) (*Withdrawal, error) {
	reason := strings.TrimSpace(in.Reason)
	if len([]rune(reason)) < minReviewReason {
		return nil, ErrReviewReasonInvalid
	}
	if len(reason) > 500 {
		reason = reason[:500]
	}
	w, err := s.Store.Get(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if w == nil {
		return nil, ErrNotFound
	}
	if w.Status != StatusFailed || !w.NeedsReview {
		return nil, ErrNotInReview
	}
	switch in.Action {
	case ReviewRetrySettlement:
		return s.reviewRetrySettlement(ctx, w, reason, in.Actor)
	case ReviewRelease:
		return s.reviewRelease(ctx, w, reason, in.Actor)
	default:
		return nil, ErrReviewActionInvalid
	}
}

func (s *Service) reviewRetrySettlement(ctx context.Context, w *Withdrawal, reason, actor string) (*Withdrawal, error) {
	if w.TxHash == "" {
		return nil, fmt.Errorf("%w: el retiro nunca se transmitió", ErrReviewActionInvalid)
	}
	rt, ok := s.Networks[w.Network]
	if !ok || !rt.configured() {
		return nil, fmt.Errorf("%w: %s", ErrNetworkNotConfigured, w.Network)
	}
	chain := rt.observer()
	receipt, err := chain.Receipt(ctx, w.TxHash)
	if err != nil {
		return nil, fmt.Errorf("consultar receipt: %w", err)
	}
	required := uint64(1)
	if w.RequiredConfirmations != nil && *w.RequiredConfirmations > 0 {
		required = uint64(*w.RequiredConfirmations)
	} else if c := chain.ConfirmationsRequired(); c > 0 {
		required = c
	}
	if receipt == nil || !receipt.Status || receipt.Confirmations < required {
		return nil, ErrReviewTxNotFinal
	}

	ledgerTx, err := s.settle(ctx, w)
	if err != nil {
		// Sigue en needs_review: el operador verá la causa real.
		return nil, fmt.Errorf("liquidar en el ledger: %s", sanitizeCause(err))
	}
	native, usdt := s.actualFees(ctx, w, receipt)
	noDetail := ""
	noReview := false
	applied, err := s.Store.Apply(ctx, w.ID, StatusFailed, StatusCompleted,
		Update{LedgerTransactionID: &ledgerTx.ID, ActualNetworkFeeNative: native, ActualNetworkFeeUSDT: usdt,
			ErrorDetail: &noDetail, NeedsReview: &noReview, MarkCompleted: true},
		Event{Type: "review_resolved", Detail: map[string]any{
			"action": string(ReviewRetrySettlement), "reason": reason, "actor": actor,
			"ledger_transaction_id": ledgerTx.ID.String(), "tx_hash": w.TxHash,
		}})
	if err != nil {
		return nil, err
	}
	if !applied {
		return nil, ErrStaleState
	}
	s.finishLimit(ctx, w, true)
	s.recordDestinationUse(ctx, w)
	if s.nonceTracked(w.Network) {
		if err := s.Nonces.MarkConfirmed(ctx, w.Network, w.FromAddress, nonceOwner(w.ID)); err != nil {
			log.Printf("withdrawal: revisión: no se pudo marcar el nonce confirmado id=%s: %v", w.ID, err)
		}
	}
	if w.FeeQuoteID != nil {
		if err := s.Quotes.UpdateStatus(ctx, *w.FeeQuoteID, appfee.QuoteStatusConsumed); err != nil {
			log.Printf("withdrawal: revisión: no se pudo consumir la cotización id=%s: %v", w.ID, err)
		}
	}
	log.Printf("withdrawal: REVISIÓN resuelta (retry_settlement) id=%s actor=%s", w.ID, actor)
	return s.refresh(ctx, w)
}

func (s *Service) reviewRelease(ctx context.Context, w *Withdrawal, reason, actor string) (*Withdrawal, error) {
	neverSent := w.TxHash == ""
	if !neverSent {
		rt, ok := s.Networks[w.Network]
		if !ok || !rt.configured() {
			return nil, fmt.Errorf("%w: %s", ErrNetworkNotConfigured, w.Network)
		}
		chain := rt.observer()
		receipt, err := chain.Receipt(ctx, w.TxHash)
		if err != nil {
			return nil, fmt.Errorf("consultar receipt: %w", err)
		}
		switch {
		case receipt != nil && receipt.Status:
			return nil, ErrReviewTxOnChain // el dinero salió: liberar sería regalarlo
		case receipt != nil: // revertida: el pago no ocurrió
		default:
			known, kerr := chain.TxKnown(ctx, w.TxHash)
			if kerr != nil {
				return nil, fmt.Errorf("consultar tx en la red: %w", kerr)
			}
			if known {
				return nil, ErrReviewTxMaybeLive
			}
		}
	}

	if err := s.releaseReservations(ctx, w); err != nil {
		return nil, fmt.Errorf("liberar reservas: %w", err)
	}
	if neverSent {
		s.releaseNonces(ctx, w)
	}
	detail := "released_by_operator: " + strings.ReplaceAll(reason, "\n", " ")
	noReview := false
	applied, err := s.Store.Apply(ctx, w.ID, StatusFailed, StatusFailed,
		Update{ErrorDetail: &detail, NeedsReview: &noReview},
		Event{Type: "review_resolved", Detail: map[string]any{
			"action": string(ReviewRelease), "reason": reason, "actor": actor, "tx_hash": w.TxHash, "released": true,
		}})
	if err != nil {
		return nil, err
	}
	if !applied {
		return nil, ErrStaleState
	}
	s.finishLimit(ctx, w, false)
	s.cancelQuote(ctx, w)
	log.Printf("withdrawal: REVISIÓN resuelta (release) id=%s actor=%s", w.ID, actor)
	return s.refresh(ctx, w)
}
