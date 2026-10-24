package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"korzadi-wallet/backend/internal/domain"
)

// DepositIncidentRepository guarda los incidentes de depósitos que requieren
// revisión humana (reorg tras acreditar, evento envenenado). Implementa
// watcher.IncidentReporter. Es solo auditoría: NO modifica ledger ni balances.
//
// Consulta operativa de incidentes abiertos:
//
//	SELECT created_at, network, kind, tx_hash, asset_code, to_address, amount, details
//	FROM deposit_incidents WHERE status = 'open' ORDER BY created_at;
type DepositIncidentRepository struct {
	pool *pgxpool.Pool
}

func NewDepositIncidentRepository(pool *pgxpool.Pool) *DepositIncidentRepository {
	return &DepositIncidentRepository{pool: pool}
}

// Report es idempotente: si ya hay un incidente ABIERTO del mismo
// (red, tipo, tx, log), no crea otro.
func (r *DepositIncidentRepository) Report(ctx context.Context, inc domain.DepositIncident) error {
	var transferID *uuid.UUID
	if inc.TransferID != uuid.Nil {
		id := inc.TransferID
		transferID = &id
	}
	details, err := json.Marshal(map[string]any{
		"detail":           inc.Details,
		"block_hash":       inc.Transfer.BlockHash,
		"block_number":     inc.Transfer.BlockNumber,
		"from_address":     inc.Transfer.FromAddress,
		"contract_address": inc.Transfer.ContractAddress,
	})
	if err != nil {
		return fmt.Errorf("serializar detalles del incidente: %w", err)
	}

	_, err = r.pool.Exec(ctx, `
		INSERT INTO deposit_incidents
			(network, kind, blockchain_transfer_id, tx_hash, log_index, asset_code, to_address, amount, details)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb)
		ON CONFLICT (network, kind, tx_hash, log_index) WHERE status = 'open' DO NOTHING
	`, inc.Network, string(inc.Kind), transferID, inc.Transfer.TxHash, int(inc.Transfer.LogIndex),
		inc.Transfer.AssetCode, inc.Transfer.ToAddress, inc.Transfer.Amount, string(details))
	if err != nil {
		return fmt.Errorf("registrar incidente de depósito: %w", err)
	}
	return nil
}
