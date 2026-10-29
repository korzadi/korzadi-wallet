package p2p

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	app "korzadi-wallet/backend/internal/application/p2p"
	p2pd "korzadi-wallet/backend/internal/domain/p2p"
)

type Handler struct {
	svc *app.Service
}

func New(svc *app.Service) *Handler { return &Handler{svc: svc} }

func getUserID(c *gin.Context) uuid.UUID {
	// La identidad sale exclusivamente del JWT validado por el middleware.
	// Nunca se acepta X-User-ID ni otro encabezado controlado por el cliente.
	id, _ := uuid.Parse(c.GetString("user_id"))
	return id
}

func (h *Handler) Service() *app.Service { return h.svc }

func (h *Handler) ListOffers(c *gin.Context) {
	offers, err := h.svc.ListOffers(c.Request.Context(), c.Query("asset"), c.Query("fiat"), c.Query("side"), 50, 0)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "error"})
		return
	}
	c.JSON(http.StatusOK, offers)
}

func (h *Handler) GetOffer(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	offer, err := h.svc.GetOffer(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "oferta no encontrada"})
		return
	}
	c.JSON(http.StatusOK, offer)
}

type createOfferReq struct {
	AssetCode            string   `json:"asset_code"`
	FiatCurrency         string   `json:"fiat_currency"`
	PricePerUnit         string   `json:"price_per_unit"`
	MinAmount            string   `json:"min_amount"`
	MaxAmount            string   `json:"max_amount"`
	TotalAmount          string   `json:"total_amount"`
	Side                 string   `json:"side"`
	PaymentMethods       []string `json:"payment_methods"`
	CountryRestrictions  []string `json:"country_restrictions"`
	PaymentTimeLimitMins int      `json:"payment_time_limit_minutes"`
	Instructions         string   `json:"instructions"`
	Terms                string   `json:"terms"`
}

func (h *Handler) CreateOffer(c *gin.Context) {
	var req createOfferReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	pp, _ := decimal.NewFromString(req.PricePerUnit)
	ma, _ := decimal.NewFromString(req.MinAmount)
	mx, _ := decimal.NewFromString(req.MaxAmount)
	ta, _ := decimal.NewFromString(req.TotalAmount)
	if pp.LessThanOrEqual(decimal.Zero) || ta.LessThanOrEqual(decimal.Zero) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_amount"})
		return
	}
	if req.PaymentTimeLimitMins <= 0 {
		req.PaymentTimeLimitMins = 30
	}
	offer, err := h.svc.CreateOffer(c.Request.Context(), getUserID(c), app.CreateOfferInput{
		AssetCode: req.AssetCode, FiatCurrency: req.FiatCurrency, PricePerUnit: pp,
		MinAmount: ma, MaxAmount: mx, TotalAmount: ta, Side: p2pd.OfferSide(req.Side),
		PaymentMethods: req.PaymentMethods, CountryRestrictions: req.CountryRestrictions,
		PaymentTimeLimitMins: req.PaymentTimeLimitMins, Instructions: req.Instructions, Terms: req.Terms,
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, offer)
}

func (h *Handler) MyOffers(c *gin.Context) {
	offers, err := h.svc.MyOffers(c.Request.Context(), getUserID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "error"})
		return
	}
	c.JSON(http.StatusOK, offers)
}

func (h *Handler) UpdateOffer(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	var req struct {
		Status string `json:"status"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	if err := h.svc.UpdateOfferStatus(c.Request.Context(), getUserID(c), id, p2pd.OfferStatus(req.Status)); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

type createOrderReq struct {
	OfferID       string `json:"offer_id"`
	Amount        string `json:"amount"`
	PaymentMethod string `json:"payment_method"`
}

func (h *Handler) CreateOrder(c *gin.Context) {
	var req createOrderReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	oid, err := uuid.Parse(req.OfferID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	amt, _ := decimal.NewFromString(req.Amount)
	order, err := h.svc.CreateOrder(c.Request.Context(), getUserID(c), app.CreateOrderInput{
		OfferID: oid, Amount: amt, PaymentMethod: req.PaymentMethod, IdempotencyKey: c.GetHeader("Idempotency-Key"),
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, order)
}

func (h *Handler) MyOrders(c *gin.Context) {
	orders, err := h.svc.MyOrders(c.Request.Context(), getUserID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "error"})
		return
	}
	c.JSON(http.StatusOK, orders)
}

func (h *Handler) GetOrder(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	order, err := h.svc.GetOrder(c.Request.Context(), getUserID(c), id)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, order)
}

func (h *Handler) MarkPaid(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	if err := h.svc.MarkPaid(c.Request.Context(), getUserID(c), id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *Handler) Release(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	if err := h.svc.Release(c.Request.Context(), getUserID(c), id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *Handler) Cancel(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	if err := h.svc.Cancel(c.Request.Context(), getUserID(c), id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *Handler) OpenDispute(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	var req struct {
		Reason      string `json:"reason"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	d, err := h.svc.OpenDispute(c.Request.Context(), getUserID(c), id, req.Reason, req.Description)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, d)
}

func (h *Handler) Messages(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	msgs, err := h.svc.Messages(c.Request.Context(), getUserID(c), id)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, msgs)
}

func (h *Handler) SendMessage(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	var req struct {
		Message string `json:"message"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	if err := h.svc.SendMessage(c.Request.Context(), getUserID(c), id, req.Message); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// Admin endpoints

func pageParams(c *gin.Context) (limit, offset int) {
	limit = 50
	if v, err := strconv.Atoi(c.Query("limit")); err == nil && v > 0 && v <= 200 {
		limit = v
	}
	if v, err := strconv.Atoi(c.Query("offset")); err == nil && v >= 0 {
		offset = v
	}
	return limit, offset
}

func (h *Handler) AdminListOrders(c *gin.Context) {
	limit, offset := pageParams(c)
	orders, err := h.svc.AdminListOrders(c.Request.Context(), c.Query("status"), c.Query("asset"), limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "error"})
		return
	}
	c.JSON(http.StatusOK, orders)
}

func (h *Handler) AdminGetOrder(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	order, err := h.svc.AdminGetOrder(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "orden no encontrada"})
		return
	}
	dispute, err := h.svc.AdminGetDispute(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"order": order, "dispute": dispute})
}

func (h *Handler) AdminResolveDispute(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	var req struct {
		Action string `json:"action"` // "release" or "refund"
		Reason string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	if req.Action != "release" && req.Action != "refund" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "acción inválida"})
		return
	}
	if err := h.svc.AdminResolveDispute(c.Request.Context(), getUserID(c), id, req.Action, req.Reason); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *Handler) AdminListOffers(c *gin.Context) {
	limit, offset := pageParams(c)
	offers, err := h.svc.AdminListOffers(c.Request.Context(), c.Query("status"), c.Query("asset"), c.Query("fiat"), c.Query("side"), limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "error"})
		return
	}
	c.JSON(http.StatusOK, offers)
}

func (h *Handler) AdminSuspendOffer(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	if err := h.svc.AdminSuspendOffer(c.Request.Context(), getUserID(c), id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
