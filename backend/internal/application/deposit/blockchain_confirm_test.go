package deposit

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
)

// ---- fakes ----

// fakeLedger mimics the real consolidated engine's key guarantee: calling
// ExecuteTransaction twice with the same idempotencyKey returns the SAME
// ledger transaction, with a nil error both times — never a second credit,
// never an error on replay (see postgres.LedgerRepository.ExecuteTransaction).
type fakeLedger struct {
	mu       sync.Mutex
	posted   map[string]*domain.LedgerTransaction // idempotencyKey -> tx
	postCall int
}

func newFakeLedger() *fakeLedger {
	return &fakeLedger{posted: map[string]*domain.LedgerTransaction{}}
}

func (f *fakeLedger) ExecuteTransaction(ctx context.Context, txType domain.TransactionType, idempotencyKey string,
	referenceID *string, description string, createdBy *uuid.UUID,
	entries []domain.LedgerEntryInput) (*domain.LedgerTransaction, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.postCall++

	if tx, ok := f.posted[idempotencyKey]; ok {
		return tx, nil // idempotent replay — silent success, matches the real engine
	}

	sum := decimal.Zero
	for _, e := range entries {
		sum = sum.Add(e.Amount)
	}
	if !sum.IsZero() {
		return nil, fmt.Errorf("ledger desbalanceado: suma=%s", sum.String())
	}

	tx := &domain.LedgerTransaction{ID: uuid.New(), Type: txType, Status: domain.TransactionStatus("completed"), IdempotencyKey: idempotencyKey}
	f.posted[idempotencyKey] = tx
	return tx, nil
}

type fakeClearing struct{ id uuid.UUID }

func (f *fakeClearing) FindClearingAccount(ctx context.Context, network domain.Network, assetCode string) (uuid.UUID, error) {
	return f.id, nil
}

type fakeUserAccounts struct{ id uuid.UUID }

func (f *fakeUserAccounts) FindOrCreateUserAccount(ctx context.Context, userID uuid.UUID, assetCode string) (uuid.UUID, error) {
	return f.id, nil
}

// fakeRecorder mimics RecordCompleted's real upsert semantics: calling it
// twice for the same natural key (network, tx_hash, log_index) must not
// create a second row — it's always safe to call after a successful ledger
// post, whether that post was fresh or a replay.
type fakeRecorder struct {
	mu    sync.Mutex
	calls int
	byKey map[string]uuid.UUID
}

func newFakeRecorder() *fakeRecorder { return &fakeRecorder{byKey: map[string]uuid.UUID{}} }

func (f *fakeRecorder) RecordCompleted(ctx context.Context, in RecordCompletedInput) (uuid.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	key := fmt.Sprintf("%s|%s|%d", in.Network, in.TxHash, in.LogIndex)
	if id, ok := f.byKey[key]; ok {
		return id, nil
	}
	id := uuid.New()
	f.byKey[key] = id
	return id, nil
}

func newService() (*BlockchainDepositService, *fakeLedger, *fakeRecorder) {
	ledger := newFakeLedger()
	recorder := newFakeRecorder()
	svc := NewBlockchainDepositService(ledger, &fakeClearing{id: uuid.New()}, &fakeUserAccounts{id: uuid.New()}, recorder)
	return svc, ledger, recorder
}

func sampleConfirmedTransfer() domain.BlockchainTransfer {
	return domain.BlockchainTransfer{
		ID:              uuid.New(),
		Network:         domain.NetworkBSC,
		AssetCode:       "USDT",
		AssetKind:       domain.AssetKindToken,
		ContractAddress: "0x55d398326f99059fF775485246999027B3197955",
		TxHash:          "0xdeadbeef",
		LogIndex:        1,
		BlockNumber:     12345,
		BlockHash:       "0xblockhash",
		FromAddress:     "0xfrom",
		ToAddress:       "0xto",
		Amount:          decimal.NewFromInt(100),
		Decimals:        18,
		Confirmations:   20,
		Status:          domain.TransferStatusConfirmed,
	}
}

func TestConfirmBlockchainDeposit_RejectsUnconfirmedTransfer(t *testing.T) {
	svc, _, _ := newService()
	transfer := sampleConfirmedTransfer()
	transfer.Status = domain.TransferStatusDetected // not confirmed yet

	_, err := svc.ConfirmBlockchainDeposit(context.Background(), transfer, uuid.New())
	if err == nil {
		t.Fatal("esperaba error: nunca debe acreditarse un depósito solo 'detected'")
	}
}

func TestConfirmBlockchainDeposit_CreditsOnce(t *testing.T) {
	svc, ledger, recorder := newService()
	transfer := sampleConfirmedTransfer()

	if _, err := svc.ConfirmBlockchainDeposit(context.Background(), transfer, uuid.New()); err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}
	if ledger.postCall != 1 {
		t.Errorf("ExecuteTransaction llamado %d veces, esperaba 1", ledger.postCall)
	}
	if recorder.calls != 1 {
		t.Errorf("RecordCompleted llamado %d veces, esperaba 1", recorder.calls)
	}
}

func TestConfirmBlockchainDeposit_EntriesAreBalanced(t *testing.T) {
	svc, ledger, _ := newService()
	transfer := sampleConfirmedTransfer()

	if _, err := svc.ConfirmBlockchainDeposit(context.Background(), transfer, uuid.New()); err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}
	if ledger.postCall != 1 {
		t.Fatalf("se esperaba exactamente un post")
	}
	// fakeLedger.ExecuteTransaction ya rechaza sumas != 0 — si llegamos
	// hasta acá sin error, las entries (crédito usuario + débito clearing)
	// sumaron exactamente cero, como exige el spec.
}

func TestConfirmBlockchainDeposit_RejectsMissingAssetCode(t *testing.T) {
	svc, _, _ := newService()
	transfer := sampleConfirmedTransfer()
	transfer.AssetCode = ""

	_, err := svc.ConfirmBlockchainDeposit(context.Background(), transfer, uuid.New())
	if err == nil {
		t.Fatal("esperaba error: un watcher que no puebla AssetCode desde el asset registry es un bug, no debe acreditarse a ciegas")
	}
}

// TestConfirmBlockchainDeposit_TwoAssetsSameNetworkDoNotCollide is the
// regression test for the CRÍTICO finding Bloque 1 fixed: before the asset
// registry existed, a single BlockchainDepositService instance was
// hardcoded to one asset ("USDT"), so a second token (USDC) on the same
// network had no safe way to share the deposit pipeline.
func TestConfirmBlockchainDeposit_TwoAssetsSameNetworkDoNotCollide(t *testing.T) {
	svc, ledger, recorder := newService()
	userID := uuid.New()

	usdt := sampleConfirmedTransfer()
	usdt.AssetCode = "USDT"
	usdt.ContractAddress = "0x55d398326f99059fF775485246999027B3197955"

	usdc := sampleConfirmedTransfer()
	usdc.AssetCode = "USDC"
	usdc.ContractAddress = "0x8ac76a51cc950d9822d68b83fe1ad97b32cd580d"
	usdc.TxHash = "0xdeadbeef" // deliberately same tx hash+network — only the contract differs

	if _, err := svc.ConfirmBlockchainDeposit(context.Background(), usdt, userID); err != nil {
		t.Fatalf("USDT: no esperaba error: %v", err)
	}
	if _, err := svc.ConfirmBlockchainDeposit(context.Background(), usdc, userID); err != nil {
		t.Fatalf("USDC: no esperaba error — un segundo asset en la misma red/tx no debe chocar con el primero: %v", err)
	}

	if recorder.calls != 2 {
		t.Errorf("RecordCompleted llamado %d veces, esperaba 2 (uno por cada asset)", recorder.calls)
	}
	if len(ledger.posted) != 2 {
		t.Errorf("entradas únicas en ledger = %d, esperaba 2", len(ledger.posted))
	}
}

// TestConfirmBlockchainDeposit_DuplicateCallIsIdempotent is the Bloque 2
// regression test for the consolidated engine's replay semantics: a
// repeated call is a SILENT idempotent success now (no more
// ErrDepositAlreadyProcessed on this path) — exactly one real credit ever
// lands in the ledger, and RecordCompleted's own upsert means the
// read-model row is safe to "recreate" on every successful call.
func TestConfirmBlockchainDeposit_DuplicateCallIsIdempotent(t *testing.T) {
	svc, ledger, recorder := newService()
	transfer := sampleConfirmedTransfer()
	userID := uuid.New()

	if _, err := svc.ConfirmBlockchainDeposit(context.Background(), transfer, userID); err != nil {
		t.Fatalf("primera llamada no esperaba error: %v", err)
	}
	depositID2, err := svc.ConfirmBlockchainDeposit(context.Background(), transfer, userID)
	if err != nil {
		t.Fatalf("segunda llamada (replay) no debe fallar: %v", err)
	}
	if ledger.postCall != 2 {
		t.Errorf("ExecuteTransaction debe seguir siendo llamado en el replay (para detectarlo), llamadas=%d", ledger.postCall)
	}
	if len(ledger.posted) != 1 {
		t.Errorf("entradas de ledger únicas = %d, esperaba 1 (una sola acreditación real)", len(ledger.posted))
	}
	if recorder.calls != 2 {
		t.Errorf("RecordCompleted se llama en ambas veces (su propio upsert es idempotente), llamadas=%d, esperaba 2", recorder.calls)
	}
	if depositID2 == uuid.Nil {
		t.Error("el replay debe seguir devolviendo el blockchain_deposits.id válido, no uuid.Nil")
	}
}

// TestConfirmBlockchainDeposit_ConcurrentWorkers_OnlyOneCredit simulates two
// watcher goroutines racing to confirm the exact same transfer (spec §18:
// "Dos workers detectando el mismo evento simultáneamente. Resultado
// esperado: UNA SOLA acreditación").
func TestConfirmBlockchainDeposit_ConcurrentWorkers_OnlyOneCredit(t *testing.T) {
	svc, ledger, recorder := newService()
	transfer := sampleConfirmedTransfer()
	userID := uuid.New()

	const workers = 20
	var wg sync.WaitGroup
	results := make([]error, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			time.Sleep(time.Duration(idx%3) * time.Millisecond)
			_, callErr := svc.ConfirmBlockchainDeposit(context.Background(), transfer, userID)
			results[idx] = callErr
		}(i)
	}
	wg.Wait()

	for i, err := range results {
		if err != nil {
			t.Errorf("worker %d: no esperaba error (todo replay debe ser éxito silencioso): %v", i, err)
		}
	}
	if len(ledger.posted) != 1 {
		t.Errorf("entradas únicas en ledger = %d, esperaba 1 (una sola acreditación real pase lo que pase)", len(ledger.posted))
	}
	if recorder.calls != workers {
		t.Errorf("RecordCompleted debe llamarse en cada intento exitoso (su upsert es idempotente), se llamó %d veces, esperaba %d", recorder.calls, workers)
	}
}

// ---- Fase A: un replay ajeno no puede marcar el depósito como acreditado ----

// Si la idempotency_key del depósito ya está ocupada por OTRA operación (p. ej.
// una transferencia con clave maliciosa), el depósito NO se acredita ni se
// registra como completado.
func TestConfirmBlockchainDeposit_KeyOccupiedByOtherOperation_Fails(t *testing.T) {
	svc, ledger, recorder := newService()
	transfer := sampleConfirmedTransfer()

	key := fmt.Sprintf("blockchain-deposit:%s:%s:%s:%s:%d",
		transfer.Network, transfer.AssetCode, transfer.ContractAddress, transfer.TxHash, transfer.LogIndex)
	ledger.posted[key] = &domain.LedgerTransaction{
		ID: uuid.New(), Type: domain.TxTypeTransfer, IdempotencyKey: key,
	}

	_, err := svc.ConfirmBlockchainDeposit(context.Background(), transfer, uuid.New())
	if err == nil {
		t.Fatal("esperaba error: la clave pertenece a una transferencia, no a un depósito")
	}
	if !errors.Is(err, domain.ErrIdempotencyPayloadMismatch) {
		t.Fatalf("esperaba ErrIdempotencyPayloadMismatch, obtuve: %v", err)
	}
	if recorder.calls != 0 {
		t.Fatalf("RecordCompleted llamado %d veces: el depósito no debe marcarse acreditado", recorder.calls)
	}
}

// Replay legítimo: misma tx de depósito con el mismo tx_hash => éxito idempotente.
func TestConfirmBlockchainDeposit_LegitimateReplay_StillSucceeds(t *testing.T) {
	svc, ledger, recorder := newService()
	transfer := sampleConfirmedTransfer()
	user := uuid.New()

	id1, err := svc.ConfirmBlockchainDeposit(context.Background(), transfer, user)
	if err != nil {
		t.Fatalf("primera: %v", err)
	}
	id2, err := svc.ConfirmBlockchainDeposit(context.Background(), transfer, user)
	if err != nil {
		t.Fatalf("replay legítimo: %v", err)
	}
	if id1 != id2 || recorder.calls != 2 || ledger.postCall != 2 {
		t.Fatalf("ids %s/%s, recorder=%d, ledger=%d", id1, id2, recorder.calls, ledger.postCall)
	}
	if len(ledger.posted) != 1 {
		t.Fatalf("debía existir una sola tx en el ledger, hay %d", len(ledger.posted))
	}
}
