package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type SecurityBiometricHandler struct {
	repo interface {
		CreateChallenge(ctx context.Context, userID uuid.UUID, kind, challenge string, expiresAt string) error
		ConsumeChallenge(ctx context.Context, challenge string) (userID uuid.UUID, kind string, sessionJSON string, err error)
	}
}

func NewSecurityBiometricHandler(repo interface{CreateChallenge(context.Context, uuid.UUID, string, string, string) error; ConsumeChallenge(context.Context, string) (uuid.UUID, string, string, error)}) *SecurityBiometricHandler {
	return &SecurityBiometricHandler{repo: repo}
}

type biometricStartRequest struct{}

type biometricVerifyRequest struct {
	Challenge string `json:"challenge" binding:"required"`
}

func (h *SecurityBiometricHandler) Start(c *gin.Context) {
	// For simplicity, return a server-side challenge to the client.
	challenge := "biometric-challenge-" + uuid.New().String()[:16]
	expiresAt := time.Now().Add(5 * time.Minute).Format("2006-01-02T15:04:05.000000Z")
	if err := h.repo.CreateChallenge(c.Request.Context(), currentUserID(c), "authentication", challenge, expiresAt); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no se pudo iniciar la verificación biométrica"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"challenge": challenge, "message": "Verificación biométrica iniciada. Usa tu dispositivo para confirmar."})
}

func (h *SecurityBiometricHandler) Verify(c *gin.Context) {
	var req biometricVerifyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	_, _, _, err := h.repo.ConsumeChallenge(c.Request.Context(), req.Challenge)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "biometric_verification_failed", "message": "verificación biométrica fallida"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "verificación biométrica confirmada"})
}
