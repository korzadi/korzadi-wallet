package handlers

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image/png"
	"net/http"
	"strconv"
	"time"

	"github.com/boombuler/barcode"
	"github.com/boombuler/barcode/qr"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/application/twofactor"
	"korzadi-wallet/backend/internal/domain"
)

type TwoFactorHandler struct {
	service   *twofactor.Service
	users     domain.UserRepository
	passwords PasswordConfirmer
	audit     domain.AuditLogger
	sessions  sessionRevoker
}

// PasswordConfirmer comprueba la contraseña actual (con bloqueo por intentos).
// accountaccess.Service lo implementa.
type PasswordConfirmer interface {
	ConfirmPassword(ctx context.Context, userID uuid.UUID, password, ip string) error
}

type sessionRevoker interface {
	RevokeAllForUser(ctx context.Context, userID uuid.UUID) error
}

// WithPasswordConfirmer habilita desactivar 2FA y regenerar códigos (exigen contraseña).
func (h *TwoFactorHandler) WithPasswordConfirmer(p PasswordConfirmer) *TwoFactorHandler {
	h.passwords = p
	return h
}

func (h *TwoFactorHandler) WithAudit(a domain.AuditLogger) *TwoFactorHandler { h.audit = a; return h }

func (h *TwoFactorHandler) WithSessions(s sessionRevoker) *TwoFactorHandler { h.sessions = s; return h }

// respondTwoFALocked responde 429 con el tiempo de espera cuando la verificación está bloqueada.
func respondTwoFALocked(c *gin.Context, le *twofactor.LockedError) {
	secs := int(le.RetryAfter(time.Now()).Seconds()) + 1
	c.Header("Retry-After", strconv.Itoa(secs))
	c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
		"error":               "2fa_locked",
		"message":             "demasiados códigos 2FA incorrectos; la verificación está bloqueada temporalmente",
		"retry_after_seconds": secs,
	})
}

// respondTwoFAError traduce errores del 2FA (bloqueo => 429) y delega el resto.
func respondTwoFAError(c *gin.Context, err error) {
	var le *twofactor.LockedError
	if errors.As(err, &le) {
		respondTwoFALocked(c, le)
		return
	}
	respondDomainError(c, err)
}

func NewTwoFactorHandler(s *twofactor.Service, users domain.UserRepository) *TwoFactorHandler {
	return &TwoFactorHandler{service: s, users: users}
}

// POST /api/security/2fa/setup — genera secreto + códigos de recuperación (una sola vez visibles).
func (h *TwoFactorHandler) Setup(c *gin.Context) {
	user, err := h.users.FindByID(c.Request.Context(), currentUserID(c))
	if err != nil || user == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "usuario no encontrado"})
		return
	}
	result, err := h.service.BeginSetup(c.Request.Context(), currentUserID(c), user.Email)
	if err != nil {
		respondDomainError(c, err)
		return
	}
	// Claves nuevas (las que lee el frontend) + las históricas, por compatibilidad.
	qrCode, qrErr := qrDataURL(result.OTPAuthURL)
	resp := gin.H{
		"secret":         result.SecretBase32,
		"otpauth_url":    result.OTPAuthURL,
		"recovery_codes": result.RecoveryCodes,
		"SecretBase32":   result.SecretBase32,
		"OTPAuthURL":     result.OTPAuthURL,
		"RecoveryCodes":  result.RecoveryCodes,
	}
	if qrErr == nil {
		resp["qr_code"] = qrCode
		resp["qr_note"] = "Escanea este código con Google Authenticator o Authy, o usa la clave manual."
	}
	c.JSON(http.StatusOK, resp)
}

type confirmRequest struct {
	Code string `json:"code" binding:"required"`
}

// POST /api/security/2fa/confirm — activa 2FA tras validar el primer código.
func (h *TwoFactorHandler) Confirm(c *gin.Context) {
	var req confirmRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.service.Confirm(c.Request.Context(), currentUserID(c), req.Code); err != nil {
		respondTwoFAError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "2FA activado"})
}

// TwoFAVerifyMiddleware exige y valida el header X-2FA-Code en operaciones sensibles
// (ej. retiros), y coloca el resultado en el contexto como "two_fa_verified".
func TwoFAVerifyMiddleware(service *twofactor.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		code := c.GetHeader("X-2FA-Code")
		if code == "" {
			c.Set("two_fa_verified", false)
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": domain.Err2FARequired.Code, "message": domain.Err2FARequired.Message,
			})
			return
		}
		ok, err := service.Verify(c.Request.Context(), currentUserID(c), code)
		var le *twofactor.LockedError
		if errors.As(err, &le) {
			c.Set("two_fa_verified", false)
			respondTwoFALocked(c, le)
			return
		}
		if err != nil || !ok {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "invalid_2fa_code", "message": "código 2FA inválido"})
			return
		}
		c.Set("two_fa_verified", true)
		c.Next()
	}
}

// GET /api/security/2fa/status — si el 2FA está activo y cuántos códigos de recuperación quedan.
func (h *TwoFactorHandler) Status(c *gin.Context) {
	enabled, remaining, err := h.service.Status(c.Request.Context(), currentUserID(c))
	if err != nil {
		respondDomainError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"enabled": enabled, "recovery_codes_remaining": remaining})
}

type twoFAActionRequest struct {
	Password string `json:"password" binding:"required,max=256"`
	Code     string `json:"code" binding:"required,max=32"`
}

// confirmIdentity exige contraseña actual Y un código 2FA válido (TOTP o de recuperación).
func (h *TwoFactorHandler) bindAndConfirmPassword(c *gin.Context) (twoFAActionRequest, bool) {
	var req twoFAActionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": "contraseña y código 2FA requeridos"})
		return req, false
	}
	if h.passwords == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "unavailable"})
		return req, false
	}
	if err := h.passwords.ConfirmPassword(c.Request.Context(), currentUserID(c), req.Password, c.ClientIP()); err != nil {
		respondDomainError(c, err)
		return req, false
	}
	return req, true
}

// POST /api/security/2fa/disable {password, code}
func (h *TwoFactorHandler) Disable(c *gin.Context) {
	req, ok := h.bindAndConfirmPassword(c)
	if !ok {
		return
	}
	uid := currentUserID(c)
	if err := h.service.Disable(c.Request.Context(), uid, req.Code); err != nil {
		respondTwoFAError(c, err)
		return
	}
	h.auditLog(c, uid, "two_factor_disabled", nil)
	c.JSON(http.StatusOK, gin.H{"message": "2FA desactivado; mientras no lo actives de nuevo no podrás retirar fondos"})
}

// POST /api/security/2fa/recovery-codes/regenerate {password, code}
func (h *TwoFactorHandler) RegenerateRecoveryCodes(c *gin.Context) {
	req, ok := h.bindAndConfirmPassword(c)
	if !ok {
		return
	}
	uid := currentUserID(c)
	codes, err := h.service.RegenerateRecoveryCodes(c.Request.Context(), uid, req.Code)
	if err != nil {
		respondTwoFAError(c, err)
		return
	}
	h.auditLog(c, uid, "two_factor_recovery_codes_regenerated", nil)
	c.JSON(http.StatusOK, gin.H{"recovery_codes": codes,
		"message": "guarda estos códigos en un lugar seguro: solo se muestran ahora y cada uno sirve una vez"})
}

// POST /api/admin/users/:id/reset-2fa {reason} — soporte (super_admin): el usuario perdió
// el dispositivo y los códigos. Borra su 2FA y cierra sus sesiones.
func (h *TwoFactorHandler) AdminReset(c *gin.Context) {
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
	if user, uerr := h.users.FindByID(c.Request.Context(), id); uerr != nil || user == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "usuario no encontrado"})
		return
	}
	if err := h.service.AdminReset(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no se pudo restablecer el 2FA"})
		return
	}
	if h.sessions != nil {
		_ = h.sessions.RevokeAllForUser(c.Request.Context(), id)
	}
	actor := currentUserID(c)
	if h.audit != nil {
		_ = h.audit.Log(c.Request.Context(), &actor, "super_admin", "two_factor_admin_reset", "user", &id,
			map[string]any{"reason": req.Reason}, c.ClientIP())
	}
	c.JSON(http.StatusOK, gin.H{"message": "2FA restablecido; el usuario debe volver a activarlo para retirar"})
}

func (h *TwoFactorHandler) auditLog(c *gin.Context, userID uuid.UUID, action string, md map[string]any) {
	if h.audit != nil {
		_ = h.audit.Log(c.Request.Context(), &userID, "user", action, "user", &userID, md, c.ClientIP())
	}
}

// qrDataURL genera una imagen PNG del código QR para la URL otpauth y la
// devuelve como data URI para incrustar directamente en el frontend.
func qrDataURL(content string) (string, error) {
	bc, err := qr.Encode(content, qr.M, qr.Auto)
	if err != nil {
		return "", err
	}
	bc, err = barcode.Scale(bc, 300, 300)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, bc); err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}
