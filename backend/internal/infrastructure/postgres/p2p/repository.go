package p2p

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	p2pd "korzadi-wallet/backend/internal/domain/p2p"
)

type Repo struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

func (r *Repo) Pool() *pgxpool.Pool { return r.pool }

func (r *Repo) WithTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repo) CreateOffer(ctx context.Context, tx pgx.Tx, o *p2pd.Offer) error {
	pm, _ := json.Marshal(o.PaymentMethods)
	cr, _ := json.Marshal(o.CountryRestrictions)
	_, err := tx.Exec(ctx, `INSERT INTO p2p_offers(id,user_id,wallet_account_id,asset_code,fiat_currency,price_per_unit,min_amount,max_amount,total_amount,available_amount,reserved_amount,side,payment_methods,country_restrictions,payment_time_limit_minutes,instructions,terms,status,version,created_at,updated_at)
	VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,now(),now())`,
		o.ID, o.UserID, o.WalletAccountID, o.AssetCode, o.FiatCurrency, o.PricePerUnit, o.MinAmount, o.MaxAmount, o.TotalAmount, o.AvailableAmount, o.ReservedAmount, o.Side, pm, cr, o.PaymentTimeLimitMins, o.Instructions, o.Terms, o.Status, o.Version)
	return err
}

func (r *Repo) GetOfferForUpdate(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*p2pd.Offer, error) {
	row := tx.QueryRow(ctx, `SELECT id,user_id,wallet_account_id,asset_code,fiat_currency,price_per_unit,min_amount,max_amount,total_amount,available_amount,reserved_amount,side,payment_methods,country_restrictions,payment_time_limit_minutes,instructions,terms,status,version,created_at,updated_at FROM p2p_offers WHERE id=$1 FOR UPDATE`, id)
	var o p2pd.Offer
	var pm, cr []byte
	if err := row.Scan(&o.ID, &o.UserID, &o.WalletAccountID, &o.AssetCode, &o.FiatCurrency, &o.PricePerUnit, &o.MinAmount, &o.MaxAmount, &o.TotalAmount, &o.AvailableAmount, &o.ReservedAmount, &o.Side, &pm, &cr, &o.PaymentTimeLimitMins, &o.Instructions, &o.Terms, &o.Status, &o.Version, &o.CreatedAt, &o.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	_ = json.Unmarshal(pm, &o.PaymentMethods)
	_ = json.Unmarshal(cr, &o.CountryRestrictions)
	return &o, nil
}

func (r *Repo) ListOffers(ctx context.Context, asset, fiat, side string, limit, offset int) ([]*p2pd.Offer, error) {
	q := `SELECT id,user_id,wallet_account_id,asset_code,fiat_currency,price_per_unit,min_amount,max_amount,total_amount,available_amount,reserved_amount,side,payment_methods,country_restrictions,payment_time_limit_minutes,instructions,terms,status,version,created_at,updated_at FROM p2p_offers WHERE status='active' AND available_amount > 0`
	args := []interface{}{}
	if asset != "" {
		q += " AND asset_code=$" + itoa(len(args)+1)
		args = append(args, asset)
	}
	if fiat != "" {
		q += " AND fiat_currency=$" + itoa(len(args)+1)
		args = append(args, fiat)
	}
	if side != "" {
		q += " AND side=$" + itoa(len(args)+1)
		args = append(args, side)
	}
	q += " ORDER BY price_per_unit ASC LIMIT $" + itoa(len(args)+1) + " OFFSET $" + itoa(len(args)+2)
	args = append(args, limit, offset)
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var res []*p2pd.Offer
	for rows.Next() {
		var o p2pd.Offer
		var pm, cr []byte
		if err := rows.Scan(&o.ID, &o.UserID, &o.WalletAccountID, &o.AssetCode, &o.FiatCurrency, &o.PricePerUnit, &o.MinAmount, &o.MaxAmount, &o.TotalAmount, &o.AvailableAmount, &o.ReservedAmount, &o.Side, &pm, &cr, &o.PaymentTimeLimitMins, &o.Instructions, &o.Terms, &o.Status, &o.Version, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(pm, &o.PaymentMethods)
		_ = json.Unmarshal(cr, &o.CountryRestrictions)
		res = append(res, &o)
	}
	return res, nil
}

func (r *Repo) ListMyOffers(ctx context.Context, userID uuid.UUID) ([]*p2pd.Offer, error) {
	rows, err := r.pool.Query(ctx, `SELECT id,user_id,wallet_account_id,asset_code,fiat_currency,price_per_unit,min_amount,max_amount,total_amount,available_amount,reserved_amount,side,payment_methods,country_restrictions,payment_time_limit_minutes,instructions,terms,status,version,created_at,updated_at FROM p2p_offers WHERE user_id=$1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var res []*p2pd.Offer
	for rows.Next() {
		var o p2pd.Offer
		var pm, cr []byte
		if err := rows.Scan(&o.ID, &o.UserID, &o.WalletAccountID, &o.AssetCode, &o.FiatCurrency, &o.PricePerUnit, &o.MinAmount, &o.MaxAmount, &o.TotalAmount, &o.AvailableAmount, &o.ReservedAmount, &o.Side, &pm, &cr, &o.PaymentTimeLimitMins, &o.Instructions, &o.Terms, &o.Status, &o.Version, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(pm, &o.PaymentMethods)
		_ = json.Unmarshal(cr, &o.CountryRestrictions)
		res = append(res, &o)
	}
	return res, nil
}

func (r *Repo) UpdateOffer(ctx context.Context, tx pgx.Tx, o *p2pd.Offer) error {
	pm, _ := json.Marshal(o.PaymentMethods)
	cr, _ := json.Marshal(o.CountryRestrictions)
	o.Version++
	o.UpdatedAt = time.Now()
	_, err := tx.Exec(ctx, `UPDATE p2p_offers SET price_per_unit=$1,min_amount=$2,max_amount=$3,payment_methods=$4,country_restrictions=$5,payment_time_limit_minutes=$6,instructions=$7,terms=$8,status=$9,version=$10,updated_at=$11 WHERE id=$12 AND version=$13`,
		o.PricePerUnit, o.MinAmount, o.MaxAmount, pm, cr, o.PaymentTimeLimitMins, o.Instructions, o.Terms, o.Status, o.Version, o.UpdatedAt, o.ID, o.Version-1)
	return err
}

func (r *Repo) ReserveOffer(ctx context.Context, tx pgx.Tx, offerID uuid.UUID, amount decimal.Decimal) error {
	_, err := tx.Exec(ctx, `UPDATE p2p_offers SET available_amount = available_amount - $1, reserved_amount = reserved_amount + $1, version = version + 1, updated_at = now() WHERE id = $2 AND available_amount >= $1 AND status = 'active'`, amount, offerID)
	return err
}

func (r *Repo) ReleaseOfferReservation(ctx context.Context, tx pgx.Tx, offerID uuid.UUID, amount decimal.Decimal) error {
	_, err := tx.Exec(ctx, `UPDATE p2p_offers SET available_amount = available_amount + $1, reserved_amount = reserved_amount - $1, version = version + 1, updated_at = now() WHERE id = $2 AND reserved_amount >= $1`, amount, offerID)
	return err
}

func (r *Repo) ConsumeOfferReservation(ctx context.Context, tx pgx.Tx, offerID uuid.UUID, amount decimal.Decimal) error {
	_, err := tx.Exec(ctx, `UPDATE p2p_offers SET reserved_amount = reserved_amount - $1, version = version + 1, updated_at = now() WHERE id = $2 AND reserved_amount >= $1`, amount, offerID)
	return err
}

func (r *Repo) CreateOrder(ctx context.Context, tx pgx.Tx, o *p2pd.Order) error {
	_, err := tx.Exec(ctx, `INSERT INTO p2p_orders(id,offer_id,buyer_id,seller_id,asset_code,fiat_currency,amount,price_per_unit,fiat_amount,commission_rate,commission_amount,commission_fiat,payment_method,payment_instructions,status,idempotency_key,escrow_reservation_id,expires_at,version,created_at,updated_at)
	VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,1,now(),now())`,
		o.ID, o.OfferID, o.BuyerID, o.SellerID, o.AssetCode, o.FiatCurrency, o.Amount, o.PricePerUnit, o.FiatAmount, o.CommissionRate, o.CommissionAmount, o.CommissionFiat, o.PaymentMethod, o.PaymentInstructions, o.Status, o.IdempotencyKey, o.EscrowReservationID, o.ExpiresAt)
	return err
}

func (r *Repo) GetOrderForUpdate(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*p2pd.Order, error) {
	row := tx.QueryRow(ctx, `SELECT id,offer_id,buyer_id,seller_id,asset_code,fiat_currency,amount,price_per_unit,fiat_amount,commission_rate,commission_amount,commission_fiat,payment_method,payment_instructions,status,idempotency_key,escrow_reservation_id,expires_at,paid_at,released_at,cancelled_at,dispute_id,version,created_at,updated_at FROM p2p_orders WHERE id=$1 FOR UPDATE`, id)
	var o p2pd.Order
	if err := row.Scan(&o.ID, &o.OfferID, &o.BuyerID, &o.SellerID, &o.AssetCode, &o.FiatCurrency, &o.Amount, &o.PricePerUnit, &o.FiatAmount, &o.CommissionRate, &o.CommissionAmount, &o.CommissionFiat, &o.PaymentMethod, &o.PaymentInstructions, &o.Status, &o.IdempotencyKey, &o.EscrowReservationID, &o.ExpiresAt, &o.PaidAt, &o.ReleasedAt, &o.CancelledAt, &o.DisputeID, &o.Version, &o.CreatedAt, &o.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &o, nil
}

func (r *Repo) GetOrderByIdem(ctx context.Context, tx pgx.Tx, key string) (*p2pd.Order, error) {
	row := tx.QueryRow(ctx, `SELECT id,offer_id,buyer_id,seller_id,asset_code,fiat_currency,amount,price_per_unit,fiat_amount,commission_rate,commission_amount,commission_fiat,payment_method,payment_instructions,status,idempotency_key,escrow_reservation_id,expires_at,paid_at,released_at,cancelled_at,dispute_id,version,created_at,updated_at FROM p2p_orders WHERE idempotency_key=$1`, key)
	var o p2pd.Order
	if err := row.Scan(&o.ID, &o.OfferID, &o.BuyerID, &o.SellerID, &o.AssetCode, &o.FiatCurrency, &o.Amount, &o.PricePerUnit, &o.FiatAmount, &o.CommissionRate, &o.CommissionAmount, &o.CommissionFiat, &o.PaymentMethod, &o.PaymentInstructions, &o.Status, &o.IdempotencyKey, &o.EscrowReservationID, &o.ExpiresAt, &o.PaidAt, &o.ReleasedAt, &o.CancelledAt, &o.DisputeID, &o.Version, &o.CreatedAt, &o.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &o, nil
}

func (r *Repo) ListMyOrders(ctx context.Context, userID uuid.UUID) ([]*p2pd.Order, error) {
	rows, err := r.pool.Query(ctx, `SELECT id,offer_id,buyer_id,seller_id,asset_code,fiat_currency,amount,price_per_unit,fiat_amount,commission_rate,commission_amount,commission_fiat,payment_method,payment_instructions,status,idempotency_key,escrow_reservation_id,expires_at,paid_at,released_at,cancelled_at,dispute_id,version,created_at,updated_at FROM p2p_orders WHERE buyer_id=$1 OR seller_id=$1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var res []*p2pd.Order
	for rows.Next() {
		var o p2pd.Order
		if err := rows.Scan(&o.ID, &o.OfferID, &o.BuyerID, &o.SellerID, &o.AssetCode, &o.FiatCurrency, &o.Amount, &o.PricePerUnit, &o.FiatAmount, &o.CommissionRate, &o.CommissionAmount, &o.CommissionFiat, &o.PaymentMethod, &o.PaymentInstructions, &o.Status, &o.IdempotencyKey, &o.EscrowReservationID, &o.ExpiresAt, &o.PaidAt, &o.ReleasedAt, &o.CancelledAt, &o.DisputeID, &o.Version, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, err
		}
		res = append(res, &o)
	}
	return res, nil
}

func (r *Repo) UpdateOrderStatus(ctx context.Context, tx pgx.Tx, o *p2pd.Order, newStatus p2pd.OrderStatus) error {
	o.Version++
	o.UpdatedAt = time.Now()
	_, err := tx.Exec(ctx, `UPDATE p2p_orders SET status=$1,version=$2,updated_at=$3 WHERE id=$4 AND version=$5`, newStatus, o.Version, o.UpdatedAt, o.ID, o.Version-1)
	return err
}

func (r *Repo) AddEvent(ctx context.Context, tx pgx.Tx, e *p2pd.OrderEvent) error {
	_, err := tx.Exec(ctx, `INSERT INTO p2p_order_events(id,order_id,actor_id,event_type,from_status,to_status,metadata,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,now())`, e.ID, e.OrderID, e.ActorID, e.EventType, e.FromStatus, e.ToStatus, e.Metadata)
	return err
}

func (r *Repo) AddMessage(ctx context.Context, tx pgx.Tx, m *p2pd.Message) error {
	_, err := tx.Exec(ctx, `INSERT INTO p2p_messages(id,order_id,sender_id,message,attachment_path,attachment_name,attachment_mime,attachment_size,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,now())`, m.ID, m.OrderID, m.SenderID, m.Message, m.AttachmentPath, m.AttachmentName, m.AttachmentMime, m.AttachmentSize)
	return err
}

func (r *Repo) ListMessages(ctx context.Context, orderID uuid.UUID) ([]*p2pd.Message, error) {
	rows, err := r.pool.Query(ctx, `SELECT id,order_id,sender_id,message,attachment_path,attachment_name,attachment_mime,attachment_size,created_at FROM p2p_messages WHERE order_id=$1 ORDER BY created_at ASC`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var res []*p2pd.Message
	for rows.Next() {
		var m p2pd.Message
		if err := rows.Scan(&m.ID, &m.OrderID, &m.SenderID, &m.Message, &m.AttachmentPath, &m.AttachmentName, &m.AttachmentMime, &m.AttachmentSize, &m.CreatedAt); err != nil {
			return nil, err
		}
		res = append(res, &m)
	}
	return res, nil
}

func (r *Repo) CreateDispute(ctx context.Context, tx pgx.Tx, d *p2pd.Dispute) error {
	_, err := tx.Exec(ctx, `INSERT INTO p2p_disputes(id,order_id,opened_by,reason,description,status,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,now(),now())`, d.ID, d.OrderID, d.OpenedBy, d.Reason, d.Description, d.Status)
	return err
}

func (r *Repo) GetDisputeByOrder(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) (*p2pd.Dispute, error) {
	row := tx.QueryRow(ctx, `SELECT id,order_id,opened_by,reason,description,status,resolver_id,resolution_reason,resolved_at,created_at,updated_at FROM p2p_disputes WHERE order_id=$1`, orderID)
	var d p2pd.Dispute
	if err := row.Scan(&d.ID, &d.OrderID, &d.OpenedBy, &d.Reason, &d.Description, &d.Status, &d.ResolverID, &d.ResolutionReason, &d.ResolvedAt, &d.CreatedAt, &d.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &d, nil
}

// GetOffer returns any offer by id regardless of status. Unlike
// GetOfferForUpdate it does not take a row lock and does not require a
// transaction, so it is safe to use for read-only lookups (market detail,
// admin detail).
func (r *Repo) GetOffer(ctx context.Context, id uuid.UUID) (*p2pd.Offer, error) {
	row := r.pool.QueryRow(ctx, `SELECT id,user_id,wallet_account_id,asset_code,fiat_currency,price_per_unit,min_amount,max_amount,total_amount,available_amount,reserved_amount,side,payment_methods,country_restrictions,payment_time_limit_minutes,instructions,terms,status,version,created_at,updated_at FROM p2p_offers WHERE id=$1`, id)
	var o p2pd.Offer
	var pm, cr []byte
	if err := row.Scan(&o.ID, &o.UserID, &o.WalletAccountID, &o.AssetCode, &o.FiatCurrency, &o.PricePerUnit, &o.MinAmount, &o.MaxAmount, &o.TotalAmount, &o.AvailableAmount, &o.ReservedAmount, &o.Side, &pm, &cr, &o.PaymentTimeLimitMins, &o.Instructions, &o.Terms, &o.Status, &o.Version, &o.CreatedAt, &o.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	_ = json.Unmarshal(pm, &o.PaymentMethods)
	_ = json.Unmarshal(cr, &o.CountryRestrictions)
	return &o, nil
}

// ListAllOffers is the admin listing: it includes every status (not just
// active) and optional status/asset/fiat/side filters.
func (r *Repo) ListAllOffers(ctx context.Context, status, asset, fiat, side string, limit, offset int) ([]*p2pd.Offer, error) {
	q := `SELECT id,user_id,wallet_account_id,asset_code,fiat_currency,price_per_unit,min_amount,max_amount,total_amount,available_amount,reserved_amount,side,payment_methods,country_restrictions,payment_time_limit_minutes,instructions,terms,status,version,created_at,updated_at FROM p2p_offers WHERE 1=1`
	args := []interface{}{}
	if status != "" {
		q += " AND status=$" + itoa(len(args)+1)
		args = append(args, status)
	}
	if asset != "" {
		q += " AND asset_code=$" + itoa(len(args)+1)
		args = append(args, asset)
	}
	if fiat != "" {
		q += " AND fiat_currency=$" + itoa(len(args)+1)
		args = append(args, fiat)
	}
	if side != "" {
		q += " AND side=$" + itoa(len(args)+1)
		args = append(args, side)
	}
	q += " ORDER BY created_at DESC LIMIT $" + itoa(len(args)+1) + " OFFSET $" + itoa(len(args)+2)
	args = append(args, limit, offset)
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var res []*p2pd.Offer
	for rows.Next() {
		var o p2pd.Offer
		var pm, cr []byte
		if err := rows.Scan(&o.ID, &o.UserID, &o.WalletAccountID, &o.AssetCode, &o.FiatCurrency, &o.PricePerUnit, &o.MinAmount, &o.MaxAmount, &o.TotalAmount, &o.AvailableAmount, &o.ReservedAmount, &o.Side, &pm, &cr, &o.PaymentTimeLimitMins, &o.Instructions, &o.Terms, &o.Status, &o.Version, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(pm, &o.PaymentMethods)
		_ = json.Unmarshal(cr, &o.CountryRestrictions)
		res = append(res, &o)
	}
	return res, nil
}

// GetOrder returns any order by id without a row lock. Admin-only read.
func (r *Repo) GetOrder(ctx context.Context, id uuid.UUID) (*p2pd.Order, error) {
	row := r.pool.QueryRow(ctx, `SELECT id,offer_id,buyer_id,seller_id,asset_code,fiat_currency,amount,price_per_unit,fiat_amount,commission_rate,commission_amount,commission_fiat,payment_method,payment_instructions,status,idempotency_key,escrow_reservation_id,expires_at,paid_at,released_at,cancelled_at,dispute_id,version,created_at,updated_at FROM p2p_orders WHERE id=$1`, id)
	var o p2pd.Order
	if err := row.Scan(&o.ID, &o.OfferID, &o.BuyerID, &o.SellerID, &o.AssetCode, &o.FiatCurrency, &o.Amount, &o.PricePerUnit, &o.FiatAmount, &o.CommissionRate, &o.CommissionAmount, &o.CommissionFiat, &o.PaymentMethod, &o.PaymentInstructions, &o.Status, &o.IdempotencyKey, &o.EscrowReservationID, &o.ExpiresAt, &o.PaidAt, &o.ReleasedAt, &o.CancelledAt, &o.DisputeID, &o.Version, &o.CreatedAt, &o.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &o, nil
}

// ListAllOrders is the admin order listing with optional status/asset
// filters, newest first.
func (r *Repo) ListAllOrders(ctx context.Context, status, asset string, limit, offset int) ([]*p2pd.Order, error) {
	q := `SELECT id,offer_id,buyer_id,seller_id,asset_code,fiat_currency,amount,price_per_unit,fiat_amount,commission_rate,commission_amount,commission_fiat,payment_method,payment_instructions,status,idempotency_key,escrow_reservation_id,expires_at,paid_at,released_at,cancelled_at,dispute_id,version,created_at,updated_at FROM p2p_orders WHERE 1=1`
	args := []interface{}{}
	if status != "" {
		q += " AND status=$" + itoa(len(args)+1)
		args = append(args, status)
	}
	if asset != "" {
		q += " AND asset_code=$" + itoa(len(args)+1)
		args = append(args, asset)
	}
	q += " ORDER BY created_at DESC LIMIT $" + itoa(len(args)+1) + " OFFSET $" + itoa(len(args)+2)
	args = append(args, limit, offset)
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var res []*p2pd.Order
	for rows.Next() {
		var o p2pd.Order
		if err := rows.Scan(&o.ID, &o.OfferID, &o.BuyerID, &o.SellerID, &o.AssetCode, &o.FiatCurrency, &o.Amount, &o.PricePerUnit, &o.FiatAmount, &o.CommissionRate, &o.CommissionAmount, &o.CommissionFiat, &o.PaymentMethod, &o.PaymentInstructions, &o.Status, &o.IdempotencyKey, &o.EscrowReservationID, &o.ExpiresAt, &o.PaidAt, &o.ReleasedAt, &o.CancelledAt, &o.DisputeID, &o.Version, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, err
		}
		res = append(res, &o)
	}
	return res, nil
}

// GetDispute returns the dispute for an order without a transaction.
func (r *Repo) GetDispute(ctx context.Context, orderID uuid.UUID) (*p2pd.Dispute, error) {
	row := r.pool.QueryRow(ctx, `SELECT id,order_id,opened_by,reason,description,status,resolver_id,resolution_reason,resolved_at,created_at,updated_at FROM p2p_disputes WHERE order_id=$1`, orderID)
	var d p2pd.Dispute
	if err := row.Scan(&d.ID, &d.OrderID, &d.OpenedBy, &d.Reason, &d.Description, &d.Status, &d.ResolverID, &d.ResolutionReason, &d.ResolvedAt, &d.CreatedAt, &d.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &d, nil
}

// UpdateDispute persists an admin resolution.
func (r *Repo) UpdateDispute(ctx context.Context, tx pgx.Tx, d *p2pd.Dispute) error {
	_, err := tx.Exec(ctx, `UPDATE p2p_disputes SET status=$1,resolver_id=$2,resolution_reason=$3,resolved_at=$4,updated_at=now() WHERE id=$5`,
		d.Status, d.ResolverID, d.ResolutionReason, d.ResolvedAt, d.ID)
	return err
}

// SetOfferStatusAdmin updates an offer status without the owner check used
// by the user-facing flow. Callers must be admin endpoints.
func (r *Repo) SetOfferStatusAdmin(ctx context.Context, tx pgx.Tx, offerID uuid.UUID, status p2pd.OfferStatus) error {
	_, err := tx.Exec(ctx, `UPDATE p2p_offers SET status=$1,version=version+1,updated_at=now() WHERE id=$2`, status, offerID)
	return err
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	if i < 0 {
		return "-" + itoa(-i)
	}
	var buf [32]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
