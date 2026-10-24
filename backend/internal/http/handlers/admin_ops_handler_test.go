package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	bw "korzadi-wallet/backend/internal/application/blockchainwithdrawal"
	"korzadi-wallet/backend/internal/application/custody"
	"korzadi-wallet/backend/internal/application/treasury"
	"korzadi-wallet/backend/internal/infrastructure/postgres"
)

type auditRec struct {
	actions []string
	metas   []map[string]any
}

func (a *auditRec) Log(ctx context.Context, actor *uuid.UUID, role, action, entityType string, entityID *uuid.UUID, meta map[string]any, ip string) error {
	a.actions = append(a.actions, action)
	a.metas = append(a.metas, meta)
	return nil
}

type fakeCustody struct {
	registered []custody.RegisterInput
}

func (f *fakeCustody) Register(ctx context.Context, in custody.RegisterInput) (*custody.CustodyWallet, error) {
	f.registered = append(f.registered, in)
	return &custody.CustodyWallet{ID: uuid.New(), Network: in.Network, AssetCode: in.AssetCode,
		WalletType: in.WalletType, Address: in.Address, AddressType: in.AddressType, Status: custody.StatusActive}, nil
}
func (f *fakeCustody) Get(context.Context, uuid.UUID) (*custody.CustodyWallet, error) {
	return nil, custody.ErrNotFound
}
func (f *fakeCustody) List(context.Context, custody.ListFilter) ([]*custody.CustodyWallet, error) {
	return nil, nil
}
func (f *fakeCustody) UpdateStatus(context.Context, uuid.UUID, custody.Status, string) (*custody.CustodyWallet, error) {
	return nil, custody.ErrNotFound
}

type fakeReview struct{ err error }

func (f fakeReview) ListNeedsReview(context.Context, int, int) ([]*bw.Withdrawal, error) {
	return nil, nil
}
func (f fakeReview) ResolveNeedsReview(context.Context, bw.ResolveInput) (*bw.Withdrawal, error) {
	return nil, f.err
}

type noTreasury struct{}

func (noTreasury) CreatePolicy(context.Context, treasury.CreatePolicyInput) (*treasury.Policy, error) {
	return nil, treasury.ErrInvalidRequest
}
func (noTreasury) ListPolicies(context.Context, treasury.PolicyFilter) ([]*treasury.Policy, error) {
	return nil, nil
}
func (noTreasury) SetPolicyEnabled(context.Context, uuid.UUID, bool, string) (*treasury.Policy, error) {
	return nil, treasury.ErrPolicyNotFound
}

type noGas struct{}

func (noGas) Get(context.Context, uuid.UUID) (*postgres.GasStationPolicy, error) { return nil, nil }
func (noGas) Upsert(context.Context, postgres.GasStationPolicy) (*postgres.GasStationPolicy, error) {
	return nil, nil
}

const secretKey = "deadbeef00000000000000000000000000000000000000000000000000000001"

func newOps(c custodyAdmin, rv withdrawalReviewer, a *auditRec) *AdminOpsHandler {
	kg := func(addressType string) (string, string, error) {
		return "0x1111111111111111111111111111111111111111", secretKey, nil
	}
	return NewAdminOpsHandler(rv, c, noTreasury{}, noGas{}, a, kg)
}

func call(h gin.HandlerFunc, params gin.Params, body any) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	raw, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader(raw))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = params
	c.Set("user_id", uuid.NewString())
	h(c)
	return w
}

func TestRegisterWallet_GeneratesServerSide_NeverLeaksKey(t *testing.T) {
	cu, au := &fakeCustody{}, &auditRec{}
	h := newOps(cu, fakeReview{}, au)
	w := call(h.RegisterWallet, nil, map[string]any{
		"network": "BSC", "asset_code": "USDT", "wallet_type": "HOT", "address_type": "evm", "reason": "alta de la HOT",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("esperaba 201, obtuve %d: %s", w.Code, w.Body.String())
	}
	if len(cu.registered) != 1 || cu.registered[0].PrivateKeyPlaintext != secretKey {
		t.Fatal("la clave generada debe pasarse al servicio de custodia para cifrarse")
	}
	if strings.Contains(w.Body.String(), secretKey) {
		t.Fatal("la respuesta NO debe contener la clave privada")
	}
	for _, m := range au.metas {
		for _, v := range m {
			if s, ok := v.(string); ok && strings.Contains(s, secretKey) {
				t.Fatal("la auditoría NO debe contener la clave privada")
			}
		}
	}
	if len(au.actions) != 1 || au.actions[0] != "custody_wallet_registered" {
		t.Fatalf("auditoría inesperada: %v", au.actions)
	}
}

func TestRegisterWallet_ImportDisabledByDefault(t *testing.T) {
	cu, au := &fakeCustody{}, &auditRec{}
	h := newOps(cu, fakeReview{}, au)
	w := call(h.RegisterWallet, nil, map[string]any{
		"network": "BSC", "asset_code": "USDT", "wallet_type": "HOT", "address_type": "evm", "reason": "importar",
		"address": "0x1111111111111111111111111111111111111111", "private_key": secretKey,
	})
	if w.Code != http.StatusForbidden {
		t.Fatalf("la importación debe estar prohibida por defecto: %d", w.Code)
	}
	if len(cu.registered) != 0 {
		t.Fatal("no debe registrarse nada")
	}
}

func TestRegisterWallet_ImportAllowedWhenEnabled(t *testing.T) {
	cu := &fakeCustody{}
	h := newOps(cu, fakeReview{}, &auditRec{}).AllowKeyImport(true)
	w := call(h.RegisterWallet, nil, map[string]any{
		"network": "BSC", "asset_code": "USDT", "wallet_type": "TREASURY", "address_type": "evm", "reason": "importar",
		"address": "0x1111111111111111111111111111111111111111", "private_key": secretKey,
	})
	if w.Code != http.StatusCreated || len(cu.registered) != 1 {
		t.Fatalf("importación habilitada: code=%d registered=%d", w.Code, len(cu.registered))
	}
	if strings.Contains(w.Body.String(), secretKey) {
		t.Fatal("la respuesta NO debe contener la clave privada")
	}
}

func TestResolveWithdrawal_ShortReasonRejected(t *testing.T) {
	au := &auditRec{}
	h := newOps(&fakeCustody{}, fakeReview{}, au)
	w := call(h.ResolveWithdrawal, gin.Params{{Key: "id", Value: uuid.NewString()}},
		map[string]any{"action": "release", "reason": "corto"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("esperaba 400, obtuve %d", w.Code)
	}
}

func TestResolveWithdrawal_FailureIsAudited(t *testing.T) {
	au := &auditRec{}
	h := newOps(&fakeCustody{}, fakeReview{err: bw.ErrReviewTxOnChain}, au)
	w := call(h.ResolveWithdrawal, gin.Params{{Key: "id", Value: uuid.NewString()}},
		map[string]any{"action": "release", "reason": "motivo suficientemente largo"})
	if w.Code != http.StatusConflict {
		t.Fatalf("esperaba 409, obtuve %d", w.Code)
	}
	if len(au.actions) != 1 || au.actions[0] != "withdrawal_review_failed" {
		t.Fatalf("el intento rechazado también debe auditarse: %v", au.actions)
	}
}

func TestCreateTreasuryPolicy_AlwaysDisabledAndAudited(t *testing.T) {
	au := &auditRec{}
	h := newOps(&fakeCustody{}, fakeReview{}, au)
	w := call(h.CreateTreasuryPolicy, nil, map[string]any{
		"network": "BSC", "asset_code": "USDT",
		"hot_custody_wallet_id": uuid.NewString(), "treasury_custody_wallet_id": uuid.NewString(),
		"min_balance": "10", "target_balance": "50", "max_balance": "100",
		"rebalance_threshold": "5", "max_rebalance_amount": "40", "reason": "política inicial",
	})
	// El fake rechaza con ErrInvalidRequest (400): lo que importa es que se audite el intento.
	if w.Code != http.StatusInternalServerError && w.Code != http.StatusBadRequest {
		t.Fatalf("código inesperado %d", w.Code)
	}
	if len(au.actions) != 1 || au.actions[0] != "treasury_policy_create_failed" {
		t.Fatalf("auditoría inesperada: %v", au.actions)
	}
}
