package admin

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

type auditLogger struct {
	repo Repository
}

func NewAuditLogger(repo Repository) *auditLogger { return &auditLogger{repo: repo} }

func (l *auditLogger) Log(ctx context.Context, actor *uuid.UUID, actorRole, action, entityType string,
	entityID *uuid.UUID, metadata map[string]any, ip string) error {
	e := AuditEntry{ID: uuid.New(), Action: action, EntityType: entityType, EntityID: entityID, IP: ip,
		Metadata: metadata, Result: "success"}
	if strings.Contains(action, "failed") {
		e.Result = "failure"
	}
	if metadata != nil {
		if v, ok := metadata["error"]; ok && v != nil && fmt.Sprint(v) != "" {
			e.Result = "failure"
		}
		if v, ok := metadata["reason"]; ok {
			e.Reason = fmt.Sprint(v)
		}
	}
	if actor != nil {
		id := *actor
		e.AdminID = &id
		if a, err := l.repo.FindByID(ctx, id); err == nil {
			e.AdminEmail = a.Email
		}
	}
	return l.repo.Audit(ctx, e)
}
