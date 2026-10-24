package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"korzadi-wallet/backend/internal/application/accountaccess"
)

// AccountHandler expone verificación de email, restablecimiento y cambio de
// contraseña. Los endpoints públicos responden SIEMPRE igual (202) exista o no
// la cuenta, para no permitir enumerar usuarios.
type AccountHandler struct {
	service *accountaccess.Service
}

func NewAccountHandler(s *accountaccess.Service) *AccountHandler {
	return &AccountHandler{service: s}
}

type tokenRequest struct {
	Token string `json:"token" binding:"required,max=200"`
}

type emailRequest struct {
	Email string `json:"email" binding:"required,email,max=255"`
}

type resetPasswordRequest struct {
	Token       string `json:"token" binding:"required,max=200"`
	NewPassword string `json:"new_password" binding:"required,max=256"`
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password" binding:"required,max=256"`
	NewPassword     string `json:"new_password" binding:"required,max=256"`
	TwoFACode       string `json:"two_fa_code" binding:"max=16"`
}

var genericAcceptedMsg = gin.H{"message": "si la cuenta existe, te enviamos un correo con las instrucciones"}

// POST /api/auth/verify-email
func (h *AccountHandler) VerifyEmail(c *gin.Context) {
	var req tokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": "token requerido"})
		return
	}
	if err := h.service.VerifyEmail(c.Request.Context(), req.Token, c.ClientIP()); err != nil {
		respondDomainError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "correo verificado"})
}

// POST /api/auth/resend-verification
func (h *AccountHandler) ResendVerification(c *gin.Context) {
	var req emailRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": "correo inválido"})
		return
	}
	h.service.ResendVerification(c.Request.Context(), req.Email, c.ClientIP())
	c.JSON(http.StatusAccepted, genericAcceptedMsg)
}

// POST /api/auth/forgot-password
func (h *AccountHandler) ForgotPassword(c *gin.Context) {
	var req emailRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": "correo inválido"})
		return
	}
	h.service.ForgotPassword(c.Request.Context(), req.Email, c.ClientIP())
	c.JSON(http.StatusAccepted, genericAcceptedMsg)
}

// POST /api/auth/reset-password
func (h *AccountHandler) ResetPassword(c *gin.Context) {
	var req resetPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": "token y nueva contraseña requeridos"})
		return
	}
	if err := h.service.ResetPassword(c.Request.Context(), req.Token, req.NewPassword, c.ClientIP()); err != nil {
		respondDomainError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "contraseña actualizada; inicia sesión con la nueva contraseña"})
}

// POST /api/account/change-password (requiere sesión)
func (h *AccountHandler) ChangePassword(c *gin.Context) {
	var req changePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": "contraseña actual y nueva requeridas"})
		return
	}
	userID := currentUserID(c)
	if err := h.service.ChangePassword(c.Request.Context(), userID, req.CurrentPassword, req.NewPassword, req.TwoFACode, c.ClientIP()); err != nil {
		respondDomainError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "contraseña actualizada; cerramos tus sesiones, inicia sesión de nuevo"})
}
