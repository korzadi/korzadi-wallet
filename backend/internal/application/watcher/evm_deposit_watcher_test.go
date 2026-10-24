package watcher

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
)

// ---- fakes ----

type fakeEVMProvider struct {
	mu             sync.Mutex
	latestBlock    uint64
	latestSequence []uint64
	latestCalls    int
	tokenEvents    map[string][]domain.OnChainTransferEvent // contract -> events
	nativeEvents   []domain.OnChainNativeTransferEvent
	receipts       map[string]*domain.TransactionReceipt
	transferCalls  int
	transferErrors map[int]error // one-based call number -> injected error
	transferRanges []fakeTransferRange
}

type fakeTransferRange struct {
	contract string
	from     uint64
	to       uint64
}

func newFakeEVMProvider() *fakeEVMProvider {
	return &fakeEVMProvider{
		tokenEvents:    map[string][]domain.OnChainTransferEvent{},
		receipts:       map[string]*domain.TransactionReceipt{},
		transferErrors: map[int]error{},
	}
}

func (f *fakeEVMProvider) Network() domain.Network { return domain.NetworkBSC }
func (f *fakeEVMProvider) HealthCheck(ctx context.Context, tokenContract string) (domain.ProviderHealth, error) {
	return domain.ProviderHealth{}, nil
}
func (f *fakeEVMProvider) LatestBlock(ctx context.Context) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.latestCalls++
	if index := f.latestCalls - 1; index < len(f.latestSequence) {
		return f.latestSequence[index], nil
	}
	return f.latestBlock, nil
}
func (f *fakeEVMProvider) NativeBalance(ctx context.Context, address string) (decimal.Decimal, error) {
	return decimal.Zero, nil
}
func (f *fakeEVMProvider) TokenBalance(ctx context.Context, address, contract string, decimals int32) (decimal.Decimal, error) {
	return decimal.Zero, nil
}
func (f *fakeEVMProvider) TransferEventsInRange(ctx context.Context, contract string, decimals int32, fromBlock, toBlock uint64) ([]domain.OnChainTransferEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.transferCalls++
	f.transferRanges = append(f.transferRanges, fakeTransferRange{contract: contract, from: fromBlock, to: toBlock})
	if err := f.transferErrors[f.transferCalls]; err != nil {
		return nil, err
	}
	var out []domain.OnChainTransferEvent
	for _, ev := range f.tokenEvents[contract] {
		if ev.BlockNumber >= fromBlock && ev.BlockNumber <= toBlock {
			out = append(out, ev)
		}
	}
	return out, nil
}
func (f *fakeEVMProvider) NativeTransfersInRange(ctx context.Context, fromBlock, toBlock uint64, watched map[string]bool) ([]domain.OnChainNativeTransferEvent, error) {
	var out []domain.OnChainNativeTransferEvent
	for _, ev := range f.nativeEvents {
		if ev.BlockNumber >= fromBlock && ev.BlockNumber <= toBlock && watched[ev.To] {
			out = append(out, ev)
		}
	}
	return out, nil
}
func (f *fakeEVMProvider) GetReceipt(ctx context.Context, txHash string) (*domain.TransactionReceipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.receipts[txHash], nil
}
func (f *fakeEVMProvider) EstimateFee(ctx context.Context, contract, to string, amount decimal.Decimal) (decimal.Decimal, error) {
	return decimal.Zero, nil
}
func (f *fakeEVMProvider) BroadcastTransaction(ctx context.Context, signedRawTx []byte) (string, error) {
	return "", nil
}
func (f *fakeEVMProvider) GetTransaction(ctx context.Context, txHash string) (*domain.TransactionReceipt, error) {
	return f.GetReceipt(ctx, txHash)
}

type fakeAddressLookup struct {
	managed map[string]uuid.UUID // lowercased address -> cryptoAddressID
	userID  uuid.UUID
}

func (f *fakeAddressLookup) FindByAddress(ctx context.Context, network domain.Network, address string) (uuid.UUID, uuid.UUID, bool, error) {
	if id, ok := f.managed[address]; ok {
		return id, f.userID, true, nil
	}
	return uuid.Nil, uuid.Nil, false, nil
}
func (f *fakeAddressLookup) ListActiveAddresses(ctx context.Context, network domain.Network) (map[string]bool, error) {
	out := map[string]bool{}
	for addr := range f.managed {
		out[addr] = true
	}
	return out, nil
}

type transferRow struct {
	transfer domain.BlockchainTransfer
	credited bool
}

type fakeTransferStore struct {
	mu     sync.Mutex
	byKey  map[string]*transferRow // network|contract|tx|logIndex -> row
	nextID int
}

func newFakeTransferStore() *fakeTransferStore {
	return &fakeTransferStore{byKey: map[string]*transferRow{}}
}

func keyOf(t domain.BlockchainTransfer) string {
	return fmt.Sprintf("%s|%s|%s|%d", t.Network, t.ContractAddress, t.TxHash, t.LogIndex)
}

func (f *fakeTransferStore) InsertDetected(ctx context.Context, t domain.BlockchainTransfer, cryptoAddressID uuid.UUID) (uuid.UUID, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := keyOf(t)
	if _, exists := f.byKey[k]; exists {
		return f.byKey[k].transfer.ID, true, nil
	}
	f.nextID++
	t.ID = uuid.New()
	t.Status = domain.TransferStatusDetected
	f.byKey[k] = &transferRow{transfer: t}
	return t.ID, false, nil
}

func (f *fakeTransferStore) UpdateConfirmations(ctx context.Context, id uuid.UUID, confirmations uint64, required uint64) (domain.TransferStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, row := range f.byKey {
		if row.transfer.ID == id {
			terminal := map[domain.TransferStatus]bool{
				domain.TransferStatusConfirmed: true, domain.TransferStatusCredited: true,
				domain.TransferStatusReorged: true, domain.TransferStatusFailed: true,
			}
			if terminal[row.transfer.Status] {
				// Mirrors the real repository's guard clause exactly:
				// UPDATE ... WHERE status NOT IN (...) — a row already
				// past this transition is untouched, RowsAffected=0,
				// caller gets back "" rather than a fabricated status.
				return "", nil
			}
			row.transfer.Confirmations = confirmations
			if confirmations >= required {
				row.transfer.Status = domain.TransferStatusConfirmed
			} else {
				row.transfer.Status = domain.TransferStatusConfirming
			}
			return row.transfer.Status, nil
		}
	}
	return "", nil
}

func (f *fakeTransferStore) MarkReorged(ctx context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, row := range f.byKey {
		if row.transfer.ID == id {
			row.transfer.Status = domain.TransferStatusReorged
		}
	}
	return nil
}

func (f *fakeTransferStore) MarkCredited(ctx context.Context, id uuid.UUID, blockchainDepositID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, row := range f.byKey {
		if row.transfer.ID == id {
			row.transfer.Status = domain.TransferStatusCredited
			row.credited = true
		}
	}
	return nil
}

func (f *fakeTransferStore) FindPendingByNetwork(ctx context.Context, network domain.Network) ([]domain.BlockchainTransfer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.BlockchainTransfer
	for _, row := range f.byKey {
		if row.transfer.Network != network {
			continue
		}
		switch row.transfer.Status {
		case domain.TransferStatusDetected, domain.TransferStatusConfirming, domain.TransferStatusConfirmed:
			out = append(out, row.transfer)
		}
	}
	return out, nil
}

type fakeSyncState struct {
	mu    sync.Mutex
	block map[domain.Network]uint64
	set   map[domain.Network]bool
}

func newFakeSyncState() *fakeSyncState {
	return &fakeSyncState{block: map[domain.Network]uint64{}, set: map[domain.Network]bool{}}
}
func (f *fakeSyncState) GetLastProcessedBlock(ctx context.Context, network domain.Network) (uint64, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.block[network], f.set[network], nil
}
func (f *fakeSyncState) AdvanceTo(ctx context.Context, network domain.Network, block uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.block[network] = block
	f.set[network] = true
	return nil
}

type fakeConfirmer struct {
	mu        sync.Mutex
	calls     int
	failFirst int // fail this many calls with a transient error before succeeding
}

func (f *fakeConfirmer) ConfirmBlockchainDeposit(ctx context.Context, transfer domain.BlockchainTransfer, userID uuid.UUID) (uuid.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.calls <= f.failFirst {
		return uuid.Nil, errors.New("transient: ledger no disponible")
	}
	return uuid.New(), nil
}

// ---- tests ----

func TestEVMDepositWatcher_DetectsTokenTransferToManagedAddress(t *testing.T) {
	provider := newFakeEVMProvider()
	provider.latestBlock = 100
	contract := "0xContract"
	provider.tokenEvents[contract] = []domain.OnChainTransferEvent{
		{ContractAddress: contract, TxHash: "0xabc", LogIndex: 0, BlockNumber: 50, BlockHash: "0xblock50", From: "0xfrom", To: "0xManaged", RawAmount: decimal.NewFromInt(10)},
	}

	addrs := &fakeAddressLookup{managed: map[string]uuid.UUID{"0xManaged": uuid.New()}, userID: uuid.New()}
	transfers := newFakeTransferStore()
	syncState := newFakeSyncState()
	confirmer := &fakeConfirmer{}

	w := NewEVMDepositWatcher(provider, addrs, addrs, transfers, syncState, confirmer, Config{
		Network:               domain.NetworkBSC,
		Assets:                []EVMAsset{{AssetCode: "USDT", Kind: domain.AssetKindToken, ContractAddress: contract, Decimals: 18}},
		ConfirmationsRequired: 15,
		MaxBlocksPerScan:      2000,
		SafetyLagBlocks:       3,
	})

	// First tick starts from the tip (safeTip=97), so block 50 is in the
	// past relative to "first run starts at tip" — force a checkpoint
	// that includes block 50 to simulate "watcher has been running a while".
	syncState.AdvanceTo(context.Background(), domain.NetworkBSC, 49)

	if err := w.scanNewBlocks(context.Background()); err != nil {
		t.Fatalf("scanNewBlocks: %v", err)
	}

	pending, _ := transfers.FindPendingByNetwork(context.Background(), domain.NetworkBSC)
	if len(pending) != 1 {
		t.Fatalf("transferencias pendientes = %d, esperaba 1", len(pending))
	}
	if pending[0].AssetCode != "USDT" {
		t.Errorf("asset code = %s, esperaba USDT — debe venir del asset registry, no hardcodeado", pending[0].AssetCode)
	}
}

func TestEVMDepositWatcher_DuplicateEventNeverProcessedTwice(t *testing.T) {
	provider := newFakeEVMProvider()
	provider.latestBlock = 100
	contract := "0xContract"
	event := domain.OnChainTransferEvent{ContractAddress: contract, TxHash: "0xabc", LogIndex: 0, BlockNumber: 50, BlockHash: "0xblock50", From: "0xfrom", To: "0xManaged", RawAmount: decimal.NewFromInt(10)}
	provider.tokenEvents[contract] = []domain.OnChainTransferEvent{event}

	addrs := &fakeAddressLookup{managed: map[string]uuid.UUID{"0xManaged": uuid.New()}, userID: uuid.New()}
	transfers := newFakeTransferStore()
	syncState := newFakeSyncState()
	confirmer := &fakeConfirmer{}

	w := NewEVMDepositWatcher(provider, addrs, addrs, transfers, syncState, confirmer, Config{
		Network:               domain.NetworkBSC,
		Assets:                []EVMAsset{{AssetCode: "USDT", Kind: domain.AssetKindToken, ContractAddress: contract, Decimals: 18}},
		ConfirmationsRequired: 15,
		MaxBlocksPerScan:      2000,
		SafetyLagBlocks:       3,
	})
	syncState.AdvanceTo(context.Background(), domain.NetworkBSC, 49)

	// Simulate the SAME range being scanned twice — e.g. after a restart
	// that resumed from an older checkpoint than strictly necessary.
	if err := w.scanTokenRange(context.Background(), EVMAsset{AssetCode: "USDT", ContractAddress: contract, Decimals: 18}, 50, 50); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	if err := w.scanTokenRange(context.Background(), EVMAsset{AssetCode: "USDT", ContractAddress: contract, Decimals: 18}, 50, 50); err != nil {
		t.Fatalf("second scan: %v", err)
	}

	pending, _ := transfers.FindPendingByNetwork(context.Background(), domain.NetworkBSC)
	if len(pending) != 1 {
		t.Fatalf("transferencias registradas = %d, esperaba exactamente 1 pese a escanear el mismo rango dos veces", len(pending))
	}
}

func TestEVMDepositWatcher_FutureRangeRetriesAtRefreshedSafeHeight(t *testing.T) {
	ctx := context.Background()
	provider := newFakeEVMProvider()
	provider.latestBlock = 200
	provider.latestSequence = []uint64{200, 130}
	provider.transferErrors[2] = &domain.BlockRangeBeyondHeadError{
		RequestedTo: 149, AvailableHead: 140, Cause: errors.New("simulated RPC head race"),
	}
	contractA, contractB := "0xTokenA", "0xTokenB"
	provider.tokenEvents[contractA] = []domain.OnChainTransferEvent{
		{ContractAddress: contractA, TxHash: "0xevent-a", BlockNumber: 60, BlockHash: "0xblock60", To: "0xManaged", RawAmount: decimal.NewFromInt(1)},
	}
	provider.tokenEvents[contractB] = []domain.OnChainTransferEvent{
		{ContractAddress: contractB, TxHash: "0xevent-b", BlockNumber: 70, BlockHash: "0xblock70", To: "0xManaged", RawAmount: decimal.NewFromInt(2)},
	}
	addrs := &fakeAddressLookup{managed: map[string]uuid.UUID{"0xManaged": uuid.New()}, userID: uuid.New()}
	transfers := newFakeTransferStore()
	syncState := newFakeSyncState()
	syncState.AdvanceTo(ctx, domain.NetworkBSC, 49)
	w := NewEVMDepositWatcher(provider, addrs, addrs, transfers, syncState, &fakeConfirmer{}, Config{
		Network: domain.NetworkBSC,
		Assets: []EVMAsset{
			{AssetCode: "TKA", Kind: domain.AssetKindToken, ContractAddress: contractA, Decimals: 18},
			{AssetCode: "TKB", Kind: domain.AssetKindToken, ContractAddress: contractB, Decimals: 18},
		},
		MaxBlocksPerScan: 100, SafetyLagBlocks: 3,
	})

	if err := w.scanNewBlocks(ctx); err != nil {
		t.Fatalf("scanNewBlocks: %v", err)
	}
	if len(provider.transferRanges) != 4 {
		t.Fatalf("llamadas TransferEventsInRange = %d, esperaba 4 (dos assets, escaneo y reintento)", len(provider.transferRanges))
	}
	for i, want := range []fakeTransferRange{
		{contract: contractA, from: 50, to: 149},
		{contract: contractB, from: 50, to: 149},
		{contract: contractA, from: 50, to: 127},
		{contract: contractB, from: 50, to: 127},
	} {
		if got := provider.transferRanges[i]; got != want {
			t.Errorf("rango %d = %+v, esperaba %+v", i, got, want)
		}
	}
	if got, ok, _ := syncState.GetLastProcessedBlock(ctx, domain.NetworkBSC); !ok || got != 127 {
		t.Fatalf("checkpoint = %d (exists=%v), esperaba 127, extremo cubierto por ambos assets", got, ok)
	}
	if pending, _ := transfers.FindPendingByNetwork(ctx, domain.NetworkBSC); len(pending) != 2 {
		t.Fatalf("transferencias pendientes = %d, esperaba 2 únicas pese a reescanear el primer asset", len(pending))
	}
}

func TestEVMDepositWatcher_FailedFutureRangeRetryDoesNotAdvanceCheckpoint(t *testing.T) {
	ctx := context.Background()
	provider := newFakeEVMProvider()
	provider.latestBlock = 200
	provider.latestSequence = []uint64{200, 130}
	provider.transferErrors[1] = &domain.BlockRangeBeyondHeadError{
		RequestedTo: 149, AvailableHead: 140, Cause: errors.New("simulated RPC head race"),
	}
	provider.transferErrors[2] = errors.New("simulated retry failure")
	contract := "0xToken"
	addrs := &fakeAddressLookup{managed: map[string]uuid.UUID{}}
	syncState := newFakeSyncState()
	syncState.AdvanceTo(ctx, domain.NetworkBSC, 49)
	w := NewEVMDepositWatcher(provider, addrs, addrs, newFakeTransferStore(), syncState, &fakeConfirmer{}, Config{
		Network:          domain.NetworkBSC,
		Assets:           []EVMAsset{{AssetCode: "TKA", Kind: domain.AssetKindToken, ContractAddress: contract, Decimals: 18}},
		MaxBlocksPerScan: 100, SafetyLagBlocks: 3,
	})

	if err := w.scanNewBlocks(ctx); err == nil {
		t.Fatal("esperaba el error del reintento")
	}
	if got, ok, _ := syncState.GetLastProcessedBlock(ctx, domain.NetworkBSC); !ok || got != 49 {
		t.Fatalf("checkpoint = %d (exists=%v), debía permanecer en 49", got, ok)
	}
	if len(provider.transferRanges) != 2 || provider.transferRanges[1].to != 127 {
		t.Fatalf("rangos tras fallo = %+v, debía haber un único reintento reducido hasta 127", provider.transferRanges)
	}
}

func TestEVMDepositWatcher_FailureInOneAssetDoesNotAdvancePartialCheckpoint(t *testing.T) {
	ctx := context.Background()
	provider := newFakeEVMProvider()
	provider.latestBlock = 100
	contractA, contractB := "0xTokenA", "0xTokenB"
	provider.tokenEvents[contractA] = []domain.OnChainTransferEvent{
		{ContractAddress: contractA, TxHash: "0xevent-a", BlockNumber: 60, BlockHash: "0xblock60", To: "0xManaged", RawAmount: decimal.NewFromInt(1)},
	}
	provider.transferErrors[2] = errors.New("ordinary asset scan failure")
	addrs := &fakeAddressLookup{managed: map[string]uuid.UUID{"0xManaged": uuid.New()}, userID: uuid.New()}
	transfers := newFakeTransferStore()
	syncState := newFakeSyncState()
	syncState.AdvanceTo(ctx, domain.NetworkBSC, 49)
	w := NewEVMDepositWatcher(provider, addrs, addrs, transfers, syncState, &fakeConfirmer{}, Config{
		Network: domain.NetworkBSC,
		Assets: []EVMAsset{
			{AssetCode: "TKA", Kind: domain.AssetKindToken, ContractAddress: contractA, Decimals: 18},
			{AssetCode: "TKB", Kind: domain.AssetKindToken, ContractAddress: contractB, Decimals: 18},
		},
		MaxBlocksPerScan: 100, SafetyLagBlocks: 3,
	})

	if err := w.scanNewBlocks(ctx); err == nil {
		t.Fatal("esperaba error del segundo asset")
	}
	if got, ok, _ := syncState.GetLastProcessedBlock(ctx, domain.NetworkBSC); !ok || got != 49 {
		t.Fatalf("checkpoint = %d (exists=%v), debía permanecer en 49 aunque el primer asset se leyó", got, ok)
	}
	if pending, _ := transfers.FindPendingByNetwork(ctx, domain.NetworkBSC); len(pending) != 1 {
		t.Fatalf("transferencias del asset procesado = %d, esperaba 1", len(pending))
	}
}

func TestEVMDepositWatcher_RereadAfterPartialFailureDoesNotDuplicateDeposits(t *testing.T) {
	ctx := context.Background()
	provider := newFakeEVMProvider()
	provider.latestBlock = 100
	contractA, contractB := "0xTokenA", "0xTokenB"
	provider.tokenEvents[contractA] = []domain.OnChainTransferEvent{
		{ContractAddress: contractA, TxHash: "0xevent-a", BlockNumber: 60, BlockHash: "0xblock60", To: "0xManaged", RawAmount: decimal.NewFromInt(1)},
	}
	provider.tokenEvents[contractB] = []domain.OnChainTransferEvent{
		{ContractAddress: contractB, TxHash: "0xevent-b", BlockNumber: 70, BlockHash: "0xblock70", To: "0xManaged", RawAmount: decimal.NewFromInt(2)},
	}
	provider.transferErrors[2] = errors.New("temporary second asset failure")
	addrs := &fakeAddressLookup{managed: map[string]uuid.UUID{"0xManaged": uuid.New()}, userID: uuid.New()}
	transfers := newFakeTransferStore()
	syncState := newFakeSyncState()
	syncState.AdvanceTo(ctx, domain.NetworkBSC, 49)
	w := NewEVMDepositWatcher(provider, addrs, addrs, transfers, syncState, &fakeConfirmer{}, Config{
		Network: domain.NetworkBSC,
		Assets: []EVMAsset{
			{AssetCode: "TKA", Kind: domain.AssetKindToken, ContractAddress: contractA, Decimals: 18},
			{AssetCode: "TKB", Kind: domain.AssetKindToken, ContractAddress: contractB, Decimals: 18},
		},
		MaxBlocksPerScan: 100, SafetyLagBlocks: 3,
	})

	if err := w.scanNewBlocks(ctx); err == nil {
		t.Fatal("esperaba fallo parcial en el primer intento")
	}
	if got, _, _ := syncState.GetLastProcessedBlock(ctx, domain.NetworkBSC); got != 49 {
		t.Fatalf("checkpoint tras escaneo parcial = %d, esperaba 49", got)
	}
	if err := w.scanNewBlocks(ctx); err != nil {
		t.Fatalf("relectura del rango pendiente: %v", err)
	}
	if got, _, _ := syncState.GetLastProcessedBlock(ctx, domain.NetworkBSC); got != 97 {
		t.Fatalf("checkpoint tras relectura completa = %d, esperaba 97", got)
	}
	if pending, _ := transfers.FindPendingByNetwork(ctx, domain.NetworkBSC); len(pending) != 2 {
		t.Fatalf("transferencias pendientes = %d, esperaba 2 sin duplicar el primer evento", len(pending))
	}
}

func TestEVMDepositWatcher_ReorgMarksTransferReorged(t *testing.T) {
	provider := newFakeEVMProvider()
	contract := "0xContract"
	addrs := &fakeAddressLookup{managed: map[string]uuid.UUID{"0xManaged": uuid.New()}, userID: uuid.New()}
	transfers := newFakeTransferStore()
	syncState := newFakeSyncState()
	confirmer := &fakeConfirmer{}

	w := NewEVMDepositWatcher(provider, addrs, addrs, transfers, syncState, confirmer, Config{
		Network:               domain.NetworkBSC,
		Assets:                []EVMAsset{{AssetCode: "USDT", Kind: domain.AssetKindToken, ContractAddress: contract, Decimals: 18}},
		ConfirmationsRequired: 15,
	})

	transfer := domain.BlockchainTransfer{
		Network: domain.NetworkBSC, AssetCode: "USDT", ContractAddress: contract,
		TxHash: "0xabc", LogIndex: 0, BlockHash: "0xORIGINAL", ToAddress: "0xManaged",
		Amount: decimal.NewFromInt(10),
	}
	id, _, _ := transfers.InsertDetected(context.Background(), transfer, uuid.New())

	// The node no longer has a receipt for this tx at all — as if the
	// block that contained it was reorged away.
	provider.receipts["0xabc"] = nil

	if err := w.progressPendingTransfers(context.Background()); err != nil {
		t.Fatalf("progressPendingTransfers: %v", err)
	}

	transfers.mu.Lock()
	var gotStatus domain.TransferStatus
	for _, row := range transfers.byKey {
		if row.transfer.ID == id {
			gotStatus = row.transfer.Status
		}
	}
	transfers.mu.Unlock()

	if gotStatus != domain.TransferStatusReorged {
		t.Errorf("status = %s, esperaba 'reorged' — nunca debe acreditarse una tx que desapareció de la cadena", gotStatus)
	}
	if confirmer.calls != 0 {
		t.Errorf("ConfirmBlockchainDeposit llamado %d veces, esperaba 0 — un reorg jamás debe acreditar", confirmer.calls)
	}
}

func TestEVMDepositWatcher_RestartResumesFromCheckpoint_NeverFromGenesis(t *testing.T) {
	provider := newFakeEVMProvider()
	provider.latestBlock = 1_000_000
	addrs := &fakeAddressLookup{managed: map[string]uuid.UUID{}}
	transfers := newFakeTransferStore()
	syncState := newFakeSyncState()
	syncState.AdvanceTo(context.Background(), domain.NetworkBSC, 999_000) // simulates a prior run's checkpoint
	confirmer := &fakeConfirmer{}

	w := NewEVMDepositWatcher(provider, addrs, addrs, transfers, syncState, confirmer, Config{
		Network:               domain.NetworkBSC,
		Assets:                []EVMAsset{{AssetCode: "USDT", Kind: domain.AssetKindToken, ContractAddress: "0xC", Decimals: 18}},
		ConfirmationsRequired: 15,
		MaxBlocksPerScan:      2000,
		SafetyLagBlocks:       3,
	})

	if err := w.scanNewBlocks(context.Background()); err != nil {
		t.Fatalf("scanNewBlocks: %v", err)
	}

	got, ok, _ := syncState.GetLastProcessedBlock(context.Background(), domain.NetworkBSC)
	if !ok {
		t.Fatal("checkpoint debería existir")
	}
	// Must resume from 999_001 (checkpoint+1), capped by MaxBlocksPerScan —
	// never from block 0/genesis (spec §10).
	if got < 999_000 {
		t.Errorf("checkpoint avanzó a %d, esperaba >= 999000 (nunca reiniciar desde genesis)", got)
	}
}

func TestEVMDepositWatcher_NativeTransferGetsCorrectAssetCode(t *testing.T) {
	provider := newFakeEVMProvider()
	provider.nativeEvents = []domain.OnChainNativeTransferEvent{
		{TxHash: "0xnative1", BlockNumber: 50, BlockHash: "0xblock50", From: "0xfrom", To: "0xManaged", RawAmount: decimal.NewFromInt(1)},
	}
	addrs := &fakeAddressLookup{managed: map[string]uuid.UUID{"0xManaged": uuid.New()}, userID: uuid.New()}
	transfers := newFakeTransferStore()
	syncState := newFakeSyncState()
	confirmer := &fakeConfirmer{}

	w := NewEVMDepositWatcher(provider, addrs, addrs, transfers, syncState, confirmer, Config{
		Network:               domain.NetworkBSC,
		Assets:                []EVMAsset{{AssetCode: "BNB", Kind: domain.AssetKindNative, Decimals: 18}},
		ConfirmationsRequired: 15,
	})

	if err := w.scanNativeRange(context.Background(), EVMAsset{AssetCode: "BNB", Kind: domain.AssetKindNative, Decimals: 18}, 50, 50); err != nil {
		t.Fatalf("scanNativeRange: %v", err)
	}

	pending, _ := transfers.FindPendingByNetwork(context.Background(), domain.NetworkBSC)
	if len(pending) != 1 {
		t.Fatalf("transferencias pendientes = %d, esperaba 1", len(pending))
	}
	if pending[0].AssetKind != domain.AssetKindNative {
		t.Errorf("asset kind = %s, esperaba native — no debe confundirse con un token Transfer event (spec §12)", pending[0].AssetKind)
	}
	if pending[0].ContractAddress != "" {
		t.Errorf("contract address = %q, esperaba vacío para una transferencia nativa", pending[0].ContractAddress)
	}
}

// TestEVMDepositWatcher_RetriesCreditAfterTransientFailure is the Bloque 4
// regression test for the CRÍTICO audit finding: before this fix,
// FindPendingByNetwork only ever returned 'detected'/'confirming' rows, so
// the instant a transfer reached 'confirmed' it permanently stopped being
// selected by future ticks — a transient failure in ConfirmBlockchainDeposit
// (Postgres hiccup, ledger timeout) left the deposit stranded in
// 'confirmed' forever, silently uncredited, with no automatic recovery
// (spec §6: "Debe poder reintentarse de forma segura").
func TestEVMDepositWatcher_RetriesCreditAfterTransientFailure(t *testing.T) {
	provider := newFakeEVMProvider()
	addrs := &fakeAddressLookup{managed: map[string]uuid.UUID{"0xManaged": uuid.New()}, userID: uuid.New()}
	transfers := newFakeTransferStore()
	syncState := newFakeSyncState()
	confirmer := &fakeConfirmer{failFirst: 1} // first credit attempt fails transiently, second succeeds

	contract := "0xContract"
	w := NewEVMDepositWatcher(provider, addrs, addrs, transfers, syncState, confirmer, Config{
		Network:               domain.NetworkBSC,
		Assets:                []EVMAsset{{AssetCode: "USDT", Kind: domain.AssetKindToken, ContractAddress: contract, Decimals: 18}},
		ConfirmationsRequired: 15,
	})

	transfer := domain.BlockchainTransfer{
		Network: domain.NetworkBSC, AssetCode: "USDT", ContractAddress: contract,
		TxHash: "0xretry", LogIndex: 0, BlockHash: "0xblockA", ToAddress: "0xManaged",
		Amount: decimal.NewFromInt(50),
	}
	transfers.InsertDetected(context.Background(), transfer, uuid.New())
	provider.receipts["0xretry"] = &domain.TransactionReceipt{TxHash: "0xretry", BlockHash: "0xblockA", Status: true, Confirmations: 15}

	// Tick 1: reaches confirmed, but ConfirmBlockchainDeposit fails.
	if err := w.progressPendingTransfers(context.Background()); err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	transfers.mu.Lock()
	var status domain.TransferStatus
	for _, row := range transfers.byKey {
		status = row.transfer.Status
	}
	transfers.mu.Unlock()
	if status != domain.TransferStatusConfirmed {
		t.Fatalf("tras tick 1, status = %s, esperaba 'confirmed' (crédito falló pero la transición a confirmed ya se había persistido)", status)
	}
	if confirmer.calls != 1 {
		t.Fatalf("llamadas a ConfirmBlockchainDeposit tras tick 1 = %d, esperaba 1", confirmer.calls)
	}

	// Without the fix, the row above would now be invisible to
	// FindPendingByNetwork forever, and tick 2 would never even look at it.
	pendingAfterTick1, _ := transfers.FindPendingByNetwork(context.Background(), domain.NetworkBSC)
	if len(pendingAfterTick1) != 1 {
		t.Fatalf("transferencias pendientes tras tick 1 = %d, esperaba 1 — una 'confirmed' sin acreditar debe seguir siendo 'pendiente' de crédito", len(pendingAfterTick1))
	}

	// Tick 2: same transfer is retried, and this time the credit succeeds.
	if err := w.progressPendingTransfers(context.Background()); err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	if confirmer.calls != 2 {
		t.Fatalf("llamadas a ConfirmBlockchainDeposit tras tick 2 = %d, esperaba 2 (un reintento real, no una acreditación fantasma)", confirmer.calls)
	}

	transfers.mu.Lock()
	for _, row := range transfers.byKey {
		status = row.transfer.Status
	}
	transfers.mu.Unlock()
	if status != domain.TransferStatusCredited {
		t.Errorf("status final = %s, esperaba 'credited' tras el reintento exitoso", status)
	}

	// Tick 3: now that it's credited, it must never be retried again —
	// exactly one credit, no matter how many more ticks run.
	if err := w.progressPendingTransfers(context.Background()); err != nil {
		t.Fatalf("tick 3: %v", err)
	}
	if confirmer.calls != 2 {
		t.Errorf("llamadas a ConfirmBlockchainDeposit tras tick 3 = %d, esperaba seguir en 2 — un depósito ya 'credited' nunca debe reprocesarse", confirmer.calls)
	}
}

// TestEVMDepositWatcher_ConfirmedTransferStillMonitoredForReorg is the
// Bloque 4 regression test for the reorg-detection-window gap that came
// bundled with the same bug: before the fix, a transfer that reached
// 'confirmed' dropped out of FindPendingByNetwork immediately, so a reorg
// landing between confirmation and credit was never checked at all.
func TestEVMDepositWatcher_ConfirmedTransferStillMonitoredForReorg(t *testing.T) {
	provider := newFakeEVMProvider()
	addrs := &fakeAddressLookup{managed: map[string]uuid.UUID{"0xManaged": uuid.New()}, userID: uuid.New()}
	transfers := newFakeTransferStore()
	syncState := newFakeSyncState()
	confirmer := &fakeConfirmer{failFirst: 1} // simulate the credit not having happened yet

	w := NewEVMDepositWatcher(provider, addrs, addrs, transfers, syncState, confirmer, Config{
		Network:               domain.NetworkBSC,
		Assets:                []EVMAsset{{AssetCode: "USDT", Kind: domain.AssetKindToken, ContractAddress: "0xC", Decimals: 18}},
		ConfirmationsRequired: 15,
	})

	transfer := domain.BlockchainTransfer{
		Network: domain.NetworkBSC, AssetCode: "USDT", ContractAddress: "0xC",
		TxHash: "0xreorgafterconfirm", LogIndex: 0, BlockHash: "0xORIGINAL", ToAddress: "0xManaged",
		Amount: decimal.NewFromInt(10),
	}
	id, _, _ := transfers.InsertDetected(context.Background(), transfer, uuid.New())
	provider.receipts["0xreorgafterconfirm"] = &domain.TransactionReceipt{TxHash: "0xreorgafterconfirm", BlockHash: "0xORIGINAL", Status: true, Confirmations: 15}

	// Tick 1: reaches 'confirmed', credit fails transiently (not credited yet).
	if err := w.progressPendingTransfers(context.Background()); err != nil {
		t.Fatalf("tick 1: %v", err)
	}

	// Now the chain reorgs: the tx's block is no longer canonical.
	provider.receipts["0xreorgafterconfirm"] = nil

	if err := w.progressPendingTransfers(context.Background()); err != nil {
		t.Fatalf("tick 2: %v", err)
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
		t.Errorf("status = %s, esperaba 'reorged' — un depósito 'confirmed' pero aún no acreditado debe seguir vigilado ante reorgs", status)
	}
}
