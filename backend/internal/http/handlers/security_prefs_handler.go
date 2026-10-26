package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/application/security/prefs"
)

type SecurityPrefsHandler struct {
	svc *prefs.Service
	confirmer interface {
		ConfirmPassword(ctx context.Context, userID uuid.UUID, password, ip string) error
	}
}

func NewSecurityPrefsHandler(svc *prefs.Service, confirmer interface{ ConfirmPassword(ctx context.Context, userID uuid.UUID, password, ip string) error }) *SecurityPrefsHandler {
	return &SecurityPrefsHandler{svc: svc, confirmer: confirmer}
}

func respondPrefsError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, prefs.ErrPinLocked):
		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "pin_locked", "message": "demasiados intentos de PIN; inténtalo más tarde"})
		return
	case errors.Is(err, prefs.ErrPinInvalid), errors.Is(err, prefs.ErrPinRequired), errors.Is(err, prefs.ErrPinLength):
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "pin_invalid", "message": err.Error()})
		return
	case errors.Is(err, prefs.ErrAppLockRequired), errors.Is(err, prefs.ErrBiometricAlready):
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "prefs_invalid", "message": err.Error()})
		return
	case errors.Is(err, prefs.ErrCredentialNotFound):
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "credential_not_found", "message": err.Error()})
		return
	default:
		respondDomainError(c, err)
		return
	}
}

type setupPinRequest struct {
	Pin           string `json:"pin" binding:"required"`
	CurrentPassword string `json:"current_password" binding:"required"`
}

type verifyPinRequest struct {
	Pin string `json:"pin" binding:"required"`
}

type disablePinRequest struct {
	CurrentPassword string `json:"current_password" binding:"required"`
}

type updatePrefsRequest struct {
	AppLockEnabled     bool   `json:"app_lock_enabled"`
	BiometricEnabled   bool   `json:"biometric_enabled"`
	EmailAlertsEnabled bool   `json:"email_alerts_enabled"`
	CurrentPassword    string `json:"current_password" binding:"required"`
}

// GET /api/security/prefs
func (h *SecurityPrefsHandler) GetPrefs(c *gin.Context) {
	p, err := h.svc.GetPrefs(c.Request.Context(), currentUserID(c))
	if err != nil {
		respondDomainError(c, err)
		return
	}
	c.JSON(http.StatusOK, h.svc.Sanitize(p))
}

// POST /api/security/pin/setup
func (h *SecurityPrefsHandler) SetupPin(c *gin.Context) {
	var req setupPinRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	if err := h.svc.SetupPin(c.Request.Context(), currentUserID(c), req.Pin, req.CurrentPassword, c.ClientIP()); err != nil {
		respondPrefsError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "PIN configurado"})
}

// POST /api/security/pin/verify
func (h *SecurityPrefsHandler) VerifyPin(c *gin.Context) {
	var req verifyPinRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	ok, err := h.svc.VerifyPin(c.Request.Context(), currentUserID(c), req.Pin)
	if err != nil {
		respondPrefsError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"valid": ok})
}

// POST /api/security/pin/disable
func (h *SecurityPrefsHandler) DisablePin(c *gin.Context) {
	var req disablePinRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	if err := h.svc.DisablePin(c.Request.Context(), currentUserID(c), req.CurrentPassword, c.ClientIP()); err != nil {
		respondPrefsError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "PIN eliminado"})
}

// POST /api/security/prefs
func (h *SecurityPrefsHandler) UpdatePrefs(c *gin.Context) {
	var req updatePrefsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	if h.confirmer != nil {
		if err := h.confirmer.ConfirmPassword(c.Request.Context(), currentUserID(c), req.CurrentPassword, c.ClientIP()); err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_current_password", "message": "contraseña actual incorrecta"})
			return
		}
	}
	if err := h.svc.UpdatePrefs(c.Request.Context(), currentUserID(c), req.AppLockEnabled, req.BiometricEnabled, req.EmailAlertsEnabled); err != nil {
		respondPrefsError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "preferencias actualizadas"})
}
