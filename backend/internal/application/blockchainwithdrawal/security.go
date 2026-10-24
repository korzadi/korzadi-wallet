package blockchainwithdrawal

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/application/security"
	"korzadi-wallet/backend/internal/domain"
)

// Controles del Bloque 8 aplicados a los retiros (Fase D). Todo es opcional:
// con Deps.Security == nil el servicio se comporta exactamente como antes.
//
//	Create:  destino (bloqueado / nuevo => espera) → velocidad → límites (reserva)
//	         → monto que exige aprobación manual (sin flujo: se rechaza)
//	Worker:  antes de firmar o transmitir se revalida freeze / kill switch /
//	         cuenta activa / compliance; si falla, el retiro se RETIENE (no se pierde)
//	Final:   completado → se confirma la reserva de límites y el destino pasa a "known";
//	         fallido o cancelado → se libera la reserva.

// securityHoldDelay: cada cuánto se reevalúa un retiro retenido por seguridad.
const securityHoldDelay = 30 * time.Second

// limitReferenceID namespacea la referencia de la reserva de límites por usuario:
// el ReferenceID es global por operación, así que usar la clave cruda del cliente
// permitiría colisiones entre usuarios.
func limitReferenceID(userID uuid.UUID, idempotencyKey string) string {
	return "withdrawal:" + userID.String() + ":" + idempotencyKey
}

// authorizeCreate ejecuta los controles previos a crear el retiro y devuelve la
// reserva de límites (nil si no hay Limits). El llamador la libera si algo
// falla después, o la entrega al retiro con SetLimitReservation.
func (s *Service) authorizeCreate(ctx context.Context, in CreateInput, network domain.Network, asset, destination string) (*security.Reservation, error) {
	sec := s.Security
	if sec == nil {
		return nil, nil
	}
	uid := in.UserID

	// 1) Destino: bloqueado => rechazo; nuevo => registro + espera + aviso al titular.
	if sec.Destination != nil {
		_, err := sec.Destination.CheckWithCooldown(ctx, uid, asset, string(network), destination, in.IP, s.Config.NewDestinationCooldown)
		if err != nil {
			var cd *security.DestinationCooldownError
			if errors.As(err, &cd) {
				if cd.FirstRequest {
					s.alertNewDestination(ctx, in, network, asset, destination, cd.AvailableAt)
					if sec.Velocity != nil {
						exceeded, verr := sec.Velocity.RecordAndLog(ctx, "new_destinations_per_user", uid.String(), in.IP)
						if verr != nil {
							return nil, fmt.Errorf("%w: velocidad de destinos: %v", ErrSecurityUnavailable, verr)
						}
						if exceeded {
							return nil, security.ErrRateLimited
						}
					}
				}
				return nil, err
			}
			if errors.Is(err, security.ErrDestinationBlocked) {
				return nil, err
			}
			return nil, fmt.Errorf("%w: destino: %v", ErrSecurityUnavailable, err)
		}
	}

	// 2) Velocidad: demasiados retiros en poco tiempo.
	if sec.Velocity != nil {
		exceeded, err := sec.Velocity.RecordAndLog(ctx, "withdrawals_per_user", uid.String(), in.IP)
		if err != nil {
			return nil, fmt.Errorf("%w: velocidad: %v", ErrSecurityUnavailable, err)
		}
		if exceeded {
			return nil, security.ErrRateLimited
		}
	}

	// 3) Límites (mín/máx/diario/mensual) con reserva atómica.
	res, policy, err := sec.Reserve(ctx, security.ReserveInput{
		UserID: uid, Operation: security.OpWithdrawal, AssetCode: asset, Network: string(network),
		Amount: in.Amount, ReferenceID: limitReferenceID(uid, in.IdempotencyKey),
	}, in.IP)
	if err != nil {
		return nil, err
	}

	// 4) Montos que la política manda a aprobación manual. Aún no existe un flujo
	// de aprobación para retiros: se RECHAZAN (fail closed) en vez de saltarse el control.
	if security.EvaluateThresholds(policy, in.Amount) == "manual_approval" {
		if res != nil {
			_ = sec.Release(context.WithoutCancel(ctx), res.ID)
		}
		return nil, security.ErrManualApprovalRequired
	}
	return res, nil
}

// alertNewDestination avisa al titular (notificación + correo) de que se pidió un
// retiro a una dirección nueva. Es la señal que permite detectar un secuestro de
// cuenta durante la espera.
func (s *Service) alertNewDestination(ctx context.Context, in CreateInput, network domain.Network, asset, destination string, availableAt time.Time) {
	if s.Alerter == nil {
		return
	}
	s.Alerter.Alert(ctx, in.UserID, "Nueva dirección de retiro",
		fmt.Sprintf("Se solicitó retirar %s %s a una dirección nueva (%s, red %s). "+
			"Por seguridad solo podrás retirar a ella a partir del %s (UTC). "+
			"Si no fuiste tú, cambia tu contraseña de inmediato y contacta a soporte.",
			in.Amount.String(), asset, maskAddress(destination), network, availableAt.UTC().Format("2006-01-02 15:04")))
}

func maskAddress(a string) string {
	if len(a) <= 12 {
		return "***"
	}
	return a[:6] + "…" + a[len(a)-4:]
}

// securityHold revalida, justo antes de firmar o transmitir, que el retiro sigue
// autorizado (freeze / kill switch / cuenta activa / compliance). Si no, el
// retiro se RETIENE y se reintenta más tarde: no se falla ni se liberan fondos,
// para que un freeze de emergencia o un bloqueo no pierda ni duplique nada.
func (s *Service) securityHold(ctx context.Context, w *Withdrawal) (time.Duration, bool) {
	if s.Security == nil {
		return 0, false
	}
	if err := s.Security.PreAuthorize(ctx, security.OpWithdrawal, w.UserID, ""); err != nil {
		log.Printf("withdrawal: id=%s RETENIDO por controles de seguridad (se reevalúa en %s): %v", w.ID, securityHoldDelay, err)
		return securityHoldDelay, true
	}
	return 0, false
}

// finishLimit confirma (commit=true) o libera la reserva de límites del retiro.
// Es idempotente: si ya estaba confirmada/liberada no hace nada.
func (s *Service) finishLimit(ctx context.Context, w *Withdrawal, commit bool) {
	if s.Security == nil || s.Security.Limits == nil {
		return
	}
	store, ok := s.Store.(LimitReservationStore)
	if !ok {
		return
	}
	ctx = context.WithoutCancel(ctx)
	id, found, err := store.GetLimitReservation(ctx, w.ID)
	if err != nil {
		log.Printf("withdrawal: id=%s no se pudo leer la reserva de límites: %v", w.ID, err)
		return
	}
	if !found {
		return
	}
	if commit {
		err = s.Security.Commit(ctx, id)
	} else {
		err = s.Security.Release(ctx, id)
	}
	if err != nil && !errors.Is(err, security.ErrReservationNotActive) {
		log.Printf("withdrawal: id=%s no se pudo %s la reserva de límites %s: %v", w.ID, map[bool]string{true: "confirmar", false: "liberar"}[commit], id, err)
	}
}

// recordDestinationUse convierte la dirección en "known" tras un retiro completado.
func (s *Service) recordDestinationUse(ctx context.Context, w *Withdrawal) {
	if s.Security == nil || s.Security.Destination == nil {
		return
	}
	if err := s.Security.Destination.RecordSuccessfulUse(context.WithoutCancel(ctx), w.UserID, w.AssetCode, string(w.Network), w.ToAddress); err != nil {
		log.Printf("withdrawal: id=%s no se pudo registrar el uso del destino: %v", w.ID, err)
	}
}
