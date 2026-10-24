package watcher

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/application/deposit"
	"korzadi-wallet/backend/internal/application/locking"
	"korzadi-wallet/backend/internal/domain"
)

// satsToBTC converts an exact satoshi integer into a decimal.Decimal BTC
// amount without ever passing through a float — kept local to the
// application layer (rather than imported from infrastructure/btcclient)
// so this package's only dependency for money math is decimal, consistent
// with the rest of the application layer.
func satsToBTC(sats int64) decimal.Decimal {
	return decimal.NewFromInt(sats).Div(decimal.NewFromInt(100_000_000))
}

// UTXOProviderLite is the subset of domain.UTXOProvider this watcher
// needs, named separately so tests can supply a minimal fake without
// implementing the withdrawal-side methods too.
type UTXOProviderLite interface {
	LatestBlockHeight(ctx context.Context) (uint64, error)
	BlockHashAtHeight(ctx context.Context, height uint64) (string, error)
	OutputsInBlock(ctx context.Context, blockHash string) ([]domain.BitcoinOutput, error)
	TransactionStatus(ctx context.Context, txHash string) (domain.BitcoinTransactionStatus, error)
}

type BTCConfig struct {
	ConfirmationsRequired uint64
	PollInterval          time.Duration
	// MaxBlocksPerScan caps how many blocks are scanned in one iteration,
	// same rationale as EVMDepositWatcher's field of the same name.
	MaxBlocksPerScan uint64
	// SafetyLagBlocks avoids scanning right up to the chain tip.
	SafetyLagBlocks uint64
}

// BTCDepositWatcher scans Bitcoin for deposits. It is a completely
// separate implementation from EVMDepositWatcher — no shared base type —
// because Bitcoin's scan unit is "every output of every transaction in a
// block" (fetched whole, then filtered locally against managed addresses)
// rather than "Transfer events matching a contract+topic filter". It
// reuses the same TransferStore/SyncStateStore/DepositConfirmer contracts
// as the EVM watcher, since the detected → confirming → confirmed →
// credited lifecycle and its idempotency/reorg handling are identical in
// shape across families — only how you SCAN differs.
type BTCDepositWatcher struct {
	provider  UTXOProviderLite
	addresses DepositAddressLookup
	transfers TransferStore
	syncState SyncStateStore
	confirmer DepositConfirmer
	assetCode string // "BTC" — Bitcoin has exactly one asset, itself
	cfg       BTCConfig
	locks     locking.Locker // opcional: ciclo exclusivo entre réplicas
}

// WithSingleton activa la ejecución exclusiva del ciclo entre réplicas.
func (w *BTCDepositWatcher) WithSingleton(l locking.Locker) *BTCDepositWatcher {
	w.locks = l
	return w
}

func NewBTCDepositWatcher(
	provider UTXOProviderLite,
	addresses DepositAddressLookup,
	transfers TransferStore,
	syncState SyncStateStore,
	confirmer DepositConfirmer,
	assetCode string,
	cfg BTCConfig,
) *BTCDepositWatcher {
	return &BTCDepositWatcher{
		provider:  provider,
		addresses: addresses,
		transfers: transfers,
		syncState: syncState,
		confirmer: confirmer,
		assetCode: assetCode,
		cfg:       cfg,
	}
}

func (w *BTCDepositWatcher) Run(ctx context.Context) {
	ticker := time.NewTicker(w.cfg.PollInterval)
	defer ticker.Stop()

	log.Printf("watcher[BITCOIN]: iniciado (poll=%s, confirmations=%d)", w.cfg.PollInterval, w.cfg.ConfirmationsRequired)

	for {
		select {
		case <-ctx.Done():
			log.Printf("watcher[BITCOIN]: detenido")
			return
		case <-ticker.C:
			if _, err := locking.RunExclusive(ctx, w.locks, locking.SingletonKey("watcher:"+string(domain.NetworkBITCOIN)), w.tick); err != nil && ctx.Err() == nil {
				log.Printf("watcher[BITCOIN]: error en ciclo (continuará en el próximo tick): %v", err)
			}
		}
	}
}

func (w *BTCDepositWatcher) tick(ctx context.Context) error {
	if err := w.scanNewBlocks(ctx); err != nil {
		return err
	}
	return w.progressPendingTransfers(ctx)
}

func (w *BTCDepositWatcher) scanNewBlocks(ctx context.Context) error {
	latest, err := w.provider.LatestBlockHeight(ctx)
	if err != nil {
		return err
	}
	if latest < w.cfg.SafetyLagBlocks {
		return nil
	}
	safeTip := latest - w.cfg.SafetyLagBlocks

	from, ok, err := w.syncState.GetLastProcessedBlock(ctx, domain.NetworkBITCOIN)
	if err != nil {
		return err
	}
	if !ok {
		from = safeTip // first run: start from the tip, not genesis (spec §10)
	} else {
		from = from + 1
	}
	if from > safeTip {
		return nil
	}

	to := safeTip
	if to-from+1 > w.cfg.MaxBlocksPerScan {
		to = from + w.cfg.MaxBlocksPerScan - 1
	}

	for height := from; height <= to; height++ {
		if err := w.scanBlock(ctx, height); err != nil {
			log.Printf("watcher[BITCOIN]: error escaneando bloque %d: %v", height, err)
			// A single bad block must not stop the checkpoint from
			// advancing past it — same poison-pill rationale as the EVM
			// watcher's per-event error handling.
		}
	}

	return w.syncState.AdvanceTo(ctx, domain.NetworkBITCOIN, to)
}

func (w *BTCDepositWatcher) scanBlock(ctx context.Context, height uint64) error {
	blockHash, err := w.provider.BlockHashAtHeight(ctx, height)
	if err != nil {
		return err
	}
	outputs, err := w.provider.OutputsInBlock(ctx, blockHash)
	if err != nil {
		return err
	}

	for _, o := range outputs {
		cryptoAddressID, _, found, err := w.addresses.FindByAddress(ctx, domain.NetworkBITCOIN, o.Address)
		if err != nil {
			log.Printf("watcher[BITCOIN]: error resolviendo dirección %s: %v", o.Address, err)
			continue
		}
		if !found {
			continue // output to an address we don't manage — not a deposit
		}

		transfer := domain.BlockchainTransfer{
			Network:     domain.NetworkBITCOIN,
			AssetCode:   w.assetCode,
			AssetKind:   domain.AssetKindNative, // BTC is Bitcoin's native (only) asset
			TxHash:      o.TxHash,
			LogIndex:    uint(o.VoutIndex), // idempotency key: (network, "", tx_hash, vout_index)
			BlockNumber: o.BlockHeight,
			BlockHash:   o.BlockHash,
			ToAddress:   o.Address,
			Amount:      satsToBTC(o.AmountSats),
			Decimals:    8,
		}

		_, duplicate, err := w.transfers.InsertDetected(ctx, transfer, cryptoAddressID)
		if err != nil {
			log.Printf("watcher[BITCOIN]: error insertando transfer tx=%s vout=%d: %v", o.TxHash, o.VoutIndex, err)
			continue
		}
		if duplicate {
			log.Printf("watcher[BITCOIN]: output duplicado ignorado tx=%s vout=%d (idempotencia OK)", o.TxHash, o.VoutIndex)
			continue
		}
		log.Printf("watcher[BITCOIN]: depósito detectado tx=%s vout=%d to=%s amount=%s BTC",
			o.TxHash, o.VoutIndex, o.Address, transfer.Amount.String())
	}
	return nil
}

// progressPendingTransfers mirrors EVMDepositWatcher's method of the same
// name, but checks status via UTXOProvider.TransactionStatus (confirmations
// + current block hash) instead of an EVM receipt — same detected →
// confirming → confirmed → credited lifecycle either way.
func (w *BTCDepositWatcher) progressPendingTransfers(ctx context.Context) error {
	pending, err := w.transfers.FindPendingByNetwork(ctx, domain.NetworkBITCOIN)
	if err != nil {
		return err
	}

	for _, t := range pending {
		status, err := w.provider.TransactionStatus(ctx, t.TxHash)
		if err != nil {
			log.Printf("watcher[BITCOIN]: no se pudo obtener estado de tx=%s: %v", t.TxHash, err)
			continue
		}
		if !status.Found || (t.BlockHash != "" && status.BlockHash != t.BlockHash) {
			log.Printf("watcher[BITCOIN]: reorg detectado en tx=%s (bloque original %s ya no es canónico)", t.TxHash, t.BlockHash)
			if err := w.transfers.MarkReorged(ctx, t.ID); err != nil {
				log.Printf("watcher[BITCOIN]: error marcando reorg tx=%s: %v", t.TxHash, err)
			}
			continue
		}

		newStatus, err := w.transfers.UpdateConfirmations(ctx, t.ID, status.Confirmations, w.cfg.ConfirmationsRequired)
		if err != nil {
			log.Printf("watcher[BITCOIN]: error actualizando confirmaciones tx=%s: %v", t.TxHash, err)
			continue
		}
		displayStatus := newStatus
		if displayStatus == "" {
			// Retrying a credit for a transfer that reached 'confirmed' on
			// a previous tick but wasn't credited yet (spec §6).
			displayStatus = t.Status
		}
		log.Printf("watcher[BITCOIN]: tx=%s confirmaciones=%d/%d estado=%s", t.TxHash, status.Confirmations, w.cfg.ConfirmationsRequired, displayStatus)

		if newStatus == domain.TransferStatusConfirmed || t.Status == domain.TransferStatusConfirmed {
			t.Confirmations = status.Confirmations
			t.Status = domain.TransferStatusConfirmed
			_, userID, found, err := w.addresses.FindByAddress(ctx, domain.NetworkBITCOIN, t.ToAddress)
			if err != nil || !found {
				log.Printf("watcher[BITCOIN]: no se pudo resolver usuario para dirección %s: %v", t.ToAddress, err)
				continue
			}
			depositID, err := w.confirmer.ConfirmBlockchainDeposit(ctx, t, userID)
			if err != nil && !errors.Is(err, deposit.ErrDepositAlreadyProcessed) {
				log.Printf("watcher[BITCOIN]: error acreditando depósito tx=%s: %v", t.TxHash, err)
				continue
			}
			if errors.Is(err, deposit.ErrDepositAlreadyProcessed) {
				log.Printf("watcher[BITCOIN]: depósito tx=%s ya había sido acreditado previamente (idempotencia OK)", t.TxHash)
			} else {
				log.Printf("watcher[BITCOIN]: depósito acreditado tx=%s user=%s amount=%s BTC", t.TxHash, userID, t.Amount.String())
			}
			if depositID != uuid.Nil {
				if err := w.transfers.MarkCredited(ctx, t.ID, depositID); err != nil {
					log.Printf("watcher[BITCOIN]: no se pudo marcar transfer como credited tx=%s: %v", t.TxHash, err)
				}
			}
		}
	}
	return nil
}
