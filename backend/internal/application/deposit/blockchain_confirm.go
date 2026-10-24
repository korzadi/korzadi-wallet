package deposit

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
)

// ErrDepositAlreadyProcessed is kept for backward compatibility with
// callers written against the earlier two-engine design (see Bloque 2
// audit). Since consolidating onto the single domain.LedgerRepository
// engine (Bloque 2), a replayed deposit is a silent idempotent success —
// ConfirmBlockchainDeposit no longer returns this error itself — but the
// symbol stays exported so existing error-checking code elsewhere
// continues to compile unchanged.
var ErrDepositAlreadyProcessed = fmt.Errorf("deposit: transferencia ya fue acreditada previamente")

// LedgerPoster is the single ledger port this service needs — it is now
// exactly domain.LedgerRepository's ExecuteTransaction, the same engine
// every other money-moving flow in the system uses (transfers, generic
// withdrawals, affiliate payouts, KorzadiVPN). Before Bloque 2 this was a
// separate PostDoubleEntry method on a standalone
// postgres.LedgerBlockchainPoster, split out only because the generic
// engine didn't yet know a system_clearing account may legitimately go
// negative. That distinction now lives inside LedgerRepository itself
// (see its doc comment), so there is no longer a reason for a second
// engine — nothing here can bypass double-entry accounting either way.
type LedgerPoster interface {
	ExecuteTransaction(ctx context.Context, txType domain.TransactionType, idempotencyKey string,
		referenceID *string, description string, createdBy *uuid.UUID,
		entries []domain.LedgerEntryInput) (*domain.LedgerTransaction, error)
}

// ClearingAccountResolver finds the system_clearing wallet_account for a
// given (network, asset) pair, e.g. the BSC/USDT clearing account seeded by
// migration 0003.
type ClearingAccountResolver interface {
	FindClearingAccount(ctx context.Context, network domain.Network, assetCode string) (uuid.UUID, error)
}

// UserAccountResolver finds a user's own wallet_account for an asset,
// creating it if it doesn't exist yet (mirrors existing wallet/account
// provisioning logic elsewhere in the app — not duplicated here).
type UserAccountResolver interface {
	FindOrCreateUserAccount(ctx context.Context, userID uuid.UUID, assetCode string) (uuid.UUID, error)
}

// BlockchainDepositRecorder persists the blockchain_deposits row
// (spec §16) and links it to the ledger transaction, marking the deposit
// 'completed' only after the ledger post succeeds. RecordCompleted's own
// upsert (ON CONFLICT on the deposit's natural key) must be idempotent —
// this service calls it every time the ledger post succeeds, whether that
// was a fresh credit or an idempotent replay of one already posted.
type BlockchainDepositRecorder interface {
	RecordCompleted(ctx context.Context, in RecordCompletedInput) (uuid.UUID, error)
}

type RecordCompletedInput struct {
	Network             domain.Network
	AssetCode           string
	UserID              uuid.UUID
	TxHash              string
	LogIndex            int
	ContractAddress     string
	BlockNumber         uint64
	BlockHash           string
	FromAddress         string
	ToAddress           string
	Amount              decimal.Decimal
	Confirmations       uint64
	LedgerTransactionID uuid.UUID
}

// BlockchainDepositService is shared by every network and every asset —
// USDT and USDC on any EVM chain, native ETH/BNB/MATIC, and Bitcoin all
// flow through the same instance. Previously this held a single fixed
// assetCode ("USDT"), which meant a second asset on the same network would
// have needed a second service instance wired by hand. The asset now comes
// from transfer.AssetCode (populated by whichever watcher detected it, from
// the asset registry) — one instance, any number of assets.
type BlockchainDepositService struct {
	ledger           LedgerPoster
	clearingAccounts ClearingAccountResolver
	userAccounts     UserAccountResolver
	deposits         BlockchainDepositRecorder
}

func NewBlockchainDepositService(
	ledger LedgerPoster,
	clearingAccounts ClearingAccountResolver,
	userAccounts UserAccountResolver,
	deposits BlockchainDepositRecorder,
) *BlockchainDepositService {
	return &BlockchainDepositService{
		ledger:           ledger,
		clearingAccounts: clearingAccounts,
		userAccounts:     userAccounts,
		deposits:         deposits,
	}
}

// ConfirmBlockchainDeposit implements watcher.DepositConfirmer. It is only
// ever called by the watcher once a transfer has independently reached
// domain.TransferStatusConfirmed (network, contract, receiver, sender,
// amount, tx hash, block and required confirmations already validated
// upstream — spec §6). This method's only remaining job is the ledger
// credit, done exactly once.
func (s *BlockchainDepositService) ConfirmBlockchainDeposit(ctx context.Context, transfer domain.BlockchainTransfer, userID uuid.UUID) (uuid.UUID, error) {
	if transfer.Status != domain.TransferStatusConfirmed {
		return uuid.Nil, fmt.Errorf("deposit: transfer %s no está en estado confirmed (estado actual: %s)", transfer.TxHash, transfer.Status)
	}

	if transfer.AssetCode == "" {
		return uuid.Nil, fmt.Errorf("deposit: transfer %s no tiene AssetCode — el watcher que la detectó debe poblarlo desde el asset registry", transfer.TxHash)
	}

	// Idempotency key mirrors spec §7 exactly: network + contract + tx_hash
	// + log_index. Never tx_hash alone — a single tx can carry multiple
	// Transfer events for the same contract. ContractAddress is empty for
	// native-coin transfers (ETH/BNB/...), which is fine: (network, "",
	// tx_hash, 0) is still a unique key since a native transfer can't
	// repeat within one tx.
	idempotencyKey := fmt.Sprintf("blockchain-deposit:%s:%s:%s:%s:%d",
		transfer.Network, transfer.AssetCode, transfer.ContractAddress, transfer.TxHash, transfer.LogIndex)

	clearingAccountID, err := s.clearingAccounts.FindClearingAccount(ctx, transfer.Network, transfer.AssetCode)
	if err != nil {
		return uuid.Nil, fmt.Errorf("deposit: no se encontró cuenta de clearing para %s/%s: %w", transfer.Network, transfer.AssetCode, err)
	}

	userAccountID, err := s.userAccounts.FindOrCreateUserAccount(ctx, userID, transfer.AssetCode)
	if err != nil {
		return uuid.Nil, fmt.Errorf("deposit: no se pudo resolver cuenta de usuario: %w", err)
	}

	tx, err := s.ledger.ExecuteTransaction(
		ctx,
		domain.TxTypeDeposit,
		idempotencyKey,
		&transfer.TxHash,
		fmt.Sprintf("Depósito %s (%s) confirmado (tx %s, log %d)", transfer.AssetCode, transfer.Network, transfer.TxHash, transfer.LogIndex),
		nil,
		[]domain.LedgerEntryInput{
			// Credit the user, debit the clearing account — sums to zero
			// per spec §14. The clearing account (account_type =
			// 'system_clearing') is allowed to go negative by
			// LedgerRepository's own account-type-aware check; nothing
			// deposit-specific has to know or enforce that here anymore.
			{WalletAccountID: userAccountID, AssetCode: transfer.AssetCode, Amount: transfer.Amount},
			{WalletAccountID: clearingAccountID, AssetCode: transfer.AssetCode, Amount: transfer.Amount.Neg()},
		},
	)
	if err != nil {
		return uuid.Nil, fmt.Errorf("deposit: fallo al postear ledger: %w", err)
	}

	// Defensa en profundidad: un replay idempotente solo es válido si la tx
	// existente ES un depósito de este mismo tx_hash. Si la clave estaba
	// ocupada por otra operación (p. ej. una transferencia con clave
	// maliciosa) NO se marca el depósito como acreditado.
	if tx.Type != domain.TxTypeDeposit || (tx.ReferenceID != nil && *tx.ReferenceID != transfer.TxHash) {
		return uuid.Nil, fmt.Errorf("deposit: la idempotency_key %q pertenece a otra operación: %w",
			idempotencyKey, domain.ErrIdempotencyPayloadMismatch)
	}

	// tx.ID is stable across a fresh post and an idempotent replay (spec
	// §18: "dos workers detectando el mismo evento simultáneamente => UNA
	// SOLA acreditación") — ExecuteTransaction returns the SAME
	// ledger_transaction either way, so RecordCompleted's own upsert (on
	// the deposit's natural key) is always safe to call here regardless
	// of which case this was.
	depositID, err := s.deposits.RecordCompleted(ctx, RecordCompletedInput{
		Network:             transfer.Network,
		AssetCode:           transfer.AssetCode,
		UserID:              userID,
		TxHash:              transfer.TxHash,
		LogIndex:            int(transfer.LogIndex),
		ContractAddress:     transfer.ContractAddress,
		BlockNumber:         transfer.BlockNumber,
		BlockHash:           transfer.BlockHash,
		FromAddress:         transfer.FromAddress,
		ToAddress:           transfer.ToAddress,
		Amount:              transfer.Amount,
		Confirmations:       transfer.Confirmations,
		LedgerTransactionID: tx.ID,
	})
	if err != nil {
		// The ledger entry is already posted and is the source of truth;
		// a failure here means the blockchain_deposits row (a read-model /
		// audit record) is out of sync and needs reconciliation — it must
		// NOT be "fixed" by re-running the ledger post, which would
		// violate idempotency in the other direction.
		return uuid.Nil, fmt.Errorf("deposit: ledger acreditado pero no se pudo registrar blockchain_deposits (requiere reconciliación): %w", err)
	}

	return depositID, nil
}
