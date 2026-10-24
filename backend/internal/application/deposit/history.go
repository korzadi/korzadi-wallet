package deposit

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/domain"
)

// BlockchainDepositRecord is the read-model view of one blockchain_deposits
// row — spec §8: enough to reconstruct "quién, qué asset, cuánto, cuándo,
// qué tx blockchain lo originó, en qué estado está" without exposing
// anything about custody (no private keys, no derivation info — those
// never lived on this table to begin with).
type BlockchainDepositRecord struct {
	ID                    uuid.UUID
	Network               domain.Network
	AssetCode             string
	TxHash                string
	FromAddress           string
	ToAddress             string
	ContractAddress       string // empty for native-coin deposits (ETH/BNB/BTC/...)
	LogIndex              int    // vout index for Bitcoin — see Bloque 1's domain.BlockchainTransfer doc comment
	BlockNumber           uint64
	BlockHash             string
	Amount                decimal.Decimal
	Confirmations         uint64
	RequiredConfirmations int
	Status                string
	DetectedAt            time.Time
	CompletedAt           *time.Time
}

// BlockchainDepositReader is the narrow read port this service needs —
// implemented by postgres.BlockchainDepositRecorderRepository (the same
// repository that already implements the write side, BlockchainDepositRecorder,
// used by the confirmation flow). Kept separate here (read vs write
// capability) so a caller that only needs history can't accidentally be
// handed the ability to record a deposit.
type BlockchainDepositReader interface {
	ListByUser(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*BlockchainDepositRecord, error)
	FindByIDForUser(ctx context.Context, id, userID uuid.UUID) (*BlockchainDepositRecord, error)
}

// HistoryService is the Application-layer seam for deposit history reads
// (spec §8-9) — the HTTP handler never queries Postgres directly, and
// ownership is resolved here from the caller-supplied userID (which the
// handler must always source from the authenticated session, never from
// a request parameter — see the handler's doc comment).
type HistoryService struct {
	reader BlockchainDepositReader
}

func NewHistoryService(reader BlockchainDepositReader) *HistoryService {
	return &HistoryService{reader: reader}
}

const (
	defaultHistoryPageSize = 50
	maxHistoryPageSize     = 200
)

// ListForUser returns userID's own blockchain deposits, newest first.
// limit<=0 falls back to a sane default; limit is always capped, so a
// client can't force an unbounded query by passing an enormous page size.
func (s *HistoryService) ListForUser(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*BlockchainDepositRecord, error) {
	if limit <= 0 {
		limit = defaultHistoryPageSize
	}
	if limit > maxHistoryPageSize {
		limit = maxHistoryPageSize
	}
	if offset < 0 {
		offset = 0
	}
	records, err := s.reader.ListByUser(ctx, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("deposit history: listar depósitos: %w", err)
	}
	return records, nil
}

// ErrDepositNotFound covers both "no existe" and "no es tuyo" — spec §9:
// a user must never be able to distinguish those two cases (that
// distinction itself would leak whether an ID belongs to someone else).
var ErrDepositNotFound = fmt.Errorf("deposit: depósito no encontrado")

// GetForUser returns a single deposit only if it belongs to userID —
// ownership is enforced in the repository's SQL WHERE clause (spec §9,
// §10 IDOR), not by fetching first and checking ownership in Go.
func (s *HistoryService) GetForUser(ctx context.Context, depositID, userID uuid.UUID) (*BlockchainDepositRecord, error) {
	record, err := s.reader.FindByIDForUser(ctx, depositID, userID)
	if err != nil {
		return nil, fmt.Errorf("deposit history: buscar depósito: %w", err)
	}
	if record == nil {
		return nil, ErrDepositNotFound
	}
	return record, nil
}
