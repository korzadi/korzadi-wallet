package watcher

import (
	"context"
	"math/big"
	"testing"

	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/tronclient"
	"korzadi-wallet/backend/internal/infrastructure/tronclient/tronfake"
	"korzadi-wallet/backend/internal/infrastructure/tronwallet"
)

// End-to-end TRON deposits: the REAL EVMDepositWatcher + the REAL tronclient
// Provider talking HTTP to an in-memory node. Nothing touches a real network.

type tronDepositEnv struct {
	chain     *tronfake.Chain
	w         *EVMDepositWatcher
	transfers *fakeTransferStore
	confirmer *fakeConfirmer
	contract  string
	user      string
	sender    string
}

func newTronDepositEnv(t *testing.T) *tronDepositEnv {
	t.Helper()
	chain := tronfake.New()
	t.Cleanup(chain.Close)

	contractKP, _ := tronwallet.GenerateKeyPair()
	userKP, _ := tronwallet.GenerateKeyPair()
	senderKP, _ := tronwallet.GenerateKeyPair()

	node := tronclient.NewFullNode(chain.URL())
	provider := tronclient.NewProvider(node, tronclient.Config{ConfirmationsRequired: 3})

	addrs := &fakeAddressLookup{managed: map[string]uuid.UUID{userKP.Address: uuid.New()}, userID: uuid.New()}
	transfers := newFakeTransferStore()
	syncState := newFakeSyncState()
	confirmer := &fakeConfirmer{}

	w := NewEVMDepositWatcher(provider, addrs, addrs, transfers, syncState, confirmer, Config{
		Network:               domain.NetworkTRON,
		Assets:                []EVMAsset{{AssetCode: "USDT", Kind: domain.AssetKindToken, ContractAddress: contractKP.Address, Decimals: 6}},
		ConfirmationsRequired: 3,
		MaxBlocksPerScan:      100,
		SafetyLagBlocks:       1,
	})
	// the watcher has been running: checkpoint at the current head
	_ = syncState.AdvanceTo(context.Background(), domain.NetworkTRON, chain.Head())
	return &tronDepositEnv{chain: chain, w: w, transfers: transfers, confirmer: confirmer,
		contract: contractKP.Address, user: userKP.Address, sender: senderKP.Address}
}

func (e *tronDepositEnv) statusOf(t *testing.T, txid string) domain.BlockchainTransfer {
	t.Helper()
	e.transfers.mu.Lock()
	defer e.transfers.mu.Unlock()
	for _, row := range e.transfers.byKey {
		if row.transfer.TxHash == txid {
			return row.transfer
		}
	}
	t.Fatalf("no hay transferencia registrada para %s", txid)
	return domain.BlockchainTransfer{}
}

func TestTronDeposit_DetectedConfirmedCreditedOnce(t *testing.T) {
	e := newTronDepositEnv(t)
	ctx := context.Background()

	txid := e.chain.Deposit(e.contract, e.sender, e.user, big.NewInt(25_500_000)) // 25.5 USDT (6 decimales)
	e.chain.Mine(2)
	if err := e.w.tick(ctx); err != nil {
		t.Fatal(err)
	}
	got := e.statusOf(t, txid)
	if got.AssetCode != "USDT" || got.Network != domain.NetworkTRON || got.ToAddress != e.user || got.Amount.String() != "25.5" {
		t.Fatalf("transferencia detectada incorrecta: %+v", got)
	}
	if e.confirmer.calls != 0 {
		t.Fatal("no se acredita antes de las confirmaciones")
	}

	e.chain.Mine(6)
	if err := e.w.tick(ctx); err != nil {
		t.Fatal(err)
	}
	if e.confirmer.calls != 1 {
		t.Fatalf("debía acreditarse exactamente una vez, got %d", e.confirmer.calls)
	}
	if st := e.statusOf(t, txid).Status; st != domain.TransferStatusCredited {
		t.Fatalf("estado = %s, esperaba credited", st)
	}

	// idempotencia: más ticks y reescaneo del mismo rango no duplican nada
	e.chain.Mine(2)
	_ = e.w.tick(ctx)
	asset := e.w.cfg.Assets[0]
	if err := e.w.scanTokenRange(ctx, asset, e.chain.Head()-12, e.chain.Head()); err != nil {
		t.Fatal(err)
	}
	pending, _ := e.transfers.FindPendingByNetwork(ctx, domain.NetworkTRON)
	e.transfers.mu.Lock()
	rows := len(e.transfers.byKey)
	e.transfers.mu.Unlock()
	if e.confirmer.calls != 1 || rows != 1 || len(pending) != 0 {
		t.Fatalf("idempotencia rota: calls=%d filas=%d pendientes=%d", e.confirmer.calls, rows, len(pending))
	}
}

func TestTronDeposit_ReorgBeforeConfirmationNeverCredits(t *testing.T) {
	e := newTronDepositEnv(t)
	ctx := context.Background()

	txid := e.chain.Deposit(e.contract, e.sender, e.user, big.NewInt(10_000_000))
	e.chain.Mine(2)
	if err := e.w.tick(ctx); err != nil {
		t.Fatal(err)
	}
	if st := e.statusOf(t, txid).Status; st == domain.TransferStatusReorged || st == domain.TransferStatusCredited {
		t.Fatalf("estado inicial inesperado: %s", st)
	}

	e.chain.Reorg(2) // el bloque con el depósito desaparece de la cadena canónica
	e.chain.Mine(6)
	if err := e.w.tick(ctx); err != nil {
		t.Fatal(err)
	}
	if st := e.statusOf(t, txid).Status; st != domain.TransferStatusReorged {
		t.Fatalf("estado = %s, esperaba reorged", st)
	}
	if e.confirmer.calls != 0 {
		t.Fatal("un depósito reorganizado jamás debe acreditarse")
	}
}

func TestTronDeposit_IgnoresUnmanagedAddressesAndOtherTokens(t *testing.T) {
	e := newTronDepositEnv(t)
	ctx := context.Background()
	stranger, _ := tronwallet.GenerateKeyPair()
	otherToken, _ := tronwallet.GenerateKeyPair()

	e.chain.Deposit(e.contract, e.sender, stranger.Address, big.NewInt(1_000_000)) // no es nuestra dirección
	e.chain.Deposit(otherToken.Address, e.sender, e.user, big.NewInt(1_000_000))   // no es el USDT configurado
	e.chain.Mine(8)
	_ = e.w.tick(ctx)
	_ = e.w.tick(ctx)

	e.transfers.mu.Lock()
	rows := len(e.transfers.byKey)
	e.transfers.mu.Unlock()
	if rows != 0 || e.confirmer.calls != 0 {
		t.Fatalf("no debía registrarse nada: filas=%d acreditaciones=%d", rows, e.confirmer.calls)
	}
}
