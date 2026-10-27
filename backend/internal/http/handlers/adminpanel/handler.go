package adminpanel

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	appadmin "korzadi-wallet/backend/internal/application/admin"
	appp2p "korzadi-wallet/backend/internal/application/p2p"
	appsecurity "korzadi-wallet/backend/internal/application/security"
	apptwofactor "korzadi-wallet/backend/internal/application/twofactor"
	"korzadi-wallet/backend/internal/http/handlers"
)

const (
	cookieName = "kz_admin_session"
	ctxAccount = "admin_account"
	ctxSession = "admin_session"
)

type Handler struct {
	svc       *appadmin.Service
	adminOps  *handlers.AdminOpsHandler
	security  *appsecurity.Service
	p2p       *appp2p.Service
	twofactor *apptwofactor.Service
	secure    bool
	limiter   *ipLimiter
}

func New(svc *appadmin.Service, adminOps *handlers.AdminOpsHandler, security *appsecurity.Service,
	p2p *appp2p.Service, twofactor *apptwofactor.Service, secureCookie bool) *Handler {
	return &Handler{svc: svc, adminOps: adminOps, security: security, p2p: p2p,
		twofactor: twofactor, secure: secureCookie, limiter: newIPLimiter()}
}

func (h *Handler) Register(r *gin.Engine) {
	g := r.Group("/api/admin")
	g.Use(h.securityHeaders)
	g.POST("/auth/login", h.rateLimit(10), h.login)
	g.POST("/auth/2fa", h.rateLimit(20), h.login2FA)
	g.POST("/auth/activate", h.rateLimit(10), h.activateStart)
	g.POST("/auth/activate/confirm", h.rateLimit(20), h.activateConfirm)

	auth := g.Group("")
	auth.Use(h.Require)
	auth.GET("/auth/me", h.me)
	auth.POST("/auth/logout", h.logout)
	auth.POST("/auth/password", h.RequireStepUp, h.changePassword)

	p := g.Group("")
	p.Use(h.Require)

	p.GET("/dashboard", h.dashboard)
	p.GET("/users", h.listUsers)
	p.POST("/users/:id/block", h.requireUsers, h.sensitive, h.blockUser)
	p.POST("/users/:id/unblock", h.requireUsers, h.sensitive, h.unblockUser)
	p.POST("/users/:id/verify-email", h.requireUsers, h.sensitive, h.verifyEmail)
	p.POST("/users/:id/reset-2fa", h.requireUsers, h.sensitive, h.resetUser2FA)
	p.POST("/users/:id/roles", h.requireSuper, h.sensitive, h.assignRole)
	p.GET("/transactions", h.listTransactions)
	p.GET("/audit", h.listAudit)

	p.GET("/administrators", h.listAdministrators)
	p.POST("/administrators/invite", h.requireSuper, h.sensitive, h.inviteAdmin)
	p.POST("/administrators/:id/status", h.requireSuper, h.sensitive, h.setAdminStatus)
	p.GET("/sessions", h.listSessions)
	p.DELETE("/sessions/:id", h.sensitive, h.revokeSession)

	if h.adminOps != nil {
		fin := p.Group("")
		fin.Use(h.requireFinancial)
		fin.GET("/withdrawals/review", h.adminOps.ListWithdrawalReviews)
		fin.POST("/withdrawals/:id/resolve", h.sensitive, h.adminOps.ResolveWithdrawal)
		fin.GET("/custody/wallets", h.adminOps.ListWallets)
		fin.GET("/hot-wallet/balances", h.adminOps.HotWalletBalances)
		fin.POST("/custody/wallets", h.sensitive, h.adminOps.RegisterWallet)
		fin.POST("/custody/wallets/:id/status", h.sensitive, h.adminOps.SetWalletStatus)
		fin.GET("/treasury/policies", h.adminOps.ListTreasuryPolicies)
		fin.POST("/treasury/policies", h.sensitive, h.adminOps.CreateTreasuryPolicy)
		fin.POST("/treasury/policies/:id/enabled", h.sensitive, h.adminOps.SetTreasuryPolicyEnabled)
		fin.GET("/gas-station/policies/:wallet_id", h.adminOps.GetGasPolicy)
		fin.PUT("/gas-station/policies/:wallet_id", h.sensitive, h.adminOps.PutGasPolicy)
	}

	if h.p2p != nil {
		pg := p.Group("/p2p")
		pg.Use(h.requireP2P)
		pg.GET("/orders", h.p2pOrders)
		pg.GET("/orders/:id", h.p2pOrder)
		pg.POST("/orders/:id/resolve", h.sensitive, h.p2pResolve)
		pg.GET("/disputes", h.p2pDisputes)
		pg.GET("/offers", h.p2pOffers)
		pg.POST("/offers/:id/suspend", h.sensitive, h.p2pSuspend)
	}

	if h.security != nil {
		sg := p.Group("/security")
		sg.GET("/freezes", h.securityFreezes)
		sg.GET("/policies", h.securityPolicies)
		sg.GET("/events", h.securityEvents)
		sg.POST("/freezes", h.requireSuper, h.sensitive, h.setFreeze)
		sg.POST("/policies", h.requireSuper, h.sensitive, h.upsertPolicy)
	}
}

func (h *Handler) securityHeaders(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Next()
}

func (h *Handler) Require(c *gin.Context) {
	raw, err := c.Cookie(cookieName)
	if err != nil || raw == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "not_authenticated"})
		return
	}
	a, sess, err := h.svc.ValidateSession(c.Request.Context(), raw)
	if err != nil {
		h.clearCookie(c)
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "session_expired"})
		return
	}
	c.Set(ctxAccount, a)
	c.Set(ctxSession, sess)
	c.Set("user_id", a.ID.String())
	c.Set("admin_role", string(a.Role))
	c.Next()
}

func (h *Handler) RequireStepUp(c *gin.Context) {
	code := strings.TrimSpace(c.GetHeader("X-2FA-Code"))
	if code == "" {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "2fa_required", "message": "se requiere un código 2FA vigente"})
		return
	}
	if !h.svc.VerifyStepUp(c.Request.Context(), h.account(c), code) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "invalid_2fa_code", "message": "código 2FA inválido"})
		return
	}
	c.Next()
}

func (h *Handler) sensitive(c *gin.Context) { h.RequireStepUp(c) }

func validReason(reason string) bool { return len([]rune(strings.TrimSpace(reason))) >= 5 }

func (h *Handler) account(c *gin.Context) *appadmin.Account {
	v, _ := c.Get(ctxAccount)
	a, _ := v.(*appadmin.Account)
	return a
}

func (h *Handler) session(c *gin.Context) *appadmin.Session {
	v, _ := c.Get(ctxSession)
	s, _ := v.(*appadmin.Session)
	return s
}

func (h *Handler) requireRoles(c *gin.Context, roles ...appadmin.Role) {
	a := h.account(c)
	if a == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "not_authenticated"})
		return
	}
	for _, r := range roles {
		if a.Role == r {
			c.Next()
			return
		}
	}
	c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
}

func (h *Handler) requireUsers(c *gin.Context) {
	if a := h.account(c); a != nil && a.Role.CanUsers() {
		c.Next()
		return
	}
	c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
}
func (h *Handler) requireFinancial(c *gin.Context) {
	if a := h.account(c); a != nil && a.Role.CanFinancial() {
		c.Next()
		return
	}
	c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
}
func (h *Handler) requireP2P(c *gin.Context) {
	if a := h.account(c); a != nil && a.Role.CanP2P() {
		c.Next()
		return
	}
	c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
}
func (h *Handler) requireSuper(c *gin.Context) {
	if a := h.account(c); a != nil && a.Role.CanManageAdmins() {
		c.Next()
		return
	}
	c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
}

func (h *Handler) audit(c *gin.Context, action, entityType string, entityID *uuid.UUID, reason, result string, meta map[string]any) {
	h.svc.Audit(c.Request.Context(), h.account(c), action, entityType, entityID, reason, result, c.ClientIP(), meta)
}

func (h *Handler) fail(c *gin.Context, err error, entityType string, entityID *uuid.UUID, reason string) {
	switch {
	case errors.Is(err, appadmin.ErrInvalidCredentials), errors.Is(err, appadmin.ErrChallenge), errors.Is(err, appadmin.ErrInvalidCode), errors.Is(err, appadmin.ErrActivation):
		h.audit(c, "admin.auth_failed", entityType, entityID, reason, "failure", nil)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_credentials"})
	case errors.Is(err, appadmin.ErrLocked):
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "locked", "message": "demasiados intentos; espera 30 segundos"})
	case errors.Is(err, appadmin.ErrInactive):
		c.JSON(http.StatusForbidden, gin.H{"error": "account_inactive"})
	case errors.Is(err, appadmin.ErrConflict):
		c.JSON(http.StatusConflict, gin.H{"error": "conflict", "message": err.Error()})
	case errors.Is(err, appadmin.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error"})
	}
}

func (h *Handler) setCookie(c *gin.Context, token string, expiry time.Time) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name: cookieName, Value: token, Path: "/api/admin", HttpOnly: true,
		Secure: h.secure, SameSite: http.SameSiteStrictMode, Expires: expiry, MaxAge: int(time.Until(expiry).Seconds()),
	})
}
func (h *Handler) clearCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name: cookieName, Value: "", Path: "/api/admin", HttpOnly: true,
		Secure: h.secure, SameSite: http.SameSiteStrictMode, MaxAge: -1, Expires: time.Unix(0, 0),
	})
}

func pageParams(c *gin.Context) (int, int) {
	limit, offset := 50, 0
	if v, err := strconv.Atoi(c.Query("limit")); err == nil && v > 0 && v <= 200 {
		limit = v
	}
	if v, err := strconv.Atoi(c.Query("offset")); err == nil && v >= 0 {
		offset = v
	}
	return limit, offset
}

type ipLimiter struct {
	mu   sync.Mutex
	seen map[string][]time.Time
}

func newIPLimiter() *ipLimiter { return &ipLimiter{seen: map[string][]time.Time{}} }
func (l *ipLimiter) allow(key string, max int, window time.Duration) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	cut := now.Add(-window)
	items := l.seen[key][:0]
	for _, t := range l.seen[key] {
		if t.After(cut) {
			items = append(items, t)
		}
	}
	if len(items) >= max {
		l.seen[key] = items
		return false
	}
	l.seen[key] = append(items, now)
	return true
}

func (h *Handler) rateLimit(max int) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !h.limiter.allow(c.ClientIP(), max, time.Minute) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "rate_limited"})
			return
		}
		c.Next()
	}
}
