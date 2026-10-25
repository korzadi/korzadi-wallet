package p2p

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type OfferStatus string

const (
	OfferActive    OfferStatus = "active"
	OfferPaused    OfferStatus = "paused"
	OfferCancelled OfferStatus = "cancelled"
	OfferClosed    OfferStatus = "closed"
)

type OfferSide string

const (
	SideBuy  OfferSide = "buy"
	SideSell OfferSide = "sell"
)

type OrderStatus string

const (
	OrderCreated       OrderStatus = "created"
	OrderEscrowLocked  OrderStatus = "escrow_locked"
	OrderPaymentMarked OrderStatus = "payment_marked"
	OrderReleased      OrderStatus = "released"
	OrderCancelled     OrderStatus = "cancelled"
	OrderDisputed      OrderStatus = "disputed"
	OrderExpired       OrderStatus = "expired"
)

type DisputeStatus string

const (
	DisputeOpen        DisputeStatus = "open"
	DisputeUnderReview DisputeStatus = "under_review"
	DisputeResolvedRel DisputeStatus = "resolved_release"
	DisputeResolvedRef DisputeStatus = "resolved_refund"
	DisputeClosed      DisputeStatus = "closed"
)

type Offer struct {
	ID                   uuid.UUID       `json:"id"`
	UserID               uuid.UUID       `json:"user_id"`
	WalletAccountID      uuid.UUID       `json:"wallet_account_id"`
	AssetCode            string          `json:"asset_code"`
	FiatCurrency         string          `json:"fiat_currency"`
	PricePerUnit         decimal.Decimal `json:"price_per_unit"`
	MinAmount            decimal.Decimal `json:"min_amount"`
	MaxAmount            decimal.Decimal `json:"max_amount"`
	TotalAmount          decimal.Decimal `json:"total_amount"`
	AvailableAmount      decimal.Decimal `json:"available_amount"`
	ReservedAmount       decimal.Decimal `json:"reserved_amount"`
	Side                 OfferSide       `json:"side"`
	PaymentMethods       []string        `json:"payment_methods"`
	CountryRestrictions  []string        `json:"country_restrictions"`
	PaymentTimeLimitMins int             `json:"payment_time_limit_minutes"`
	Instructions         string          `json:"instructions,omitempty"`
	Terms                string          `json:"terms,omitempty"`
	Status               OfferStatus     `json:"status"`
	Version              int             `json:"version"`
	CreatedAt            time.Time       `json:"created_at"`
	UpdatedAt            time.Time       `json:"updated_at"`
}

type Order struct {
	ID                  uuid.UUID       `json:"id"`
	OfferID             uuid.UUID       `json:"offer_id"`
	BuyerID             uuid.UUID       `json:"buyer_id"`
	SellerID            uuid.UUID       `json:"seller_id"`
	AssetCode           string          `json:"asset_code"`
	FiatCurrency        string          `json:"fiat_currency"`
	Amount              decimal.Decimal `json:"amount"`
	PricePerUnit        decimal.Decimal `json:"price_per_unit"`
	FiatAmount          decimal.Decimal `json:"fiat_amount"`
	CommissionRate      decimal.Decimal `json:"commission_rate,omitempty"`   // basis points
	CommissionAmount    decimal.Decimal `json:"commission_amount,omitempty"` // in asset
	CommissionFiat      decimal.Decimal `json:"commission_fiat,omitempty"`   // in fiat
	PaymentMethod       string          `json:"payment_method,omitempty"`
	PaymentInstructions string          `json:"payment_instructions,omitempty"`
	Status              OrderStatus     `json:"status"`
	IdempotencyKey      string          `json:"-"`
	EscrowReservationID *uuid.UUID      `json:"escrow_reservation_id,omitempty"`
	ExpiresAt           time.Time       `json:"expires_at"`
	PaidAt              *time.Time      `json:"paid_at,omitempty"`
	ReleasedAt          *time.Time      `json:"released_at,omitempty"`
	CancelledAt         *time.Time      `json:"cancelled_at,omitempty"`
	DisputeID           *uuid.UUID      `json:"dispute_id,omitempty"`
	Version             int             `json:"version"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
}

type OrderEvent struct {
	ID         uuid.UUID      `json:"id"`
	OrderID    uuid.UUID      `json:"order_id"`
	ActorID    *uuid.UUID     `json:"actor_id,omitempty"`
	EventType  string         `json:"event_type"`
	FromStatus *OrderStatus   `json:"from_status,omitempty"`
	ToStatus   *OrderStatus   `json:"to_status,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
}

type Message struct {
	ID             uuid.UUID `json:"id"`
	OrderID        uuid.UUID `json:"order_id"`
	SenderID       uuid.UUID `json:"sender_id"`
	Message        string    `json:"message,omitempty"`
	AttachmentPath string    `json:"attachment_path,omitempty"`
	AttachmentName string    `json:"attachment_name,omitempty"`
	AttachmentMime string    `json:"attachment_mime,omitempty"`
	AttachmentSize int       `json:"attachment_size,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

type Dispute struct {
	ID               uuid.UUID     `json:"id"`
	OrderID          uuid.UUID     `json:"order_id"`
	OpenedBy         uuid.UUID     `json:"opened_by"`
	Reason           string        `json:"reason"`
	Description      string        `json:"description,omitempty"`
	Status           DisputeStatus `json:"status"`
	ResolverID       *uuid.UUID    `json:"resolver_id,omitempty"`
	ResolutionReason string        `json:"resolution_reason,omitempty"`
	ResolvedAt       *time.Time    `json:"resolved_at,omitempty"`
	CreatedAt        time.Time     `json:"created_at"`
	UpdatedAt        time.Time     `json:"updated_at"`
}
