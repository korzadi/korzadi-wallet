package sweep

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
)

// harness wires a Service against every fake, mirroring
// blockchainwithdrawal's test harness pattern (see process_test.go /
// service_test.go there): each field is independently inspectable so a
// test can assert exactly what the service told its collaborators to do.
type harness struct {
	t *testing.T

	store      *fakeStore
	candidates *fakeCandidates
	nonces     *fakeNonces
	signer     *fakeSigner
	registry   *fakeRegistry
	wallets    *fakeWallets
	audit      *fakeAudit
	gas        *fakeGas
	chain      *fakeChain

	svc *Service

	network   domain.Network
	userID    uuid.UUID
	addressID uuid.UUID
	fromAddr  string
	toAddr    string
}

func newHarness(t *testing.T, mutate func(*Config)) *harness {
	t.Helper()
	h := &harness{
		t:          t,
		store:      newFakeStore(),
		candidates: &fakeCandidates{},
		nonces:     newFakeNonces(),
		signer:     &fakeSigner{},
		registry:   newFakeRegistry(),
		wallets:    newFakeWallets(),
		audit:      &fakeAudit{},
		gas:        &fakeGas{},
		network:    domain.NetworkBSC,
		userID:     uuid.New(),
		addressID:  uuid.New(),
		fromAddr:   "0xFROM00000000000000000000000000000000AA",
		toAddr:     "0xTREASURY000000000000000000000000000BB",
	}
	h.chain = newFakeChain(h.network)

	h.registry.add(domain.NetworkAsset{
		Network: h.network, AssetCode: "BNB", Kind: domain.AssetKindNative, Decimals: 18, Enabled: true,
	})
	h.registry.add(domain.NetworkAsset{
		Network: h.network, AssetCode: "USDT", Kind: domain.AssetKindToken, Decimals: 18,
		ContractAddress: "0xCONTRACT00000000000000000000000000000CC", Enabled: true,
	})
	h.wallets.add("TREASURY", string(h.network), "BNB", &CustodyWallet{ID: uuid.New(), Address: h.toAddr})
	h.wallets.add("TREASURY", string(h.network), "USDT", &CustodyWallet{ID: uuid.New(), Address: h.toAddr})

	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.DustThresholds["BNB"] = decimal.NewFromFloat(0.01)
	cfg.DustThresholds["USDT"] = decimal.NewFromInt(5)
	cfg.PollInterval = time.Millisecond
	cfg.LeaseDuration = time.Minute
	cfg.BatchSize = 10
	cfg.CandidateScan = 10
	cfg.BroadcastGrace = time.Hour
	cfg.MaxPrepareTries = 3
	if mutate != nil {
		mutate(&cfg)
	}

	networks := map[domain.Network]NetworkRuntime{h.network: {Chain: h.chain, Gas: h.gas}}

	svc, err := NewService(Deps{
		Store: h.store, Candidates: h.candidates, Networks: networks,
		Nonces: h.nonces, Signer: h.signer, Registry: h.registry,
		Wallets: h.wallets, Audit: h.audit, Config: cfg, InstanceID: "test",
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	h.svc = svc
	return h
}

func (h *harness) candidate(assetCode string) Candidate {
	return Candidate{
		Network: h.network, AssetCode: assetCode, FromAddress: h.fromAddr,
		FromUserID: h.userID, FromCryptoAddressID: h.addressID,
	}
}

func ctx() context.Context { return context.Background() }

// seedSweep inserts sw directly into the fake store (bypassing Create/the
// eligibility pipeline) so a test can start from an arbitrary mid-lifecycle
// state (e.g. already broadcasted) without re-deriving it every time.
func (h *harness) seedSweep(sw *Sweep) *Sweep {
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	cp := *sw
	if cp.ID == uuid.Nil {
		cp.ID = uuid.New()
	}
	now := time.Now().UTC()
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt, cp.UpdatedAt = now, now
	}
	h.store.byID[cp.ID] = &cp
	if !cp.Status.IsTerminal() {
		h.store.inFlight[inflightKey(cp.Network, cp.AssetCode, cp.FromAddress)] = cp.ID
	}
	out := cp
	return &out
}
