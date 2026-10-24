package security

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/domain"
)

// RoleResolver is the exact shape of postgres.RoleRepository.GetRolesForUser
// (and of the callback already used by middleware.RequireRole) — defined
// here structurally so Guard can reuse whatever role source main.go
// already wires up, without importing postgres or creating a second role
// system (spec §3: "no crear privilegios administrativos implícitos").
type RoleResolver interface {
	GetRolesForUser(ctx context.Context, userID uuid.UUID) ([]string, error)
}

// UserStatusResolver is the exact shape of domain.UserRepository.FindByID
// — any domain.UserRepository already satisfies this.
type UserStatusResolver interface {
	FindByID(ctx context.Context, id uuid.UUID) (*domain.User, error)
}

// Guard centralizes the authorization decisions spec §10 asks for:
// authentication is assumed already done (middleware.AuthRequired), this
// only adds authorization, ownership, role and account-status checks on
// top, plus IDOR_ATTEMPT / AUTHORIZATION_DENIED events.
type Guard struct {
	roles  RoleResolver
	users  UserStatusResolver
	events *EventLogger
}

func NewGuard(roles RoleResolver, users UserStatusResolver, events *EventLogger) *Guard {
	return &Guard{roles: roles, users: users, events: events}
}

// RequireActiveAccount fails unless the user exists and is
// domain.UserStatusActive — sensitive operations (spec §6/§7/§10) must
// never proceed for a blocked, deleted or still-pending-verification
// account, even if their JWT is still technically valid (e.g. blocked
// mid-session).
func (g *Guard) RequireActiveAccount(ctx context.Context, userID uuid.UUID) error {
	user, err := g.users.FindByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("security: verificar estado de cuenta: %w", err)
	}
	if user == nil {
		return domain.ErrUserNotFound
	}
	if user.Status != domain.UserStatusActive {
		return fmt.Errorf("%w: cuenta en estado %q", domain.ErrAccountLocked, user.Status)
	}
	return nil
}

// RequireRole fails unless userID has at least one of allowed roles,
// logging AUTHORIZATION_DENIED on rejection. Mirrors
// middleware.RequireRole's logic exactly (same allow-if-any-match
// semantics) so HTTP-layer and service-layer checks never disagree.
func (g *Guard) RequireRole(ctx context.Context, userID uuid.UUID, ip string, allowed ...string) error {
	roles, err := g.roles.GetRolesForUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("security: no se pudo verificar rol: %w", err)
	}
	for _, r := range roles {
		for _, a := range allowed {
			if r == a {
				return nil
			}
		}
	}
	g.logDenied(ctx, userID, ip, "", nil, fmt.Sprintf("rol insuficiente (requiere uno de %v)", allowed))
	return ErrAuthorizationDenied
}

// CheckOwnership fails with ErrAuthorizationDenied (and logs
// IDOR_ATTEMPT) when requestingUserID does not own the resource — spec
// §10's exact example ("un usuario A nunca debe poder leer/modificar/...
// de B"). This is a defense-in-depth belt for call sites that build their
// own SQL ownership filter (like transfer.FindByIDForUser already does);
// it does not replace filtering ownership at the query level, which
// remains the primary defense.
func (g *Guard) CheckOwnership(ctx context.Context, requestingUserID, resourceOwnerID uuid.UUID, resourceType string, resourceID *uuid.UUID, ip string) error {
	if requestingUserID == resourceOwnerID {
		return nil
	}
	if g.events != nil {
		uid := requestingUserID
		_ = g.events.Log(ctx, Event{
			Type:         EventIDORAttempt,
			Severity:     "critical",
			UserID:       &uid,
			ResourceType: resourceType,
			ResourceID:   resourceID,
			IP:           ip,
			Metadata:     map[string]any{"attempted_owner": resourceOwnerID.String()},
		})
	}
	return ErrAuthorizationDenied
}

func (g *Guard) logDenied(ctx context.Context, userID uuid.UUID, ip, resourceType string, resourceID *uuid.UUID, reason string) {
	if g.events == nil {
		return
	}
	uid := userID
	_ = g.events.Log(ctx, Event{
		Type:         EventAuthorizationDenied,
		Severity:     "warning",
		UserID:       &uid,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		IP:           ip,
		Metadata:     map[string]any{"reason": reason},
	})
}
