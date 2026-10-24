//go:build integration

package tests

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"korzadi-wallet/backend/internal/application/ledger"
	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/postgres"
)

func TestLedgerHardening_AppendOnlyAndAssetMatch(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	a, b := createTestAccount(t, pool), createTestAccount(t, pool)
	l := postgres.NewLedgerRepository(pool)
	testAdjustment(t, pool, l, b, 1)
	tx, err := l.ExecuteTransaction(ctx, domain.TxTypeTransfer, "transfer:"+uuid.NewString(), nil, "integration", nil, []domain.LedgerEntryInput{
		{WalletAccountID: a, AssetCode: "USD", Amount: decimal.NewFromInt(1)}, {WalletAccountID: b, AssetCode: "USD", Amount: decimal.NewFromInt(-1)},
	})
	if err != nil {
		t.Fatal(err)
	}
	var entryID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM ledger_entries WHERE ledger_transaction_id=$1 LIMIT 1`, tx.ID).Scan(&entryID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE ledger_entries SET amount=amount WHERE id=$1`, entryID); err == nil {
		t.Fatal("entry UPDATE should be rejected")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM ledger_entries WHERE id=$1`, entryID); err == nil {
		t.Fatal("entry DELETE should be rejected")
	}
	if _, err := pool.Exec(ctx, `UPDATE ledger_transactions SET updated_at=updated_at WHERE id=$1`, tx.ID); err != nil {
		t.Fatalf("updated_at-only update should pass: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE ledger_transactions SET description='changed' WHERE id=$1`, tx.ID); err == nil {
		t.Fatal("transaction business-field UPDATE should be rejected")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM ledger_transactions WHERE id=$1`, tx.ID); err == nil {
		t.Fatal("transaction DELETE should be rejected")
	}
	var deleteRule string
	if err := pool.QueryRow(ctx, `SELECT c.confdeltype::text FROM pg_constraint c WHERE c.conname='ledger_entries_ledger_transaction_id_fkey'`).Scan(&deleteRule); err != nil || deleteRule != "r" {
		t.Fatalf("expected ON DELETE RESTRICT, rule=%q err=%v", deleteRule, err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO ledger_entries(id,ledger_transaction_id,wallet_account_id,amount,asset_code) VALUES($1,$2,$3,1,'EUR')`, uuid.New(), tx.ID, a); err == nil {
		t.Fatal("entry asset mismatch should be rejected")
	}
}

func TestLedgerHardening_AdjustmentRequiresSuperAdminAndAudits(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	account := createTestAccount(t, pool)
	l := postgres.NewLedgerRepository(pool)
	key := "adjustment:" + uuid.NewString()
	entries := []domain.LedgerEntryInput{{WalletAccountID: account, AssetCode: "USD", Amount: decimal.NewFromInt(12)}}
	if _, err := l.ExecuteTransaction(ctx, domain.TxTypeAdjustment, key, nil, "bypass", nil, entries); !errors.Is(err, domain.ErrAdjustmentRequiresService) {
		t.Fatalf("direct adjustment should fail closed: %v", err)
	}
	actor := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,first_name,last_name) VALUES($1,$2,'x','Test','User')`, actor, actor.String()+"@test.local"); err != nil {
		t.Fatal(err)
	}
	svc := ledger.NewService(l, postgres.NewReservationRepository(pool)).WithAdjustmentAuthorization(postgres.NewRoleRepository(pool), nil)
	if _, err := svc.ExecuteAdjustment(ctx, actor, "", key, "motivo", entries); !errors.Is(err, ledger.ErrAdjustmentForbidden) {
		t.Fatalf("non-admin adjustment should fail: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_roles(user_id,role_id) SELECT $1,id FROM roles WHERE name='super_admin'`, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ExecuteAdjustment(ctx, actor, "", key, "   ", entries); err == nil {
		t.Fatal("empty reason should fail")
	}
	tx, err := svc.ExecuteAdjustment(ctx, actor, "", key, "corrección documentada", entries)
	if err != nil {
		t.Fatal(err)
	}
	var audits, events int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE entity_type='ledger_transaction' AND entity_id=$1 AND action='ledger_adjustment_posted'`, tx.ID).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM security_events WHERE resource_type='ledger_transaction' AND resource_id=$1 AND event_type='LEDGER_ADJUSTMENT'`, tx.ID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if audits != 1 || events != 1 {
		t.Fatalf("expected atomic audit+security rows, audits=%d events=%d", audits, events)
	}
}

func TestLedgerHardening_IntegrityReportIncludesLedgerAndReservations(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	account := createTestAccount(t, pool)
	l := postgres.NewLedgerRepository(pool)
	testAdjustment(t, pool, l, account, 100)
	reservationRepo := postgres.NewReservationRepository(pool)
	if _, err := reservationRepo.Reserve(ctx, account, "USD", decimal.NewFromInt(15), "integration", nil); err != nil {
		t.Fatal(err)
	}
	report := postgres.NewLedgerFundsRepository(pool)
	mismatches, err := report.FindLedgerIntegrityMismatches(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, mismatch := range mismatches {
		if mismatch.WalletAccountID == account {
			t.Fatalf("newly funded/reserved test account should reconcile, got %+v", mismatch)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE wallet_accounts SET balance_cache=balance_cache+1, locked_balance=locked_balance+1 WHERE id=$1`, account); err != nil {
		t.Fatal(err)
	}
	mismatches, err = report.FindLedgerIntegrityMismatches(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, mismatch := range mismatches {
		if mismatch.WalletAccountID == account {
			if !mismatch.LedgerBalance.Equal(decimal.NewFromInt(100)) || !mismatch.ActiveReservations.Equal(decimal.NewFromInt(15)) {
				t.Fatalf("integrity discrepancy values wrong: %+v", mismatch)
			}
			return
		}
	}
	t.Fatalf("integrity discrepancy for test account missing: %+v", mismatches)
}
