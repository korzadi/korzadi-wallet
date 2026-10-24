package middleware

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
)

// ServerToServerHMAC valida que la petición venga firmada con el secreto compartido,
// usado por integraciones externas como KorzadiVPN (nunca para usuarios finales).
// Header esperado: X-Korzadi-Signature: hex(hmac_sha256(body, secret))
func ServerToServerHMAC(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// AUDIT FIX (HIGH): with an empty secret HMAC-SHA256 is still computable by
		// anyone (key = ""), which would let an attacker forge a "valid"
		// signature and charge users' balances. Fail closed when unconfigured.
		if secret == "" {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "integración no configurada"})
			return
		}
		sig := c.GetHeader("X-Korzadi-Signature")
		if sig == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "firma faltante"})
			return
		}
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "cuerpo inválido"})
			return
		}
		// Restaurar el body para que el handler siguiente pueda leerlo.
		c.Request.Body = io.NopCloser(bytes.NewReader(body))

		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		expected := hex.EncodeToString(mac.Sum(nil))

		if !hmac.Equal([]byte(sig), []byte(expected)) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "firma inválida"})
			return
		}
		c.Next()
	}
}
