package sweep

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
)

// ---- 1. Sweep candidato válido ----

func TestDiscoverAndClaim_ValidCandidateBecomesPendingSweep(t *testing.T) {
	h := newHarness(t, nil)
	h.candidates.items = []Candidate{h.candidate("BNB")}

	evaluated, claimed, err := h.svc.DiscoverAndClaim(ctx())
	if err != nil {
		t.Fatalf("DiscoverAndClaim: %v", err)
	}
	if evaluated != 1 || claimed != 1 {
		t.Fatalf("esperaba evaluated=1 claimed=1, obtuve evaluated=%d claimed=%d", evaluated, claimed)
	}
	if len(h.store.byID) != 1 {
		t.Fatalf("esperaba exactamente 1 sweep persistido, hay %d", len(h.store.byID))
	}
	for _, sw := range h.store.byID {
		if sw.Status != StatusPending {
			t.Fatalf("esperaba status=pending, obtuve %s", sw.Status)
		}
		if sw.FromAddress != h.fromAddr || sw.ToAddress != h.toAddr {
			t.Fatalf("direcciones incorrectas: from=%s to=%s", sw.FromAddress, sw.ToAddress)
		}
		if !sw.Amount.IsPositive() {
			t.Fatalf("el monto debe ser positivo, obtuve %s", sw.Amount)
		}
	}
}

// ---- 2. No sweep si no existe custody wallet compatible (fail closed) ----

func TestEvaluateCandidate_NoCustodyWallet_FailsClosed(t *testing.T) {
	h := newHarness(t, nil)
	// USDC no tiene ninguna custody wallet TREASURY configurada.
	h.registry.add(domain.NetworkAsset{Network: h.network, AssetCode: "USDC", Kind: domain.AssetKindToken, Decimals: 6, ContractAddress: "0xUSDC", Enabled: true})
	h.svc.deps.Config.DustThresholds["USDC"] = decimal.NewFromInt(1)

	_, err := h.svc.evaluateCandidate(ctx(), h.candidate("USDC"))
	if !errors.Is(err, ErrNoCustodyDestination) {
		t.Fatalf("esperaba ErrNoCustodyDestination, obtuve %v", err)
	}

	// Nunca debe haberse creado una fila.
	h.candidates.items = []Candidate{h.candidate("USDC")}
	evaluated, claimed, err := h.svc.DiscoverAndClaim(ctx())
	if err != nil {
		t.Fatalf("DiscoverAndClaim: %v", err)
	}
	if evaluated != 1 || claimed != 0 || len(h.store.byID) != 0 {
		t.Fatalf("no debía crearse ningún sweep sin custody wallet: evaluated=%d claimed=%d filas=%d", evaluated, claimed, len(h.store.byID))
	}
}

// ---- 3. No sweep de red no soportada (TRON) / asset no habilitado / sin dust ----

func TestEvaluateCandidate_UnsupportedNetwork_TRON(t *testing.T) {
	h := newHarness(t, nil)
	tronCandidate := Candidate{Network: domain.NetworkTRON, AssetCode: "USDT", FromAddress: "TAddress", FromUserID: h.userID, FromCryptoAddressID: h.addressID}

	_, err := h.svc.evaluateCandidate(ctx(), tronCandidate)
	if !errors.Is(err, ErrUnsupportedNetwork) {
		t.Fatalf("esperaba ErrUnsupportedNetwork para TRON, obtuve %v", err)
	}
}

func TestEvaluateCandidate_AssetNotEnabled(t *testing.T) {
	h := newHarness(t, nil)
	h.registry.add(domain.NetworkAsset{Network: h.network, AssetCode: "DISABLED", Kind: domain.AssetKindToken, Decimals: 18, ContractAddress: "0xD", Enabled: false})
	h.svc.deps.Config.DustThresholds["DISABLED"] = decimal.NewFromInt(1)

	_, err := h.svc.evaluateCandidate(ctx(), h.candidate("DISABLED"))
	if !errors.Is(err, ErrAssetNotEnabled) {
		t.Fatalf("esperaba ErrAssetNotEnabled, obtuve %v", err)
	}
}

// asset sin umbral de dust configurado: fail closed (nunca barrer "por defecto").
func TestEvaluateCandidate_NoDustConfigured_NeverSweeps(t *testing.T) {
	h := newHarness(t, nil)
	h.registry.add(domain.NetworkAsset{Network: h.network, AssetCode: "NODUST", Kind: domain.AssetKindNative, Decimals: 18, Enabled: true})
	h.wallets.add("TREASURY", string(h.network), "NODUST", &CustodyWallet{Address: h.toAddr})

	_, err := h.svc.evaluateCandidate(ctx(), h.candidate("NODUST"))
	if !errors.Is(err, ErrAssetNotEnabled) {
		t.Fatalf("un asset sin dust configurado nunca debe ser barrido, obtuve %v", err)
	}
}

// ---- 4. Idempotencia: la misma oportunidad no genera dos filas ----

func TestDiscoverAndClaim_Idempotent_SameCandidateTwiceOnlyOneRow(t *testing.T) {
	h := newHarness(t, nil)
	h.candidates.items = []Candidate{h.candidate("BNB")}

	if _, claimed, err := h.svc.DiscoverAndClaim(ctx()); err != nil || claimed != 1 {
		t.Fatalf("primer scan: claimed=%d err=%v", claimed, err)
	}
	// El mismo candidato sigue apareciendo (el depósito confirmado no
	// desaparece) pero YA hay un sweep en curso para esa dirección/asset.
	_, claimed, err := h.svc.DiscoverAndClaim(ctx())
	if err != nil {
		t.Fatalf("segundo scan: %v", err)
	}
	if claimed != 0 {
		t.Fatalf("el segundo scan NO debe crear una segunda fila, claimed=%d", claimed)
	}
	if len(h.store.byID) != 1 {
		t.Fatalf("debe seguir habiendo exactamente 1 sweep, hay %d", len(h.store.byID))
	}
}

// ---- 5. Dos workers simultáneos: solo uno reclama ----

func TestCreate_ConcurrentWorkers_OnlyOneClaims(t *testing.T) {
	h := newHarness(t, nil)
	sw1, err := h.svc.evaluateCandidate(ctx(), h.candidate("BNB"))
	if err != nil {
		t.Fatalf("evaluateCandidate: %v", err)
	}
	sw2 := *sw1 // "worker B" evaluó el mismo candidato de forma independiente

	// Worker A gana la carrera.
	created1, err := h.store.Create(ctx(), sw1)
	if err != nil {
		t.Fatalf("worker A: %v", err)
	}
	// Worker B pierde la carrera contra la MISMA restricción
	// (network, asset_code, from_address) que uq_sweeps_inflight_source
	// implementa en Postgres.
	_, err = h.store.Create(ctx(), &sw2)
	if !errors.Is(err, ErrInFlight) {
		t.Fatalf("esperaba ErrInFlight para el segundo worker, obtuve %v", err)
	}
	if len(h.store.byID) != 1 {
		t.Fatalf("solo debe existir 1 sweep, hay %d", len(h.store.byID))
	}
	if created1.Status != StatusPending {
		t.Fatalf("el ganador debe quedar pending, quedó %s", created1.Status)
	}
}

// Un sweep completed/failed libera la dirección para un futuro sweep.
func TestCreate_TerminalSweepFreesAddressForNextCandidate(t *testing.T) {
	h := newHarness(t, nil)
	sw, err := h.svc.evaluateCandidate(ctx(), h.candidate("BNB"))
	if err != nil {
		t.Fatalf("evaluateCandidate: %v", err)
	}
	created, err := h.store.Create(ctx(), sw)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	one := decimal.NewFromInt(1)
	if _, err := h.store.Apply(ctx(), created.ID, StatusPending, StatusFailed, Update{Amount: &one}, Event{Type: "failed"}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	// Ahora un depósito nuevo a la misma dirección debe poder generar OTRO sweep.
	sw2, err := h.svc.evaluateCandidate(ctx(), h.candidate("BNB"))
	if err != nil {
		t.Fatalf("evaluateCandidate tras liberar: %v", err)
	}
	if _, err := h.store.Create(ctx(), sw2); err != nil {
		t.Fatalf("un sweep terminal debe liberar la dirección: %v", err)
	}
}
