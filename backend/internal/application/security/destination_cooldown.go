package security

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ErrDestinationCooldown: la dirección de destino es nueva para este usuario y
// todavía está en el período de espera de seguridad. Es el control clásico
// contra el vaciado de cuentas secuestradas: dar tiempo (y un aviso al
// titular) para detectar y bloquear antes de que salgan los fondos.
var ErrDestinationCooldown = errors.New("security: dirección de destino nueva en período de espera")

// DestinationCooldownError lleva cuándo se habilita la dirección.
type DestinationCooldownError struct {
	AvailableAt time.Time
	// FirstRequest es true cuando ESTA llamada fue la que registró la dirección
	// (el primer intento): es el momento de avisar al titular.
	FirstRequest bool
}

func (e *DestinationCooldownError) Error() string {
	return fmt.Sprintf("%s (disponible a partir de %s)", ErrDestinationCooldown.Error(), e.AvailableAt.UTC().Format(time.RFC3339))
}

func (e *DestinationCooldownError) Is(target error) bool { return target == ErrDestinationCooldown }

// DestinationCooldownRepository es opcional: un repositorio que no lo
// implemente simplemente no aplica espera (comportamiento anterior).
type DestinationCooldownRepository interface {
	// RegisterPending registra la dirección como 'pending' si todavía no tiene
	// fila (nunca degrada una existente) y devuelve cuándo se vio por primera vez.
	RegisterPending(ctx context.Context, userID uuid.UUID, assetCode, network, hash string) (firstSeen time.Time, created bool, err error)
}

// CheckWithCooldown aplica Check y, si la dirección es NUEVA, la registra y
// exige que pase `cooldown` desde la primera vez que se vio. cooldown <= 0
// desactiva la espera. Una dirección 'known' o 'allowlisted' nunca espera, y
// una 'blocked' siempre se rechaza (ErrDestinationBlocked).
func (g *DestinationGuard) CheckWithCooldown(ctx context.Context, userID uuid.UUID, assetCode, network, rawDestination, ip string,
	cooldown time.Duration) (DestinationStatus, error) {

	status, err := g.Check(ctx, userID, assetCode, network, rawDestination, ip)
	if err != nil {
		return status, err
	}
	if status != DestinationNew || cooldown <= 0 {
		return status, nil
	}
	repo, ok := g.repo.(DestinationCooldownRepository)
	if !ok {
		return status, nil
	}
	firstSeen, created, err := repo.RegisterPending(ctx, userID, assetCode, network, HashDestination(rawDestination))
	if err != nil {
		return status, fmt.Errorf("security: registrar destino pendiente: %w", err)
	}
	availableAt := firstSeen.Add(cooldown)
	if time.Now().Before(availableAt) {
		return status, &DestinationCooldownError{AvailableAt: availableAt, FirstRequest: created}
	}
	return status, nil
}

// SetStatus permite a un administrador marcar un destino como allowlisted,
// blocked o known (p. ej. aprobar antes de tiempo una dirección en espera).
func (g *DestinationGuard) SetStatus(ctx context.Context, userID uuid.UUID, assetCode, network, rawDestination string, status DestinationStatus) error {
	switch status {
	case DestinationAllowlisted, DestinationBlocked, DestinationKnown:
	default:
		return fmt.Errorf("security: estado de destino no permitido: %q", status)
	}
	return g.repo.SetStatus(ctx, userID, assetCode, network, HashDestination(rawDestination), status)
}
