package fee

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
)

// LedgerReservations is the narrow slice of application/ledger.Service
// this package needs — exactly its Reserve/Release/Consume/FindReservation
// methods (spec §19: "el motor debe tener una interfaz limpia para
// reserve/release/consume, siempre utilizando el sistema de Reservations
// existente"). No new reservation mechanism is introduced here.
type LedgerReservations interface {
	Reserve(ctx context.Context, walletAccountID uuid.UUID, assetCode string, amount decimal.Decimal, reason string, referenceID *string) (*domain.Reservation, error)
	Release(ctx context.Context, reservationID uuid.UUID) error
	Consume(ctx context.Context, reservationID uuid.UUID) error
	FindReservation(ctx context.Context, reservationID uuid.UUID) (*domain.Reservation, error)
}

// WalletAccountResolver resolves a user's wallet_account for a given
// asset — exactly domain.WalletRepository's shape, narrowed to what this
// package needs (find the wallet, then its USDT account) without pulling
// in the full interface (CreateWallet, ListAccounts, ...).
type WalletAccountResolver interface {
	FindWalletByUserID(ctx context.Context, userID uuid.UUID) (*domain.Wallet, error)
	GetOrCreateAccount(ctx context.Context, walletID uuid.UUID, assetCode string) (*domain.WalletAccount, error)
}

// ReservationService implements spec §19-22: reserve/release/consume the
// Total Fee (always in USDT — spec §20, even though ServiceFee/NetworkFee
// stay separately visible on the quote itself) against a specific
// FeeQuote, with idempotency keyed on the quote itself rather than a
// second, parallel key — spec §22 explicitly prefers reusing whatever
// mechanism best fits the existing Ledger over inventing a new one.
type ReservationService struct {
	ledger  LedgerReservations
	wallets WalletAccountResolver
	quotes  QuoteStore
}

func NewReservationService(ledger LedgerReservations, wallets WalletAccountResolver, quotes QuoteStore) *ReservationService {
	return &ReservationService{ledger: ledger, wallets: wallets, quotes: quotes}
}

// ReserveFee reserves quote.TotalFee in USDT against the quote's user.
//
// Idempotency (spec §22): the quote row itself is the idempotency
// boundary. SetReservationID is an atomic compare-and-set ("claim this
// reservation for this quote only if no reservation is claimed yet") —
// if two callers race to reserve the same quote, both create a
// Reservation (Reserve itself has no dedup key), but only one wins the
// claim; the loser immediately releases its now-redundant reservation and
// returns the winner's instead. Exactly one reservation ends up attached
// to the quote and left active, regardless of how many concurrent callers
// tried (spec §11 concurrency requirement, applied to fees instead of
// transfers).
func (s *ReservationService) ReserveFee(ctx context.Context, quoteID uuid.UUID) (*domain.Reservation, error) {
	quote, err := s.quotes.FindByID(ctx, quoteID)
	if err != nil {
		return nil, fmt.Errorf("buscar cotización: %w", err)
	}
	if quote == nil {
		return nil, ErrQuoteNotFound
	}

	if quote.ReservationID != nil {
		// Already reserved (a prior call, possibly this exact retry) —
		// return the existing reservation rather than creating a second
		// one. This is the fast, common idempotent-replay path.
		return s.ledger.FindReservation(ctx, *quote.ReservationID)
	}

	if quote.Status != QuoteStatusActive {
		return nil, ErrQuoteNotActive
	}
	if quote.IsExpired(time.Now().UTC()) {
		return nil, ErrQuoteExpired
	}

	wallet, err := s.wallets.FindWalletByUserID(ctx, quote.UserID)
	if err != nil {
		return nil, fmt.Errorf("wallet del usuario: %w", err)
	}
	if wallet == nil {
		return nil, fmt.Errorf("%w: usuario sin wallet", ErrReservationFailed)
	}
	account, err := s.wallets.GetOrCreateAccount(ctx, wallet.ID, quote.FeeAssetCode)
	if err != nil {
		return nil, fmt.Errorf("cuenta de %s: %w", quote.FeeAssetCode, err)
	}

	quoteIDStr := quoteID.String()
	reservation, err := s.ledger.Reserve(ctx, account.ID, quote.FeeAssetCode, quote.TotalFee,
		fmt.Sprintf("fee_quote:%s:%s", quote.OperationType, quoteID), &quoteIDStr)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrReservationFailed, err)
	}

	claimed, err := s.quotes.SetReservationID(ctx, quoteID, reservation.ID)
	if err != nil {
		return nil, fmt.Errorf("asociar reserva a la cotización: %w", err)
	}
	if !claimed {
		// Lost the race — someone else's SetReservationID landed first.
		// Our reservation is redundant: release it and hand back theirs.
		_ = s.ledger.Release(ctx, reservation.ID)
		winning, err := s.quotes.FindByID(ctx, quoteID)
		if err != nil || winning == nil || winning.ReservationID == nil {
			return nil, fmt.Errorf("%w: no se pudo resolver la reserva concurrente ganadora", ErrReservationFailed)
		}
		return s.ledger.FindReservation(ctx, *winning.ReservationID)
	}

	if err := s.quotes.UpdateStatus(ctx, quoteID, QuoteStatusReserved); err != nil {
		return nil, fmt.Errorf("actualizar estado de cotización: %w", err)
	}

	return reservation, nil
}

// ReleaseFee cancels a quote's reservation (e.g. the future withdrawal it
// was protecting funds for was abandoned) — funds return to the user's
// available USDT balance, and the quote is marked cancelled so it can't
// be reserved again.
func (s *ReservationService) ReleaseFee(ctx context.Context, quoteID uuid.UUID) error {
	quote, err := s.quotes.FindByID(ctx, quoteID)
	if err != nil {
		return fmt.Errorf("buscar cotización: %w", err)
	}
	if quote == nil {
		return ErrQuoteNotFound
	}
	if quote.ReservationID == nil {
		return fmt.Errorf("%w: la cotización no tiene una reserva activa", ErrReservationFailed)
	}
	if err := s.ledger.Release(ctx, *quote.ReservationID); err != nil {
		return fmt.Errorf("liberar reserva: %w", err)
	}
	return s.quotes.UpdateStatus(ctx, quoteID, QuoteStatusCancelled)
}

// ConsumeFee finalizes a quote's reservation — spec §38 step 6-8: the
// future withdrawal service calls this once the withdrawal itself
// completes, permanently taking the Total Fee out of "locked" (it never
// returns to available). This does NOT post the ServiceFee/NetworkFee
// split to the ledger as revenue/expense entries — spec §24 is explicit
// that final accounting for a withdrawal's fee belongs to the withdrawal
// block, not this one. ConsumeFee only resolves the hold; posting the
// actual USDT debit (and any revenue/expense bookkeeping) is Bloque 6B's
// job, using the same Ledger.ExecuteTransaction as everything else.
func (s *ReservationService) ConsumeFee(ctx context.Context, quoteID uuid.UUID) error {
	quote, err := s.quotes.FindByID(ctx, quoteID)
	if err != nil {
		return fmt.Errorf("buscar cotización: %w", err)
	}
	if quote == nil {
		return ErrQuoteNotFound
	}
	if quote.ReservationID == nil {
		return fmt.Errorf("%w: la cotización no tiene una reserva activa", ErrReservationFailed)
	}
	if err := s.ledger.Consume(ctx, *quote.ReservationID); err != nil {
		return fmt.Errorf("consumir reserva: %w", err)
	}
	return s.quotes.UpdateStatus(ctx, quoteID, QuoteStatusConsumed)
}
