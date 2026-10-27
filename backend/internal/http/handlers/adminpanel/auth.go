package adminpanel

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	appadmin "korzadi-wallet/backend/internal/application/admin"
)

func (h *Handler) login(c *gin.Context) {
	var req struct {
		Email    string `json:"email"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	email := req.Email
	if email == "" {
		email = req.Username
	}
	res, err := h.svc.Login(c.Request.Context(), email, req.Password, c.ClientIP(), c.Request.UserAgent())
	if err != nil {
		h.audit(c, "admin.login_failed", "admin_account", nil, "", "failure", map[string]any{"email": strings.ToLower(strings.TrimSpace(email))})
		h.fail(c, err, "", nil, "")
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "2fa_required", "challenge": res.Challenge, "expires_at": res.ExpiresAt})
}

func (h *Handler) login2FA(c *gin.Context) {
	var req struct {
		Challenge string `json:"challenge"`
		Code      string `json:"code"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Challenge == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	res, err := h.svc.CompleteLogin(c.Request.Context(), req.Challenge, req.Code, c.ClientIP(), c.Request.UserAgent())
	if err != nil {
		h.audit(c, "admin.2fa_failed", "admin_account", nil, "", "failure", nil)
		if errors.Is(err, appadmin.ErrInvalidCode) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_2fa_code"})
			return
		}
		h.fail(c, err, "", nil, "")
		return
	}
	h.setCookie(c, res.Token, res.ExpiresAt)
	h.svc.Audit(c.Request.Context(), res.Account, "admin.login", "admin_account", &res.Account.ID, "", "success", c.ClientIP(), nil)
	c.JSON(http.StatusOK, gin.H{"admin": accountView(res.Account), "session": sessionView(res.Session)})
}

func (h *Handler) logout(c *gin.Context) {
	raw, _ := c.Cookie(cookieName)
	a := h.account(c)
	h.svc.Logout(c.Request.Context(), raw)
	h.svc.Audit(c.Request.Context(), a, "admin.logout", "admin_account", nil, "", "success", c.ClientIP(), nil)
	h.clearCookie(c)
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *Handler) me(c *gin.Context) {
	a := h.account(c)
	sessions, _ := h.svc.ListSessions(c.Request.Context(), a.ID)
	c.JSON(http.StatusOK, gin.H{"admin": accountView(a), "sessions": sessionsView(sessions),
		"permissions": permissions(a.Role), "session": sessionView(h.session(c))})
}

func (h *Handler) activateStart(c *gin.Context) {
	var req struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Token == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	res, err := h.svc.ActivateStart(c.Request.Context(), req.Token, req.Password)
	if err != nil {
		h.fail(c, err, "admin_activation", nil, "")
		return
	}
	c.JSON(http.StatusOK, gin.H{"email": res.Email, "secret": res.Secret, "otpauth_url": res.OTPAuthURL,
		"next": "scan_qr_and_confirm"})
}

func (h *Handler) activateConfirm(c *gin.Context) {
	var req struct {
		Token string `json:"token"`
		Code  string `json:"code"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Token == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	if err := h.svc.ActivateConfirm(c.Request.Context(), req.Token, req.Code); err != nil {
		if errors.Is(err, appadmin.ErrInvalidCode) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_2fa_code"})
			return
		}
		h.fail(c, err, "admin_activation", nil, "")
		return
	}
	h.svc.Audit(c.Request.Context(), nil, "admin.activated", "admin_account", nil, "", "success", c.ClientIP(), nil)
	c.JSON(http.StatusOK, gin.H{"status": "activated"})
}

func (h *Handler) changePassword(c *gin.Context) {
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
		Reason          string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	a := h.account(c)
	if err := h.svc.ChangePassword(c.Request.Context(), a, req.CurrentPassword, req.NewPassword); err != nil {
		h.audit(c, "admin.password_change_failed", "admin_account", &a.ID, req.Reason, "failure", nil)
		h.fail(c, err, "admin_account", &a.ID, req.Reason)
		return
	}
	h.audit(c, "admin.password_change", "admin_account", &a.ID, req.Reason, "success", nil)
	h.clearCookie(c)
	c.JSON(http.StatusOK, gin.H{"status": "password_changed", "reauthenticate": true})
}
