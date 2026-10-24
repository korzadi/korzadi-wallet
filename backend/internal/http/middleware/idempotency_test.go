package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func idemRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/x", IdempotencyKeyRequired(), func(c *gin.Context) {
		k, _ := c.Get("idempotency_key")
		c.String(http.StatusOK, k.(string))
	})
	return r
}

func doIdem(key string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("{}"))
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	idemRouter().ServeHTTP(w, req)
	return w
}

func TestIdempotencyKeyRequired_Missing(t *testing.T) {
	if w := doIdem(""); w.Code != http.StatusBadRequest {
		t.Fatalf("sin header esperaba 400, obtuve %d", w.Code)
	}
}

func TestIdempotencyKeyRequired_AcceptsValid(t *testing.T) {
	w := doIdem("550e8400-e29b-41d4-a716-446655440000")
	if w.Code != http.StatusOK {
		t.Fatalf("clave válida esperaba 200, obtuve %d", w.Code)
	}
}

func TestIdempotencyKeyRequired_RejectsInvalid(t *testing.T) {
	cases := []string{
		"short",                  // < 8
		strings.Repeat("a", 129), // > 128
		"has space in it",        // charset
		"blockchain-deposit:BSC:USDT:0x1:0xabc:1", // prefijo reservado
		"WITHDRAWAL-SETTLE:abc12345",              // reservado, otra capitalización
		"korzadivpn:order-1",                      // reservado
	}
	for _, k := range cases {
		if w := doIdem(k); w.Code != http.StatusBadRequest {
			t.Errorf("clave %q esperaba 400, obtuve %d", k, w.Code)
		}
	}
}
