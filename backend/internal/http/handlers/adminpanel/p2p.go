package adminpanel

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func (h *Handler) p2pOrders(c *gin.Context) {
	limit, offset := pageParams(c)
	rows, err := h.p2p.AdminListOrders(c.Request.Context(), c.Query("status"), c.Query("asset"), limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "p2p_orders_unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": rows, "limit": limit, "offset": offset})
}

func (h *Handler) p2pOrder(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_id"})
		return
	}
	order, err := h.p2p.AdminGetOrder(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return
	}
	dispute, _ := h.p2p.AdminGetDispute(c.Request.Context(), id)
	c.JSON(http.StatusOK, gin.H{"order": order, "dispute": dispute})
}

func (h *Handler) p2pResolve(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_id"})
		return
	}
	var req struct {
		Action string `json:"action"`
		Reason string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || !validReason(req.Reason) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "reason_required"})
		return
	}
	if req.Action != "release" && req.Action != "refund" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_action"})
		return
	}
	if err := h.p2p.AdminResolveDispute(c.Request.Context(), uuid.Nil, id, req.Action, req.Reason); err != nil {
		h.audit(c, "p2p.dispute_resolve_failed", "p2p_order", &id, req.Reason, "failure", map[string]any{"action": req.Action, "error": err.Error()})
		c.JSON(http.StatusConflict, gin.H{"error": "resolve_failed", "message": err.Error()})
		return
	}
	h.audit(c, "p2p.dispute_resolve", "p2p_order", &id, req.Reason, "success", map[string]any{"action": req.Action})
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *Handler) p2pDisputes(c *gin.Context) {
	limit, offset := pageParams(c)
	rows, total, err := h.svc.ListDisputes(c.Request.Context(), c.Query("status"), limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "p2p_disputes_unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": rows, "total": total, "limit": limit, "offset": offset})
}

func (h *Handler) p2pOffers(c *gin.Context) {
	limit, offset := pageParams(c)
	rows, err := h.p2p.AdminListOffers(c.Request.Context(), c.Query("status"), c.Query("asset"), c.Query("fiat"), c.Query("side"), limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "p2p_offers_unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": rows, "limit": limit, "offset": offset})
}

func (h *Handler) p2pSuspend(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_id"})
		return
	}
	var req reasonRequest
	if err := c.ShouldBindJSON(&req); err != nil || !validReason(req.Reason) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "reason_required"})
		return
	}
	if err := h.p2p.AdminSuspendOffer(c.Request.Context(), h.account(c).ID, id); err != nil {
		h.audit(c, "p2p.offer_suspend_failed", "p2p_offer", &id, req.Reason, "failure", map[string]any{"error": err.Error()})
		c.JSON(http.StatusConflict, gin.H{"error": "suspend_failed", "message": err.Error()})
		return
	}
	h.audit(c, "p2p.offer_suspend", "p2p_offer", &id, req.Reason, "success", nil)
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
