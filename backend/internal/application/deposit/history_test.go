package deposit

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
)

type fakeDepositReader struct {
	byUser map[uuid.UUID][]*BlockchainDepositRecord
	calls  struct {
		listLimit  int
		listOffset int
	}
}

func newFakeDepositReader() *fakeDepositReader {
	return &fakeDepositReader{byUser: map[uuid.UUID][]*BlockchainDepositRecord{}}
}

func (f *fakeDepositReader) ListByUser(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*BlockchainDepositRecord, error) {
	f.calls.listLimit = limit
	f.calls.listOffset = offset
	return f.byUser[userID], nil
}

func (f *fakeDepositReader) FindByIDForUser(ctx context.Context, id, userID uuid.UUID) (*BlockchainDepositRecord, error) {
	for _, d := range f.byUser[userID] {
		if d.ID == id {
			return d, nil
		}
	}
	return nil, nil // mirrors the real repository: not found OR not yours, indistinguishable
}

func sampleRecord(userVisible bool) *BlockchainDepositRecord {
	return &BlockchainDepositRecord{
		ID: uuid.New(), Network: domain.NetworkBSC, AssetCode: "USDT",
		TxHash: "0xabc", ToAddress: "0xManaged", Amount: decimal.NewFromInt(10),
		Confirmations: 20, RequiredConfirmations: 15, Status: "credited",
	}
}

func TestListForUser_DefaultsPageSizeWhenUnset(t *testing.T) {
	reader := newFakeDepositReader()
	svc := NewHistoryService(reader)

	if _, err := svc.ListForUser(context.Background(), uuid.New(), 0, 0); err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}
	if reader.calls.listLimit != defaultHistoryPageSize {
		t.Errorf("limit = %d, esperaba el default %d", reader.calls.listLimit, defaultHistoryPageSize)
	}
}

func TestListForUser_CapsExcessivePageSize(t *testing.T) {
	reader := newFakeDepositReader()
	svc := NewHistoryService(reader)

	if _, err := svc.ListForUser(context.Background(), uuid.New(), 999999, 0); err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}
	if reader.calls.listLimit != maxHistoryPageSize {
		t.Errorf("limit = %d, esperaba el tope %d — un cliente no debe poder forzar una consulta sin límite", reader.calls.listLimit, maxHistoryPageSize)
	}
}

func TestListForUser_RejectsNegativeOffset(t *testing.T) {
	reader := newFakeDepositReader()
	svc := NewHistoryService(reader)

	if _, err := svc.ListForUser(context.Background(), uuid.New(), 10, -50); err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}
	if reader.calls.listOffset != 0 {
		t.Errorf("offset = %d, esperaba 0 (un offset negativo se normaliza, nunca se pasa tal cual a SQL)", reader.calls.listOffset)
	}
}

func TestListForUser_OnlyReturnsOwnRecords(t *testing.T) {
	reader := newFakeDepositReader()
	userA, userB := uuid.New(), uuid.New()
	reader.byUser[userA] = []*BlockchainDepositRecord{sampleRecord(true)}
	reader.byUser[userB] = []*BlockchainDepositRecord{sampleRecord(true), sampleRecord(true)}

	svc := NewHistoryService(reader)

	listA, err := svc.ListForUser(context.Background(), userA, 0, 0)
	if err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}
	if len(listA) != 1 {
		t.Fatalf("depósitos de userA = %d, esperaba 1 — nunca debe ver los de userB", len(listA))
	}
}

func TestGetForUser_ReturnsErrDepositNotFound_WhenBelongsToAnotherUser(t *testing.T) {
	reader := newFakeDepositReader()
	owner, attacker := uuid.New(), uuid.New()
	record := sampleRecord(true)
	reader.byUser[owner] = []*BlockchainDepositRecord{record}

	svc := NewHistoryService(reader)

	_, err := svc.GetForUser(context.Background(), record.ID, attacker)
	if err != ErrDepositNotFound {
		t.Fatalf("esperaba ErrDepositNotFound cuando el depósito pertenece a otro usuario, obtuve: %v", err)
	}
}

func TestGetForUser_ReturnsErrDepositNotFound_WhenIDDoesNotExist(t *testing.T) {
	reader := newFakeDepositReader()
	svc := NewHistoryService(reader)

	_, err := svc.GetForUser(context.Background(), uuid.New(), uuid.New())
	if err != ErrDepositNotFound {
		t.Fatalf("esperaba ErrDepositNotFound para un id inexistente, obtuve: %v", err)
	}
}

func TestGetForUser_ReturnsRecordForOwner(t *testing.T) {
	reader := newFakeDepositReader()
	owner := uuid.New()
	record := sampleRecord(true)
	reader.byUser[owner] = []*BlockchainDepositRecord{record}

	svc := NewHistoryService(reader)

	got, err := svc.GetForUser(context.Background(), record.ID, owner)
	if err != nil {
		t.Fatalf("no esperaba error: %v", err)
	}
	if got.ID != record.ID {
		t.Errorf("id = %s, esperaba %s", got.ID, record.ID)
	}
}
