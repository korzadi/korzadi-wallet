package p2p

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
	"korzadi-wallet/backend/internal/domain"
	p2pd "korzadi-wallet/backend/internal/domain/p2p"
	"korzadi-wallet/backend/internal/infrastructure/postgres"
	p2prepo "korzadi-wallet/backend/internal/infrastructure/postgres/p2p"
)

var (
	ErrInsufficientBalance = errors.New("saldo insuficiente")
	ErrOfferNotAvailable   = errors.New("oferta no disponible")
	ErrInvalidAmount       = errors.New("cantidad inválida")
	ErrInvalidTransition   = errors.New("transición no permitida")
	ErrNotParticipant      = errors.New("no autorizado para esta orden")
	ErrDisputed            = errors.New("orden en disputa")
	ErrExpired             = errors.New("orden expirada")
	ErrCommissionCalc      = errors.New("error calculando comisión")
)

type Service struct {
	repo           *p2prepo.Repo
	ledger         *postgres.LedgerRepository
	resv           *postgres.ReservationRepository
	accounts       *postgres.UserWalletAccountRepository
	wallets        *postgres.WalletRepository
	notifier       domain.Notifier
	commissionRate decimal.Decimal // basis points, e.g., 100 = 1%
}

func New(repo *p2prepo.Repo, ledger *postgres.LedgerRepository, resv *postgres.ReservationRepository, accounts *postgres.UserWalletAccountRepository, wallets *postgres.WalletRepository, notifier domain.Notifier, commissionRate decimal.Decimal) *Service {
	if notifier == nil {
		notifier = &noopNotifier{}
	}
	if commissionRate.IsZero() {
		commissionRate = decimal.NewFromInt(100) // 1% default
	}
	return &Service{repo: repo, ledger: ledger, resv: resv, accounts: accounts, wallets: wallets, notifier: notifier, commissionRate: commissionRate}
}

type noopNotifier struct{}

func (n *noopNotifier) Notify(ctx context.Context, userID uuid.UUID, eventType, title, body string) error {
	return nil
}

func (s *Service) CreateOffer(ctx context.Context, userID uuid.UUID, input CreateOfferInput) (*p2pd.Offer, error) {
	if input.MinAmount.LessThanOrEqual(decimal.Zero) || input.MaxAmount.LessThan(input.MinAmount) || input.TotalAmount.LessThan(input.MinAmount) {
		return nil, ErrInvalidAmount
	}
	// Get wallet account for asset
	wallet, err := s.wallets.FindWalletByUserID(ctx, userID)
	if err != nil || wallet == nil {
		return nil, fmt.Errorf("wallet no encontrado")
	}
	accountID, err := s.accounts.FindOrCreateUserAccount(ctx, userID, input.AssetCode)
	if err != nil {
		return nil, err
	}
	// Check available balance for sell offers
	if input.Side == p2pd.SideSell {
		bal, err := s.resv.GetBalance(ctx, accountID)
		if err != nil {
			return nil, err
		}
		if bal.Available.LessThan(input.TotalAmount) {
			return nil, ErrInsufficientBalance
		}
	}
	offer := &p2pd.Offer{
		ID: uuid.New(), UserID: userID, WalletAccountID: accountID,
		AssetCode: input.AssetCode, FiatCurrency: input.FiatCurrency,
		PricePerUnit: input.PricePerUnit, MinAmount: input.MinAmount, MaxAmount: input.MaxAmount,
		TotalAmount: input.TotalAmount, AvailableAmount: input.TotalAmount, ReservedAmount: decimal.Zero,
		Side: input.Side, PaymentMethods: input.PaymentMethods, CountryRestrictions: input.CountryRestrictions,
		PaymentTimeLimitMins: input.PaymentTimeLimitMins, Instructions: input.Instructions, Terms: input.Terms,
		Status: p2pd.OfferActive, Version: 1,
	}
	err = s.repo.WithTx(ctx, func(tx pgx.Tx) error {
		return s.repo.CreateOffer(ctx, tx, offer)
	})
	return offer, err
}

type CreateOfferInput struct {
	AssetCode            string
	FiatCurrency         string
	PricePerUnit         decimal.Decimal
	MinAmount            decimal.Decimal
	MaxAmount            decimal.Decimal
	TotalAmount          decimal.Decimal
	Side                 p2pd.OfferSide
	PaymentMethods       []string
	CountryRestrictions  []string
	PaymentTimeLimitMins int
	Instructions         string
	Terms                string
}

func (s *Service) ListOffers(ctx context.Context, asset, fiat, side string, limit, offset int) ([]*p2pd.Offer, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	return s.repo.ListOffers(ctx, asset, fiat, side, limit, offset)
}

func (s *Service) MyOffers(ctx context.Context, userID uuid.UUID) ([]*p2pd.Offer, error) {
	return s.repo.ListMyOffers(ctx, userID)
}

func (s *Service) UpdateOfferStatus(ctx context.Context, userID uuid.UUID, offerID uuid.UUID, status p2pd.OfferStatus) error {
	return s.repo.WithTx(ctx, func(tx pgx.Tx) error {
		o, err := s.repo.GetOfferForUpdate(ctx, tx, offerID)
		if err != nil || o == nil {
			return ErrOfferNotAvailable
		}
		if o.UserID != userID {
			return ErrNotParticipant
		}
		if o.Status == p2pd.OfferCancelled {
			return ErrInvalidTransition
		}
		o.Status = status
		return s.repo.UpdateOffer(ctx, tx, o)
	})
}

func (s *Service) CreateOrder(ctx context.Context, buyerID uuid.UUID, input CreateOrderInput) (*p2pd.Order, error) {
	if input.Amount.LessThanOrEqual(decimal.Zero) {
		return nil, ErrInvalidAmount
	}
	// Check for existing order with same idempotency key
	var existing *p2pd.Order
	err := s.repo.WithTx(ctx, func(tx pgx.Tx) error {
		ex, _ := s.repo.GetOrderByIdem(ctx, tx, input.IdempotencyKey)
		existing = ex
		return nil
	})
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}

	var order *p2pd.Order
	err = s.repo.WithTx(ctx, func(tx pgx.Tx) error {
		o, err := s.repo.GetOfferForUpdate(ctx, tx, input.OfferID)
		if err != nil || o == nil {
			return ErrOfferNotAvailable
		}
		if o.Status != p2pd.OfferActive {
			return ErrOfferNotAvailable
		}
		if input.Amount.LessThan(o.MinAmount) || input.Amount.GreaterThan(o.MaxAmount) {
			return ErrInvalidAmount
		}
		if o.AvailableAmount.LessThan(input.Amount) {
			return ErrOfferNotAvailable
		}
		if o.UserID == buyerID {
			return errors.New("no puedes operar con tu propia oferta")
		}
		// Reserve offer
		if err := s.repo.ReserveOffer(ctx, tx, o.ID, input.Amount); err != nil {
			return err
		}
		// Create escrow reservation for seller
		orderID := uuid.New()
		oidStr := orderID.String()
		resv, err := s.resv.Reserve(ctx, o.WalletAccountID, o.AssetCode, input.Amount, "p2p_escrow", &oidStr)
		if err != nil {
			return err
		}
		fiatAmount := input.Amount.Mul(o.PricePerUnit)
		// Calculate commission (basis points)
		commissionRate := s.commissionRate
		commissionAmount := input.Amount.Mul(commissionRate).Div(decimal.NewFromInt(10000))
		commissionFiat := fiatAmount.Mul(commissionRate).Div(decimal.NewFromInt(10000))
		now := time.Now()
		ord := &p2pd.Order{
			ID: orderID, OfferID: o.ID, BuyerID: buyerID, SellerID: o.UserID,
			AssetCode: o.AssetCode, FiatCurrency: o.FiatCurrency,
			Amount: input.Amount, PricePerUnit: o.PricePerUnit, FiatAmount: fiatAmount,
			CommissionRate: commissionRate, CommissionAmount: commissionAmount, CommissionFiat: commissionFiat,
			PaymentMethod: input.PaymentMethod, PaymentInstructions: o.Instructions,
			Status: p2pd.OrderEscrowLocked, IdempotencyKey: input.IdempotencyKey,
			EscrowReservationID: &resv.ID,
			ExpiresAt:           now.Add(time.Duration(o.PaymentTimeLimitMins) * time.Minute),
		}
		if err := s.repo.CreateOrder(ctx, tx, ord); err != nil {
			return err
		}
		ev := &p2pd.OrderEvent{ID: uuid.New(), OrderID: orderID, ActorID: &buyerID, EventType: "order_created", FromStatus: nil, ToStatus: &[]p2pd.OrderStatus{p2pd.OrderEscrowLocked}[0]}
		_ = s.repo.AddEvent(ctx, tx, ev)
		order = ord
		return nil
	})
	// Notify seller of new order
	if order != nil {
		go s.notifier.Notify(context.Background(), order.SellerID, "p2p_order_created", "Nueva orden P2P", fmt.Sprintf("Tienes una nueva orden de venta: %s %s", order.Amount.String(), order.AssetCode))
		go s.notifier.Notify(context.Background(), order.BuyerID, "p2p_order_created", "Orden P2P creada", fmt.Sprintf("Tu orden de compra de %s %s está en escrow", order.Amount.String(), order.AssetCode))
	}
	return order, err
}

type CreateOrderInput struct {
	OfferID        uuid.UUID
	Amount         decimal.Decimal
	PaymentMethod  string
	IdempotencyKey string
}

func (s *Service) MyOrders(ctx context.Context, userID uuid.UUID) ([]*p2pd.Order, error) {
	return s.repo.ListMyOrders(ctx, userID)
}

func (s *Service) GetOrder(ctx context.Context, userID uuid.UUID, orderID uuid.UUID) (*p2pd.Order, error) {
	var ord *p2pd.Order
	err := s.repo.WithTx(ctx, func(tx pgx.Tx) error {
		o, err := s.repo.GetOrderForUpdate(ctx, tx, orderID)
		if err != nil {
			return err
		}
		if o == nil {
			return fmt.Errorf("orden no encontrada")
		}
		if o.BuyerID != userID && o.SellerID != userID {
			return ErrNotParticipant
		}
		ord = o
		return nil
	})
	return ord, err
}

func (s *Service) MarkPaid(ctx context.Context, buyerID uuid.UUID, orderID uuid.UUID) error {
	return s.repo.WithTx(ctx, func(tx pgx.Tx) error {
		o, err := s.repo.GetOrderForUpdate(ctx, tx, orderID)
		if err != nil || o == nil {
			return fmt.Errorf("orden no encontrada")
		}
		if o.BuyerID != buyerID {
			return ErrNotParticipant
		}
		if o.Status != p2pd.OrderEscrowLocked {
			return ErrInvalidTransition
		}
		if time.Now().After(o.ExpiresAt) {
			return ErrExpired
		}
		from := o.Status
		to := p2pd.OrderPaymentMarked
		o.Status = to
		o.PaidAt = &[]time.Time{time.Now()}[0]
		if err := s.repo.UpdateOrderStatus(ctx, tx, o, to); err != nil {
			return err
		}
		ev := &p2pd.OrderEvent{ID: uuid.New(), OrderID: orderID, ActorID: &buyerID, EventType: "payment_marked", FromStatus: &from, ToStatus: &to}
		// Notify seller
		go s.notifier.Notify(context.Background(), o.SellerID, "p2p_payment_marked", "Pago marcado", fmt.Sprintf("El comprador marcó el pago en la orden de %s %s", o.Amount.String(), o.AssetCode))
		return s.repo.AddEvent(ctx, tx, ev)
	})
}

func (s *Service) Release(ctx context.Context, sellerID uuid.UUID, orderID uuid.UUID) error {
	return s.repo.WithTx(ctx, func(tx pgx.Tx) error {
		o, err := s.repo.GetOrderForUpdate(ctx, tx, orderID)
		if err != nil || o == nil {
			return fmt.Errorf("orden no encontrada")
		}
		if o.SellerID != sellerID {
			return ErrNotParticipant
		}
		if o.Status != p2pd.OrderPaymentMarked {
			return ErrInvalidTransition
		}
		if time.Now().After(o.ExpiresAt) {
			return ErrExpired
		}
		// Check dispute
		d, _ := s.repo.GetDisputeByOrder(ctx, tx, orderID)
		if d != nil && d.Status == p2pd.DisputeOpen || d != nil && d.Status == p2pd.DisputeUnderReview {
			return ErrDisputed
		}
		from := o.Status
		to := p2pd.OrderReleased
		now := time.Now()
		o.Status = to
		o.ReleasedAt = &now
		if err := s.repo.UpdateOrderStatus(ctx, tx, o, to); err != nil {
			return err
		}
		// Get accounts
		sellerAccountID, err := s.accounts.FindOrCreateUserAccount(ctx, o.SellerID, o.AssetCode)
		if err != nil {
			return err
		}
		buyerAccountID, err := s.accounts.FindOrCreateUserAccount(ctx, o.BuyerID, o.AssetCode)
		if err != nil {
			return err
		}
		// System clearing account for commissions
		clearingWallet, err := s.wallets.FindWalletByUserID(ctx, uuid.Nil)
		var clearingAccountID uuid.UUID
		if clearingWallet != nil {
			clearingAccountID, _ = s.accounts.FindOrCreateUserAccount(ctx, uuid.Nil, o.AssetCode)
		}
		// Transfer from seller escrow: buyer gets full amount, seller gets amount - commission, commission to clearing
		idStr := orderID.String()
		idem := fmt.Sprintf("p2p_release:%s", idStr)
		entries := []domain.LedgerEntryInput{
			{WalletAccountID: sellerAccountID, AssetCode: o.AssetCode, Amount: o.Amount.Neg()},
			{WalletAccountID: buyerAccountID, AssetCode: o.AssetCode, Amount: o.Amount},
		}
		if !o.CommissionAmount.IsZero() && clearingAccountID != uuid.Nil {
			entries = append(entries, domain.LedgerEntryInput{WalletAccountID: clearingAccountID, AssetCode: o.AssetCode, Amount: o.CommissionAmount})
		}
		if _, err := s.ledger.ExecuteTransaction(ctx, domain.TxTypeTransfer, idem, &idStr, "P2P release", nil, entries); err != nil {
			return err
		}
		// Consume reservation
		if o.EscrowReservationID != nil {
			_ = s.resv.Consume(ctx, *o.EscrowReservationID)
		}
		_ = s.repo.ConsumeOfferReservation(ctx, tx, o.OfferID, o.Amount)
		ev := &p2pd.OrderEvent{ID: uuid.New(), OrderID: orderID, ActorID: &sellerID, EventType: "released", FromStatus: &from, ToStatus: &to}
		// Notify both parties
		go s.notifier.Notify(context.Background(), o.BuyerID, "p2p_released", "Orden completada", fmt.Sprintf("Recibiste %s %s", o.Amount.String(), o.AssetCode))
		go s.notifier.Notify(context.Background(), o.SellerID, "p2p_released", "Orden completada", fmt.Sprintf("Liberaste %s %s (comisión: %s)", o.Amount.String(), o.AssetCode, o.CommissionAmount.String()))
		return s.repo.AddEvent(ctx, tx, ev)
	})
}

func (s *Service) Cancel(ctx context.Context, userID uuid.UUID, orderID uuid.UUID) error {
	return s.repo.WithTx(ctx, func(tx pgx.Tx) error {
		o, err := s.repo.GetOrderForUpdate(ctx, tx, orderID)
		if err != nil || o == nil {
			return fmt.Errorf("orden no encontrada")
		}
		if o.BuyerID != userID && o.SellerID != userID {
			return ErrNotParticipant
		}
		if o.Status == p2pd.OrderReleased || o.Status == p2pd.OrderDisputed {
			return ErrInvalidTransition
		}
		if o.Status == p2pd.OrderPaymentMarked && o.SellerID != userID {
			return errors.New("solo el vendedor puede cancelar después de marcar pago")
		}
		from := o.Status
		to := p2pd.OrderCancelled
		now := time.Now()
		o.Status = to
		o.CancelledAt = &now
		if err := s.repo.UpdateOrderStatus(ctx, tx, o, to); err != nil {
			return err
		}
		// Release reservation back to seller
		if o.EscrowReservationID != nil {
			_ = s.resv.Release(ctx, *o.EscrowReservationID)
		}
		_ = s.repo.ReleaseOfferReservation(ctx, tx, o.OfferID, o.Amount)
		ev := &p2pd.OrderEvent{ID: uuid.New(), OrderID: orderID, ActorID: &userID, EventType: "cancelled", FromStatus: &from, ToStatus: &to}
		// Notify both parties
		go s.notifier.Notify(context.Background(), o.BuyerID, "p2p_cancelled", "Orden cancelada", fmt.Sprintf("La orden de %s %s fue cancelada", o.Amount.String(), o.AssetCode))
		go s.notifier.Notify(context.Background(), o.SellerID, "p2p_cancelled", "Orden cancelada", fmt.Sprintf("La orden de %s %s fue cancelada", o.Amount.String(), o.AssetCode))
		return s.repo.AddEvent(ctx, tx, ev)
	})
}

func (s *Service) OpenDispute(ctx context.Context, userID uuid.UUID, orderID uuid.UUID, reason, desc string) (*p2pd.Dispute, error) {
	var d *p2pd.Dispute
	err := s.repo.WithTx(ctx, func(tx pgx.Tx) error {
		o, err := s.repo.GetOrderForUpdate(ctx, tx, orderID)
		if err != nil || o == nil {
			return fmt.Errorf("orden no encontrada")
		}
		if o.BuyerID != userID && o.SellerID != userID {
			return ErrNotParticipant
		}
		if o.Status == p2pd.OrderReleased || o.Status == p2pd.OrderCancelled {
			return ErrInvalidTransition
		}
		ex, _ := s.repo.GetDisputeByOrder(ctx, tx, orderID)
		if ex != nil {
			d = ex
			return nil
		}
		from := o.Status
		to := p2pd.OrderDisputed
		o.Status = to
		if err := s.repo.UpdateOrderStatus(ctx, tx, o, to); err != nil {
			return err
		}
		disp := &p2pd.Dispute{ID: uuid.New(), OrderID: orderID, OpenedBy: userID, Reason: reason, Description: desc, Status: p2pd.DisputeOpen}
		if err := s.repo.CreateDispute(ctx, tx, disp); err != nil {
			return err
		}
		d = disp
		ev := &p2pd.OrderEvent{ID: uuid.New(), OrderID: orderID, ActorID: &userID, EventType: "dispute_opened", FromStatus: &from, ToStatus: &to}
		// Notify both parties
		go s.notifier.Notify(context.Background(), o.BuyerID, "p2p_dispute", "Disputa abierta", fmt.Sprintf("Se abrió una disputa en la orden de %s %s", o.Amount.String(), o.AssetCode))
		go s.notifier.Notify(context.Background(), o.SellerID, "p2p_dispute", "Disputa abierta", fmt.Sprintf("Se abrió una disputa en la orden de %s %s", o.Amount.String(), o.AssetCode))
		return s.repo.AddEvent(ctx, tx, ev)
	})
	return d, err
}

func (s *Service) Messages(ctx context.Context, userID uuid.UUID, orderID uuid.UUID) ([]*p2pd.Message, error) {
	var ord *p2pd.Order
	err := s.repo.WithTx(ctx, func(tx pgx.Tx) error {
		o, err := s.repo.GetOrderForUpdate(ctx, tx, orderID)
		if err != nil || o == nil {
			return fmt.Errorf("orden no encontrada")
		}
		if o.BuyerID != userID && o.SellerID != userID {
			return ErrNotParticipant
		}
		ord = o
		return nil
	})
	if err != nil {
		return nil, err
	}
	_ = ord
	return s.repo.ListMessages(ctx, orderID)
}

func (s *Service) SendMessage(ctx context.Context, userID uuid.UUID, orderID uuid.UUID, msg string) error {
	return s.repo.WithTx(ctx, func(tx pgx.Tx) error {
		o, err := s.repo.GetOrderForUpdate(ctx, tx, orderID)
		if err != nil || o == nil {
			return fmt.Errorf("orden no encontrada")
		}
		if o.BuyerID != userID && o.SellerID != userID {
			return ErrNotParticipant
		}
		m := &p2pd.Message{ID: uuid.New(), OrderID: orderID, SenderID: userID, Message: msg}
		return s.repo.AddMessage(ctx, tx, m)
	})
}

// GetOffer returns a single offer by id. Reads are not participant-scoped:
// the market listing exposes only active offers separately.
func (s *Service) GetOffer(ctx context.Context, id uuid.UUID) (*p2pd.Offer, error) {
	o, err := s.repo.GetOffer(ctx, id)
	if err != nil {
		return nil, err
	}
	if o == nil {
		return nil, ErrOfferNotAvailable
	}
	return o, nil
}

// AdminListOffers returns every offer, including paused/cancelled/closed.
func (s *Service) AdminListOffers(ctx context.Context, status, asset, fiat, side string, limit, offset int) ([]*p2pd.Offer, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	return s.repo.ListAllOffers(ctx, status, asset, fiat, side, limit, offset)
}

// AdminListOrders returns every order with optional status/asset filters.
func (s *Service) AdminListOrders(ctx context.Context, status, asset string, limit, offset int) ([]*p2pd.Order, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	return s.repo.ListAllOrders(ctx, status, asset, limit, offset)
}

// AdminGetOrder returns any order without participant checks.
func (s *Service) AdminGetOrder(ctx context.Context, orderID uuid.UUID) (*p2pd.Order, error) {
	o, err := s.repo.GetOrder(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if o == nil {
		return nil, fmt.Errorf("orden no encontrada")
	}
	return o, nil
}

// AdminGetDispute returns the dispute attached to an order (if any).
func (s *Service) AdminGetDispute(ctx context.Context, orderID uuid.UUID) (*p2pd.Dispute, error) {
	return s.repo.GetDispute(ctx, orderID)
}

// AdminSuspendOffer force-cancels an offer without the owner check used by
// the user-facing flow.
func (s *Service) AdminSuspendOffer(ctx context.Context, adminID uuid.UUID, offerID uuid.UUID) error {
	var ownerID uuid.UUID
	err := s.repo.WithTx(ctx, func(tx pgx.Tx) error {
		o, err := s.repo.GetOfferForUpdate(ctx, tx, offerID)
		if err != nil || o == nil {
			return ErrOfferNotAvailable
		}
		if o.Status == p2pd.OfferCancelled {
			return ErrInvalidTransition
		}
		ownerID = o.UserID
		if err := s.repo.SetOfferStatusAdmin(ctx, tx, offerID, p2pd.OfferCancelled); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	if ownerID != uuid.Nil {
		go s.notifier.Notify(context.Background(), ownerID, "p2p_offer_suspended", "Oferta suspendida", "Un administrador suspendió una de tus ofertas P2P.")
	}
	return nil
}

// AdminResolveDispute closes a disputed order. Action "release" delivers the
// escrowed asset to the buyer; "refund" returns it to the seller. Both paths
// mutate money and therefore run inside a single DB transaction with the
// order row locked and an idempotent ledger key to avoid double settlement.
func (s *Service) AdminResolveDispute(ctx context.Context, adminID uuid.UUID, orderID uuid.UUID, action, reason string) error {
	if action != "release" && action != "refund" {
		return fmt.Errorf("acción inválida: debe ser release o refund")
	}
	return s.repo.WithTx(ctx, func(tx pgx.Tx) error {
		o, err := s.repo.GetOrderForUpdate(ctx, tx, orderID)
		if err != nil || o == nil {
			return fmt.Errorf("orden no encontrada")
		}
		switch o.Status {
		case p2pd.OrderReleased, p2pd.OrderCancelled, p2pd.OrderExpired:
			return ErrInvalidTransition
		}
		d, err := s.repo.GetDisputeByOrder(ctx, tx, orderID)
		if err != nil {
			return err
		}
		if d == nil {
			return fmt.Errorf("la orden no tiene disputa")
		}
		from := o.Status
		now := time.Now()
		actor := adminID
		if action == "release" {
			to := p2pd.OrderReleased
			o.Status = to
			o.ReleasedAt = &now
			if err := s.repo.UpdateOrderStatus(ctx, tx, o, to); err != nil {
				return err
			}
			sellerAccountID, err := s.accounts.FindOrCreateUserAccount(ctx, o.SellerID, o.AssetCode)
			if err != nil {
				return err
			}
			buyerAccountID, err := s.accounts.FindOrCreateUserAccount(ctx, o.BuyerID, o.AssetCode)
			if err != nil {
				return err
			}
			clearingWallet, _ := s.wallets.FindWalletByUserID(ctx, uuid.Nil)
			var clearingAccountID uuid.UUID
			if clearingWallet != nil {
				clearingAccountID, _ = s.accounts.FindOrCreateUserAccount(ctx, uuid.Nil, o.AssetCode)
			}
			entries := []domain.LedgerEntryInput{
				{WalletAccountID: sellerAccountID, AssetCode: o.AssetCode, Amount: o.Amount.Neg()},
				{WalletAccountID: buyerAccountID, AssetCode: o.AssetCode, Amount: o.Amount},
			}
			// Only post the commission leg when a system clearing account
			// exists; the existing user-facing release follows the same
			// guard so both paths stay consistent.
			if !o.CommissionAmount.IsZero() && clearingAccountID != uuid.Nil {
				entries = append(entries, domain.LedgerEntryInput{WalletAccountID: clearingAccountID, AssetCode: o.AssetCode, Amount: o.CommissionAmount})
			}
			idStr := orderID.String()
			idem := fmt.Sprintf("p2p_admin_release:%s", idStr)
			if _, err := s.ledger.ExecuteTransaction(ctx, domain.TxTypeTransfer, idem, &idStr, "P2P disputa resuelta (liberación)", &actor, entries); err != nil {
				return err
			}
			if o.EscrowReservationID != nil {
				if err := s.resv.Consume(ctx, *o.EscrowReservationID); err != nil {
					return err
				}
			}
			_ = s.repo.ConsumeOfferReservation(ctx, tx, o.OfferID, o.Amount)
			d.Status = p2pd.DisputeResolvedRel
		} else {
			to := p2pd.OrderCancelled
			o.Status = to
			o.CancelledAt = &now
			if err := s.repo.UpdateOrderStatus(ctx, tx, o, to); err != nil {
				return err
			}
			if o.EscrowReservationID != nil {
				if err := s.resv.Release(ctx, *o.EscrowReservationID); err != nil {
					return err
				}
			}
			_ = s.repo.ReleaseOfferReservation(ctx, tx, o.OfferID, o.Amount)
			d.Status = p2pd.DisputeResolvedRef
		}
		d.ResolverID = &actor
		d.ResolutionReason = reason
		d.ResolvedAt = &now
		if err := s.repo.UpdateDispute(ctx, tx, d); err != nil {
			return err
		}
		to := o.Status
		ev := &p2pd.OrderEvent{ID: uuid.New(), OrderID: orderID, ActorID: &actor, EventType: "dispute_resolved_" + action, FromStatus: &from, ToStatus: &to}
		if err := s.repo.AddEvent(ctx, tx, ev); err != nil {
			return err
		}
		go s.notifier.Notify(context.Background(), o.BuyerID, "p2p_dispute_resolved", "Disputa resuelta", fmt.Sprintf("La disputa de la orden de %s %s fue resuelta: %s", o.Amount.String(), o.AssetCode, action))
		go s.notifier.Notify(context.Background(), o.SellerID, "p2p_dispute_resolved", "Disputa resuelta", fmt.Sprintf("La disputa de la orden de %s %s fue resuelta: %s", o.Amount.String(), o.AssetCode, action))
		return nil
	})
}

// ExpireOrders marks expired orders and releases escrow back to seller.
// Should be called periodically by a background worker.
func (s *Service) ExpireOrders(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = 100
	}
	expiredCount := 0
	err := s.repo.WithTx(ctx, func(tx pgx.Tx) error {
		// Find expired orders
		rows, err := tx.Query(ctx, `SELECT id, buyer_id, seller_id, amount, asset_code, escrow_reservation_id, offer_id FROM p2p_orders WHERE status IN ('created','escrow_locked','payment_marked') AND expires_at < now() LIMIT $1 FOR UPDATE SKIP LOCKED`, limit)
		if err != nil {
			return err
		}
		defer rows.Close()

		type expiredOrder struct {
			ID                  uuid.UUID
			BuyerID             uuid.UUID
			SellerID            uuid.UUID
			Amount              decimal.Decimal
			AssetCode           string
			EscrowReservationID *uuid.UUID
			OfferID             uuid.UUID
		}
		var orders []expiredOrder
		for rows.Next() {
			var o expiredOrder
			if err := rows.Scan(&o.ID, &o.BuyerID, &o.SellerID, &o.Amount, &o.AssetCode, &o.EscrowReservationID, &o.OfferID); err != nil {
				return err
			}
			orders = append(orders, o)
		}

		for _, o := range orders {
			// Update order status
			_, err := tx.Exec(ctx, `UPDATE p2p_orders SET status='expired', updated_at=now(), version=version+1 WHERE id=$1`, o.ID)
			if err != nil {
				return err
			}
			// Release escrow reservation
			if o.EscrowReservationID != nil {
				_ = s.resv.Release(ctx, *o.EscrowReservationID)
			}
			// Release offer reservation
			_ = s.repo.ReleaseOfferReservation(ctx, tx, o.OfferID, o.Amount)
			// Add event
			ev := &p2pd.OrderEvent{ID: uuid.New(), OrderID: o.ID, ActorID: nil, EventType: "expired", ToStatus: &[]p2pd.OrderStatus{p2pd.OrderExpired}[0]}
			_ = s.repo.AddEvent(ctx, tx, ev)
			// Notify
			go s.notifier.Notify(context.Background(), o.BuyerID, "p2p_expired", "Orden expirada", fmt.Sprintf("La orden de %s %s expiró y el escrow fue devuelto", o.Amount.String(), o.AssetCode))
			go s.notifier.Notify(context.Background(), o.SellerID, "p2p_expired", "Orden expirada", fmt.Sprintf("La orden de %s %s expiró y el escrow fue devuelto", o.Amount.String(), o.AssetCode))
			expiredCount++
		}
		return nil
	})
	return expiredCount, err
}
