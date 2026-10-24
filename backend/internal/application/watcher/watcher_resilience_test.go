package watcher

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
)

// ---- fakes Fase B ----------------------------------------------------------

type fakeFailedRow struct {
	id       uuid.UUID
	t        domain.BlockchainTransfer
	attempts int
	status   string
}

type fakeFailedStore struct {
	mu      sync.Mutex
	rows    map[string]*fakeFailedRow
	saveErr error
}

func newFakeFailedStore() *fakeFailedStore {
	return &fakeFailedStore{rows: map[string]*fakeFailedRow{}}
}

func (f *fakeFailedStore) Save(ctx context.Context, t domain.BlockchainTransfer, cause error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.saveErr != nil {
		return f.saveErr
	}
	k := keyOf(t)
	if r, ok := f.rows[k]; ok {
		r.attempts++
		if r.status == "resolved" {
			r.status = "pending"
		}
		return nil
	}
	f.rows[k] = &fakeFailedRow{id: uuid.New(), t: t, attempts: 1, status: "pending"}
	return nil
}

func (f *fakeFailedStore) ListRetryable(ctx context.Context, network domain.Network, limit int) ([]domain.FailedDepositEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.FailedDepositEvent
	for _, r := range f.rows {
		if r.status == "pending" && r.t.Network == network && len(out) < limit {
			out = append(out, domain.FailedDepositEvent{ID: r.id, Transfer: r.t, Attempts: r.attempts})
		}
	}
	return out, nil
}

func (f *fakeFailedStore) MarkResolved(ctx context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.rows {
		if r.id == id && r.status == "pending" {
			r.status = "resolved"
		}
	}
	return nil
}

func (f *fakeFailedStore) RecordAttemptFailure(ctx context.Context, id uuid.UUID, cause error, poisonAfter int) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.rows {
		if r.id == id && r.status == "pending" {
			r.attempts++
			if r.attempts >= poisonAfter {
				r.status = "poisoned"
				return true, nil
			}
			return false, nil
		}
	}
	return false, nil
}

func (f *fakeFailedStore) CountPending(ctx context.Context, network domain.Network) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.rows {
		if r.status == "pending" {
			n++
		}
	}
	return n, nil
}

func (f *fakeFailedStore) statusOf(t domain.BlockchainTransfer) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.rows[keyOf(t)]; ok {
		return r.status
	}
	return ""
}

func (f *fakeFailedStore) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.rows)
}

// fakeIncidents imita la idempotencia del repositorio real: un incidente
// ABIERTO por (red, tipo, tx, log).
type fakeIncidents struct {
	mu    sync.Mutex
	list  []domain.DepositIncident
	calls int
}

func (f *fakeIncidents) Report(ctx context.Context, inc domain.DepositIncident) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	for _, e := range f.list {
		if e.Network == inc.Network && e.Kind == inc.Kind && e.Transfer.TxHash == inc.Transfer.TxHash && e.Transfer.LogIndex == inc.Transfer.LogIndex {
			return nil
		}
	}
	f.list = append(f.list, inc)
	return nil
}

func (f *fakeIncidents) snapshot() []domain.DepositIncident {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.DepositIncident(nil), f.list...)
}

// FindRecentlyCredited hace que fakeTransferStore cumpla CreditedTransferLister.
func (f *fakeTransferStore) FindRecentlyCredited(ctx context.Context, network domain.Network, since time.Time, limit int) ([]domain.BlockchainTransfer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.BlockchainTransfer
	for _, row := range f.byKey {
		if row.transfer.Network == network && row.transfer.Status == domain.TransferStatusCredited {
			out = append(out, row.transfer)
		}
	}
	return out, nil
}

func (f *fakeTransferStore) statusOfTx(tx string) domain.TransferStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, row := range f.byKey {
		if row.transfer.TxHash == tx {
			return row.transfer.Status
		}
	}
	return ""
}

// flakyTransfers simula una BD que falla al insertar.
type flakyTransfers struct {
	*fakeTransferStore
	mu      sync.Mutex
	failing bool
	inserts int
}

func (f *flakyTransfers) setFailing(b bool) { f.mu.Lock(); f.failing = b; f.mu.Unlock() }
func (f *flakyTransfers) insertCalls() int  { f.mu.Lock(); defer f.mu.Unlock(); return f.inserts }

func (f *flakyTransfers) InsertDetected(ctx context.Context, t domain.BlockchainTransfer, id uuid.UUID) (uuid.UUID, bool, error) {
	f.mu.Lock()
	f.inserts++
	failing := f.failing
	f.mu.Unlock()
	if failing {
		return uuid.Nil, false, errors.New("db caída")
	}
	return f.fakeTransferStore.InsertDetected(ctx, t, id)
}

// seqReceiptProvider devuelve receipts en secuencia (para simular un nodo RPC atrasado).
type seqReceiptProvider struct {
	*fakeEVMProvider
	mu  sync.Mutex
	seq []*domain.TransactionReceipt
	i   int
}

func (s *seqReceiptProvider) GetReceipt(ctx context.Context, txHash string) (*domain.TransactionReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.i >= len(s.seq) {
		return s.seq[len(s.seq)-1], nil
	}
	r := s.seq[s.i]
	s.i++
	return r, nil
}

// ---- helpers -------------------------------------------------------------------

const rContract = "0xContract"

func rEvent(tx string, block uint64) domain.OnChainTransferEvent {
	return domain.OnChainTransferEvent{
		ContractAddress: rContract, TxHash: tx, LogIndex: 0, BlockNumber: block,
		BlockHash: "0xblock50", From: "0xfrom", To: "0xManaged", RawAmount: decimal.NewFromInt(10),
	}
}

func newRWatcher(provider domain.BlockchainProvider, transfers TransferStore, sync *fakeSyncState) *EVMDepositWatcher {
	addrs := &fakeAddressLookup{managed: map[string]uuid.UUID{"0xManaged": uuid.New()}, userID: uuid.New()}
	return NewEVMDepositWatcher(provider, addrs, addrs, transfers, sync, &fakeConfirmer{}, Config{
		Network:               domain.NetworkBSC,
		Assets:                []EVMAsset{{AssetCode: "USDT", Kind: domain.AssetKindToken, ContractAddress: rContract, Decimals: 18}},
		ConfirmationsRequired: 15,
		MaxBlocksPerScan:      2000,
		SafetyLagBlocks:       3,
		MaxEventAttempts:      3,
	})
}

func credited(t *testing.T, store *fakeTransferStore, tx string) domain.BlockchainTransfer {
	t.Helper()
	tr := domain.BlockchainTransfer{
		Network: domain.NetworkBSC, AssetCode: "USDT", AssetKind: domain.AssetKindToken, ContractAddress: rContract,
		TxHash: tx, LogIndex: 0, BlockNumber: 50, BlockHash: "0xblock50", ToAddress: "0xManaged",
		Amount: decimal.NewFromInt(10), Decimals: 18,
	}
	id, _, err := store.InsertDetected(context.Background(), tr, uuid.New())
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := store.MarkCredited(context.Background(), id, uuid.New()); err != nil {
		t.Fatalf("credit: %v", err)
	}
	tr.ID = id
	tr.Status = domain.TransferStatusCredited
	return tr
}

// ---- tests: eventos que fallan --------------------------------------------------

// El bug original: un evento que falla se descartaba y el checkpoint avanzaba.
// Ahora se aparca, el checkpoint avanza, y el evento se recupera después.
func TestResilience_FailedEvent_IsParked_AndRecoveredLater(t *testing.T) {
	ctx := context.Background()
	provider := newFakeEVMProvider()
	provider.latestBlock = 100
	provider.tokenEvents[rContract] = []domain.OnChainTransferEvent{rEvent("0xabc", 50)}

	transfers := &flakyTransfers{fakeTransferStore: newFakeTransferStore(), failing: true}
	sync := newFakeSyncState()
	sync.AdvanceTo(ctx, domain.NetworkBSC, 49)
	failed := newFakeFailedStore()
	w := newRWatcher(provider, transfers, sync)
	w.WithFailedEventStore(failed)

	if err := w.scanNewBlocks(ctx); err != nil {
		t.Fatalf("scanNewBlocks: %v", err)
	}
	if got, _, _ := sync.GetLastProcessedBlock(ctx, domain.NetworkBSC); got != 97 {
		t.Fatalf("checkpoint = %d, esperaba 97 (el evento quedó aparcado, no bloquea)", got)
	}
	if failed.count() != 1 {
		t.Fatalf("eventos aparcados = %d, esperaba 1", failed.count())
	}
	if p, _ := transfers.FindPendingByNetwork(ctx, domain.NetworkBSC); len(p) != 0 {
		t.Fatalf("no debía haber transfers registrados aún: %d", len(p))
	}

	// La BD vuelve: el reintento registra el evento y lo marca resuelto.
	transfers.setFailing(false)
	if err := w.retryFailedEvents(ctx); err != nil {
		t.Fatalf("retryFailedEvents: %v", err)
	}
	if p, _ := transfers.FindPendingByNetwork(ctx, domain.NetworkBSC); len(p) != 1 {
		t.Fatalf("tras el reintento esperaba 1 transfer registrado, hay %d (depósito perdido)", len(p))
	}
	if st := failed.statusOf(rRow("0xabc")); st != "resolved" {
		t.Fatalf("estado del evento aparcado = %q, esperaba resolved", st)
	}
}

func rRow(tx string) domain.BlockchainTransfer {
	return domain.BlockchainTransfer{Network: domain.NetworkBSC, ContractAddress: rContract, TxHash: tx, LogIndex: 0}
}

// Sin FailedEventStore NO se puede conservar el evento => el checkpoint no avanza
// y el siguiente tick reescanea el rango (nunca se pierde).
func TestResilience_NoFailedStore_CheckpointDoesNotAdvance_ThenRecovers(t *testing.T) {
	ctx := context.Background()
	provider := newFakeEVMProvider()
	provider.latestBlock = 100
	provider.tokenEvents[rContract] = []domain.OnChainTransferEvent{rEvent("0xabc", 50)}

	transfers := &flakyTransfers{fakeTransferStore: newFakeTransferStore(), failing: true}
	sync := newFakeSyncState()
	sync.AdvanceTo(ctx, domain.NetworkBSC, 49)
	w := newRWatcher(provider, transfers, sync)

	if err := w.scanNewBlocks(ctx); err == nil {
		t.Fatal("esperaba error: el evento falló y no hay dónde aparcarlo")
	}
	if got, _, _ := sync.GetLastProcessedBlock(ctx, domain.NetworkBSC); got != 49 {
		t.Fatalf("checkpoint = %d, debía quedarse en 49", got)
	}

	transfers.setFailing(false)
	if err := w.scanNewBlocks(ctx); err != nil {
		t.Fatalf("reintento: %v", err)
	}
	if p, _ := transfers.FindPendingByNetwork(ctx, domain.NetworkBSC); len(p) != 1 {
		t.Fatalf("el depósito debía registrarse en el reintento, hay %d", len(p))
	}
	if got, _, _ := sync.GetLastProcessedBlock(ctx, domain.NetworkBSC); got != 97 {
		t.Fatalf("checkpoint = %d, esperaba 97", got)
	}
}

// Si ni siquiera se puede aparcar, el checkpoint tampoco avanza.
func TestResilience_ParkingFails_CheckpointDoesNotAdvance(t *testing.T) {
	ctx := context.Background()
	provider := newFakeEVMProvider()
	provider.latestBlock = 100
	provider.tokenEvents[rContract] = []domain.OnChainTransferEvent{rEvent("0xabc", 50)}

	transfers := &flakyTransfers{fakeTransferStore: newFakeTransferStore(), failing: true}
	sync := newFakeSyncState()
	sync.AdvanceTo(ctx, domain.NetworkBSC, 49)
	failed := newFakeFailedStore()
	failed.saveErr = errors.New("db caída también para aparcar")
	w := newRWatcher(provider, transfers, sync)
	w.WithFailedEventStore(failed)

	if err := w.scanNewBlocks(ctx); err == nil {
		t.Fatal("esperaba error")
	}
	if got, _, _ := sync.GetLastProcessedBlock(ctx, domain.NetworkBSC); got != 49 {
		t.Fatalf("checkpoint = %d, debía quedarse en 49", got)
	}
}

// Un evento envenenado agota sus reintentos, abre UN incidente crítico y deja
// de reintentarse, sin bloquear el resto.
func TestResilience_PoisonEvent_ReportsIncident_AndStopsRetrying(t *testing.T) {
	ctx := context.Background()
	provider := newFakeEVMProvider()
	provider.latestBlock = 100
	provider.tokenEvents[rContract] = []domain.OnChainTransferEvent{rEvent("0xbad", 50)}

	transfers := &flakyTransfers{fakeTransferStore: newFakeTransferStore(), failing: true}
	sync := newFakeSyncState()
	sync.AdvanceTo(ctx, domain.NetworkBSC, 49)
	failed := newFakeFailedStore()
	incidents := &fakeIncidents{}
	w := newRWatcher(provider, transfers, sync)
	w.WithFailedEventStore(failed).WithIncidentReporter(incidents)

	if err := w.scanNewBlocks(ctx); err != nil { // intento 1 (aparcado)
		t.Fatalf("scan: %v", err)
	}
	_ = w.retryFailedEvents(ctx) // intento 2
	if len(incidents.snapshot()) != 0 {
		t.Fatal("aún no debía haber incidente (MaxEventAttempts=3)")
	}
	_ = w.retryFailedEvents(ctx) // intento 3 => poisoned
	inc := incidents.snapshot()
	if len(inc) != 1 || inc[0].Kind != domain.IncidentPoisonedEvent {
		t.Fatalf("esperaba 1 incidente poisoned_event, obtuve %+v", inc)
	}
	if failed.statusOf(rRow("0xbad")) != "poisoned" {
		t.Fatalf("estado = %q, esperaba poisoned", failed.statusOf(rRow("0xbad")))
	}

	before := transfers.insertCalls()
	_ = w.retryFailedEvents(ctx)
	_ = w.retryFailedEvents(ctx)
	if transfers.insertCalls() != before {
		t.Fatal("un evento envenenado no debe reintentarse más")
	}
	if len(incidents.snapshot()) != 1 {
		t.Fatal("no debe duplicarse el incidente")
	}
}

// ---- tests: reorg / revert tras acreditar -------------------------------------------

func newRecheckWatcher(t *testing.T, provider domain.BlockchainProvider) (*EVMDepositWatcher, *fakeTransferStore, *fakeIncidents) {
	t.Helper()
	store := newFakeTransferStore()
	incidents := &fakeIncidents{}
	w := newRWatcher(provider, store, newFakeSyncState())
	w.WithIncidentReporter(incidents).WithPostCreditRecheck(store, time.Hour)
	return w, store, incidents
}

func TestResilience_ReorgAfterCredit_ReportsIncident_AndLeavesStateUntouched(t *testing.T) {
	provider := newFakeEVMProvider()
	provider.receipts["0xabc"] = &domain.TransactionReceipt{TxHash: "0xabc", BlockHash: "0xOTHER", Status: true, Confirmations: 40}
	w, store, incidents := newRecheckWatcher(t, provider)
	credited(t, store, "0xabc")

	if err := w.recheckCreditedTransfers(context.Background()); err != nil {
		t.Fatalf("recheck: %v", err)
	}
	inc := incidents.snapshot()
	if len(inc) != 1 || inc[0].Kind != domain.IncidentReorgAfterCredit {
		t.Fatalf("esperaba 1 incidente reorg_after_credit, obtuve %+v", inc)
	}
	if inc[0].TransferID == uuid.Nil {
		t.Fatal("el incidente debe referenciar el transfer")
	}
	if got := store.statusOfTx("0xabc"); got != domain.TransferStatusCredited {
		t.Fatalf("el estado cambió a %q: no debe haber reversión automática", got)
	}

	// Re-verificar de nuevo no duplica el incidente abierto.
	_ = w.recheckCreditedTransfers(context.Background())
	if len(incidents.snapshot()) != 1 {
		t.Fatal("el incidente abierto no debe duplicarse")
	}
}

func TestResilience_MissingReceiptAfterCredit_ReportsReorg(t *testing.T) {
	provider := newFakeEVMProvider() // sin receipts => nil
	w, store, incidents := newRecheckWatcher(t, provider)
	credited(t, store, "0xabc")

	_ = w.recheckCreditedTransfers(context.Background())
	inc := incidents.snapshot()
	if len(inc) != 1 || inc[0].Kind != domain.IncidentReorgAfterCredit {
		t.Fatalf("esperaba reorg_after_credit, obtuve %+v", inc)
	}
}

func TestResilience_RevertedAfterCredit_ReportsIncident(t *testing.T) {
	provider := newFakeEVMProvider()
	provider.receipts["0xabc"] = &domain.TransactionReceipt{TxHash: "0xabc", BlockHash: "0xblock50", Status: false}
	w, store, incidents := newRecheckWatcher(t, provider)
	credited(t, store, "0xabc")

	_ = w.recheckCreditedTransfers(context.Background())
	inc := incidents.snapshot()
	if len(inc) != 1 || inc[0].Kind != domain.IncidentRevertedAfterCredit {
		t.Fatalf("esperaba reverted_after_credit, obtuve %+v", inc)
	}
}

func TestResilience_HealthyCredited_NoIncident(t *testing.T) {
	provider := newFakeEVMProvider()
	provider.receipts["0xabc"] = &domain.TransactionReceipt{TxHash: "0xabc", BlockHash: "0xblock50", Status: true, Confirmations: 30}
	w, store, incidents := newRecheckWatcher(t, provider)
	credited(t, store, "0xabc")

	_ = w.recheckCreditedTransfers(context.Background())
	if n := len(incidents.snapshot()); n != 0 {
		t.Fatalf("no debía haber incidentes, hay %d", n)
	}
}

// Un nodo RPC atrasado que responde "no existe" una vez NO debe generar un
// incidente: el hallazgo debe repetirse en la 2ª consulta.
func TestResilience_TransientMissingReceipt_NoFalsePositive(t *testing.T) {
	provider := &seqReceiptProvider{
		fakeEVMProvider: newFakeEVMProvider(),
		seq: []*domain.TransactionReceipt{
			nil, // 1ª consulta: nodo atrasado
			{TxHash: "0xabc", BlockHash: "0xblock50", Status: true, Confirmations: 30}, // 2ª: normal
		},
	}
	w, store, incidents := newRecheckWatcher(t, provider)
	credited(t, store, "0xabc")

	_ = w.recheckCreditedTransfers(context.Background())
	if n := len(incidents.snapshot()); n != 0 {
		t.Fatalf("falso positivo: %d incidente(s) por una respuesta transitoria", n)
	}
}

// ---- tests: backfill y lag ------------------------------------------------------------

func TestResilience_Backfill_DoesNotTouchCheckpoint_AndScansInChunks(t *testing.T) {
	ctx := context.Background()
	provider := newFakeEVMProvider()
	provider.latestBlock = 1000
	provider.tokenEvents[rContract] = []domain.OnChainTransferEvent{rEvent("0xaaa", 120), rEvent("0xbbb", 250)}

	transfers := newFakeTransferStore()
	sync := newFakeSyncState()
	w := newRWatcher(provider, transfers, sync)
	w.cfg.MaxBlocksPerScan = 100

	if err := w.Backfill(ctx, 100, 299); err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if p, _ := transfers.FindPendingByNetwork(ctx, domain.NetworkBSC); len(p) != 2 {
		t.Fatalf("transfers registrados = %d, esperaba 2", len(p))
	}
	if provider.transferCalls != 2 {
		t.Fatalf("llamadas a getLogs = %d, esperaba 2 lotes ([100,199] y [200,299])", provider.transferCalls)
	}
	if _, ok, _ := sync.GetLastProcessedBlock(ctx, domain.NetworkBSC); ok {
		t.Fatal("el backfill NO debe tocar el checkpoint del watcher en vivo")
	}

	// Idempotente: repetirlo no duplica nada.
	if err := w.Backfill(ctx, 100, 299); err != nil {
		t.Fatalf("Backfill repetido: %v", err)
	}
	if p, _ := transfers.FindPendingByNetwork(ctx, domain.NetworkBSC); len(p) != 2 {
		t.Fatalf("tras repetir hay %d transfers, esperaba 2", len(p))
	}
}

func TestResilience_Backfill_InvalidRangeAndClipping(t *testing.T) {
	provider := newFakeEVMProvider()
	provider.latestBlock = 1000
	w := newRWatcher(provider, newFakeTransferStore(), newFakeSyncState())

	if err := w.Backfill(context.Background(), 500, 100); err == nil {
		t.Fatal("esperaba error por rango invertido")
	}
	if err := w.Backfill(context.Background(), 990, 5000); err != nil { // 'to' se recorta a 997
		t.Fatalf("rango recortado al bloque seguro: %v", err)
	}
	if err := w.Backfill(context.Background(), 2000, 3000); err == nil {
		t.Fatal("esperaba error: el rango está por encima del bloque seguro")
	}
}

func TestResilience_Backfill_FailedEventIsParked(t *testing.T) {
	ctx := context.Background()
	provider := newFakeEVMProvider()
	provider.latestBlock = 1000
	provider.tokenEvents[rContract] = []domain.OnChainTransferEvent{rEvent("0xaaa", 120)}

	transfers := &flakyTransfers{fakeTransferStore: newFakeTransferStore(), failing: true}
	failed := newFakeFailedStore()
	w := newRWatcher(provider, transfers, newFakeSyncState())
	w.WithFailedEventStore(failed)

	if err := w.Backfill(ctx, 100, 199); err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if failed.count() != 1 {
		t.Fatalf("eventos aparcados = %d, esperaba 1", failed.count())
	}
}

func TestResilience_Stats_ReportsLag(t *testing.T) {
	ctx := context.Background()
	provider := newFakeEVMProvider()
	provider.latestBlock = 1000 // bloque seguro = 997
	sync := newFakeSyncState()
	sync.AdvanceTo(ctx, domain.NetworkBSC, 100)
	w := newRWatcher(provider, newFakeTransferStore(), sync)
	w.cfg.MaxBlocksPerScan = 50

	if err := w.scanNewBlocks(ctx); err != nil {
		t.Fatalf("scan: %v", err)
	}
	st := w.Stats()
	if st.SafeTip != 997 || st.LastProcessed != 150 || st.LagBlocks != 847 {
		t.Fatalf("stats = %+v, esperaba SafeTip=997 LastProcessed=150 Lag=847", st)
	}
}

// tick: un fallo al escanear no debe impedir progresar/acreditar lo ya detectado.
func TestResilience_Tick_ProgressRunsEvenIfScanFails(t *testing.T) {
	ctx := context.Background()
	provider := newFakeEVMProvider()
	provider.latestBlock = 100
	provider.tokenEvents[rContract] = []domain.OnChainTransferEvent{rEvent("0xnew", 50)}
	provider.receipts["0xold"] = &domain.TransactionReceipt{TxHash: "0xold", BlockHash: "0xblock50", Status: true, Confirmations: 30}

	transfers := &flakyTransfers{fakeTransferStore: newFakeTransferStore()}
	// Un transfer ya detectado y con confirmaciones suficientes (se insertó antes de la falla).
	old := rRow("0xold")
	old.AssetCode, old.AssetKind, old.BlockNumber, old.BlockHash = "USDT", domain.AssetKindToken, 50, "0xblock50"
	old.ToAddress, old.Amount, old.Decimals = "0xManaged", decimal.NewFromInt(10), 18
	transfers.fakeTransferStore.InsertDetected(ctx, old, uuid.New())

	sync := newFakeSyncState()
	sync.AdvanceTo(ctx, domain.NetworkBSC, 49)
	transfers.setFailing(true) // el escaneo del evento nuevo fallará (sin store => error)
	w := newRWatcher(provider, transfers, sync)

	err := w.tick(ctx)
	if err == nil {
		t.Fatal("esperaba error del escaneo")
	}
	if st := transfers.statusOfTx("0xold"); st != domain.TransferStatusCredited {
		t.Fatalf("estado de 0xold = %q: el progreso de pendientes debía ejecutarse igual y acreditarlo", st)
	}
}
