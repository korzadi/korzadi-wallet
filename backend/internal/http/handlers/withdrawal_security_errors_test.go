package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	bw "korzadi-wallet/backend/internal/application/blockchainwithdrawal"
	"korzadi-wallet/backend/internal/application/security"
)

func wdErr(err error) (int, map[string]any) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	respondWithdrawalError(c, err)
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return w.Code, body
}

func TestWithdrawalErrors_SecurityControlsMapToProperStatus(t *testing.T) {
	avail := time.Now().Add(time.Hour)
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"cooldown", fmt.Errorf("x: %w", &security.DestinationCooldownError{AvailableAt: avail, FirstRequest: true}), http.StatusForbidden, "new_destination_cooldown"},
		{"manual approval no es 202", security.ErrManualApprovalRequired, http.StatusForbidden, "manual_approval_required"},
		{"destino bloqueado", security.ErrDestinationBlocked, http.StatusForbidden, "authorization_denied"},
		{"velocidad", security.ErrRateLimited, http.StatusTooManyRequests, "rate_limited"},
		{"límite diario", security.ErrDailyLimitExceeded, http.StatusBadRequest, "daily_limit_exceeded"},
		{"freeze", security.ErrOperationFrozen, http.StatusServiceUnavailable, "operation_unavailable"},
		{"seguridad no disponible", fmt.Errorf("%w: redis", bw.ErrSecurityUnavailable), http.StatusServiceUnavailable, "security_unavailable"},
	}
	for _, tc := range cases {
		status, body := wdErr(tc.err)
		if status != tc.status || body["error"] != tc.code {
			t.Errorf("%s: %d %v, esperaba %d %q", tc.name, status, body, tc.status, tc.code)
		}
	}
}

func TestWithdrawalErrors_CooldownIncludesAvailableAt_AndNoInternalLeak(t *testing.T) {
	avail := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	_, body := wdErr(&security.DestinationCooldownError{AvailableAt: avail})
	if body["available_at"] != avail.Format(time.RFC3339) {
		t.Fatalf("available_at = %v", body["available_at"])
	}
	// Un error desconocido sigue siendo un 500 genérico.
	status, body := wdErr(errors.New("boom con detalles internos"))
	if status != http.StatusInternalServerError || body["error"] != "internal_error" {
		t.Fatalf("error desconocido: %d %v", status, body)
	}
}
