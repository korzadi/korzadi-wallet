package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
)

// entryQuerier lo satisfacen *pgxpool.Pool y pgx.Tx.
type entryQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// netEntries agrega los entries por (cuenta, asset) y descarta los netos en
// cero, de modo que dos listas equivalentes se comparan igual sin importar el
// orden ni cómo se partieron.
func netEntries(entries []domain.LedgerEntryInput) map[string]decimal.Decimal {
	out := make(map[string]decimal.Decimal, len(entries))
	for _, e := range entries {
		k := e.WalletAccountID.String() + "|" + e.AssetCode
		out[k] = out[k].Add(e.Amount)
	}
	for k, v := range out {
		if v.IsZero() {
			delete(out, k)
		}
	}
	return out
}

func sameNetEntries(a, b map[string]decimal.Decimal) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		w, ok := b[k]
		if !ok || !v.Equal(w) {
			return false
		}
	}
	return true
}

// verifyReplayPayload comprueba que una transacción ya existente con la misma
// idempotency_key sea EXACTAMENTE la operación que se pide ahora: mismo tipo y
// mismo conjunto de entries (cuenta, asset, monto). Si no, devuelve
// domain.ErrIdempotencyPayloadMismatch: una clave reutilizada para otra cosa
// nunca debe tratarse como éxito idempotente.
func verifyReplayPayload(ctx context.Context, q entryQuerier, existing *domain.LedgerTransaction,
	txType domain.TransactionType, entries []domain.LedgerEntryInput) error {

	if existing.Type != txType {
		return domain.ErrIdempotencyPayloadMismatch
	}

	rows, err := q.Query(ctx, `
		SELECT wallet_account_id, asset_code, amount
		FROM ledger_entries
		WHERE ledger_transaction_id = $1
	`, existing.ID)
	if err != nil {
		return fmt.Errorf("leer entries para verificar replay: %w", err)
	}
	defer rows.Close()

	stored := make([]domain.LedgerEntryInput, 0, len(entries))
	for rows.Next() {
		var id uuid.UUID
		var asset string
		var amount decimal.Decimal
		if err := rows.Scan(&id, &asset, &amount); err != nil {
			return fmt.Errorf("escanear entry para verificar replay: %w", err)
		}
		stored = append(stored, domain.LedgerEntryInput{WalletAccountID: id, AssetCode: asset, Amount: amount})
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterar entries para verificar replay: %w", err)
	}

	if !sameNetEntries(netEntries(stored), netEntries(entries)) {
		return domain.ErrIdempotencyPayloadMismatch
	}
	return nil
}
