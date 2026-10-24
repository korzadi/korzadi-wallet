package middleware

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func hmacRouter(secret string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/x", ServerToServerHMAC(secret), func(c *gin.Context) { c.Status(http.StatusOK) })
	return r
}

func sign(secret, body string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(body))
	return hex.EncodeToString(m.Sum(nil))
}

func TestServerToServerHMAC_EmptySecret_FailsClosed(t *testing.T) {
	body := `{"user_id":"x"}`
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body))
	req.Header.Set("X-Korzadi-Signature", sign("", body)) // forgeable with an empty key
	w := httptest.NewRecorder()
	hmacRouter("").ServeHTTP(w, req)
	if w.Code == http.StatusOK {
		t.Fatal("con secreto vacío la petición NO debe pasar")
	}
}

func TestServerToServerHMAC_ValidAndInvalid(t *testing.T) {
	body := `{"a":1}`
	r := hmacRouter("s3cret-value-for-test")

	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body))
	req.Header.Set("X-Korzadi-Signature", sign("s3cret-value-for-test", body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("firma válida rechazada: %d", w.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body))
	req.Header.Set("X-Korzadi-Signature", sign("otro", body))
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("firma inválida debía dar 401, obtuve %d", w.Code)
	}
}
