package blockchainwithdrawal

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	appfee "korzadi-wallet/backend/internal/application/fee"
	"korzadi-wallet/backend/internal/application/locking"
	"korzadi-wallet/backend/internal/application/security"
	"korzadi-wallet/backend/internal/domain"
)

// Deps are the collaborators of Service. Every one is an interface defined
// in ports.go, so the service is fully unit-testable with in-memory fakes.
type Deps struct {
	Store  Store
	Nonces NonceStore

	Ledger       LedgerPort
	UserAccounts UserAccounts
	Clearing     ClearingAccounts

	Fees        FeeEngine
	FeeReserver FeeReserver
	Quotes      QuoteStore

	Users        UserReader
	Wallets      WalletReader
	LedgerAssets LedgerAssets
	Registry     domain.AssetRegistry
	Addresses    AddressBook
	// HotWallets, when set, makes every withdrawal leave from the custody HOT
	// wallet of (network, asset) instead of the user's deposit address.
	HotWallets HotWalletBook

	Signer   Signer
	Networks map[domain.Network]NetworkRuntime

	Config Config

	// Security (Bloque 8, opcional; nil = sin controles adicionales): destino
	// nuevo con espera, velocidad, límites con reserva, aprobación manual y
	// revalidación antes de firmar. Con Security.Limits el Store debe implementar
	// LimitReservationStore.
	Security *security.Service
	// Alerter (opcional) avisa al titular al registrar una dirección nueva.
	Alerter Alerter

	// Locks serializa entre PROCESOS la sección "comprobar saldo + firmar" de
	// cada wallet HOT (pg_advisory_lock en producción). Obligatorio cuando se
	// usa HotWallets: sin él dos réplicas podrían aprobar dos retiros contra el
	// mismo saldo on-chain.
	Locks locking.Locker
	// HotLockWait acota la espera del lock de una HOT (0 = 30s). Al vencer, el
	// worker reintenta enseguida sin consumir intentos del retiro.
	HotLockWait time.Duration

	InstanceID string           // identifies this process as a lease owner
	Now        func() time.Time // injectable clock (tests)
}

// Service is the withdrawal service: request validation + creation + fund
// reservation (HTTP-facing) and the step machine the Worker drives
// (process.go). Both halves share the same persisted state.
type Service struct {
	Deps
}

func NewService(d Deps) (*Service, error) {
	switch {
	case d.Store == nil, d.Nonces == nil, d.Ledger == nil, d.UserAccounts == nil, d.Clearing == nil,
		d.Fees == nil, d.FeeReserver == nil, d.Quotes == nil, d.Users == nil, d.Wallets == nil,
		d.LedgerAssets == nil, d.Registry == nil, d.Addresses == nil:
		return nil, errors.New("withdrawals: dependencia requerida ausente")
	}
	if d.HotWallets != nil {
		// Fail closed at startup: without the in-flight sum two withdrawals could
		// both be approved against the same on-chain balance.
		if _, ok := d.Store.(InFlightReader); !ok {
			return nil, errors.New("withdrawals: con HotWallets el Store debe implementar InFlightReader")
		}
		// Fail closed: un lock solo en memoria no protege con varias réplicas.
		if d.Locks == nil {
			return nil, errors.New("withdrawals: con HotWallets se requiere Locks (pg_advisory_lock) para firmar con varias réplicas")
		}
	}
	if d.Security != nil && d.Security.Limits != nil {
		// Fail closed: sin persistir la reserva no se podría confirmar ni liberar.
		if _, ok := d.Store.(LimitReservationStore); !ok {
			return nil, errors.New("withdrawals: con Security.Limits el Store debe implementar LimitReservationStore")
		}
	}
	if d.Now == nil {
		d.Now = func() time.Time { return time.Now().UTC() }
	}
	if d.InstanceID == "" {
		d.InstanceID = "wd-" + uuid.NewString()[:8]
	}
	if d.Config.Limits == nil {
		d.Config.Limits = map[string]AssetLimits{}
	}
	if d.Config.LeaseDuration <= 0 || d.Config.PollInterval <= 0 || d.Config.BatchSize <= 0 {
		return nil, errors.New("withdrawals: configuración operativa inválida (lease/poll/batch)")
	}
	if d.Networks == nil {
		d.Networks = map[domain.Network]NetworkRuntime{}
	}
	return &Service{Deps: d}, nil
}

// ---- Create ----

// CreateInput carries ONLY what the client may decide. The user comes from
// the JWT; fees come from the Fee Engine; contracts/decimals/RPC come from
// the registries — none of those are ever accepted from a request.
type CreateInput struct {
	UserID             uuid.UUID
	AssetCode          string
	Network            string
	Amount             decimal.Decimal
	DestinationAddress string
	IdempotencyKey     string
	FeeQuoteID         *uuid.UUID // optional: withdraw against an existing, still-valid quote
	TwoFAVerified      bool
	IP                 string // IP del cliente (auditoría de los controles de seguridad)
}

// CreateResult: Created=false means an idempotent replay of an existing
// withdrawal (same user + idempotency key + same parameters).
type CreateResult struct {
	Withdrawal *Withdrawal
	Created    bool
}

// Create validates the request, obtains a Fee Quote from the Bloque 6A
// engine, records the withdrawal (idempotent, race-safe) and reserves the
// amount + fee. It performs NO on-chain action: signing and broadcasting are
// done later by the Worker, so an HTTP request dying at any point can never
// leave a half-sent transaction.
func (s *Service) Create(ctx context.Context, in CreateInput) (*CreateResult, error) {
	network := domain.Network(strings.ToUpper(strings.TrimSpace(in.Network)))
	asset := strings.ToUpper(strings.TrimSpace(in.AssetCode))

	// Bitcoin is DISABLED: refuse before ANY quote, reservation, ledger
	// write, signing or broadcast.
	if network.Family() == domain.FamilyBitcoin {
		return nil, ErrBitcoinDisabled
	}
	if !s.Config.Enabled {
		return nil, ErrWithdrawalsDisabled
	}
	if err := ValidateIdempotencyKey(in.IdempotencyKey); err != nil {
		return nil, err
	}
	if fam := network.Family(); fam != domain.FamilyEVM && fam != domain.FamilyTron {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedNetwork, in.Network)
	}
	if asset == "" || in.UserID == uuid.Nil {
		return nil, ErrAssetNotEnabled
	}

	// Idempotent replay comes first: the same request must return the same
	// result even if conditions (limits, quotes) changed since.
	if existing, err := s.Store.FindByUserKey(ctx, in.UserID, in.IdempotencyKey); err != nil {
		return nil, fmt.Errorf("buscar retiro por idempotency key: %w", err)
	} else if existing != nil {
		return s.replay(existing, network, asset, in)
	}

	runtime, ok := s.Networks[network]
	if !ok || !runtime.configured() {
		return nil, fmt.Errorf("%w: %s", ErrNetworkNotConfigured, network)
	}

	if err := s.checkAccountActive(ctx, in.UserID); err != nil {
		return nil, err
	}

	limits, ok := s.Config.Limits[asset]
	if !ok {
		return nil, fmt.Errorf("%w: sin límites configurados para %s", ErrAssetNotEnabled, asset)
	}
	ledgerAsset, found, err := s.LedgerAssets.Find(ctx, asset)
	if err != nil {
		return nil, fmt.Errorf("consultar asset: %w", err)
	}
	if !found || !ledgerAsset.IsEnabled {
		return nil, fmt.Errorf("%w: %s", ErrAssetNotEnabled, asset)
	}
	netAsset, found, err := s.Registry.Get(ctx, network, asset)
	if err != nil {
		return nil, fmt.Errorf("consultar registro de assets: %w", err)
	}
	if !found || !netAsset.Enabled || netAsset.Network != network {
		return nil, fmt.Errorf("%w: %s en %s", ErrAssetNotEnabled, asset, network)
	}
	if runtime.Resource != nil && netAsset.Kind != domain.AssetKindToken {
		// TRON: only TRC-20 withdrawals; TRX is the fuel (gas station), not a product.
		return nil, fmt.Errorf("%w: %s en %s", ErrAssetNotEnabled, asset, network)
	}
	if netAsset.Kind == domain.AssetKindToken && netAsset.ContractAddress == "" {
		return nil, fmt.Errorf("%w: el registro no tiene contrato para %s en %s", ErrAssetNotEnabled, asset, network)
	}

	if err := ValidateAmount(in.Amount, netAsset.Decimals, ledgerAsset.Decimals, limits); err != nil {
		return nil, err
	}
	destination, err := ValidateDestination(network, netAsset.ContractAddress, in.DestinationAddress)
	// A8 FIX: verificación defensiva de gas y destino nuevo confirmada.
	if in.DestinationAddress == "" || !netAsset.Enabled {
		return nil, fmt.Errorf("%w: destino vacío o activo no habilitado", ErrAssetNotEnabled)
	}

	if err != nil {
		return nil, err
	}

	var fromAddress string
	if s.HotWallets != nil {
		// Camino B: the payer is the custody HOT wallet of this network/asset.
		// Fail closed when none is registered and active.
		fromAddress, err = s.HotWallets.FindActiveHotAddress(ctx, network, asset)
		if err != nil || fromAddress == "" {
			return nil, fmt.Errorf("%w: %s/%s", ErrNoHotWallet, network, asset)
		}
	} else {
		fromAddress, err = s.Addresses.FindActiveDepositAddress(ctx, network, in.UserID)
		if err != nil || fromAddress == "" {
			return nil, fmt.Errorf("%w: %v", ErrNoDepositAddress, err)
		}
	}
	if strings.EqualFold(fromAddress, destination) {
		return nil, ErrDestinationIsSelf
	}

	// ---- Bloque 8: destino, velocidad, límites y aprobación manual ----
	reservation, serr := s.authorizeCreate(ctx, in, network, asset, destination)
	if serr != nil {
		if errors.Is(serr, security.ErrReplay) {
			// Misma solicitud en paralelo: converge con el retiro ya creado.
			if existing, ferr := s.Store.FindByUserKey(ctx, in.UserID, in.IdempotencyKey); ferr == nil && existing != nil {
				return s.replay(existing, network, asset, in)
			}
		}
		return nil, serr
	}
	limitHeld := reservation != nil
	defer func() {
		// Si no llegó a pertenecer a un retiro (error posterior o carrera perdida), se libera.
		if limitHeld {
			_ = s.Security.Release(context.WithoutCancel(ctx), reservation.ID)
		}
	}()

	quote, err := s.obtainQuote(ctx, in, network, asset)
	if err != nil {
		return nil, err
	}

	requiredConfirmations := int64(runtime.observer().ConfirmationsRequired())
	if netAsset.ConfirmationsRequired > 0 {
		requiredConfirmations = int64(netAsset.ConfirmationsRequired)
	}
	quoteID := quote.ID
	w := &Withdrawal{
		ID:                        uuid.New(),
		UserID:                    in.UserID,
		Network:                   network,
		AssetCode:                 asset,
		Amount:                    in.Amount,
		ToAddress:                 destination,
		FromAddress:               fromAddress,
		Status:                    StatusPending,
		IdempotencyKey:            in.IdempotencyKey,
		TwoFAVerified:             in.TwoFAVerified,
		FeeQuoteID:                &quoteID,
		ServiceFeeUSDT:            quote.ServiceFee,
		EstimatedNetworkFeeUSDT:   quote.NetworkFee,
		TotalFeeUSDT:              quote.TotalFee,
		EstimatedNetworkFeeNative: quote.NetworkFeeNative,
		NativeAssetCode:           quote.NativeAssetCode,
		RequiredConfirmations:     &requiredConfirmations,
	}

	owner := s.newOwner()
	stored, created, err := s.Store.CreateIfAbsent(ctx, w, limits.DailyMax, owner, s.Config.LeaseDuration)
	if err != nil {
		return nil, err
	}
	if !created {
		return s.replay(stored, network, asset, in)
	}
	if reservation != nil {
		// La reserva de límites ahora pertenece a este retiro.
		if ls, ok := s.Store.(LimitReservationStore); ok {
			if err := ls.SetLimitReservation(ctx, stored.ID, reservation.ID); err != nil {
				// Queda retenida (cuenta contra el límite): es el lado conservador.
				log.Printf("withdrawal: id=%s no se pudo asociar la reserva de límites %s: %v", stored.ID, reservation.ID, err)
			}
		}
		limitHeld = false
	}

	log.Printf("withdrawal: creado id=%s user=%s network=%s asset=%s amount=%s status=%s",
		stored.ID, stored.UserID, stored.Network, stored.AssetCode, stored.Amount.String(), stored.Status)

	// We hold the lease from CreateIfAbsent: reserve now, then hand the
	// row to the Worker.
	final, err := s.reserveAndUnlease(ctx, stored, owner)
	if err != nil {
		// El withdrawal SÍ se creó (y, si el fallo es final, ya quedó en
		// failed con sus reservas liberadas): se devuelve para que el
		// caller pueda inspeccionarlo, junto con el error para que lo
		// trate como fallo.
		return &CreateResult{Withdrawal: final, Created: true}, err
	}
	return &CreateResult{Withdrawal: final, Created: true}, nil
}

func (s *Service) replay(existing *Withdrawal, network domain.Network, asset string, in CreateInput) (*CreateResult, error) {
	if existing.Network != network || existing.AssetCode != asset ||
		!existing.Amount.Equal(in.Amount) || !strings.EqualFold(existing.ToAddress, in.DestinationAddress) {
		return nil, ErrIdempotencyKeyReused
	}
	return &CreateResult{Withdrawal: existing, Created: false}, nil
}

func (s *Service) checkAccountActive(ctx context.Context, userID uuid.UUID) error {
	user, err := s.Users.FindByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("consultar usuario: %w", err)
	}
	if user == nil || user.Status != domain.UserStatusActive {
		return ErrAccountInactive
	}
	wallet, err := s.Wallets.FindWalletByUserID(ctx, userID)
	if err != nil {
		return fmt.Errorf("consultar wallet: %w", err)
	}
	if wallet == nil || wallet.Status != domain.WalletStatusActive {
		return ErrAccountInactive
	}
	return nil
}

// obtainQuote asks the Fee Engine (never recomputes fees here). If the
// client supplied a quote it must be the caller's own, for exactly this
// operation, and still valid — an expired quote is REJECTED, never used.
func (s *Service) obtainQuote(ctx context.Context, in CreateInput, network domain.Network, asset string) (*appfee.FeeQuote, error) {
	var quote *appfee.FeeQuote
	if in.FeeQuoteID != nil {
		q, err := s.Fees.GetQuote(ctx, *in.FeeQuoteID, in.UserID)
		if err != nil {
			return nil, err
		}
		if err := s.Fees.ValidateQuote(q, s.Now()); err != nil {
			return nil, err // ErrQuoteExpired / ErrQuoteNotActive
		}
		quote = q
	} else {
		q, err := s.Fees.EstimateFee(ctx, appfee.EstimateFeeInput{
			UserID:             in.UserID,
			OperationType:      appfee.OperationWithdrawal,
			Network:            network,
			OperationAssetCode: asset,
			OperationAmount:    in.Amount,
		})
		if err != nil {
			return nil, err
		}
		quote = q
	}

	if quote.OperationType != appfee.OperationWithdrawal || quote.Network != network ||
		quote.OperationAssetCode != asset || quote.FeeAssetCode != FeeAssetCode || quote.UserID != in.UserID {
		return nil, ErrQuoteMismatch
	}
	if quote.ServiceFee.IsNegative() || quote.NetworkFee.IsNegative() ||
		!quote.TotalFee.Equal(quote.ServiceFee.Add(quote.NetworkFee)) || !quote.TotalFee.IsPositive() {
		return nil, fmt.Errorf("%w: cotización inconsistente", ErrQuoteMismatch)
	}
	return quote, nil
}

// ---- Reads (ownership enforced in SQL) ----

func (s *Service) Get(ctx context.Context, userID, id uuid.UUID) (*Withdrawal, error) {
	w, err := s.Store.GetForUser(ctx, id, userID)
	if err != nil {
		return nil, err
	}
	if w == nil {
		return nil, ErrNotFound
	}
	return w, nil
}

func (s *Service) List(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*Withdrawal, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	return s.Store.ListByUser(ctx, userID, limit, offset)
}

// ---- Cancel ----

// Cancel cancels a withdrawal that has not started any on-chain step
// (pending or reserved). It takes the lease first so it can never race the
// Worker moving the row to processing; if the Worker holds the row (or it
// is already processing) the cancel is refused.
func (s *Service) Cancel(ctx context.Context, userID, id uuid.UUID) (*Withdrawal, error) {
	w, err := s.Store.GetForUser(ctx, id, userID)
	if err != nil {
		return nil, err
	}
	if w == nil {
		return nil, ErrNotFound
	}
	owner := s.newOwner()
	ok, err := s.Store.Lease(ctx, id, owner, s.Config.LeaseDuration)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrCannotCancel
	}
	defer s.unlease(ctx, id, owner, 0, false)

	w, err = s.Store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if w == nil || (w.Status != StatusPending && w.Status != StatusReserved) {
		return nil, ErrCannotCancel
	}
	if err := s.releaseReservations(ctx, w); err != nil {
		return nil, err
	}
	detail := "cancelled: cancelado por el usuario"
	applied, err := s.Store.Apply(ctx, id, w.Status, StatusCancelled,
		Update{ErrorDetail: &detail}, Event{Type: "cancelled", Detail: map[string]any{"by": "user"}})
	if err != nil {
		return nil, err
	}
	if !applied {
		return nil, ErrCannotCancel
	}
	s.finishLimit(ctx, w, false)
	s.cancelQuote(ctx, w)
	return s.Store.Get(ctx, id)
}

// ---- Reservation step (shared by Create and the Worker) ----

func amountReservationReason(id uuid.UUID) string { return "withdrawal_amount:" + id.String() }

func feeReservationReason(quoteID uuid.UUID) string {
	// Must mirror fee.ReservationService.ReserveFee's reason format so an
	// orphaned fee reservation (crash between Reserve and SetReservationID)
	// can be adopted instead of leaking locked funds.
	return fmt.Sprintf("fee_quote:%s:%s", appfee.OperationWithdrawal, quoteID)
}

func (s *Service) reserveAndUnlease(ctx context.Context, w *Withdrawal, owner string) (*Withdrawal, error) {
	updated, err := s.reserveStep(ctx, w)
	s.unlease(ctx, w.ID, owner, 0, err != nil && !isFinalFailure(err))
	return updated, err
}

// reserveStep is idempotent: it can be re-run after a crash at any point
// and converges to "reserved" without ever creating a second reservation.
// The caller must hold the row's lease.
//
// Returns (row, ErrInsufficientFunds | appfee.ErrQuoteExpired) after moving
// the row to failed (nothing left reserved); (row, other error) for a
// transient problem (row stays pending; the Worker retries).
func (s *Service) reserveStep(ctx context.Context, w *Withdrawal) (*Withdrawal, error) {
	if w.Status != StatusPending {
		return w, nil
	}
	if w.FeeQuoteID == nil {
		return s.failWithRelease(ctx, w, "invalid_state", errors.New("retiro sin cotización de fee"), false)
	}
	if w.Attempts >= s.Config.MaxReserveTries {
		return s.failWithRelease(ctx, w, "reserve_retries_exhausted", errors.New("no se pudieron reservar los fondos"), false)
	}

	// 1) Withdrawal amount reservation (asset being withdrawn).
	if w.AmountReservationID == nil {
		accountID, err := s.UserAccounts.FindOrCreateUserAccount(ctx, w.UserID, w.AssetCode)
		if err != nil {
			return w, fmt.Errorf("cuenta de usuario para %s: %w", w.AssetCode, err)
		}
		ref := w.ID.String()
		reason := amountReservationReason(w.ID)

		res, err := s.Ledger.FindReservationByReference(ctx, ref, reason)
		if err != nil {
			return w, fmt.Errorf("buscar reserva del monto: %w", err)
		}
		if res == nil {
			res, err = s.Ledger.Reserve(ctx, accountID, w.AssetCode, w.Amount, reason, &ref)
			if err != nil {
				if isInsufficientFunds(err) {
					return s.failWithRelease(ctx, w, "insufficient_funds", err, true)
				}
				return w, fmt.Errorf("reservar monto: %w", err)
			}
		}
		if res.Status != domain.ReservationActive {
			return s.failWithRelease(ctx, w, "reservation_lost", errors.New("la reserva del monto no está activa"), false)
		}
		resID := res.ID
		applied, err := s.Store.Apply(ctx, w.ID, StatusPending, StatusPending,
			Update{AmountReservationID: &resID},
			Event{Type: "amount_reserved", Detail: map[string]any{"reservation_id": resID.String(), "asset": w.AssetCode, "amount": w.Amount.String()}})
		if err != nil {
			return w, err
		}
		if !applied {
			return s.refresh(ctx, w)
		}
		w.AmountReservationID = &resID
	}

	// 2) Fee reservation (USDT, Bloque 6A).
	if w.FeeReservationID == nil {
		res, err := s.reserveFee(ctx, w)
		if err != nil {
			switch {
			case isInsufficientFunds(err):
				return s.failWithRelease(ctx, w, "insufficient_funds", err, true)
			case errors.Is(err, appfee.ErrQuoteExpired), errors.Is(err, appfee.ErrQuoteNotActive):
				return s.failWithRelease(ctx, w, "quote_expired", err, true)
			default:
				return w, fmt.Errorf("reservar fee: %w", err)
			}
		}
		if res.Status != domain.ReservationActive {
			return s.failWithRelease(ctx, w, "reservation_lost", errors.New("la reserva del fee no está activa"), false)
		}
		resID := res.ID
		applied, err := s.Store.Apply(ctx, w.ID, StatusPending, StatusPending,
			Update{FeeReservationID: &resID},
			Event{Type: "fee_reserved", Detail: map[string]any{
				"reservation_id": resID.String(), "quote_id": w.FeeQuoteID.String(),
				"service_fee_usdt": w.ServiceFeeUSDT.String(), "network_fee_usdt": w.EstimatedNetworkFeeUSDT.String(),
				"total_fee_usdt": w.TotalFeeUSDT.String(),
			}})
		if err != nil {
			return w, err
		}
		if !applied {
			return s.refresh(ctx, w)
		}
		w.FeeReservationID = &resID
	}

	// 3) pending → reserved.
	if _, err := s.Store.Apply(ctx, w.ID, StatusPending, StatusReserved, Update{}, Event{Type: "reserved"}); err != nil {
		return w, err
	}
	return s.refresh(ctx, w)
}

// reserveFee reserves the quote's Total Fee through the 6A ReservationService
// (idempotent per quote), first adopting an orphaned reservation left by a
// crash between Reserve and SetReservationID.
func (s *Service) reserveFee(ctx context.Context, w *Withdrawal) (*domain.Reservation, error) {
	quoteID := *w.FeeQuoteID
	orphan, err := s.Ledger.FindReservationByReference(ctx, quoteID.String(), feeReservationReason(quoteID))
	if err != nil {
		return nil, fmt.Errorf("buscar reserva de fee: %w", err)
	}
	if orphan != nil && orphan.Status == domain.ReservationActive {
		q, err := s.Quotes.FindByID(ctx, quoteID)
		if err != nil {
			return nil, fmt.Errorf("buscar cotización: %w", err)
		}
		if q != nil && q.ReservationID == nil {
			claimed, err := s.Quotes.SetReservationID(ctx, quoteID, orphan.ID)
			if err != nil {
				return nil, fmt.Errorf("adoptar reserva de fee: %w", err)
			}
			if claimed {
				if err := s.Quotes.UpdateStatus(ctx, quoteID, appfee.QuoteStatusReserved); err != nil {
					return nil, fmt.Errorf("actualizar estado de cotización: %w", err)
				}
				return orphan, nil
			}
		}
	}
	return s.FeeReserver.ReserveFee(ctx, quoteID)
}

// ---- failure / release helpers ----

// failWithRelease releases whatever is reserved for w and only THEN moves it
// to failed (so a crash in between leaves a non-final row whose next run
// finishes the job — never a "failed" row with funds still locked). Use it
// only when NOTHING was signed/broadcast, or when the transaction is
// definitively known not to exist / not to be executable.
//
// It returns the refreshed row plus a typed error for the caller to surface:
// ErrInsufficientFunds and appfee.ErrQuoteExpired map to their sentinels;
// everything else returns (row, nil).
func (s *Service) failWithRelease(ctx context.Context, w *Withdrawal, code string, cause error, surface bool) (*Withdrawal, error) {
	if err := s.releaseReservations(ctx, w); err != nil {
		return w, fmt.Errorf("liberar reservas: %w", err)
	}
	s.releaseNonces(ctx, w)

	detail := fmt.Sprintf("%s: %s", code, sanitizeCause(cause))
	needsReview := false
	applied, err := s.Store.Apply(ctx, w.ID, w.Status, StatusFailed,
		Update{ErrorDetail: &detail, NeedsReview: &needsReview},
		Event{Type: "failed", Detail: map[string]any{"code": code, "cause": sanitizeCause(cause), "released": true}})
	if err != nil {
		return w, err
	}
	if !applied {
		return s.refresh(ctx, w)
	}
	s.finishLimit(ctx, w, false)
	s.cancelQuote(ctx, w)
	log.Printf("withdrawal: FALLÓ id=%s network=%s asset=%s code=%s (fondos liberados)", w.ID, w.Network, w.AssetCode, code)

	updated, gerr := s.refresh(ctx, w)
	if gerr != nil {
		return w, gerr
	}
	if surface {
		switch code {
		case "insufficient_funds":
			return updated, ErrInsufficientFunds
		case "quote_expired":
			return updated, appfee.ErrQuoteExpired
		}
	}
	return updated, nil
}

// failNeedsReview moves the withdrawal to failed WITHOUT releasing anything:
// used when a transaction may exist on-chain and its real state cannot be
// determined automatically. Funds stay locked until an operator resolves it.
func (s *Service) failNeedsReview(ctx context.Context, w *Withdrawal, code string, cause error) (*Withdrawal, error) {
	detail := fmt.Sprintf("%s: %s", code, sanitizeCause(cause))
	review := true
	applied, err := s.Store.Apply(ctx, w.ID, w.Status, StatusFailed,
		Update{ErrorDetail: &detail, NeedsReview: &review},
		Event{Type: "needs_review", Detail: map[string]any{"code": code, "cause": sanitizeCause(cause), "released": false}})
	if err != nil {
		return w, err
	}
	if !applied {
		return s.refresh(ctx, w)
	}
	log.Printf("withdrawal: REQUIERE REVISIÓN MANUAL id=%s network=%s asset=%s code=%s tx=%s (fondos NO liberados)", w.ID, w.Network, w.AssetCode, code, w.TxHash)
	return s.refresh(ctx, w)
}

// releaseReservations releases the amount and fee reservations of w,
// including a reservation created just before a crash whose id was never
// persisted (found via its reference). Already-released is success.
func (s *Service) releaseReservations(ctx context.Context, w *Withdrawal) error {
	amountID := w.AmountReservationID
	if amountID == nil {
		if res, err := s.Ledger.FindReservationByReference(ctx, w.ID.String(), amountReservationReason(w.ID)); err != nil {
			return err
		} else if res != nil {
			amountID = &res.ID
		}
	}
	feeID := w.FeeReservationID
	if feeID == nil && w.FeeQuoteID != nil {
		if res, err := s.Ledger.FindReservationByReference(ctx, w.FeeQuoteID.String(), feeReservationReason(*w.FeeQuoteID)); err != nil {
			return err
		} else if res != nil {
			feeID = &res.ID
		}
	}
	for _, id := range []*uuid.UUID{amountID, feeID} {
		if id == nil {
			continue
		}
		if err := s.Ledger.Release(ctx, *id); err != nil && !errors.Is(err, domain.ErrReservationNotActive) {
			return err
		}
	}
	return nil
}

// releaseNonces frees the nonces this withdrawal owns — ONLY valid when the
// transaction was never accepted by the network. Best effort: a nonce left
// reserved can only cause a benign gap that the next Allocate skips over,
// never a double spend.
func (s *Service) releaseNonces(ctx context.Context, w *Withdrawal) {
	if w.FromAddress == "" {
		return
	}
	if err := s.Nonces.Release(ctx, w.Network, w.FromAddress, nonceOwner(w.ID)); err != nil {
		log.Printf("withdrawal: no se pudo liberar nonce id=%s: %v", w.ID, err)
	}
	if rt, ok := s.Networks[w.Network]; ok && rt.Gas != nil {
		if station, ok := rt.Gas.(interface{ Address() string }); ok {
			if err := s.Nonces.Release(ctx, w.Network, station.Address(), gasOwner(w.ID)); err != nil {
				log.Printf("withdrawal: no se pudo liberar nonce de gas id=%s: %v", w.ID, err)
			}
		}
	}
}

func (s *Service) cancelQuote(ctx context.Context, w *Withdrawal) {
	if w.FeeQuoteID == nil {
		return
	}
	if err := s.Quotes.UpdateStatus(ctx, *w.FeeQuoteID, appfee.QuoteStatusCancelled); err != nil {
		log.Printf("withdrawal: no se pudo cancelar la cotización id=%s: %v", w.ID, err)
	}
}

func (s *Service) refresh(ctx context.Context, w *Withdrawal) (*Withdrawal, error) {
	updated, err := s.Store.Get(ctx, w.ID)
	if err != nil {
		return w, err
	}
	if updated == nil {
		return w, ErrNotFound
	}
	return updated, nil
}

// unlease frees the lease with a context that survives the caller's
// cancellation (an HTTP client disconnecting must not strand the lease).
func (s *Service) unlease(ctx context.Context, id uuid.UUID, owner string, delay time.Duration, bump bool) {
	bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := s.Store.Unlease(bg, id, owner, delay, bump); err != nil {
		log.Printf("withdrawal: no se pudo liberar el lease id=%s: %v", id, err)
	}
}

// newOwner returns a unique lease token for ONE acquisition (instance id +
// random suffix): a lease that expired and was re-taken by someone else can
// never be freed by its previous holder.
func (s *Service) newOwner() string { return s.InstanceID + "/" + uuid.NewString()[:8] }

func nonceOwner(id uuid.UUID) string { return "withdrawal:" + id.String() }
func gasOwner(id uuid.UUID) string   { return "gas:" + id.String() }

func isInsufficientFunds(err error) bool {
	if err == nil {
		return false
	}
	// ReserveFee wraps the ledger error with %v, which drops the chain, so
	// the message is checked too.
	return errors.Is(err, domain.ErrInsufficientFunds) ||
		errors.Is(err, ErrInsufficientFunds) ||
		strings.Contains(err.Error(), domain.ErrInsufficientFunds.Message)
}

// isFinalFailure reports whether a reserveStep error already moved the row
// to a final state (so the lease bump/backoff is unnecessary).
func isFinalFailure(err error) bool {
	return errors.Is(err, ErrInsufficientFunds) || errors.Is(err, appfee.ErrQuoteExpired)
}

// sanitizeCause keeps the cause short and single-line so it is safe to
// persist in error_detail / events.
func sanitizeCause(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ReplaceAll(err.Error(), "\n", " ")
	if len(msg) > 300 {
		msg = msg[:300]
	}
	return msg
}
