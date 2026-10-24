package watcher

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/domain"
)

// ---- Contratos opcionales (Fase B) -----------------------------------------
//
// Se inyectan con los métodos With* para NO cambiar la firma de
// NewEVMDepositWatcher. Si no se inyectan, el watcher sigue siendo seguro:
// un evento que falla detiene el avance del checkpoint (y se reintenta todo el
// rango en el siguiente tick) en lugar de perderse.

// FailedEventStore persiste los eventos que no se pudieron registrar.
type FailedEventStore interface {
	// Save aparca el evento (upsert; incrementa intentos si ya existía).
	Save(ctx context.Context, t domain.BlockchainTransfer, cause error) error
	// ListRetryable devuelve eventos 'pending' cuyo backoff ya venció.
	ListRetryable(ctx context.Context, network domain.Network, limit int) ([]domain.FailedDepositEvent, error)
	MarkResolved(ctx context.Context, id uuid.UUID) error
	// RecordAttemptFailure suma un intento fallido; si alcanza poisonAfter el
	// evento pasa a 'poisoned' y se devuelve poisoned=true.
	RecordAttemptFailure(ctx context.Context, id uuid.UUID, cause error, poisonAfter int) (poisoned bool, err error)
	CountPending(ctx context.Context, network domain.Network) (int, error)
}

// IncidentReporter registra incidentes para revisión humana. Debe ser
// idempotente: reportar dos veces el mismo incidente abierto no duplica.
type IncidentReporter interface {
	Report(ctx context.Context, inc domain.DepositIncident) error
}

// CreditedTransferLister lista depósitos ya acreditados recientemente, para
// re-verificarlos contra la cadena (reorg tras acreditar).
type CreditedTransferLister interface {
	FindRecentlyCredited(ctx context.Context, network domain.Network, since time.Time, limit int) ([]domain.BlockchainTransfer, error)
}

// Stats es una instantánea del estado del watcher (lag, fallidos...).
type Stats struct {
	Network       domain.Network
	LatestBlock   uint64
	SafeTip       uint64
	LastProcessed uint64
	LagBlocks     uint64
	FailedPending int
	LastTickAt    time.Time
	LastAdvanceAt time.Time
}

const (
	defaultMaxEventAttempts  = 10
	defaultLagWarnBlocks     = 200
	defaultRetryBatch        = 50
	defaultRecheckWindow     = 24 * time.Hour
	defaultRecheckBatchLimit = 500
)

func (c Config) maxEventAttempts() int {
	if c.MaxEventAttempts > 0 {
		return c.MaxEventAttempts
	}
	return defaultMaxEventAttempts
}

func (c Config) lagWarnBlocks() uint64 {
	if c.LagWarnBlocks > 0 {
		return c.LagWarnBlocks
	}
	return defaultLagWarnBlocks
}

func (c Config) retryBatch() int {
	if c.RetryBatch > 0 {
		return c.RetryBatch
	}
	return defaultRetryBatch
}

// ---- Builders opcionales -----------------------------------------------------

// WithFailedEventStore activa el aparcado/reintento de eventos fallidos.
func (w *EVMDepositWatcher) WithFailedEventStore(s FailedEventStore) *EVMDepositWatcher {
	w.failed = s
	return w
}

// WithIncidentReporter activa el registro de incidentes (reorg tras acreditar,
// eventos venenosos).
func (w *EVMDepositWatcher) WithIncidentReporter(r IncidentReporter) *EVMDepositWatcher {
	w.incidents = r
	return w
}

// WithPostCreditRecheck activa la re-verificación de depósitos ya acreditados
// durante `window` (0 => 24h). Requiere también WithIncidentReporter.
func (w *EVMDepositWatcher) WithPostCreditRecheck(l CreditedTransferLister, window time.Duration) *EVMDepositWatcher {
	w.credited = l
	if window <= 0 {
		window = defaultRecheckWindow
	}
	w.recheckWindow = window
	return w
}

// Stats devuelve una copia del estado actual (seguro entre goroutines).
func (w *EVMDepositWatcher) Stats() Stats {
	w.statsMu.Lock()
	defer w.statsMu.Unlock()
	return w.stats
}

func (w *EVMDepositWatcher) noteTick() {
	w.statsMu.Lock()
	w.stats.Network = w.cfg.Network
	w.stats.LastTickAt = time.Now()
	w.statsMu.Unlock()
}

func (w *EVMDepositWatcher) noteChainView(latest, safeTip uint64, lastProcessed uint64, haveCheckpoint bool) {
	w.statsMu.Lock()
	defer w.statsMu.Unlock()
	w.stats.Network = w.cfg.Network
	w.stats.LatestBlock = latest
	w.stats.SafeTip = safeTip
	if haveCheckpoint {
		w.stats.LastProcessed = lastProcessed
		if safeTip > lastProcessed {
			w.stats.LagBlocks = safeTip - lastProcessed
		} else {
			w.stats.LagBlocks = 0
		}
	}
}

func (w *EVMDepositWatcher) noteAdvanced(to uint64) {
	w.statsMu.Lock()
	w.stats.LastProcessed = to
	if w.stats.SafeTip > to {
		w.stats.LagBlocks = w.stats.SafeTip - to
	} else {
		w.stats.LagBlocks = 0
	}
	w.stats.LastAdvanceAt = time.Now()
	lag, safeTip := w.stats.LagBlocks, w.stats.SafeTip
	w.statsMu.Unlock()

	if lag > w.cfg.lagWarnBlocks() {
		log.Printf("watcher[%s]: ADVERTENCIA lag=%d bloques (procesado=%d, bloque_seguro=%d, umbral=%d)",
			w.cfg.Network, lag, to, safeTip, w.cfg.lagWarnBlocks())
	}
}

func (w *EVMDepositWatcher) noteFailedPending(n int) {
	w.statsMu.Lock()
	w.stats.FailedPending = n
	w.statsMu.Unlock()
}

// ---- Manejo de eventos que fallan ---------------------------------------------

// handleOrPark registra el evento; si falla, lo APARCA en el FailedEventStore.
// Devuelve error (=> el checkpoint NO avanza) si no hay forma segura de
// conservar el evento: sin store, o si el propio guardado falla.
func (w *EVMDepositWatcher) handleOrPark(ctx context.Context, transfer domain.BlockchainTransfer) error {
	herr := w.handleTransfer(ctx, transfer)
	if herr == nil {
		return nil
	}
	log.Printf("watcher[%s]: error procesando evento %s tx=%s log=%d: %v",
		w.cfg.Network, transfer.AssetCode, transfer.TxHash, transfer.LogIndex, herr)

	if w.failed == nil {
		return fmt.Errorf("watcher[%s]: evento %s tx=%s log=%d falló y no hay FailedEventStore; el checkpoint NO avanza: %w",
			w.cfg.Network, transfer.AssetCode, transfer.TxHash, transfer.LogIndex, herr)
	}
	if serr := w.failed.Save(ctx, transfer, herr); serr != nil {
		return fmt.Errorf("watcher[%s]: evento %s tx=%s log=%d falló y NO se pudo aparcar; el checkpoint NO avanza: %w",
			w.cfg.Network, transfer.AssetCode, transfer.TxHash, transfer.LogIndex, errors.Join(herr, serr))
	}
	log.Printf("watcher[%s]: evento %s tx=%s log=%d aparcado para reintento",
		w.cfg.Network, transfer.AssetCode, transfer.TxHash, transfer.LogIndex)
	return nil
}

// retryFailedEvents reintenta los eventos aparcados cuyo backoff venció.
// Un evento que agota MaxEventAttempts pasa a 'poisoned' y genera un
// incidente crítico; deja de reintentarse y NO bloquea a los demás.
func (w *EVMDepositWatcher) retryFailedEvents(ctx context.Context) error {
	if w.failed == nil {
		return nil
	}
	events, err := w.failed.ListRetryable(ctx, w.cfg.Network, w.cfg.retryBatch())
	if err != nil {
		return fmt.Errorf("listar eventos fallidos: %w", err)
	}
	for _, fe := range events {
		herr := w.handleTransfer(ctx, fe.Transfer)
		if herr == nil {
			if err := w.failed.MarkResolved(ctx, fe.ID); err != nil {
				log.Printf("watcher[%s]: evento tx=%s reintentado OK pero no se pudo marcar resuelto: %v",
					w.cfg.Network, fe.Transfer.TxHash, err)
				continue
			}
			log.Printf("watcher[%s]: evento aparcado recuperado tx=%s log=%d tras %d intento(s)",
				w.cfg.Network, fe.Transfer.TxHash, fe.Transfer.LogIndex, fe.Attempts)
			continue
		}
		poisoned, rerr := w.failed.RecordAttemptFailure(ctx, fe.ID, herr, w.cfg.maxEventAttempts())
		if rerr != nil {
			log.Printf("watcher[%s]: no se pudo registrar intento fallido tx=%s: %v", w.cfg.Network, fe.Transfer.TxHash, rerr)
			continue
		}
		if poisoned {
			w.reportIncident(ctx, domain.DepositIncident{
				Network:  w.cfg.Network,
				Kind:     domain.IncidentPoisonedEvent,
				Transfer: fe.Transfer,
				Details: fmt.Sprintf("evento agotó %d reintentos sin poder registrarse; último error: %v",
					w.cfg.maxEventAttempts(), herr),
			})
		}
	}
	if n, err := w.failed.CountPending(ctx, w.cfg.Network); err == nil {
		w.noteFailedPending(n)
	}
	return nil
}

func (w *EVMDepositWatcher) reportIncident(ctx context.Context, inc domain.DepositIncident) {
	log.Printf("watcher[%s]: ALERTA CRÍTICA incidente=%s tx=%s log=%d asset=%s to=%s amount=%s: %s",
		w.cfg.Network, inc.Kind, inc.Transfer.TxHash, inc.Transfer.LogIndex, inc.Transfer.AssetCode,
		inc.Transfer.ToAddress, inc.Transfer.Amount.String(), inc.Details)
	if w.incidents == nil {
		return
	}
	if err := w.incidents.Report(ctx, inc); err != nil {
		log.Printf("watcher[%s]: no se pudo registrar el incidente %s tx=%s: %v", w.cfg.Network, inc.Kind, inc.Transfer.TxHash, err)
	}
}

// ---- Reorg / revert tras acreditar ---------------------------------------------

func (w *EVMDepositWatcher) inspectCredited(ctx context.Context, t domain.BlockchainTransfer) (domain.DepositIncidentKind, string, error) {
	r, err := w.provider.GetReceipt(ctx, t.TxHash)
	if err != nil {
		return "", "", err
	}
	switch {
	case r == nil:
		return domain.IncidentReorgAfterCredit, "el tx ya no existe en la cadena canónica (receipt inexistente)", nil
	case r.BlockHash != t.BlockHash:
		return domain.IncidentReorgAfterCredit,
			fmt.Sprintf("el tx se movió del bloque %s (altura %d) a %s", t.BlockHash, t.BlockNumber, r.BlockHash), nil
	case !r.Status:
		return domain.IncidentRevertedAfterCredit, "el tx figura como revertido (status=false)", nil
	}
	return "", "", nil
}

// recheckCreditedTransfers vuelve a comprobar, durante una ventana, los
// depósitos YA acreditados. Si el tx desaparece, cambia de bloque o aparece
// revertido, registra un incidente crítico (auditoría). NO revierte el ledger
// ni toca el estado del depósito: una reversión automática puede falsear los
// libros peor que dejarlo; la decisión es humana.
//
// Para evitar falsos positivos por un nodo RPC atrasado, el hallazgo debe
// repetirse en una segunda consulta antes de reportarse.
func (w *EVMDepositWatcher) recheckCreditedTransfers(ctx context.Context) error {
	if w.credited == nil || w.incidents == nil {
		return nil
	}
	since := time.Now().Add(-w.recheckWindow)
	list, err := w.credited.FindRecentlyCredited(ctx, w.cfg.Network, since, defaultRecheckBatchLimit)
	if err != nil {
		return fmt.Errorf("listar depósitos acreditados recientes: %w", err)
	}
	for _, t := range list {
		kind, detail, err := w.inspectCredited(ctx, t)
		if err != nil {
			log.Printf("watcher[%s]: recheck tx=%s: no se pudo obtener receipt: %v", w.cfg.Network, t.TxHash, err)
			continue
		}
		if kind == "" {
			continue
		}
		kind2, _, err2 := w.inspectCredited(ctx, t)
		if err2 != nil || kind2 != kind {
			log.Printf("watcher[%s]: recheck tx=%s: hallazgo %s no confirmado en la 2ª consulta; se ignora por ahora",
				w.cfg.Network, t.TxHash, kind)
			continue
		}
		w.reportIncident(ctx, domain.DepositIncident{
			Network:    w.cfg.Network,
			Kind:       kind,
			TransferID: t.ID,
			Transfer:   t,
			Details:    detail,
		})
	}
	return nil
}

// ---- Backfill -------------------------------------------------------------------

// Backfill re-escanea el rango [from,to] de bloques SIN tocar el checkpoint,
// para recuperar depósitos de un hueco histórico. Es idempotente: los eventos
// ya registrados se ignoran (clave única), y los nuevos entran como
// 'detected' para que el watcher en vivo los confirme y acredite por el flujo
// normal. No acredita nada por sí mismo.
func (w *EVMDepositWatcher) Backfill(ctx context.Context, from, to uint64) error {
	if from > to {
		return fmt.Errorf("backfill: rango inválido (from=%d > to=%d)", from, to)
	}
	latest, err := w.provider.LatestBlock(ctx)
	if err != nil {
		return fmt.Errorf("backfill: no se pudo leer el último bloque: %w", err)
	}
	var safeTip uint64
	if latest > w.cfg.SafetyLagBlocks {
		safeTip = latest - w.cfg.SafetyLagBlocks
	}
	if to > safeTip {
		log.Printf("watcher[%s]: backfill: 'to'=%d recortado al bloque seguro %d", w.cfg.Network, to, safeTip)
		to = safeTip
	}
	if from > to {
		return fmt.Errorf("backfill: nada que escanear (from=%d > bloque seguro %d)", from, to)
	}

	chunk := w.cfg.MaxBlocksPerScan
	if chunk == 0 {
		chunk = 100
	}
	log.Printf("watcher[%s]: backfill iniciado [%d,%d] en lotes de %d", w.cfg.Network, from, to, chunk)

	for start := from; start <= to; {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := to
		if to-start+1 > chunk {
			end = start + chunk - 1
		}
		for _, asset := range w.cfg.Assets {
			var serr error
			if asset.Kind == domain.AssetKindNative {
				serr = w.scanNativeRange(ctx, asset, start, end)
			} else {
				serr = w.scanTokenRange(ctx, asset, start, end)
			}
			if serr != nil {
				return fmt.Errorf("backfill: error en [%d,%d] asset %s (reintentable desde %d): %w",
					start, end, asset.AssetCode, start, serr)
			}
		}
		log.Printf("watcher[%s]: backfill [%d,%d] OK", w.cfg.Network, start, end)
		if end == to {
			break
		}
		start = end + 1
	}
	log.Printf("watcher[%s]: backfill completado [%d,%d]", w.cfg.Network, from, to)
	return nil
}
