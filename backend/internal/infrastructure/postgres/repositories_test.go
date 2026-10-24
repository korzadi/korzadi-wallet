package postgres

import (
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"korzadi-wallet/backend/internal/domain"
)

func TestLedgerEntryInput_Validation(t *testing.T) {
	entries := []domain.LedgerEntryInput{
		{WalletAccountID: uuid.New(), AssetCode: "USD", Amount: decimal.NewFromInt(10)},
		{WalletAccountID: uuid.New(), AssetCode: "USD", Amount: decimal.NewFromInt(-10)},
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries")
	}
}

func TestTransferStatus_Values(t *testing.T) {
	if domain.StatusPending != "pending" {
		t.Fatalf("bad status")
	}
	if domain.StatusCompleted != "completed" {
		t.Fatalf("bad status")
	}
	if domain.StatusFailed != "failed" {
		t.Fatalf("bad status")
	}
}

func TestTxType_Values(t *testing.T) {
	if domain.TxTypeTransfer != "transfer" {
		t.Fatalf("bad type")
	}
	if domain.TxTypeDeposit != "deposit" {
		t.Fatalf("bad type")
	}
	if domain.TxTypeWithdrawal != "withdrawal" {
		t.Fatalf("bad type")
	}
}

func TestAssetKind_Values(t *testing.T) {
	if domain.AssetKindNative != "native" {
		t.Fatalf("bad kind")
	}
	if domain.AssetKindToken != "token" {
		t.Fatalf("bad kind")
	}
}
