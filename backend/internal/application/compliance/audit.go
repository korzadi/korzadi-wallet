package compliance

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
)

// piiKeys/secretKeys: metadata keys removed (not masked) before anything is
// written to audit_logs (spec §16/§23). Second line of defense.
var forbiddenAuditKeys = map[string]bool{
	"password": true, "password_hash": true, "token": true, "access_token": true, "refresh_token": true,
	"jwt": true, "secret": true, "private_key": true, "privatekey": true, "seed": true, "mnemonic": true,
	"api_secret": true, "webhook_secret": true, "signature": true,
	"document_number": true, "documentnumber": true, "id_number": true, "national_id": true,
	"document": true, "image": true, "selfie": true, "face": true, "biometric": true, "biometrics": true,
	"full_name": true, "date_of_birth": true, "dob": true, "address": true, "raw_payload": true, "payload": true,
}

const maxAuditString = 500

func sanitizeMeta(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		if forbiddenAuditKeys[strings.ToLower(k)] {
			continue
		}
		if s, ok := v.(string); ok && len(s) > maxAuditString {
			v = s[:maxAuditString]
		}
		out[k] = v
	}
	return out
}

// auditEvent is the minimum record of spec §23:
// actor, action, resource, user_id, old_state, new_state, reason, request_id, created_at.
type auditEvent struct {
	Actor      Actor
	Action     string
	Resource   string
	ResourceID *uuid.UUID
	UserID     uuid.UUID
	OldState   string
	NewState   string
	Reason     string
	Extra      map[string]any
}

// audit is best-effort AFTER commit (same convention as security.EventLogger
// and every other domain.AuditLogger caller). The authoritative, transactional
// trail for KYC is kyc_status_history / risk_level_history / hold and flag rows.
func (s *Service) audit(ctx context.Context, e auditEvent) {
	if s.auditLog == nil {
		return
	}
	meta := map[string]any{
		"action":     e.Action,
		"resource":   e.Resource,
		"user_id":    e.UserID.String(),
		"old_state":  e.OldState,
		"new_state":  e.NewState,
		"reason":     e.Reason,
		"request_id": e.Actor.RequestID,
		"actor_type": string(e.Actor.Type),
		"created_at": s.now().UTC().Format(time.RFC3339),
	}
	if e.Actor.Ref != "" {
		meta["actor_ref"] = e.Actor.Ref
	}
	for k, v := range e.Extra {
		meta[k] = v
	}
	meta = sanitizeMeta(meta)
	role := strings.Join(e.Actor.Roles, ",")
	if role == "" {
		role = strings.ToLower(string(e.Actor.Type))
	}
	if err := s.auditLog.Log(ctx, e.Actor.UserID, role, e.Action, e.Resource, e.ResourceID, meta, e.Actor.IP); err != nil {
		// Never log the payload — only that it failed.
		log.Printf("compliance: fallo al escribir auditoría action=%s", e.Action)
	}
}
