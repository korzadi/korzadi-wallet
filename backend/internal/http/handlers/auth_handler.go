package handlers

import (
	"errors"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"

	"korzadi-wallet/backend/internal/application/auth"
	"korzadi-wallet/backend/internal/application/twofactor"
	"korzadi-wallet/backend/internal/domain"
)

type AuthHandler struct {
	service *auth.Service
}

func NewAuthHandler(s *auth.Service) *AuthHandler {
	return &AuthHandler{service: s}
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
	c.JSON(http.StatusCreated, authResponse(result))
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
	c.JSON(http.StatusOK, authResponse(result))
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
	c.JSON(http.StatusOK, authResponse(result))
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

func (h *AuthHandler) Refresh(c *gin.Context) {
	var req refreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	result, err := h.service.Refresh(c.Request.Context(), req.RefreshToken, c.ClientIP(), c.Request.UserAgent())
	if err != nil {
		respondDomainError(c, err)
		return
	}
	c.JSON(http.StatusOK, authResponse(result))
}

func (h *AuthHandler) Logout(c *gin.Context) {
	var req refreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.service.Logout(c.Request.Context(), req.RefreshToken); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "error al cerrar sesión"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "sesión cerrada"})
}

func authResponse(r *auth.AuthResult) gin.H {
	return gin.H{
		"user": gin.H{
			"id": r.User.ID, "email": r.User.Email, "first_name": r.User.FirstName,
			"last_name": r.User.LastName, "status": r.User.Status,
		},
		"access_token":  r.AccessToken,
		"refresh_token": r.RefreshToken,
		"expires_at":    r.ExpiresAt,
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
		case "account_locked", "refresh_reuse_detected", "invalid_2fa_code":
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
