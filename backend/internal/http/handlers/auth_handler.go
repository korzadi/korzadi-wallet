package handlers

import (
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"korzadi-wallet/backend/internal/application/auth"
	"korzadi-wallet/backend/internal/application/twofactor"
	"korzadi-wallet/backend/internal/domain"
)

const (
	refreshCookieName = "kz_refresh"
	refreshCookiePath = "/api/auth"
)

type AuthHandler struct {
	service      *auth.Service
	cookieSecure bool
}

func NewAuthHandler(s *auth.Service) *AuthHandler {
	return &AuthHandler{service: s}
}

// SetCookieSecure enables the Secure flag on the refresh cookie. It must be
// true in production (HTTPS); false is only for local HTTP development.
func (h *AuthHandler) SetCookieSecure(secure bool) *AuthHandler {
	h.cookieSecure = secure
	return h
}

func (h *AuthHandler) setRefreshCookie(c *gin.Context, token string, expires time.Time) {
	if token == "" {
		return
	}
	if expires.IsZero() {
		expires = time.Now().Add(24 * time.Hour)
	}
	maxAge := int(time.Until(expires).Seconds())
	if maxAge < 60 {
		maxAge = 60
	}
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie(refreshCookieName, token, maxAge, refreshCookiePath, "", h.cookieSecure, true)
}

func (h *AuthHandler) clearRefreshCookie(c *gin.Context) {
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie(refreshCookieName, "", -1, refreshCookiePath, "", h.cookieSecure, true)
}

func (h *AuthHandler) respondSession(c *gin.Context, status int, result *auth.AuthResult) {
	h.setRefreshCookie(c, result.RefreshToken, result.RefreshExpiresAt)
	c.JSON(status, authResponse(result))
}

type registerRequest struct {
	Email     string  `json:"email" binding:"required,email"`
	Password  string  `json:"password" binding:"required,min=10"`
	FirstName string  `json:"first_name" binding:"required"`
	LastName  string  `json:"last_name" binding:"required"`
	Phone     *string `json:"phone"`
}

func (h *AuthHandler) Register(c *gin.Context) {
	var req registerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	result, err := h.service.Register(c.Request.Context(), auth.RegisterInput{
		Email: req.Email, Password: req.Password, FirstName: req.FirstName,
		LastName: req.LastName, Phone: req.Phone,
	}, c.ClientIP(), c.Request.UserAgent())
	if err != nil {
		respondDomainError(c, err)
		return
	}
	if result.AccessToken == "" {
		c.JSON(http.StatusCreated, gin.H{
			"user":                        userResponse(result.User),
			"requires_email_verification": true,
			"message":                     "cuenta creada; verifica tu correo electrónico para iniciar sesión",
		})
		return
	}
	h.respondSession(c, http.StatusCreated, result)
}

type loginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

func (h *AuthHandler) Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	result, err := h.service.Login(c.Request.Context(), req.Email, req.Password, c.ClientIP(), c.Request.UserAgent())
	if err != nil {
		respondDomainError(c, err)
		return
	}
	if result.TwoFactorRequired {
		// No access/refresh token here: only the short-lived challenge.
		c.JSON(http.StatusOK, gin.H{
			"two_factor_required": true,
			"challenge_token":     result.TwoFactorToken,
			"expires_at":          result.TwoFactorExpiresAt,
		})
		return
	}
	h.respondSession(c, http.StatusOK, result)
}

type loginTwoFactorRequest struct {
	ChallengeToken string `json:"challenge_token" binding:"required,max=256"`
	Code           string `json:"code" binding:"required,max=32"`
}

// LoginTwoFactor completes a pending 2FA login. It is deliberately separate
// from /security/2fa/confirm (which activates 2FA in an already-authenticated
// session) and never accepts a normal access token or refresh token.
func (h *AuthHandler) LoginTwoFactor(c *gin.Context) {
	var req loginTwoFactorRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": "desafío y código 2FA requeridos"})
		return
	}
	result, err := h.service.CompleteTwoFactorLogin(
		c.Request.Context(), req.ChallengeToken, req.Code, c.ClientIP(), c.Request.UserAgent())
	if err != nil {
		respondDomainError(c, err)
		return
	}
	h.respondSession(c, http.StatusOK, result)
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// refreshTokenFromRequest prefers the HttpOnly cookie and keeps accepting a
// body token only for backward compatibility with sessions issued before the
// cookie migration.
func (h *AuthHandler) refreshTokenFromRequest(c *gin.Context) string {
	var req refreshRequest
	_ = c.ShouldBindJSON(&req)
	if req.RefreshToken != "" {
		return req.RefreshToken
	}
	token, _ := c.Cookie(refreshCookieName)
	return token
}

func (h *AuthHandler) Refresh(c *gin.Context) {
	token := h.refreshTokenFromRequest(c)
	if token == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "refresh_token_required", "message": "no hay sesión renovable"})
		return
	}
	result, err := h.service.Refresh(c.Request.Context(), token, c.ClientIP(), c.Request.UserAgent())
	if err != nil {
		h.clearRefreshCookie(c)
		respondDomainError(c, err)
		return
	}
	h.respondSession(c, http.StatusOK, result)
}

func (h *AuthHandler) Logout(c *gin.Context) {
	token := h.refreshTokenFromRequest(c)
	if token == "" {
		h.clearRefreshCookie(c)
		c.JSON(http.StatusBadRequest, gin.H{"error": "refresh_token_required"})
		return
	}
	if err := h.service.Logout(c.Request.Context(), token); err != nil {
		h.clearRefreshCookie(c)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "error al cerrar sesión"})
		return
	}
	h.clearRefreshCookie(c)
	c.JSON(http.StatusOK, gin.H{"message": "sesión cerrada"})
}

func userResponse(u *domain.User) gin.H {
	return gin.H{
		"id": u.ID, "email": u.Email, "first_name": u.FirstName,
		"last_name": u.LastName, "status": u.Status,
	}
}

func authResponse(r *auth.AuthResult) gin.H {
	return gin.H{
		"user":         userResponse(r.User),
		"access_token": r.AccessToken,
		"expires_at":   r.ExpiresAt,
	}
}

func respondDomainError(c *gin.Context, err error) {
	// 2FA bloqueado por demasiados intentos: 429 con el tiempo de espera.
	var locked *twofactor.LockedError
	if errors.As(err, &locked) {
		respondTwoFALocked(c, locked)
		return
	}
	if de, ok := err.(*domain.DomainError); ok {
		status := http.StatusBadRequest
		switch de.Code {
		case "invalid_credentials", "invalid_refresh", "refresh_expired",
			"invalid_2fa_challenge", "2fa_challenge_expired", "2fa_required":
			status = http.StatusUnauthorized
		case "account_locked", "refresh_reuse_detected", "invalid_2fa_code",
			"email_not_verified", "account_deleted":
			status = http.StatusForbidden
		case "2fa_unavailable":
			status = http.StatusServiceUnavailable
		case "idempotency_key_reused", "idempotency_payload_mismatch":
			status = http.StatusConflict
		case "insufficient_funds":
			status = http.StatusUnprocessableEntity
		case "user_not_found", "reservation_not_found":
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": de.Code, "message": de.Message})
		return
	}
	log.Printf("[internal_error] %s %s: %v", c.Request.Method, c.FullPath(), err)
	c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "error interno"})
}
