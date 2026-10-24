package watcher

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
)

type fakeUTXOProvider struct {
	latestHeight uint64
	blockHashes  map[uint64]string
	outputs      map[string][]domain.BitcoinOutput // blockHash -> outputs
	statuses     map[string]domain.BitcoinTransactionStatus
}

func newFakeUTXOProvider() *fakeUTXOProvider {
	return &fakeUTXOProvider{
		blockHashes: map[uint64]string{},
		outputs:     map[string][]domain.BitcoinOutput{},
		statuses:    map[string]domain.BitcoinTransactionStatus{},
	}
}

func (f *fakeUTXOProvider) LatestBlockHeight(ctx context.Context) (uint64, error) {
	return f.latestHeight, nil
}
func (f *fakeUTXOProvider) BlockHashAtHeight(ctx context.Context, height uint64) (string, error) {
	return f.blockHashes[height], nil
}
func (f *fakeUTXOProvider) OutputsInBlock(ctx context.Context, blockHash string) ([]domain.BitcoinOutput, error) {
	return f.outputs[blockHash], nil
}
func (f *fakeUTXOProvider) TransactionStatus(ctx context.Context, txHash string) (domain.BitcoinTransactionStatus, error) {
	return f.statuses[txHash], nil
}

func TestBTCDepositWatcher_DetectsOutputToManagedAddress(t *testing.T) {
	provider := newFakeUTXOProvider()
	provider.latestHeight = 100
	provider.blockHashes[50] = "hash50"
	provider.outputs["hash50"] = []domain.BitcoinOutput{
		{TxHash: "tx1", VoutIndex: 0, Address: "bc1qmanaged", AmountSats: 150_000_000, BlockHeight: 50, BlockHash: "hash50"},
		{TxHash: "tx1", VoutIndex: 1, Address: "bc1qchange", AmountSats: 5_000, BlockHeight: 50, BlockHash: "hash50"}, // change output, not ours
	}

	addrs := &fakeAddressLookup{managed: map[string]uuid.UUID{"bc1qmanaged": uuid.New()}, userID: uuid.New()}
	transfers := newFakeTransferStore()
	syncState := newFakeSyncState()
	confirmer := &fakeConfirmer{}

	w := NewBTCDepositWatcher(provider, addrs, transfers, syncState, confirmer, "BTC", BTCConfig{
		ConfirmationsRequired: 3,
		MaxBlocksPerScan:      50,
		SafetyLagBlocks:       1,
	})

	if err := w.scanBlock(context.Background(), 50); err != nil {
		t.Fatalf("scanBlock: %v", err)
	}

	pending, _ := transfers.FindPendingByNetwork(context.Background(), domain.NetworkBITCOIN)
	if len(pending) != 1 {
		t.Fatalf("depósitos detectados = %d, esperaba 1 (el output de cambio no nos pertenece)", len(pending))
	}
	if pending[0].LogIndex != 0 {
		t.Errorf("vout index registrado = %d, esperaba 0", pending[0].LogIndex)
	}
	if !pending[0].Amount.Equal(satsToBTC(150_000_000)) {
		t.Errorf("amount = %s, esperaba %s", pending[0].Amount, satsToBTC(150_000_000))
	}
}

// TestBTCDepositWatcher_TwoOutputsSameTxAreBothTracked verifies the UTXO
// idempotency key is (network, tx_hash, vout_index) — NOT tx_hash alone —
// exactly like the EVM (network, contract, tx_hash, log_index) key. A
// single Bitcoin transaction paying two of our addresses must record two
// separate deposits, not collapse into one or silently drop the second.
func TestBTCDepositWatcher_TwoOutputsSameTxAreBothTracked(t *testing.T) {
	provider := newFakeUTXOProvider()
	provider.blockHashes[50] = "hash50"
	provider.outputs["hash50"] = []domain.BitcoinOutput{
		{TxHash: "tx1", VoutIndex: 0, Address: "bc1qmanagedA", AmountSats: 100_000, BlockHeight: 50, BlockHash: "hash50"},
		{TxHash: "tx1", VoutIndex: 1, Address: "bc1qmanagedB", AmountSats: 200_000, BlockHeight: 50, BlockHash: "hash50"},
	}
	addrs := &fakeAddressLookup{managed: map[string]uuid.UUID{
		"bc1qmanagedA": uuid.New(),
		"bc1qmanagedB": uuid.New(),
	}, userID: uuid.New()}
	transfers := newFakeTransferStore()
	syncState := newFakeSyncState()
	confirmer := &fakeConfirmer{}

	w := NewBTCDepositWatcher(provider, addrs, transfers, syncState, confirmer, "BTC", BTCConfig{ConfirmationsRequired: 3})

	if err := w.scanBlock(context.Background(), 50); err != nil {
		t.Fatalf("scanBlock: %v", err)
	}

	pending, _ := transfers.FindPendingByNetwork(context.Background(), domain.NetworkBITCOIN)
	if len(pending) != 2 {
		t.Fatalf("depósitos detectados = %d, esperaba 2 — una misma tx pagando a dos direcciones nuestras debe generar dos depósitos", len(pending))
	}
}

func TestBTCDepositWatcher_ReorgMarksTransferReorged(t *testing.T) {
	provider := newFakeUTXOProvider()
	addrs := &fakeAddressLookup{managed: map[string]uuid.UUID{"bc1qmanaged": uuid.New()}, userID: uuid.New()}
	transfers := newFakeTransferStore()
	syncState := newFakeSyncState()
	confirmer := &fakeConfirmer{}

	w := NewBTCDepositWatcher(provider, addrs, transfers, syncState, confirmer, "BTC", BTCConfig{ConfirmationsRequired: 3})

	transfer := domain.BlockchainTransfer{
		Network: domain.NetworkBITCOIN, AssetCode: "BTC",
		TxHash: "tx1", LogIndex: 0, BlockHash: "hash50-ORIGINAL", ToAddress: "bc1qmanaged",
	}
	id, _, _ := transfers.InsertDetected(context.Background(), transfer, uuid.New())

	// The node no longer knows about this tx at all — as if the block was
	// reorged away and the tx wasn't re-included elsewhere (yet).
	provider.statuses["tx1"] = domain.BitcoinTransactionStatus{Found: false}

	if err := w.progressPendingTransfers(context.Background()); err != nil {
		t.Fatalf("progressPendingTransfers: %v", err)
	}

	transfers.mu.Lock()
	var status domain.TransferStatus
	for _, row := range transfers.byKey {
		if row.transfer.ID == id {
			status = row.transfer.Status
		}
	}
	transfers.mu.Unlock()

	if status != domain.TransferStatusReorged {
		t.Errorf("status = %s, esperaba 'reorged'", status)
	}
	if confirmer.calls != 0 {
		t.Errorf("ConfirmBlockchainDeposit llamado %d veces, esperaba 0", confirmer.calls)
	}
}

func TestBTCDepositWatcher_ConfirmsAndCreditsAfterEnoughConfirmations(t *testing.T) {
	provider := newFakeUTXOProvider()
	addrs := &fakeAddressLookup{managed: map[string]uuid.UUID{"bc1qmanaged": uuid.New()}, userID: uuid.New()}
	transfers := newFakeTransferStore()
	syncState := newFakeSyncState()
	confirmer := &fakeConfirmer{}

	w := NewBTCDepositWatcher(provider, addrs, transfers, syncState, confirmer, "BTC", BTCConfig{ConfirmationsRequired: 3})

	transfer := domain.BlockchainTransfer{
		Network: domain.NetworkBITCOIN, AssetCode: "BTC",
		TxHash: "tx1", LogIndex: 0, BlockHash: "hash50", ToAddress: "bc1qmanaged",
	}
	transfers.InsertDetected(context.Background(), transfer, uuid.New())
	provider.statuses["tx1"] = domain.BitcoinTransactionStatus{Found: true, BlockHash: "hash50", Confirmations: 3}

	if err := w.progressPendingTransfers(context.Background()); err != nil {
		t.Fatalf("progressPendingTransfers: %v", err)
	}

	if confirmer.calls != 1 {
		t.Errorf("ConfirmBlockchainDeposit llamado %d veces, esperaba 1 tras alcanzar 3/3 confirmaciones", confirmer.calls)
	}
}

// TestBTCDepositWatcher_RetriesCreditAfterTransientFailure mirrors the EVM
// regression test for the same Bloque 4 CRÍTICO finding: a Bitcoin deposit
// that reaches 'confirmed' but fails to credit on the first attempt must be
// retried on a later tick, not stranded forever.
func TestBTCDepositWatcher_RetriesCreditAfterTransientFailure(t *testing.T) {
	provider := newFakeUTXOProvider()
	addrs := &fakeAddressLookup{managed: map[string]uuid.UUID{"bc1qmanaged": uuid.New()}, userID: uuid.New()}
	transfers := newFakeTransferStore()
	syncState := newFakeSyncState()
	confirmer := &fakeConfirmer{failFirst: 1}

	w := NewBTCDepositWatcher(provider, addrs, transfers, syncState, confirmer, "BTC", BTCConfig{ConfirmationsRequired: 3})

	transfer := domain.BlockchainTransfer{
		Network: domain.NetworkBITCOIN, AssetCode: "BTC",
		TxHash: "txretry", LogIndex: 0, BlockHash: "hashA", ToAddress: "bc1qmanaged",
		Amount: decimal.NewFromInt(1),
	}
	transfers.InsertDetected(context.Background(), transfer, uuid.New())
	provider.statuses["txretry"] = domain.BitcoinTransactionStatus{Found: true, BlockHash: "hashA", Confirmations: 3}

	if err := w.progressPendingTransfers(context.Background()); err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	if confirmer.calls != 1 {
		t.Fatalf("llamadas tras tick 1 = %d, esperaba 1 (falla transitoria simulada)", confirmer.calls)
	}

	pendingAfterTick1, _ := transfers.FindPendingByNetwork(context.Background(), domain.NetworkBITCOIN)
	if len(pendingAfterTick1) != 1 {
		t.Fatalf("pendientes tras tick 1 = %d, esperaba 1 — no debe perderse el depósito confirmado sin acreditar", len(pendingAfterTick1))
	}

	if err := w.progressPendingTransfers(context.Background()); err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	if confirmer.calls != 2 {
		t.Fatalf("llamadas tras tick 2 = %d, esperaba 2 (reintento exitoso)", confirmer.calls)
	}

	if err := w.progressPendingTransfers(context.Background()); err != nil {
		t.Fatalf("tick 3: %v", err)
	}
	if confirmer.calls != 2 {
		t.Errorf("llamadas tras tick 3 = %d, esperaba seguir en 2 — un depósito ya acreditado nunca se reprocesa", confirmer.calls)
	}
}
