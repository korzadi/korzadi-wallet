//go:build integration

package tests

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/infrastructure/postgres"
)

func failedEventTransfer(tx string) domain.BlockchainTransfer {
	return domain.BlockchainTransfer{
		Network: domain.NetworkBSC, AssetCode: "USDT", AssetKind: domain.AssetKindToken,
		ContractAddress: "0xContractTest", TxHash: tx, LogIndex: 3, BlockNumber: 123, BlockHash: "0xblock",
		FromAddress: "0xfrom", ToAddress: "0xto", Amount: decimal.RequireFromString("12.5"), Decimals: 18,
	}
}

func TestWatcherFailedEvents_Lifecycle(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	repo := postgres.NewWatcherFailedEventRepository(pool)
	ctx := context.Background()

	tx := "0xfail-" + uuid.NewString()
	defer pool.Exec(ctx, `DELETE FROM watcher_failed_events WHERE tx_hash = $1`, tx)
	tr := failedEventTransfer(tx)

	findTx := func(list []domain.FailedDepositEvent) *domain.FailedDepositEvent {
		for i := range list {
			if list[i].Transfer.TxHash == tx {
				return &list[i]
			}
		}
		return nil
	}

	if err := repo.Save(ctx, tr, errors.New("db caída")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := repo.Save(ctx, tr, errors.New("db caída otra vez")); err != nil { // upsert => attempts=2
		t.Fatalf("Save (upsert): %v", err)
	}
	var attempts int
	if err := pool.QueryRow(ctx, `SELECT attempts FROM watcher_failed_events WHERE tx_hash = $1`, tx).Scan(&attempts); err != nil || attempts != 2 {
		t.Fatalf("attempts = %d (err=%v), esperaba 2", attempts, err)
	}

	// Backoff: recién fallado => todavía no es reintentable.
	list, err := repo.ListRetryable(ctx, domain.NetworkBSC, 100)
	if err != nil {
		t.Fatalf("ListRetryable: %v", err)
	}
	if findTx(list) != nil {
		t.Fatal("un evento recién fallado no debe ser reintentable (backoff)")
	}

	// Pasado el backoff aparece, con todos sus campos intactos.
	if _, err := pool.Exec(ctx, `UPDATE watcher_failed_events SET last_attempt_at = now() - interval '1 hour' WHERE tx_hash = $1`, tx); err != nil {
		t.Fatalf("envejecer fila: %v", err)
	}
	list, _ = repo.ListRetryable(ctx, domain.NetworkBSC, 100)
	fe := findTx(list)
	if fe == nil {
		t.Fatal("el evento debía ser reintentable tras el backoff")
	}
	got := fe.Transfer
	if got.LogIndex != 3 || got.BlockNumber != 123 || got.AssetKind != domain.AssetKindToken ||
		!got.Amount.Equal(tr.Amount) || got.ContractAddress != "0xContractTest" || got.Decimals != 18 {
		t.Fatalf("campos alterados al guardar/leer: %+v", got)
	}

	// Intentos fallidos hasta envenenar.
	poisoned, err := repo.RecordAttemptFailure(ctx, fe.ID, errors.New("x"), 100)
	if err != nil || poisoned {
		t.Fatalf("no debía envenenarse aún (poisoned=%v err=%v)", poisoned, err)
	}
	poisoned, err = repo.RecordAttemptFailure(ctx, fe.ID, errors.New("x"), 4) // attempts 3 -> 4
	if err != nil || !poisoned {
		t.Fatalf("debía envenenarse al llegar a 4 intentos (poisoned=%v err=%v)", poisoned, err)
	}
	pool.Exec(ctx, `UPDATE watcher_failed_events SET last_attempt_at = now() - interval '1 hour' WHERE tx_hash = $1`, tx)
	list, _ = repo.ListRetryable(ctx, domain.NetworkBSC, 100)
	if findTx(list) != nil {
		t.Fatal("un evento envenenado no debe volver a listarse")
	}
}

func TestWatcherFailedEvents_ResolveAndReopen(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	repo := postgres.NewWatcherFailedEventRepository(pool)
	ctx := context.Background()

	tx := "0xres-" + uuid.NewString()
	defer pool.Exec(ctx, `DELETE FROM watcher_failed_events WHERE tx_hash = $1`, tx)
	tr := failedEventTransfer(tx)

	if err := repo.Save(ctx, tr, errors.New("e")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	var id uuid.UUID
	pool.QueryRow(ctx, `SELECT id FROM watcher_failed_events WHERE tx_hash = $1`, tx).Scan(&id)
	if err := repo.MarkResolved(ctx, id); err != nil {
		t.Fatalf("MarkResolved: %v", err)
	}
	var status string
	pool.QueryRow(ctx, `SELECT status FROM watcher_failed_events WHERE tx_hash = $1`, tx).Scan(&status)
	if status != "resolved" {
		t.Fatalf("status = %s, esperaba resolved", status)
	}
	// Si el mismo evento vuelve a fallar, se reabre.
	if err := repo.Save(ctx, tr, errors.New("e2")); err != nil {
		t.Fatalf("Save de reapertura: %v", err)
	}
	pool.QueryRow(ctx, `SELECT status FROM watcher_failed_events WHERE tx_hash = $1`, tx).Scan(&status)
	if status != "pending" {
		t.Fatalf("status = %s, esperaba pending (reabierto)", status)
	}
}

func TestDepositIncidents_ReportIsIdempotent_AndFindRecentlyCredited(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	incidents := postgres.NewDepositIncidentRepository(pool)
	transfers := postgres.NewBlockchainTransferRepository(pool)
	ctx := context.Background()

	tx := "0xinc-" + uuid.NewString()
	defer pool.Exec(ctx, `DELETE FROM deposit_incidents WHERE tx_hash = $1`, tx)
	defer pool.Exec(ctx, `DELETE FROM blockchain_transfers WHERE tx_hash = $1`, tx)

	var transferID uuid.UUID
	err := pool.QueryRow(ctx, `
		INSERT INTO blockchain_transfers
			(network, asset_code, contract_address, tx_hash, log_index, block_number, block_hash,
			 from_address, to_address, amount, decimals, confirmations, status)
		VALUES ('BSC','USDT','0xContractTest',$1,0,50,'0xblock50','0xfrom','0xto',10,18,30,'credited')
		RETURNING id
	`, tx).Scan(&transferID)
	if err != nil {
		t.Fatalf("insertar transfer acreditado: %v", err)
	}

	// FindRecentlyCredited respeta la ventana.
	recent, err := transfers.FindRecentlyCredited(ctx, domain.NetworkBSC, time.Now().Add(-time.Hour), 1000)
	if err != nil {
		t.Fatalf("FindRecentlyCredited: %v", err)
	}
	found := false
	for _, r := range recent {
		if r.TxHash == tx {
			found = true
			if r.Status != domain.TransferStatusCredited || r.AssetKind != domain.AssetKindToken {
				t.Fatalf("fila inesperada: %+v", r)
			}
		}
	}
	if !found {
		t.Fatal("el depósito acreditado debía aparecer en la ventana")
	}
	future, _ := transfers.FindRecentlyCredited(ctx, domain.NetworkBSC, time.Now().Add(time.Hour), 1000)
	for _, r := range future {
		if r.TxHash == tx {
			t.Fatal("no debe aparecer fuera de la ventana")
		}
	}

	// Report idempotente por (red, tipo, tx, log) mientras esté abierto.
	inc := domain.DepositIncident{
		Network: domain.NetworkBSC, Kind: domain.IncidentReorgAfterCredit, TransferID: transferID,
		Transfer: domain.BlockchainTransfer{
			TxHash: tx, LogIndex: 0, AssetCode: "USDT", ToAddress: "0xto", Amount: decimal.NewFromInt(10),
			BlockHash: "0xblock50", BlockNumber: 50, FromAddress: "0xfrom", ContractAddress: "0xContractTest",
		},
		Details: "prueba",
	}
	for i := 0; i < 3; i++ {
		if err := incidents.Report(ctx, inc); err != nil {
			t.Fatalf("Report #%d: %v", i, err)
		}
	}
	countOpen := func() int {
		var n int
		pool.QueryRow(ctx, `SELECT count(*) FROM deposit_incidents WHERE tx_hash = $1 AND status = 'open'`, tx).Scan(&n)
		return n
	}
	if n := countOpen(); n != 1 {
		t.Fatalf("incidentes abiertos = %d, esperaba 1", n)
	}

	// Cerrado el incidente, uno nuevo sí puede abrirse.
	pool.Exec(ctx, `UPDATE deposit_incidents SET status = 'resolved', resolved_at = now() WHERE tx_hash = $1`, tx)
	if err := incidents.Report(ctx, inc); err != nil {
		t.Fatalf("Report tras resolver: %v", err)
	}
	if n := countOpen(); n != 1 {
		t.Fatalf("incidentes abiertos tras reabrir = %d, esperaba 1", n)
	}

	// Un incidente sin transfer (evento envenenado) también se registra.
	poison := inc
	poison.Kind = domain.IncidentPoisonedEvent
	poison.TransferID = uuid.Nil
	if err := incidents.Report(ctx, poison); err != nil {
		t.Fatalf("Report poisoned_event sin transfer: %v", err)
	}
}
