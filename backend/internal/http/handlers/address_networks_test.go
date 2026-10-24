package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"korzadi-wallet/backend/internal/domain"
)

func newNetRouter(h *AddressHandler, c *CryptoHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/addresses", h.Create)
	r.GET("/networks", h.ActiveNetworks)
	r.GET("/tron", c.GetDepositAddress)
	return r
}

// A network without a running watcher must be refused BEFORE any key is
// generated (the nil service proves the service is never reached).
func TestAddressCreate_NetworkWithoutWatcher_Rejected(t *testing.T) {
	h := NewAddressHandler(nil)
	h.SetAllowedNetworks([]domain.Network{domain.NetworkBSC, domain.NetworkETHEREUM})
	r := newNetRouter(h, NewCryptoHandler(nil))

	for _, network := range []string{"BITCOIN", "TRON", "POLYGON"} {
		req := httptest.NewRequest(http.MethodPost, "/addresses", strings.NewReader(`{"network":"`+network+`"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: esperaba 400, obtuve %d", network, w.Code)
		}
		if !strings.Contains(w.Body.String(), "red no habilitada") {
			t.Fatalf("%s: cuerpo inesperado: %s", network, w.Body.String())
		}
	}
}

func TestActiveNetworks_ListsOnlyWatchedNetworks(t *testing.T) {
	h := NewAddressHandler(nil)
	h.SetAllowedNetworks([]domain.Network{domain.NetworkETHEREUM, domain.NetworkBSC})
	r := newNetRouter(h, NewCryptoHandler(nil))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/networks", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("esperaba 200, obtuve %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"BSC"`) || !strings.Contains(body, `"ETHEREUM"`) {
		t.Fatalf("faltan redes activas: %s", body)
	}
	if strings.Contains(body, "BITCOIN") || strings.Contains(body, "TRON") {
		t.Fatalf("no debe listar redes sin watcher: %s", body)
	}
}

func TestTronDepositAddress_Unavailable(t *testing.T) {
	r := newNetRouter(NewAddressHandler(nil), NewCryptoHandler(nil))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/tron", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("esperaba 503, obtuve %d", w.Code)
	}
}
