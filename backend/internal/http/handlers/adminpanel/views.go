package adminpanel

import (
	"time"

	appadmin "korzadi-wallet/backend/internal/application/admin"
)

func accountView(a *appadmin.Account) map[string]any {
	if a == nil {
		return nil
	}
	return map[string]any{
		"id": a.ID, "email": a.Email, "display_name": a.DisplayName,
		"role": a.Role, "role_label": a.Role.Label(), "status": a.Status,
		"totp_confirmed_at": a.TOTPConfirmedAt, "last_login_at": a.LastLoginAt, "created_at": a.CreatedAt,
	}
}

func sessionView(s *appadmin.Session) map[string]any {
	if s == nil {
		return nil
	}
	return map[string]any{"id": s.ID, "created_at": s.CreatedAt, "last_seen_at": s.LastSeenAt,
		"expires_at": s.ExpiresAt, "ip": s.IP, "user_agent": s.UserAgent}
}

func sessionsView(in []appadmin.Session) []map[string]any {
	out := make([]map[string]any, 0, len(in))
	for i := range in {
		out = append(out, sessionView(&in[i]))
	}
	return out
}

func permissions(r appadmin.Role) map[string]bool {
	return map[string]bool{
		"view":             true,
		"write":            r.CanWrite(),
		"financial":        r.CanFinancial(),
		"p2p":              r.CanP2P(),
		"users":            r.CanUsers(),
		"manage_admins":    r.CanManageAdmins(),
		"security_freeze":  r.CanManageAdmins(),
	}
}

func boolPtr(v bool) *bool { return &v }
func nowPtr() *time.Time  { t := time.Now().UTC(); return &t }
