package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	appfee "korzadi-wallet/backend/internal/application/fee"
)

func TestFeeHandler_Estimate_Validation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name string
		body string
		want int
	}{
		{"bad_json", "{", http.StatusBadRequest},
		{"bad_amount", `{"operation":"withdrawal","network":"BSC","asset":"USDT","amount":"abc"}`, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(rec)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/fees/estimate", strings.NewReader(tt.body))
			ctx.Request.Header.Set("Content-Type", "application/json")
			h := NewFeeHandler(nil)
			h.Estimate(ctx)
			if rec.Code != tt.want {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestFeeHandler_GetQuote_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/fees/quotes/invalid", nil)
	ctx.Params = gin.Params{{Key: "id", Value: "invalid"}}
	h := NewFeeHandler(nil)
	h.GetQuote(ctx)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestRespondFeeError_DoesNotLeakProviderDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	respondFeeError(ctx, appfee.ErrPriceUnavailable)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "provider") && strings.Contains(rec.Body.String(), "detail") {
		t.Fatalf("body may leak details: %s", rec.Body.String())
	}
}

func TestWalletHandler_BuyIsDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/buy", nil)
	h := NewWalletHandler(nil, nil, nil, nil)
	h.Buy(ctx)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "buy_not_available") {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}
