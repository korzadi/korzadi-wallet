package custody

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/domain"
)

func newTestService() (*Service, *fakeRepository, *fakeEncryptor, *fakeAuditLogger) {
	repo := newFakeRepository()
	enc := &fakeEncryptor{}
	audit := &fakeAuditLogger{}
	return NewService(repo, enc, audit, nil), repo, enc, audit
}

func validHotInput() RegisterInput {
	return RegisterInput{
		Network:             domain.Network("BSC"),
		AssetCode:           "USDT",
		WalletType:          WalletTypeHot,
		Address:             "0x000000000000000000000000000000000000dead",
		AddressType:         "evm",
		PrivateKeyPlaintext: "super-secret-hex-key",
		Actor:               "admin:test",
	}
}

// ---- CREATE ----

func TestRegister_CreatesHotWallet(t *testing.T) {
	svc, _, enc, audit := newTestService()

	w, err := svc.Register(context.Background(), validHotInput())
	if err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}
	if w.WalletType != WalletTypeHot {
		t.Errorf("wallet_type = %s, esperaba HOT", w.WalletType)
	}
	if w.Status != StatusActive {
		t.Errorf("status inicial = %s, esperaba active", w.Status)
	}
	if enc.calls != 1 {
		t.Errorf("Encrypt debe llamarse exactamente una vez, se llamó %d", enc.calls)
	}
	if len(audit.events) != 1 || audit.events[0].Action != "custody_wallet_created" {
		t.Errorf("esperaba un evento custody_wallet_created, tengo %+v", audit.events)
	}
}

func TestRegister_CreatesGasWallet(t *testing.T) {
	svc, _, _, _ := newTestService()
	in := validHotInput()
	in.WalletType = WalletTypeGas
	in.AssetCode = "BNB"
	in.Address = "0x00000000000000000000000000000000000000f1"

	w, err := svc.Register(context.Background(), in)
	if err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}
	if w.WalletType != WalletTypeGas {
		t.Errorf("wallet_type = %s, esperaba GAS", w.WalletType)
	}
	if w.AssetCode != "BNB" {
		t.Errorf("asset_code = %s, esperaba BNB", w.AssetCode)
	}
}

func TestRegister_CreatesTreasuryWallet(t *testing.T) {
	svc, _, _, _ := newTestService()
	in := validHotInput()
	in.WalletType = WalletTypeTreasury
	in.Address = "0x00000000000000000000000000000000000000f2"

	w, err := svc.Register(context.Background(), in)
	if err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}
	if w.WalletType != WalletTypeTreasury {
		t.Errorf("wallet_type = %s, esperaba TREASURY", w.WalletType)
	}
}

func TestRegister_DifferentCombosCoexist(t *testing.T) {
	// spec: HOT/BSC/USDT, HOT/TRON/USDT, GAS/BSC/BNB y TREASURY/BSC/USDT
	// deben poder coexistir sin chocar entre sí.
	svc, _, _, _ := newTestService()

	combos := []RegisterInput{
		{Network: "BSC", AssetCode: "USDT", WalletType: WalletTypeHot, Address: "0x000000000000000000000000000000000000a001", AddressType: "evm", PrivateKeyPlaintext: "k1"},
		{Network: "TRON", AssetCode: "USDT", WalletType: WalletTypeHot, Address: "TA0000000000000000000000000000a002", AddressType: "tron", PrivateKeyPlaintext: "k2"},
		{Network: "BSC", AssetCode: "BNB", WalletType: WalletTypeGas, Address: "0x000000000000000000000000000000000000a003", AddressType: "evm", PrivateKeyPlaintext: "k3"},
		{Network: "BSC", AssetCode: "USDT", WalletType: WalletTypeTreasury, Address: "0x000000000000000000000000000000000000a004", AddressType: "evm", PrivateKeyPlaintext: "k4"},
	}
	for i, in := range combos {
		if _, err := svc.Register(context.Background(), in); err != nil {
			t.Fatalf("combo %d (%s/%s/%s): no esperaba error: %v", i, in.WalletType, in.Network, in.AssetCode, err)
		}
	}
}

// ---- VALIDATION ----

func TestRegister_RejectsInvalidWalletType(t *testing.T) {
	svc, _, _, _ := newTestService()
	in := validHotInput()
	in.WalletType = WalletType("COLD") // not implemented yet, per spec

	_, err := svc.Register(context.Background(), in)
	if !errors.Is(err, ErrInvalidWalletType) {
		t.Fatalf("error = %v, esperaba ErrInvalidWalletType", err)
	}
}

func TestRegister_RejectsInvalidAddressType(t *testing.T) {
	svc, _, _, _ := newTestService()
	in := validHotInput()
	in.AddressType = "solana"

	_, err := svc.Register(context.Background(), in)
	if !errors.Is(err, ErrInvalidAddressType) {
		t.Fatalf("error = %v, esperaba ErrInvalidAddressType", err)
	}
}

func TestRegister_RejectsUnknownNetwork(t *testing.T) {
	svc, _, _, _ := newTestService()
	in := validHotInput()
	in.Network = "DOGECHAIN"

	_, err := svc.Register(context.Background(), in)
	if !errors.Is(err, ErrUnknownNetwork) {
		t.Fatalf("error = %v, esperaba ErrUnknownNetwork", err)
	}
}

func TestRegister_RejectsUnknownAsset(t *testing.T) {
	svc, _, _, _ := newTestService()
	in := validHotInput()
	in.AssetCode = "DOGE"

	_, err := svc.Register(context.Background(), in)
	if !errors.Is(err, ErrUnknownAsset) {
		t.Fatalf("error = %v, esperaba ErrUnknownAsset", err)
	}
}

func TestRegister_RejectsInvalidAddress(t *testing.T) {
	svc, _, _, _ := newTestService()

	cases := []string{
		"not-an-address",
		"0x123", // too short
		" 0x000000000000000000000000000000000000dead",
		"0x0000000000000000000000000000000000000000000000000000000000000000000000000000000000000", // >64 chars
	}
	for _, addr := range cases {
		in := validHotInput()
		in.Address = addr
		if _, err := svc.Register(context.Background(), in); !errors.Is(err, ErrInvalidAddress) {
			t.Errorf("address %q: error = %v, esperaba ErrInvalidAddress", addr, err)
		}
	}
}

func TestRegister_RejectsBadEIP55Checksum(t *testing.T) {
	svc, _, _, _ := newTestService()
	in := validHotInput()
	// Mixed-case but wrong checksum.
	in.Address = "0x000000000000000000000000000000000000dEad"
	if _, err := svc.Register(context.Background(), in); !errors.Is(err, ErrInvalidAddress) {
		t.Fatalf("error = %v, esperaba ErrInvalidAddress (checksum EIP-55)", err)
	}
}

func TestRegister_RejectsEmptyPrivateKey(t *testing.T) {
	svc, _, _, _ := newTestService()
	in := validHotInput()
	in.PrivateKeyPlaintext = "   "

	_, err := svc.Register(context.Background(), in)
	if !errors.Is(err, ErrEmptyPrivateKey) {
		t.Fatalf("error = %v, esperaba ErrEmptyPrivateKey", err)
	}
}

func TestRegister_EnforcesNetworkAssetPairing_WhenValidatorWired(t *testing.T) {
	repo := newFakeRepository()
	enc := &fakeEncryptor{}
	assets := newFakeAssetValidator("BSC|USDT") // BNB on BSC NOT enabled
	svc := NewService(repo, enc, nil, assets)

	in := validHotInput()
	in.WalletType = WalletTypeGas
	in.AssetCode = "BNB"

	_, err := svc.Register(context.Background(), in)
	if !errors.Is(err, ErrAssetNotEnabledOnNetwork) {
		t.Fatalf("error = %v, esperaba ErrAssetNotEnabledOnNetwork", err)
	}
}

// ---- DUPLICATES ----

func TestRegister_RejectsDuplicateAddress(t *testing.T) {
	svc, _, _, _ := newTestService()
	in := validHotInput()

	if _, err := svc.Register(context.Background(), in); err != nil {
		t.Fatalf("primer registro: no esperaba error: %v", err)
	}

	// Same address, even for a different wallet_type/asset — must still
	// be rejected: an address is never registered twice.
	dup := in
	dup.WalletType = WalletTypeTreasury
	dup.AssetCode = "USDT"
	_, err := svc.Register(context.Background(), dup)
	if !errors.Is(err, ErrDuplicateAddress) {
		t.Fatalf("error = %v, esperaba ErrDuplicateAddress", err)
	}
}

// ---- STATUS ----

func TestUpdateStatus_AllowsActiveToDisabledAndBack(t *testing.T) {
	svc, _, _, _ := newTestService()
	w, err := svc.Register(context.Background(), validHotInput())
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	disabled, err := svc.UpdateStatus(context.Background(), w.ID, StatusDisabled, "admin:test")
	if err != nil {
		t.Fatalf("active->disabled: no esperaba error: %v", err)
	}
	if disabled.Status != StatusDisabled {
		t.Fatalf("status = %s, esperaba disabled", disabled.Status)
	}

	reactivated, err := svc.UpdateStatus(context.Background(), w.ID, StatusActive, "admin:test")
	if err != nil {
		t.Fatalf("disabled->active: no esperaba error: %v", err)
	}
	if reactivated.Status != StatusActive {
		t.Fatalf("status = %s, esperaba active", reactivated.Status)
	}
}

func TestUpdateStatus_RetiredIsTerminal(t *testing.T) {
	svc, _, _, _ := newTestService()
	w, err := svc.Register(context.Background(), validHotInput())
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	if _, err := svc.UpdateStatus(context.Background(), w.ID, StatusRetired, "admin:test"); err != nil {
		t.Fatalf("active->retired: no esperaba error: %v", err)
	}

	for _, target := range []Status{StatusActive, StatusDisabled} {
		_, err := svc.UpdateStatus(context.Background(), w.ID, target, "admin:test")
		if !errors.Is(err, ErrInvalidStatusTransition) {
			t.Errorf("retired->%s: error = %v, esperaba ErrInvalidStatusTransition — una wallet RETIRED nunca debe reactivarse", target, err)
		}
	}
}

func TestUpdateStatus_RejectsInvalidStatusValue(t *testing.T) {
	svc, _, _, _ := newTestService()
	w, err := svc.Register(context.Background(), validHotInput())
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	_, err = svc.UpdateStatus(context.Background(), w.ID, Status("frozen"), "admin:test")
	if !errors.Is(err, ErrInvalidStatus) {
		t.Fatalf("error = %v, esperaba ErrInvalidStatus", err)
	}
}

func TestUpdateStatus_NotFound(t *testing.T) {
	svc, _, _, _ := newTestService()
	_, err := svc.UpdateStatus(context.Background(), uuid.New(), StatusDisabled, "admin:test")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, esperaba ErrNotFound", err)
	}
}

// ---- SECURITY ----

func TestRegister_NeverExposesPrivateKeyInPublicResult(t *testing.T) {
	svc, _, _, _ := newTestService()
	w, err := svc.Register(context.Background(), validHotInput())
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	// CustodyWallet (the public type) has no field that can hold key
	// material — this is a compile-time guarantee, but we also check the
	// %+v representation as a regression guard in case a field is ever
	// added carelessly.
	repr := fmt.Sprintf("%+v", w)
	if strings.Contains(repr, "super-secret-hex-key") || strings.Contains(repr, "enc:") {
		t.Fatalf("la representación pública de la custody wallet contiene material de clave: %s", repr)
	}
}

func TestRegister_AuditEventNeverContainsPrivateKey(t *testing.T) {
	svc, _, _, audit := newTestService()
	if _, err := svc.Register(context.Background(), validHotInput()); err != nil {
		t.Fatalf("register: %v", err)
	}
	for _, ev := range audit.events {
		repr := fmt.Sprintf("%+v", ev)
		if strings.Contains(repr, "super-secret-hex-key") || strings.Contains(repr, "enc:") {
			t.Fatalf("el evento de auditoría contiene material de clave: %s", repr)
		}
	}
}

func TestRegister_EncryptsBeforePersisting(t *testing.T) {
	repo := newFakeRepository()
	enc := &fakeEncryptor{}
	svc := NewService(repo, enc, nil, nil)

	w, err := svc.Register(context.Background(), validHotInput())
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	stored, err := repo.Get(context.Background(), w.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if stored.EncryptedPrivateKey == "super-secret-hex-key" {
		t.Fatal("la clave privada se guardó en texto plano")
	}
	if !strings.HasPrefix(stored.EncryptedPrivateKey, "enc:") {
		t.Fatalf("la clave privada guardada no pasó por el encryptor: %q", stored.EncryptedPrivateKey)
	}
}

// ---- REPOSITORY (via fake — exercises Create/Get/List/UpdateStatus contract) ----

func TestList_FiltersByWalletType(t *testing.T) {
	svc, _, _, _ := newTestService()
	hot := validHotInput()
	if _, err := svc.Register(context.Background(), hot); err != nil {
		t.Fatalf("register hot: %v", err)
	}
	gas := hot
	gas.WalletType = WalletTypeGas
	gas.AssetCode = "BNB"
	gas.Address = "0x000000000000000000000000000000000000b0b0"
	if _, err := svc.Register(context.Background(), gas); err != nil {
		t.Fatalf("register gas: %v", err)
	}

	wt := WalletTypeGas
	list, err := svc.List(context.Background(), ListFilter{WalletType: &wt})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].WalletType != WalletTypeGas {
		t.Fatalf("list = %+v, esperaba exactamente una wallet GAS", list)
	}
}

func TestGet_ReturnsSameDataAsRegistered(t *testing.T) {
	svc, _, _, _ := newTestService()
	created, err := svc.Register(context.Background(), validHotInput())
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	fetched, err := svc.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if fetched.Address != created.Address || fetched.Network != created.Network || fetched.AssetCode != created.AssetCode {
		t.Fatalf("fetched = %+v, esperaba coincidir con created = %+v", fetched, created)
	}
}
