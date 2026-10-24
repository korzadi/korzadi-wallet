package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/infrastructure/postgres"
)

type NotificationHandler struct {
	repo *postgres.NotificationRepository
}

func NewNotificationHandler(repo *postgres.NotificationRepository) *NotificationHandler {
	return &NotificationHandler{repo: repo}
}

func (h *NotificationHandler) List(c *gin.Context) {
	limit, offset := parsePagination(c)
	list, err := h.repo.ListByUser(c.Request.Context(), currentUserID(c), limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "error al listar notificaciones"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"notifications": list})
}

func (h *NotificationHandler) MarkRead(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id inválido"})
		return
	}
	if err := h.repo.MarkRead(c.Request.Context(), id, currentUserID(c)); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "marcada como leída"})
}
