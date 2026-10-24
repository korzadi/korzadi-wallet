package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	appfee "korzadi-wallet/backend/internal/application/fee"
)

func TestFeeHandlerRejectsMalformedFinancialRequestsBeforeCallingEngine(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct{ name, body string }{
		{name: "invalid json", body: "{"},
		{name: "invalid amount", body: `{"operation":"withdrawal","network":"BSC","asset":"USDT","amount":"NaN"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(rec)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/wallet/fees/estimate", strings.NewReader(tc.body))
			NewFeeHandler(nil).Estimate(ctx)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestFeeHandlerQuoteLookupAndProviderFailureAreSafe(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/wallet/fees/quotes/not-a-uuid", nil)
	ctx.Params = gin.Params{{Key: "id", Value: "not-a-uuid"}}
	NewFeeHandler(nil).GetQuote(ctx)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("invalid quote id status=%d", rec.Code)
	}

	rec = httptest.NewRecorder()
	ctx, _ = gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	respondFeeError(ctx, errors.Join(appfee.ErrPriceUnavailable, errors.New("provider detail must not be reflected")))
	if rec.Code != http.StatusServiceUnavailable || strings.Contains(rec.Body.String(), "provider detail") {
		t.Fatalf("unsafe provider failure response: status=%d body=%s", rec.Code, rec.Body.String())
	}
}
