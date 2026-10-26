//go:build integration

package tests

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"korzadi-wallet/backend/internal/application/ledger"
	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/postgres"
)

func testAdjustment(t *testing.T, pool *pgxpool.Pool, l *postgres.LedgerRepository, account uuid.UUID, amount int64) {
	t.Helper()
	ctx := context.Background()
	actor := uuid.New()
	_, err := pool.Exec(ctx, `INSERT INTO users (id,email,password_hash,first_name,last_name) VALUES ($1,$2,'x','Test','Admin')`, actor, actor.String()+"@test.local")
	if err != nil {
		t.Fatalf("crear actor de adjustment: %v", err)
	}
	if err := postgres.NewRoleRepository(pool).AssignRole(ctx, actor, "super_admin"); err != nil {
		t.Fatalf("asignar super_admin: %v", err)
	}
	svc := ledger.NewService(l, postgres.NewReservationRepository(pool)).WithAdjustmentAuthorization(postgres.NewRoleRepository(pool), nil)
	var assetCode string
	if err := pool.QueryRow(ctx, `SELECT asset_code FROM wallet_accounts WHERE id=$1`, account).Scan(&assetCode); err != nil {
		t.Fatalf("obtener asset de la cuenta %s: %v", account, err)
	}
	_, err = svc.ExecuteAdjustment(ctx, actor, "", "adjustment:"+uuid.NewString(), "fondeo de integración", []domain.LedgerEntryInput{{WalletAccountID: account, AssetCode: assetCode, Amount: decimal.NewFromInt(amount)}})
	if err != nil {
		t.Fatalf("fondeo por adjustment: %v", err)
	}
}
