package adminpanel

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	appadmin "korzadi-wallet/backend/internal/application/admin"
)

type reasonRequest struct {
	Reason string `json:"reason"`
}

func (h *Handler) dashboard(c *gin.Context) {
	d, err := h.svc.Dashboard(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "dashboard_unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"users_total": d.UsersTotal, "users_active": d.UsersActive, "users_blocked": d.UsersBlocked,
		"usdt_total": d.USDTTotal.String(), "withdrawals_review": d.WithdrawalsReview,
		"p2p_disputes_open": d.P2PDisputesOpen, "security_events_today": d.SecurityEventsToday,
		"recent_audit": d.RecentAudit,
	})
}

func (h *Handler) listUsers(c *gin.Context) {
	limit, offset := pageParams(c)
	rows, total, err := h.svc.ListUsers(c.Request.Context(), c.Query("search"), c.Query("status"), limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "users_unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": rows, "total": total, "limit": limit, "offset": offset})
}

func (h *Handler) listTransactions(c *gin.Context) {
	limit, offset := pageParams(c)
	rows, total, err := h.svc.ListTransactions(c.Request.Context(), c.Query("type"), limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "transactions_unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": rows, "total": total, "limit": limit, "offset": offset})
}

func (h *Handler) listAudit(c *gin.Context) {
	limit, offset := pageParams(c)
	rows, total, err := h.svc.ListAudit(c.Request.Context(), c.Query("result"), c.Query("search"), limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "audit_unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": rows, "total": total, "limit": limit, "offset": offset})
}

func (h *Handler) listAdministrators(c *gin.Context) {
	rows, err := h.svc.ListAdmins(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "admins_unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": rows})
}

func (h *Handler) inviteAdmin(c *gin.Context) {
	var req struct {
		Email  string `json:"email"`
		Role   string `json:"role"`
		Reason string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || !validReason(req.Reason) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "reason_required", "message": "correo, rol y motivo (5+) son obligatorios"})
		return
	}
	role := appadmin.Role(strings.TrimSpace(req.Role))
	if !role.Valid() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_role"})
		return
	}
	a := h.account(c)
	res, err := h.svc.Invite(c.Request.Context(), a.Email, req.Email, role, req.Reason)
	if err != nil {
		h.audit(c, "admin.invite_failed", "admin_account", nil, req.Reason, "failure", map[string]any{"email": req.Email})
		h.fail(c, err, "admin_account", nil, req.Reason)
		return
	}
	h.audit(c, "admin.invite", "admin_account", &res.Admin.ID, req.Reason, "success", map[string]any{"email": res.Admin.Email, "role": role})
	out := gin.H{"admin": res.Admin, "email_delivered": res.EmailDelivered}
	if !res.EmailDelivered {
		out["activation_url"] = res.ActivationURL
		out["activation_token"] = res.ActivationToken
	}
	c.JSON(http.StatusCreated, out)
}

func (h *Handler) setAdminStatus(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_id"})
		return
	}
	var req struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || !validReason(req.Reason) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "reason_required"})
		return
	}
	me := h.account(c)
	if id == me.ID {
		c.JSON(http.StatusConflict, gin.H{"error": "cannot_change_own_status"})
		return
	}
	if req.Status != "active" && req.Status != "suspended" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_status"})
		return
	}
	if err := h.svc.SetAdminStatus(c.Request.Context(), id, req.Status); err != nil {
		h.audit(c, "admin.status_failed", "admin_account", &id, req.Reason, "failure", map[string]any{"status": req.Status})
		h.fail(c, err, "admin_account", &id, req.Reason)
		return
	}
	h.audit(c, "admin.status_changed", "admin_account", &id, req.Reason, "success", map[string]any{"status": req.Status})
	c.JSON(http.StatusOK, gin.H{"status": req.Status})
}

func (h *Handler) listSessions(c *gin.Context) {
	rows, err := h.svc.ListSessions(c.Request.Context(), h.account(c).ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "sessions_unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": sessionsView(rows)})
}

func (h *Handler) revokeSession(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_id"})
		return
	}
	a := h.account(c)
	if id == h.session(c).ID {
		c.JSON(http.StatusConflict, gin.H{"error": "cannot_revoke_current_session"})
		return
	}
	if err := h.svc.RevokeSessionByID(c.Request.Context(), a.ID, id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "session_revoke_failed"})
		return
	}
	h.audit(c, "admin.session_revoked", "admin_session", &id, "revocación desde el panel", "success", nil)
	c.JSON(http.StatusOK, gin.H{"status": "revoked"})
}

func (h *Handler) blockUser(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var req reasonRequest
	if err := c.ShouldBindJSON(&req); err != nil || !validReason(req.Reason) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "reason_required"})
		return
	}
	n, err := h.svc.SetUserStatus(c.Request.Context(), id, "blocked")
	if err != nil {
		h.audit(c, "user.block_failed", "user", &id, req.Reason, "failure", nil)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "user_block_failed"})
		return
	}
	if n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "user_not_found"})
		return
	}
	_ = h.svc.RevokeUserSessions(c.Request.Context(), id)
	h.audit(c, "user.block", "user", &id, req.Reason, "success", nil)
	c.JSON(http.StatusOK, gin.H{"status": "blocked"})
}

func (h *Handler) unblockUser(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var req reasonRequest
	if err := c.ShouldBindJSON(&req); err != nil || !validReason(req.Reason) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "reason_required"})
		return
	}
	n, err := h.svc.UnblockUser(c.Request.Context(), id)
	if err != nil {
		h.audit(c, "user.unblock_failed", "user", &id, req.Reason, "failure", nil)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "user_unblock_failed"})
		return
	}
	if n == 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "user_not_blocked"})
		return
	}
	h.audit(c, "user.unblock", "user", &id, req.Reason, "success", nil)
	c.JSON(http.StatusOK, gin.H{"status": "unblocked"})
}

func (h *Handler) verifyEmail(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var req reasonRequest
	if err := c.ShouldBindJSON(&req); err != nil || !validReason(req.Reason) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "reason_required"})
		return
	}
	n, err := h.svc.MarkEmailVerified(c.Request.Context(), id)
	if err != nil {
		h.audit(c, "user.verify_email_failed", "user", &id, req.Reason, "failure", nil)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "verify_failed"})
		return
	}
	if n == 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "not_eligible"})
		return
	}
	h.audit(c, "user.verify_email", "user", &id, req.Reason, "success", nil)
	c.JSON(http.StatusOK, gin.H{"status": "verified"})
}

func (h *Handler) resetUser2FA(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var req reasonRequest
	if err := c.ShouldBindJSON(&req); err != nil || !validReason(req.Reason) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "reason_required"})
		return
	}
	if h.twofactor == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "2fa_unavailable"})
		return
	}
	if err := h.twofactor.AdminReset(c.Request.Context(), id); err != nil {
		h.audit(c, "user.reset_2fa_failed", "user", &id, req.Reason, "failure", nil)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "reset_failed"})
		return
	}
	_ = h.svc.RevokeUserSessions(c.Request.Context(), id)
	h.audit(c, "user.reset_2fa", "user", &id, req.Reason, "success", nil)
	c.JSON(http.StatusOK, gin.H{"status": "reset"})
}

func (h *Handler) assignRole(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var req struct {
		Role   string `json:"role"`
		Reason string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || !validReason(req.Reason) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "reason_required"})
		return
	}
	if req.Role != "user" && req.Role != "affiliate" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_role"})
		return
	}
	if err := h.svc.AssignUserRole(c.Request.Context(), id, req.Role); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "role_failed"})
		return
	}
	h.audit(c, "user.role_assigned", "user", &id, req.Reason, "success", map[string]any{"role": req.Role})
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func parseID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_id"})
		return uuid.Nil, false
	}
	return id, true
}
