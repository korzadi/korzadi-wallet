package security

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/google/uuid"
)

// DestinationStatus is spec §15's minimal, non-KYC vocabulary.
type DestinationStatus string

const (
	DestinationNew         DestinationStatus = "new"
	DestinationKnown       DestinationStatus = "known"
	DestinationBlocked     DestinationStatus = "blocked"
	DestinationAllowlisted DestinationStatus = "allowlisted"
)

// DestinationRepository persists known_destinations. It never stores the
// destination address in plaintext outside of what the caller already
// keeps elsewhere (e.g. the withdrawal row itself) — only its hash, used
// purely to recognize "have we seen this exact destination for this user
// before", never to reconstruct the address.
type DestinationRepository interface {
	// Lookup returns the current status for (userID, assetCode, network,
	// hash), or DestinationNew if no row exists yet.
	Lookup(ctx context.Context, userID uuid.UUID, assetCode, network, hash string) (DestinationStatus, error)
	// RecordUse upserts the destination as "known" (if it was "new") and
	// bumps its use counter — called after a withdrawal to that
	// destination actually completes, not merely attempted.
	RecordUse(ctx context.Context, userID uuid.UUID, assetCode, network, hash string) error
	// SetStatus lets an admin explicitly block or allowlist a destination.
	SetStatus(ctx context.Context, userID uuid.UUID, assetCode, network, hash string, status DestinationStatus) error
}

// HashDestination normalizes and hashes a raw destination string (address,
// or any stable identifier of "where the money goes") so it can be
// compared and stored without ever persisting or logging the address
// itself in security_events.
func HashDestination(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

// DestinationGuard applies spec §15's policy: a blocked destination is
// always rejected; a brand-new destination is flagged (caller typically
// wires this into StepUpGuard); allowlisted/known proceed normally.
type DestinationGuard struct {
	repo   DestinationRepository
	events *EventLogger
}

func NewDestinationGuard(repo DestinationRepository, events *EventLogger) *DestinationGuard {
	return &DestinationGuard{repo: repo, events: events}
}

// Check returns the destination's status and, for a "new" destination,
// logs NEW_DESTINATION so it shows up in security_events even if the
// caller doesn't end up requiring step-up for it.
func (g *DestinationGuard) Check(ctx context.Context, userID uuid.UUID, assetCode, network, rawDestination, ip string) (DestinationStatus, error) {
	hash := HashDestination(rawDestination)
	status, err := g.repo.Lookup(ctx, userID, assetCode, network, hash)
	if err != nil {
		return "", err
	}
	if status == DestinationBlocked {
		return status, ErrDestinationBlocked
	}
	if status == DestinationNew && g.events != nil {
		uid := userID
		_ = g.events.Log(ctx, Event{
			Type:      EventNewDestination,
			Severity:  "info",
			UserID:    &uid,
			Operation: OpWithdrawal,
			IP:        ip,
			Metadata:  map[string]any{"asset_code": assetCode, "network": network},
		})
	}
	return status, nil
}

// RecordSuccessfulUse should be called once a withdrawal to this
// destination actually completes — turning a "new" destination "known"
// for next time.
func (g *DestinationGuard) RecordSuccessfulUse(ctx context.Context, userID uuid.UUID, assetCode, network, rawDestination string) error {
	return g.repo.RecordUse(ctx, userID, assetCode, network, HashDestination(rawDestination))
}
