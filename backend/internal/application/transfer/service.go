package transfer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/application/security"
	"korzadi-wallet/backend/internal/domain"
)

// LedgerAsset mirrors one row of the `assets` table (the global
// ledger-level catalog) — owned here in the application layer so
// postgres.AssetRepository can implement AssetValidator by returning this
// exact type, matching the pattern already used for
// deposit.BlockchainDepositRecord (Bloque 4): the DTO lives in
// application code, infrastructure imports it, not the other way around.
type LedgerAsset struct {
	Code      string
	Name      string
	Decimals  int32
	IsEnabled bool
}

// AssetValidator resolves whether an asset code is real and currently
// enabled — implemented by postgres.AssetRepository. Bloque 5 fix: before
// this, AssetCode flowed straight from the HTTP request into
// GetOrCreateAccount/ExecuteTransaction with no validation at all; an
// unregistered code would eventually fail on a Postgres foreign-key
// violation instead of a clean domain error (spec §5: "resolver el asset
// mediante el registro interno", never assume a client-supplied symbol
// exists).
type AssetValidator interface {
	Find(ctx context.Context, code string) (asset LedgerAsset, found bool, err error)
}

// RecipientResolver is the narrow slice of domain.UserRepository this
// service needs to resolve a recipient by their verified email — the
// existing, already-verified identity mechanism (spec §3: "audita qué
// mecanismos de identificación ya existen... NO inventes un nuevo sistema
// de identidad"). Sending directly by internal user ID remains supported
// for backward compatibility, but email is the friendlier, already-proven
// path this block adds.
type RecipientResolver interface {
	FindByEmail(ctx context.Context, email string) (*domain.User, error)
}

type Service struct {
	wallets   domain.WalletRepository
	ledger    domain.LedgerRepository
	transfers domain.TransferRepository
	assets    AssetValidator
	users     RecipientResolver
	audit     domain.AuditLogger
	notifier  domain.Notifier

	// security is Bloque 8's optional control layer (authorization,
	// limits, kill switch / emergency freeze, step-up). It is nil unless
	// SetSecurity is called, matching this Service's existing convention
	// for optional collaborators (audit, notifier): every call site below
	// checks for nil before using it, so a caller that never wires
	// Bloque 8 (e.g. an existing test using NewService directly) keeps
	// working exactly as before — Bloque 8 adds controls, it never
	// changes transfer.Service's behavior for a caller that doesn't ask
	// for them.
	security *security.Service
}

func NewService(wallets domain.WalletRepository, ledger domain.LedgerRepository,
	transfers domain.TransferRepository, assets AssetValidator, users RecipientResolver,
	audit domain.AuditLogger, notifier domain.Notifier) *Service {
	return &Service{wallets: wallets, ledger: ledger, transfers: transfers, assets: assets, users: users, audit: audit, notifier: notifier}
}

// SetSecurity wires Bloque 8's controls into this service. Called once at
// startup (see cmd/api/main.go); never required — nil is a fully valid,
// backward-compatible state.
func (s *Service) SetSecurity(svc *security.Service) {
	s.security = svc
}

type CreateTransferInput struct {
	SenderUserID uuid.UUID
	// Recipient identifies who receives the funds — set exactly one of
	// RecipientUserID or RecipientEmail. Never sourced from anything but
	// what the sender typed in as the recipient; sender identity itself
	// always comes from SenderUserID (the authenticated caller), never
	// from this input.
	RecipientUserID uuid.UUID
	RecipientEmail  string
	AssetCode       string
	Amount          decimal.Decimal
	IdempotencyKey  string
	Note            string
	IP              string
	// TwoFACode is only required when the amount crosses this
	// operation's step-up threshold (Bloque 8 spec §12/§13); ignored
	// otherwise. Empty is fine for a normal, low-value transfer.
	TwoFACode string
	// PinCode is an alternative step-up code (4-8 digit PIN) that can be
	// provided when the user has a PIN configured and 2FA is not enabled.
	PinCode string
}

// resolveRecipient turns whichever recipient identifier was supplied into
// a concrete user ID, using only the existing, already-verified identity
// mechanism (email) — never inventing a new one.
func (s *Service) resolveRecipient(ctx context.Context, in CreateTransferInput) (uuid.UUID, error) {
	if in.RecipientUserID != uuid.Nil {
		return in.RecipientUserID, nil
	}
	email := strings.TrimSpace(strings.ToLower(in.RecipientEmail))
	if email == "" {
		return uuid.Nil, fmt.Errorf("%w: se requiere un destinatario", domain.ErrUserNotFound)
	}
	user, err := s.users.FindByEmail(ctx, email)
	if err != nil {
		return uuid.Nil, fmt.Errorf("resolver destinatario: %w", err)
	}
	if user == nil {
		return uuid.Nil, domain.ErrUserNotFound
	}
	if !user.EmailVerified {
		// A transfer to an unverified email could land on an account the
		// sender only believes exists (typo, unclaimed signup, etc.) —
		// treating it identically to "not found" avoids leaking which
		// emails are registered at all while refusing the risky case.
		return uuid.Nil, domain.ErrUserNotFound
	}
	return user.ID, nil
}

func (s *Service) Execute(ctx context.Context, in CreateTransferInput) (*domain.Transfer, error) {
	if in.Amount.LessThanOrEqual(decimal.Zero) {
		return nil, domain.ErrInvalidAmount
	}

	asset, found, err := s.assets.Find(ctx, in.AssetCode)
	if err != nil {
		return nil, fmt.Errorf("validar asset: %w", err)
	}
	if !found || !asset.IsEnabled {
		return nil, domain.ErrAssetDisabled
	}
	if decimalPlaces(in.Amount) > asset.Decimals {
		return nil, fmt.Errorf("%w: %s admite hasta %d decimales", domain.ErrInvalidAmount, in.AssetCode, asset.Decimals)
	}

	receiverUserID, err := s.resolveRecipient(ctx, in)
	if err != nil {
		return nil, err
	}
	if in.SenderUserID == receiverUserID {
		return nil, domain.ErrSelfTransfer
	}

	// ---- Idempotency fast path ----
	if existing, err := s.transfers.FindByIdempotencyKey(ctx, in.IdempotencyKey); err == nil && existing != nil {
		// AUDIT FIX: idempotency keys are globally unique but were never bound to
		// the caller/payload — another user's key returned THEIR transfer (data
		// leak) and a reused key with a different payload silently "succeeded".
		if !sameTransferRequest(existing, in, receiverUserID) {
			return nil, ErrIdempotencyKeyConflict
		}
		return s.reconcileExisting(ctx, existing)
	}

	// ---- Bloque 8: pre-authorization (active account, kill switch, emergency freeze) ----
	if s.security != nil {
		if err := s.security.PreAuthorize(ctx, security.OpTransfer, in.SenderUserID, in.IP); err != nil {
			return nil, err
		}
	}

	// ---- Bloque 9: recipient-side compliance (refuses the transfer; the
	// sender keeps their funds; blockchain deposits are never gated) ----
	if s.security != nil {
		if err := s.security.CheckRecipient(ctx, security.OpTransfer, receiverUserID); err != nil {
			return nil, err
		}
	}

	senderWallet, err := s.wallets.FindWalletByUserID(ctx, in.SenderUserID)
	if err != nil {
		return nil, fmt.Errorf("wallet remitente: %w", err)
	}
	if senderWallet == nil {
		return nil, domain.ErrUserNotFound
	}
	receiverWallet, err := s.wallets.FindWalletByUserID(ctx, receiverUserID)
	if err != nil {
		return nil, fmt.Errorf("wallet destinatario: %w", err)
	}
	if receiverWallet == nil {
		// The recipient resolved to a real user (checked above) but has
		// no wallet yet — treat identically to "recipient not found" from
		// the sender's point of view (spec §12: destinatario inválido ->
		// error apropiado, sin tocar el Ledger). No wallet is created here.
		return nil, domain.ErrUserNotFound
	}

	// ---- Bloque 8: limit reservation + step-up ----
	// Reserved with ReferenceID = the idempotency key, so a genuine
	// retry of the exact same request (not a "loser" of the create-race
	// handled below, which releases its own reservation) can never
	// double-reserve against the sender's daily/monthly limit (spec §9).
	var reservation *security.Reservation
	if s.security != nil {
		res, policy, rerr := s.security.Reserve(ctx, security.ReserveInput{
			UserID: in.SenderUserID, Operation: security.OpTransfer, AssetCode: in.AssetCode,
			Amount: in.Amount, ReferenceID: in.IdempotencyKey,
		}, in.IP)
		if rerr != nil {
			return nil, rerr
		}
		reservation = res
		if s.security.StepUp != nil {
			if serr := s.security.StepUp.RequireForAmount(ctx, policy, in.Amount, in.SenderUserID, in.TwoFACode, in.PinCode, in.IP); serr != nil {
				_ = s.security.Release(ctx, reservation.ID)
				return nil, serr
			}
		}
	}

	now := time.Now().UTC()
	newTransfer := &domain.Transfer{
		ID:             uuid.New(),
		SenderUserID:   in.SenderUserID,
		ReceiverUserID: receiverUserID,
		AssetCode:      in.AssetCode,
		Amount:         in.Amount,
		Status:         domain.StatusPending,
		IdempotencyKey: in.IdempotencyKey,
		Note:           in.Note,
		CreatedAt:      now,
	}
	if err := s.transfers.Create(ctx, newTransfer); err != nil {
		if errors.Is(err, domain.ErrIdempotencyReplay) {
			// Lost a concurrent race: another request with this exact
			// idempotency_key already created (or is creating) the row.
			// Re-fetch and converge on its outcome instead of erroring —
			// this is what makes "same request sent twice at the same
			// instant" produce one transfer, not a hard failure for the
			// loser (spec §10, §11). Our own reservation is unused (the
			// winner's copy is what actually posts), so release it back.
			if s.security != nil && reservation != nil {
				_ = s.security.Release(ctx, reservation.ID)
			}
			existing, findErr := s.transfers.FindByIdempotencyKey(ctx, in.IdempotencyKey)
			if findErr != nil || existing == nil {
				return nil, fmt.Errorf("crear registro de transferencia: %w", err)
			}
			if !sameTransferRequest(existing, in, receiverUserID) {
				return nil, ErrIdempotencyKeyConflict
			}
			return s.reconcileExisting(ctx, existing)
		}
		if s.security != nil && reservation != nil {
			_ = s.security.Release(ctx, reservation.ID)
		}
		return nil, fmt.Errorf("crear registro de transferencia: %w", err)
	}

	return s.postAndFinalize(ctx, newTransfer, in, reservation)
}

// ErrIdempotencyKeyConflict is returned when an Idempotency-Key already
// belongs to a different sender or to a transfer with different parameters.
var ErrIdempotencyKeyConflict = &domain.DomainError{
	Code:    "idempotency_key_reused",
	Message: "el Idempotency-Key ya fue usado con otra solicitud",
}

// sameTransferRequest reports whether a stored transfer is exactly the request
// being retried (same sender, recipient, asset and amount).
func sameTransferRequest(existing *domain.Transfer, in CreateTransferInput, receiverUserID uuid.UUID) bool {
	return existing.SenderUserID == in.SenderUserID &&
		existing.ReceiverUserID == receiverUserID &&
		existing.AssetCode == in.AssetCode &&
		existing.Amount.Equal(in.Amount)
}

// reconcileExisting handles every retry of an already-known idempotency_key.
// A transfer in a terminal state (completed/failed/cancelled) is done —
// return it as-is. A transfer still 'pending' means a PRIOR attempt was
// interrupted somewhere between creating this row and finishing the ledger
// post (a crash, a transient error, a concurrent request that's still
// in flight) — Bloque 5 fix: this used to just return the stale pending
// row and stop, silently never completing the transfer. Now it actually
// drives that same row through to completion, exactly like the retry-safety
// fix Bloque 4 applied to blockchain deposits stuck between 'confirmed'
// and 'credited'.
func (s *Service) reconcileExisting(ctx context.Context, existing *domain.Transfer) (*domain.Transfer, error) {
	if existing.Status == domain.StatusCompleted || existing.Status == domain.StatusFailed || existing.Status == domain.StatusCancelled {
		return existing, nil
	}

	// Still pending. If the ledger already has this idempotency_key, the
	// money movement already happened — just the transfer row's status
	// update didn't land; finish that bookkeeping now.
	if ledgerTx := s.findLedgerTxForTransfer(ctx, existing); ledgerTx != nil {
		if err := s.transfers.UpdateStatus(ctx, existing.ID, domain.StatusCompleted, &ledgerTx.ID); err != nil {
			return nil, fmt.Errorf("actualizar transferencia reconciliada: %w", err)
		}
		existing.Status = domain.StatusCompleted
		existing.LedgerTransactionID = &ledgerTx.ID
		return existing, nil
	}

	// No ledger transaction exists yet — the money never moved. Retry the
	// actual posting now, reusing this same transfer row (same ID, same
	// idempotency_key) rather than creating a second one. No new Bloque 8
	// reservation is made here: the original request's reservation (made
	// in Execute, before this row was created) is still active and will
	// be committed/released by this same call to postAndFinalize below —
	// passing reservation=nil here only means "this particular call
	// won't itself resolve a reservation", not that none exists.
	in := CreateTransferInput{
		SenderUserID: existing.SenderUserID, RecipientUserID: existing.ReceiverUserID,
		AssetCode: existing.AssetCode, Amount: existing.Amount, IdempotencyKey: existing.IdempotencyKey,
		Note: existing.Note,
	}
	return s.postAndFinalize(ctx, existing, in, nil)
}

// transferLedgerKey es la idempotency_key con la que una transferencia se
// postea en el ledger: namespaceada por remitente, de modo que la clave que
// manda el usuario jamás coincide con una clave interna (depósitos, retiros...)
// ni con la de otro usuario.
func transferLedgerKey(t *domain.Transfer) string {
	return domain.NamespacedIdempotencyKey(domain.IdemPrefixTransfer, t.SenderUserID, t.IdempotencyKey)
}

// findLedgerTxForTransfer busca la tx de ledger de una transferencia ya
// posteada: primero con la clave namespaceada y, por compatibilidad con filas
// anteriores a este cambio, con la clave cruda SOLO si esa tx es de tipo
// transfer y su reference_id es esta misma transferencia (evita re-postear y
// pagar dos veces una transferencia legacy).
func (s *Service) findLedgerTxForTransfer(ctx context.Context, t *domain.Transfer) *domain.LedgerTransaction {
	if tx, err := s.ledger.FindByIdempotencyKey(ctx, transferLedgerKey(t)); err == nil && tx != nil {
		return tx
	}
	if tx, err := s.ledger.FindByIdempotencyKey(ctx, t.IdempotencyKey); err == nil && tx != nil &&
		tx.Type == domain.TxTypeTransfer && tx.ReferenceID != nil && *tx.ReferenceID == t.ID.String() {
		return tx
	}
	return nil
}

// postAndFinalize performs the actual atomic ledger post (debit sender,
// credit receiver) for a transfer row that already exists, then updates
// its status to match the outcome. reservation, when non-nil, is the
// Bloque 8 limit reservation Execute made for this attempt: committed on
// success, released on any failure path (via the deferred cleanup below),
// so a reservation is never left dangling in the common case where this
// function runs to completion within one Execute call.
func (s *Service) postAndFinalize(ctx context.Context, t *domain.Transfer, in CreateTransferInput, reservation *security.Reservation) (*domain.Transfer, error) {
	committed := false
	if s.security != nil && reservation != nil {
		defer func() {
			if !committed {
				_ = s.security.Release(ctx, reservation.ID)
			}
		}()
	}

	senderWallet, err := s.wallets.FindWalletByUserID(ctx, t.SenderUserID)
	if err != nil || senderWallet == nil {
		return nil, fmt.Errorf("wallet remitente: %w", err)
	}
	receiverWallet, err := s.wallets.FindWalletByUserID(ctx, t.ReceiverUserID)
	if err != nil || receiverWallet == nil {
		return nil, fmt.Errorf("wallet destinatario: %w", err)
	}
	senderAccount, err := s.wallets.GetOrCreateAccount(ctx, senderWallet.ID, t.AssetCode)
	if err != nil {
		return nil, err
	}
	receiverAccount, err := s.wallets.GetOrCreateAccount(ctx, receiverWallet.ID, t.AssetCode)
	if err != nil {
		return nil, err
	}

	ref := t.ID.String()
	ledgerTx, err := s.ledger.ExecuteTransaction(ctx, domain.TxTypeTransfer, transferLedgerKey(t), &ref,
		fmt.Sprintf("Transferencia %s -> %s", t.SenderUserID, t.ReceiverUserID), &t.SenderUserID,
		[]domain.LedgerEntryInput{
			{WalletAccountID: senderAccount.ID, AssetCode: t.AssetCode, Amount: t.Amount.Neg()},
			{WalletAccountID: receiverAccount.ID, AssetCode: t.AssetCode, Amount: t.Amount},
		})
	if err != nil {
		_ = s.transfers.UpdateStatus(ctx, t.ID, domain.StatusFailed, nil)
		if s.audit != nil {
			_ = s.audit.Log(ctx, &t.SenderUserID, "user", "transfer_failed", "transfer", &t.ID,
				map[string]any{"reason": err.Error()}, in.IP)
		}
		return nil, err
	}

	if err := s.transfers.UpdateStatus(ctx, t.ID, domain.StatusCompleted, &ledgerTx.ID); err != nil {
		return nil, err
	}
	t.Status = domain.StatusCompleted
	t.LedgerTransactionID = &ledgerTx.ID

	if s.security != nil && reservation != nil {
		_ = s.security.Commit(ctx, reservation.ID)
		committed = true
	}

	if s.audit != nil {
		_ = s.audit.Log(ctx, &t.SenderUserID, "user", "transfer_completed", "transfer", &t.ID,
			map[string]any{"amount": t.Amount.String(), "asset": t.AssetCode, "receiver": t.ReceiverUserID}, in.IP)
	}
	if s.notifier != nil {
		_ = s.notifier.Notify(ctx, t.SenderUserID, "transfer_sent",
			"Enviaste dinero", fmt.Sprintf("Enviaste %s %s.", t.Amount.String(), t.AssetCode))
		_ = s.notifier.Notify(ctx, t.ReceiverUserID, "transfer_received",
			"Recibiste dinero", fmt.Sprintf("Recibiste %s %s.", t.Amount.String(), t.AssetCode))
	}

	return t, nil
}

// decimalPlaces returns how many digits appear after the decimal point in
// d, exactly as parsed (shopspring/decimal preserves the input's exponent
// rather than normalizing trailing zeros away — "1.50" reports 2, not 1).
// Rejecting excess precision here is deliberate: silently truncating a
// user-supplied amount to fit the asset's decimals would discard value
// without clear consent, which is worse than asking them to resend a
// canonical amount string.
func decimalPlaces(d decimal.Decimal) int32 {
	return -d.Exponent()
}

// ---- History (spec §14-15) ----

// Direction distinguishes a transfer's role from the perspective of the
// user asking about it — the same row is "sent" for the sender and
// "received" for the receiver.
type Direction string

const (
	DirectionSent     Direction = "sent"
	DirectionReceived Direction = "received"
)

// TransferView is the read-model shape for history endpoints — same
// underlying domain.Transfer, annotated with which side the requesting
// user was on so the client doesn't have to compare IDs itself.
type TransferView struct {
	*domain.Transfer
	Direction        Direction
	CounterpartyID   uuid.UUID
	CounterpartyName string // best-effort display name; empty if unavailable
}

func toView(t *domain.Transfer, forUserID uuid.UUID) TransferView {
	if t.SenderUserID == forUserID {
		return TransferView{Transfer: t, Direction: DirectionSent, CounterpartyID: t.ReceiverUserID}
	}
	return TransferView{Transfer: t, Direction: DirectionReceived, CounterpartyID: t.SenderUserID}
}

const (
	defaultHistoryPageSize = 50
	maxHistoryPageSize     = 200
)

// ListForUser returns userID's own transfers (sent or received), newest
// first, each annotated with Direction from userID's point of view.
func (s *Service) ListForUser(ctx context.Context, userID uuid.UUID, limit, offset int) ([]TransferView, error) {
	if limit <= 0 {
		limit = defaultHistoryPageSize
	}
	if limit > maxHistoryPageSize {
		limit = maxHistoryPageSize
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.transfers.ListByUser(ctx, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("listar transferencias: %w", err)
	}
	out := make([]TransferView, 0, len(rows))
	for _, t := range rows {
		out = append(out, toView(t, userID))
	}
	return out, nil
}

// ErrTransferNotFound covers both "no existe" and "no te pertenece" —
// deliberately indistinguishable to the caller (spec §15 IDOR).
var ErrTransferNotFound = fmt.Errorf("transfer: transferencia no encontrada")

// GetForUser returns a single transfer only if userID was the sender or
// receiver — ownership enforced in the repository's SQL, not in Go.
func (s *Service) GetForUser(ctx context.Context, transferID, userID uuid.UUID) (*TransferView, error) {
	t, err := s.transfers.FindByIDForUser(ctx, transferID, userID)
	if err != nil {
		return nil, fmt.Errorf("buscar transferencia: %w", err)
	}
	if t == nil {
		return nil, ErrTransferNotFound
	}
	view := toView(t, userID)
	return &view, nil
}
