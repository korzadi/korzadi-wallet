package handlers

import (
	"context"
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"korzadi-wallet/backend/internal/domain"
)

type AdminHandler struct {
	pool  *pgxpool.Pool
	users domain.UserRepository
	audit domain.AuditLogger
	roles RoleAssigner
}

type RoleAssigner interface {
	AssignRole(ctx context.Context, userID uuid.UUID, roleName string) error
}

func NewAdminHandler(pool *pgxpool.Pool, users domain.UserRepository, audit domain.AuditLogger, roles RoleAssigner) *AdminHandler {
	return &AdminHandler{pool: pool, users: users, audit: audit, roles: roles}
}

func (h *AdminHandler) ListUsers(c *gin.Context) {
	limit, offset := parsePagination(c)
	rows, err := h.pool.Query(c.Request.Context(), `
		SELECT id, email, first_name, last_name, status, created_at
		FROM users ORDER BY created_at DESC LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "error al listar usuarios"})
		return
	}
	defer rows.Close()

	var users []gin.H
	for rows.Next() {
		var id uuid.UUID
		var email, firstName, lastName, status string
		var createdAt any
		if err := rows.Scan(&id, &email, &firstName, &lastName, &status, &createdAt); err != nil {
			continue
		}
		users = append(users, gin.H{"id": id, "email": email, "first_name": firstName,
			"last_name": lastName, "status": status, "created_at": createdAt})
	}
	c.JSON(http.StatusOK, gin.H{"users": users})
}

func (h *AdminHandler) BlockUser(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id inválido"})
		return
	}
	user, err := h.users.FindByID(c.Request.Context(), id)
	if err != nil || user == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "usuario no encontrado"})
		return
	}
	actorID := currentUserID(c)
	// AUDIT FIX (MEDIUM): an admin could block a super_admin (who alone can
	// unblock) or lock themselves out. Blocking a super_admin requires being one.
	if id == actorID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no puedes bloquear tu propia cuenta"})
		return
	}
	targetIsSuper, terr := h.hasRole(c.Request.Context(), id, "super_admin")
	if terr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no se pudo verificar el rol del usuario"})
		return
	}
	if targetIsSuper {
		actorIsSuper, aerr := h.hasRole(c.Request.Context(), actorID, "super_admin")
		if aerr != nil || !actorIsSuper {
			c.JSON(http.StatusForbidden, gin.H{"error": "solo un super_admin puede bloquear a otro super_admin"})
			return
		}
	}
	user.Status = domain.UserStatusBlocked
	if err := h.users.Update(c.Request.Context(), user); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no se pudo bloquear al usuario"})
		return
	}
	// AUDIT FIX: blocking must also kill every live refresh token, otherwise
	// the user keeps minting access tokens until the sessions expire.
	if _, err := h.pool.Exec(c.Request.Context(),
		`UPDATE sessions SET revoked = TRUE WHERE user_id = $1 AND revoked = FALSE`, id); err != nil {
		log.Printf("admin: no se pudieron revocar las sesiones del usuario bloqueado %s: %v", id, err)
	}
	if h.audit != nil {
		_ = h.audit.Log(c.Request.Context(), &actorID, "admin", "user_blocked", "user", &id, nil, c.ClientIP())
	}
	c.JSON(http.StatusOK, gin.H{"message": "usuario bloqueado"})
}

type unblockRequest struct {
	Reason string `json:"reason" binding:"required,min=5,max=500"`
}

// UnblockUser reactiva una cuenta BLOQUEADA. Exige un motivo (queda en la
// auditoría), no toca cuentas borradas ni activas, y respeta la verificación
// de correo: si el correo nunca se verificó, la cuenta vuelve a
// pending_verification (no se salta la verificación).
func (h *AdminHandler) UnblockUser(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id inválido"})
		return
	}
	var req unblockRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "reason_required", "message": "indica el motivo del desbloqueo (5 a 500 caracteres)"})
		return
	}
	user, err := h.users.FindByID(c.Request.Context(), id)
	if err != nil || user == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "usuario no encontrado"})
		return
	}
	switch user.Status {
	case domain.UserStatusBlocked:
		// ok
	case domain.UserStatusDeleted:
		c.JSON(http.StatusConflict, gin.H{"error": "user_deleted", "message": "una cuenta eliminada no puede reactivarse"})
		return
	default:
		c.JSON(http.StatusConflict, gin.H{"error": "user_not_blocked", "message": "la cuenta no está bloqueada"})
		return
	}

	previous := user.Status
	if user.EmailVerified {
		user.Status = domain.UserStatusActive
	} else {
		user.Status = domain.UserStatusPendingVerification
	}
	user.LockedUntil = nil
	if err := h.users.Update(c.Request.Context(), user); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no se pudo reactivar al usuario"})
		return
	}
	_ = h.users.ResetFailedLogin(c.Request.Context(), id)

	actorID := currentUserID(c)
	if h.audit != nil {
		_ = h.audit.Log(c.Request.Context(), &actorID, "super_admin", "user_unblocked", "user", &id,
			map[string]any{"reason": req.Reason, "previous_status": string(previous), "new_status": string(user.Status)},
			c.ClientIP())
	}
	c.JSON(http.StatusOK, gin.H{"message": "usuario reactivado", "status": user.Status})
}

// emailVerifierStore lo implementa el repositorio de usuarios de Postgres.
type emailVerifierStore interface {
	MarkEmailVerified(ctx context.Context, id uuid.UUID) error
}

// ManualVerifyEmail permite a un super_admin verificar el correo de una cuenta
// (y activarla si estaba pending_verification) cuando el correo no puede
// entregarse. Exige un motivo y queda en auditoría. No toca cuentas
// bloqueadas ni eliminadas.
func (h *AdminHandler) ManualVerifyEmail(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id inválido"})
		return
	}
	var req unblockRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "reason_required", "message": "indica el motivo (5 a 500 caracteres)"})
		return
	}
	user, err := h.users.FindByID(c.Request.Context(), id)
	if err != nil || user == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "usuario no encontrado"})
		return
	}
	if user.Status == domain.UserStatusDeleted || user.Status == domain.UserStatusBlocked {
		c.JSON(http.StatusConflict, gin.H{"error": "user_not_eligible", "message": "la cuenta está bloqueada o eliminada"})
		return
	}
	if user.EmailVerified {
		c.JSON(http.StatusConflict, gin.H{"error": "already_verified", "message": "el correo ya está verificado"})
		return
	}
	store, ok := h.users.(emailVerifierStore)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "operación no soportada"})
		return
	}
	if err := store.MarkEmailVerified(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no se pudo verificar el correo"})
		return
	}
	actorID := currentUserID(c)
	if h.audit != nil {
		_ = h.audit.Log(c.Request.Context(), &actorID, "super_admin", "email_manually_verified", "user", &id,
			map[string]any{"reason": req.Reason, "previous_status": string(user.Status)}, c.ClientIP())
	}
	c.JSON(http.StatusOK, gin.H{"message": "correo verificado manualmente"})
}

type assignRoleRequest struct {
	Role string `json:"role" binding:"required"`
}

func (h *AdminHandler) AssignRole(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id inválido"})
		return
	}
	var req assignRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	allowedRoles := map[string]bool{"user": true, "affiliate": true, "admin": true, "super_admin": true}
	if !allowedRoles[req.Role] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "rol inválido"})
		return
	}
	if err := h.roles.AssignRole(c.Request.Context(), id, req.Role); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no se pudo asignar el rol"})
		return
	}
	actorID := currentUserID(c)
	if h.audit != nil {
		_ = h.audit.Log(c.Request.Context(), &actorID, "super_admin", "role_assigned", "user", &id,
			map[string]any{"role": req.Role}, c.ClientIP())
	}
	c.JSON(http.StatusOK, gin.H{"message": "rol asignado"})
}

func (h *AdminHandler) ListTransactions(c *gin.Context) {
	limit, offset := parsePagination(c)
	rows, err := h.pool.Query(c.Request.Context(), `
		SELECT id, type, status, idempotency_key, reference_id, created_at, completed_at
		FROM ledger_transactions ORDER BY created_at DESC LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "error al listar transacciones"})
		return
	}
	defer rows.Close()

	var txs []gin.H
	for rows.Next() {
		var id uuid.UUID
		var typ, status, idemKey string
		var refID *string
		var createdAt, completedAt any
		if err := rows.Scan(&id, &typ, &status, &idemKey, &refID, &createdAt, &completedAt); err != nil {
			continue
		}
		txs = append(txs, gin.H{"id": id, "type": typ, "status": status, "idempotency_key": idemKey,
			"reference_id": refID, "created_at": createdAt, "completed_at": completedAt})
	}
	c.JSON(http.StatusOK, gin.H{"transactions": txs})
}

func (h *AdminHandler) ListAudit(c *gin.Context) {
	limit, offset := parsePagination(c)
	rows, err := h.pool.Query(c.Request.Context(), `
		SELECT id, actor_user_id, actor_role, action, entity_type, entity_id, ip_address, created_at
		FROM audit_logs ORDER BY created_at DESC LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "error al listar auditoría"})
		return
	}
	defer rows.Close()

	var logs []gin.H
	for rows.Next() {
		var id uuid.UUID
		var actorID *uuid.UUID
		var actorRole, action string
		var entityType *string
		var entityID *uuid.UUID
		var ip *string
		var createdAt any
		if err := rows.Scan(&id, &actorID, &actorRole, &action, &entityType, &entityID, &ip, &createdAt); err != nil {
			continue
		}
		logs = append(logs, gin.H{"id": id, "actor_user_id": actorID, "actor_role": actorRole,
			"action": action, "entity_type": entityType, "entity_id": entityID, "ip": ip, "created_at": createdAt})
	}
	c.JSON(http.StatusOK, gin.H{"audit_logs": logs})
}

func parsePagination(c *gin.Context) (limit, offset int) {
	limit, _ = strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ = strconv.Atoi(c.DefaultQuery("offset", "0"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	return
}

// hasRole reports whether the user currently holds the named role.
func (h *AdminHandler) hasRole(ctx context.Context, userID uuid.UUID, role string) (bool, error) {
	var ok bool
	err := h.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM user_roles ur JOIN roles r ON r.id = ur.role_id
			WHERE ur.user_id = $1 AND r.name = $2
		)`, userID, role).Scan(&ok)
	return ok, err
}
