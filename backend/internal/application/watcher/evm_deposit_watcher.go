// Package watcher implements background indexing of on-chain deposits.
// It is a standalone worker/service (spec §10: "No hagas un watcher
// infinito dentro de un HTTP handler") — cmd/api/main.go starts one
// instance per configured network with `go watcher.Run(ctx)` alongside,
// not inside, the HTTP server.
package watcher

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/application/deposit"
	"korzadi-wallet/backend/internal/application/locking"
	"korzadi-wallet/backend/internal/domain"
)

// DepositAddressLookup resolves an on-chain address to the internal
// deposit-address record it belongs to, if any. Implemented by
// postgres.AddressToUserResolver. Kept as a narrow interface here so
// the watcher depends on a contract, not a concrete infrastructure type.
type DepositAddressLookup interface {
	FindByAddress(ctx context.Context, network domain.Network, address string) (cryptoAddressID uuid.UUID, userID uuid.UUID, found bool, err error)
}

// WatchedAddressLister supplies the full set of managed addresses on a
// network, needed only for native-coin scanning (see EVMAsset.Kind ==
// AssetKindNative) — native transfers aren't filterable server-side by
// recipient the way ERC-20 Transfer logs are, so the watcher must check
// every block's transactions against this set itself.
type WatchedAddressLister interface {
	ListActiveAddresses(ctx context.Context, network domain.Network) (map[string]bool, error)
}

type recipientFilteredTransferProvider interface {
	TransferEventsInRangeForRecipients(ctx context.Context, contract string, decimals int32, fromBlock, toBlock uint64, recipients []string) ([]domain.OnChainTransferEvent, error)
}

// TransferStore is the persistence contract the watcher needs from
// BlockchainTransferRepository.
type TransferStore interface {
	// InsertDetected returns duplicate=true (not an error) when the tuple
	// (network, contract, tx_hash, log_index) already exists.
	InsertDetected(ctx context.Context, t domain.BlockchainTransfer, cryptoAddressID uuid.UUID) (id uuid.UUID, duplicate bool, err error)
	UpdateConfirmations(ctx context.Context, id uuid.UUID, confirmations uint64, required uint64) (domain.TransferStatus, error)
	MarkReorged(ctx context.Context, id uuid.UUID) error
	// MarkCredited closes the loop once the ledger credit has been posted
	// (or was already posted on a prior attempt — replay-safe).
	MarkCredited(ctx context.Context, id uuid.UUID, blockchainDepositID uuid.UUID) error
	FindPendingByNetwork(ctx context.Context, network domain.Network) ([]domain.BlockchainTransfer, error)
}

// SyncStateStore is the persistence contract for the block checkpoint.
type SyncStateStore interface {
	GetLastProcessedBlock(ctx context.Context, network domain.Network) (block uint64, ok bool, err error)
	AdvanceTo(ctx context.Context, network domain.Network, block uint64) error
}

// DepositConfirmer is implemented by the deposit application service. The
// watcher calls this ONLY after a transfer has reached
// domain.TransferStatusConfirmed — it never touches the ledger directly
// (spec §14: blockchain never substitutes the ledger; a dedicated service
// owns double-entry, idempotency and atomicity).
type DepositConfirmer interface {
	// ConfirmBlockchainDeposit returns the id of the blockchain_deposits row
	// created/linked by the ledger post, so the watcher can close the loop
	// by marking the source blockchain_transfers row as 'credited'.
	ConfirmBlockchainDeposit(ctx context.Context, transfer domain.BlockchainTransfer, userID uuid.UUID) (blockchainDepositID uuid.UUID, err error)
}

// EVMAsset is one asset this watcher instance scans for — sourced from the
// asset registry (domain.NetworkAsset) at construction time. A watcher
// scans every enabled asset on its network in each tick: this is exactly
// the change that lets one network carry USDT *and* USDC (or any other
// token) without a second watcher instance or any code change — only a
// new row in the asset registry.
type EVMAsset struct {
	AssetCode       string
	Kind            domain.AssetKind
	ContractAddress string // empty for native
	Decimals        int32
}

type Config struct {
	Network               domain.Network
	Assets                []EVMAsset
	ConfirmationsRequired uint64
	PollInterval          time.Duration
	// MaxBlocksPerScan caps how many blocks are scanned in one iteration,
	// so a watcher that's far behind (e.g. after downtime) doesn't request
	// an unbounded log range from the RPC in a single call.
	MaxBlocksPerScan uint64
	// SafetyLagBlocks avoids scanning right up to the chain tip, since the
	// most recent few blocks are the ones most likely to be reorged.
	SafetyLagBlocks uint64

	// Fase B (todos opcionales; 0 => valor por defecto).
	// MaxEventAttempts: reintentos de un evento aparcado antes de marcarlo
	// 'poisoned' y abrir un incidente crítico (default 10).
	MaxEventAttempts int
	// LagWarnBlocks: umbral de lag (bloque seguro - último procesado) a
	// partir del cual se emite una ADVERTENCIA en logs (default 200).
	LagWarnBlocks uint64
	// RetryBatch: máximo de eventos aparcados reintentados por tick (default 50).
	RetryBatch int
}

// EVMDepositWatcher scans one EVM network (Ethereum, BSC, Polygon,
// Arbitrum, Optimism or Base — the same code serves all of them) for
// deposits, across every asset configured for that network. It replaces
// the earlier BSCDepositWatcher, which was hardcoded to a single
// contract/asset per instance.
type EVMDepositWatcher struct {
	provider  domain.BlockchainProvider
	addresses DepositAddressLookup
	watched   WatchedAddressLister
	transfers TransferStore
	syncState SyncStateStore
	confirmer DepositConfirmer
	cfg       Config

	// Fase B (inyectados con With*; todos opcionales).
	failed        FailedEventStore
	incidents     IncidentReporter
	credited      CreditedTransferLister
	recheckWindow time.Duration

	// locks (opcional): con varias réplicas, solo una ejecuta el ciclo de esta
	// red a la vez (pg_try_advisory_lock). Sin él, todas lo ejecutan: es
	// correcto (índices únicos + checkpoint que solo avanza) pero duplica las
	// llamadas RPC.
	locks locking.Locker

	statsMu sync.Mutex
	stats   Stats
}

// WithSingleton activa la ejecución exclusiva del ciclo entre réplicas.
func (w *EVMDepositWatcher) WithSingleton(l locking.Locker) *EVMDepositWatcher {
	w.locks = l
	return w
}

func NewEVMDepositWatcher(
	provider domain.BlockchainProvider,
	addresses DepositAddressLookup,
	watched WatchedAddressLister,
	transfers TransferStore,
	syncState SyncStateStore,
	confirmer DepositConfirmer,
	cfg Config,
) *EVMDepositWatcher {
	return &EVMDepositWatcher{
		provider:  provider,
		addresses: addresses,
		watched:   watched,
		transfers: transfers,
		syncState: syncState,
		confirmer: confirmer,
		cfg:       cfg,
	}
}

// Run is the worker's main loop. Call it as `go watcher.Run(ctx)`. It is
// restartable and idempotent: on every restart it resumes from the last
// persisted checkpoint, and every insert/credit path is protected by
// unique constraints and status guards, so re-processing an already-seen
// range never double-credits (spec §10, §18 concurrency requirement).
func (w *EVMDepositWatcher) Run(ctx context.Context) {
	ticker := time.NewTicker(w.cfg.PollInterval)
	defer ticker.Stop()

	log.Printf("watcher[%s]: iniciado (poll=%s, confirmations=%d, assets=%d)",
		w.cfg.Network, w.cfg.PollInterval, w.cfg.ConfirmationsRequired, len(w.cfg.Assets))

	for {
		select {
		case <-ctx.Done():
			log.Printf("watcher[%s]: detenido", w.cfg.Network)
			return
		case <-ticker.C:
			// Un solo ciclo por red entre todas las réplicas.
			if _, err := locking.RunExclusive(ctx, w.locks, locking.SingletonKey("watcher:"+string(w.cfg.Network)), w.tick); err != nil && ctx.Err() == nil {
				log.Printf("watcher[%s]: error en ciclo (continuará en el próximo tick): %v", w.cfg.Network, err)
			}
		}
	}
}

func (w *EVMDepositWatcher) tick(ctx context.Context) error {
	w.noteTick()

	// 1) Reintentar eventos aparcados (un fallo aquí no frena el escaneo).
	if err := w.retryFailedEvents(ctx); err != nil {
		log.Printf("watcher[%s]: error reintentando eventos aparcados: %v", w.cfg.Network, err)
	}

	// 2) Escanear bloques nuevos y 3) progresar pendientes. Son independientes:
	// que falle getLogs no debe impedir confirmar/acreditar lo ya detectado.
	scanErr := w.scanNewBlocks(ctx)
	progressErr := w.progressPendingTransfers(ctx)

	// 4) Re-verificar depósitos ya acreditados (reorg tras acreditar).
	if err := w.recheckCreditedTransfers(ctx); err != nil {
		log.Printf("watcher[%s]: error en re-verificación post-acreditación: %v", w.cfg.Network, err)
	}

	if scanErr != nil {
		return scanErr
	}
	return progressErr
}

func (w *EVMDepositWatcher) scanNewBlocks(ctx context.Context) error {
	latest, err := w.provider.LatestBlock(ctx)
	if err != nil {
		return err // provider failure — tolerated, retried next tick per spec §10
	}
	if latest < w.cfg.SafetyLagBlocks {
		return nil
	}
	safeTip := latest - w.cfg.SafetyLagBlocks

	last, ok, err := w.syncState.GetLastProcessedBlock(ctx, w.cfg.Network)
	if err != nil {
		return err
	}
	var from uint64
	if !ok {
		// First run: start from the current tip rather than genesis —
		// historical backfill, if needed, is a deliberate one-off
		// operation (Backfill / cmd/watcher-backfill), not the
		// steady-state watcher's job.
		from = safeTip
		log.Printf("watcher[%s]: primera ejecución sin checkpoint: se inicia en el bloque seguro %d (no se recupera historial; usar watcher-backfill si hace falta)",
			w.cfg.Network, safeTip)
	} else {
		from = last + 1
	}
	w.noteChainView(latest, safeTip, last, ok)
	if from > safeTip {
		return nil // nothing new yet
	}

	to := safeTip
	if to-from+1 > w.cfg.MaxBlocksPerScan {
		to = from + w.cfg.MaxBlocksPerScan - 1
	}

	// A provider may answer eth_blockNumber and eth_getLogs from nodes at
	// different heights. Retry that specific race once, after refreshing the
	// head, and only for a strictly smaller range. Re-scan every asset so the
	// checkpoint always describes a range fully covered by all assets.
	for attempt := 0; ; attempt++ {
		err := w.scanAssetRanges(ctx, from, to)
		if err == nil {
			break
		}
		var headErr *domain.BlockRangeBeyondHeadError
		if attempt > 0 || !errors.As(err, &headErr) {
			return err
		}

		refreshedHead, refreshErr := w.provider.LatestBlock(ctx)
		if refreshErr != nil {
			return fmt.Errorf("refrescar altura BSC tras fallo de rango: %w (error original: %v)", refreshErr, err)
		}
		// Use the lower of the head observed by the RPC adapter after the
		// failed query and this fresh watcher observation. This is conservative
		// when the RPC endpoint is backed by nodes with different head heights.
		observedHead := headErr.AvailableHead
		if refreshedHead < observedHead {
			observedHead = refreshedHead
		}
		if observedHead <= w.cfg.SafetyLagBlocks || to <= from {
			return err
		}
		refreshedSafeTip := observedHead - w.cfg.SafetyLagBlocks
		retryTo := to - 1 // the recovery must strictly reduce the requested range
		if refreshedSafeTip < retryTo {
			retryTo = refreshedSafeTip
		}
		if retryTo < from {
			return err
		}
		log.Printf("watcher[%s]: rango futuro detectado; reintentando una vez [%d,%d] (altura observada=%d)",
			w.cfg.Network, from, retryTo, observedHead)
		to = retryTo
	}

	// Only checkpoint the endpoint shared by every asset scan above. If the
	// recovery narrowed the range, the next tick starts at to+1 with no gap.
	if err := w.syncState.AdvanceTo(ctx, w.cfg.Network, to); err != nil {
		return err
	}
	w.noteAdvanced(to)
	return nil
}

func (w *EVMDepositWatcher) scanAssetRanges(ctx context.Context, from, to uint64) error {
	for _, asset := range w.cfg.Assets {
		if asset.Kind == domain.AssetKindNative {
			if err := w.scanNativeRange(ctx, asset, from, to); err != nil {
				return fmt.Errorf(
					"watcher[%s]: error escaneando nativo %s en rango [%d,%d]: %w",
					w.cfg.Network,
					asset.AssetCode,
					from,
					to,
					err,
				)
			}
			continue
		}

		if err := w.scanTokenRange(ctx, asset, from, to); err != nil {
			return fmt.Errorf(
				"watcher[%s]: error escaneando %s (%s) en rango [%d,%d]: %w",
				w.cfg.Network,
				asset.AssetCode,
				asset.ContractAddress,
				from,
				to,
				err,
			)
		}
	}
	return nil
}

func (w *EVMDepositWatcher) scanTokenRange(ctx context.Context, asset EVMAsset, from, to uint64) error {
	var events []domain.OnChainTransferEvent
	var err error
	if filtered, ok := w.provider.(recipientFilteredTransferProvider); ok {
		watched, listErr := w.watched.ListActiveAddresses(ctx, w.cfg.Network)
		if listErr != nil {
			return listErr
		}
		recipients := make([]string, 0, len(watched))
		for address := range watched {
			recipients = append(recipients, address)
		}
		events, err = filtered.TransferEventsInRangeForRecipients(ctx, asset.ContractAddress, asset.Decimals, from, to, recipients)
	} else {
		events, err = w.provider.TransferEventsInRange(ctx, asset.ContractAddress, asset.Decimals, from, to)
	}
	if err != nil {
		return err
	}
	for _, ev := range events {
		transfer := domain.BlockchainTransfer{
			Network:         w.cfg.Network,
			AssetCode:       asset.AssetCode,
			AssetKind:       domain.AssetKindToken,
			ContractAddress: ev.ContractAddress,
			TxHash:          ev.TxHash,
			LogIndex:        ev.LogIndex,
			BlockNumber:     ev.BlockNumber,
			BlockHash:       ev.BlockHash,
			FromAddress:     ev.From,
			ToAddress:       ev.To,
			Amount:          ev.RawAmount,
			Decimals:        int(asset.Decimals),
		}
		// Fase B: un evento que falla se APARCA (FailedEventStore) para
		// reintentarlo; si no se puede conservar, se devuelve el error y el
		// checkpoint NO avanza. Nunca se descarta en silencio.
		if err := w.handleOrPark(ctx, transfer); err != nil {
			return err
		}
	}
	return nil
}

func (w *EVMDepositWatcher) scanNativeRange(ctx context.Context, asset EVMAsset, from, to uint64) error {
	watchedAddrs, err := w.watched.ListActiveAddresses(ctx, w.cfg.Network)
	if err != nil {
		return err
	}
	if len(watchedAddrs) == 0 {
		return nil
	}

	events, err := w.provider.NativeTransfersInRange(ctx, from, to, watchedAddrs)
	if err != nil {
		return err
	}
	for _, ev := range events {
		transfer := domain.BlockchainTransfer{
			Network:     w.cfg.Network,
			AssetCode:   asset.AssetCode,
			AssetKind:   domain.AssetKindNative,
			TxHash:      ev.TxHash,
			LogIndex:    0, // native transfers can't repeat within one tx — (network, "", tx_hash, 0) is a valid unique key
			BlockNumber: ev.BlockNumber,
			BlockHash:   ev.BlockHash,
			FromAddress: ev.From,
			ToAddress:   ev.To,
			Amount:      ev.RawAmount,
			Decimals:    int(asset.Decimals),
		}
		if err := w.handleOrPark(ctx, transfer); err != nil {
			return err
		}
	}
	return nil
}

func (w *EVMDepositWatcher) handleTransfer(ctx context.Context, transfer domain.BlockchainTransfer) error {
	cryptoAddressID, _, found, err := w.addresses.FindByAddress(ctx, w.cfg.Network, transfer.ToAddress)
	if err != nil {
		return err
	}
	if !found {
		return nil // Transfer to an address we don't manage — not a deposit.
	}

	_, duplicate, err := w.transfers.InsertDetected(ctx, transfer, cryptoAddressID)
	if err != nil {
		return err
	}
	if duplicate {
		log.Printf("watcher[%s]: evento duplicado ignorado asset=%s tx=%s log=%d (idempotencia OK)",
			w.cfg.Network, transfer.AssetCode, transfer.TxHash, transfer.LogIndex)
		return nil
	}
	log.Printf("watcher[%s]: depósito detectado asset=%s tx=%s log=%d to=%s amount=%s",
		w.cfg.Network, transfer.AssetCode, transfer.TxHash, transfer.LogIndex, transfer.ToAddress, transfer.Amount.String())
	return nil
}

// progressPendingTransfers re-checks every detected/confirming transfer:
// recomputes confirmations, detects reorgs (receipt gone or block hash
// changed at that height), and — only once confirmations are sufficient
// AND the receipt still reports success — hands off to the deposit
// service exactly once (guarded by the 'confirmed'-only status check in
// TransferStore.MarkCredited, called from the confirmer).
func (w *EVMDepositWatcher) progressPendingTransfers(ctx context.Context) error {
	pending, err := w.transfers.FindPendingByNetwork(ctx, w.cfg.Network)
	if err != nil {
		return err
	}

	for _, t := range pending {
		receipt, err := w.provider.GetReceipt(ctx, t.TxHash)
		if err != nil {
			log.Printf("watcher[%s]: no se pudo obtener receipt de tx=%s: %v", w.cfg.Network, t.TxHash, err)
			continue
		}
		if receipt == nil || receipt.BlockHash != t.BlockHash {
			// The tx vanished from, or moved within, the chain since we
			// detected it: a reorg (spec §9). Never treat as confirmed.
			log.Printf("watcher[%s]: reorg detectado en tx=%s (bloque original %s ya no es canónico)", w.cfg.Network, t.TxHash, t.BlockHash)
			if err := w.transfers.MarkReorged(ctx, t.ID); err != nil {
				log.Printf("watcher[%s]: error marcando reorg tx=%s: %v", w.cfg.Network, t.TxHash, err)
			}
			continue
		}
		if !receipt.Status {
			log.Printf("watcher[%s]: tx=%s revertida on-chain, no se acreditará", w.cfg.Network, t.TxHash)
			continue
		}

		confirmationsRequired := w.confirmationsRequiredFor(t.AssetCode)
		newStatus, err := w.transfers.UpdateConfirmations(ctx, t.ID, receipt.Confirmations, confirmationsRequired)
		if err != nil {
			log.Printf("watcher[%s]: error actualizando confirmaciones tx=%s: %v", w.cfg.Network, t.TxHash, err)
			continue
		}
		displayStatus := newStatus
		if displayStatus == "" {
			// UpdateConfirmations's guard clause returns "" for a row
			// already past 'confirming' — this branch means we're
			// re-visiting a transfer that reached 'confirmed' on a
			// previous tick but hasn't been credited yet (spec §6 retry).
			displayStatus = t.Status
		}
		log.Printf("watcher[%s]: tx=%s asset=%s confirmaciones=%d/%d estado=%s",
			w.cfg.Network, t.TxHash, t.AssetCode, receipt.Confirmations, confirmationsRequired, displayStatus)

		if newStatus == domain.TransferStatusConfirmed || t.Status == domain.TransferStatusConfirmed {
			t.Confirmations = receipt.Confirmations
			t.Status = domain.TransferStatusConfirmed
			_, userID, found, err := w.addresses.FindByAddress(ctx, w.cfg.Network, t.ToAddress)
			if err != nil || !found {
				log.Printf("watcher[%s]: no se pudo resolver usuario para dirección %s: %v", w.cfg.Network, t.ToAddress, err)
				continue
			}
			depositID, err := w.confirmer.ConfirmBlockchainDeposit(ctx, t, userID)
			if err != nil && !errors.Is(err, deposit.ErrDepositAlreadyProcessed) {
				log.Printf("watcher[%s]: error acreditando depósito tx=%s: %v", w.cfg.Network, t.TxHash, err)
				continue
			}
			if errors.Is(err, deposit.ErrDepositAlreadyProcessed) {
				log.Printf("watcher[%s]: depósito tx=%s ya había sido acreditado previamente (idempotencia OK)", w.cfg.Network, t.TxHash)
			} else {
				log.Printf("watcher[%s]: depósito acreditado tx=%s asset=%s user=%s amount=%s", w.cfg.Network, t.TxHash, t.AssetCode, userID, t.Amount.String())
			}
			if depositID != uuid.Nil {
				if err := w.transfers.MarkCredited(ctx, t.ID, depositID); err != nil {
					// The ledger credit already happened (or had already
					// happened) and is authoritative; this only means the
					// staging row's status is stale until the next
					// reconciliation pass — logged, not fatal.
					log.Printf("watcher[%s]: no se pudo marcar transfer como credited tx=%s: %v", w.cfg.Network, t.TxHash, err)
				}
			}
		}
	}
	return nil
}

// confirmationsRequiredFor looks up a per-asset override, falling back to
// the network-wide default — mirrors domain.NetworkAsset.ConfirmationsRequired.
func (w *EVMDepositWatcher) confirmationsRequiredFor(assetCode string) uint64 {
	for _, a := range w.cfg.Assets {
		if a.AssetCode == assetCode {
			// Per-asset override isn't threaded through EVMAsset today
			// (Config carries only the network default) — every asset on
			// a network currently shares that network's confirmation
			// count. Documented here as the extension point if a future
			// asset needs a different value.
			break
		}
	}
	return w.cfg.ConfirmationsRequired
}
