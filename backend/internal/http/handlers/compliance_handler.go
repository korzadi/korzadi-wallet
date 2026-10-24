package handlers

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"korzadi-wallet/backend/internal/application/compliance"
)

// ComplianceHandler exposes Bloque 9's KYC/AML endpoints. Authorization is
// NEVER derived from the request: the acting user comes from the JWT and the
// roles are re-read from the database on every admin call.
type ComplianceHandler struct {
	svc      *compliance.Service
	getRoles func(userID string) ([]string, error)
}

func NewComplianceHandler(svc *compliance.Service, getRoles func(userID string) ([]string, error)) *ComplianceHandler {
	return &ComplianceHandler{svc: svc, getRoles: getRoles}
}

const maxWebhookBody = 1 << 20 // 1 MiB

func requestID(c *gin.Context) string {
	id := strings.TrimSpace(c.GetHeader("X-Request-ID"))
	if id == "" || len(id) > 100 {
		id = uuid.NewString()
	}
	c.Header("X-Request-ID", id)
	return id
}

// errorStatus maps compliance errors to a coarse HTTP status + stable code.
// Messages never include provider details, PII, or internal state.
func complianceError(c *gin.Context, err error) {
	status, code := http.StatusInternalServerError, "internal_error"
	switch {
	case errors.Is(err, compliance.ErrForbidden), errors.Is(err, compliance.ErrSelfAction):
		status, code = http.StatusForbidden, "forbidden"
	case errors.Is(err, compliance.ErrCaseNotFound), errors.Is(err, compliance.ErrFlagNotFound),
		errors.Is(err, compliance.ErrHoldNotFound), errors.Is(err, compliance.ErrScreeningNotFound),
		errors.Is(err, compliance.ErrUnknownProvider):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, compliance.ErrInvalidTransition), errors.Is(err, compliance.ErrConcurrentUpdate),
		errors.Is(err, compliance.ErrFlagNotOpen), errors.Is(err, compliance.ErrHoldAlreadyReleased),
		errors.Is(err, compliance.ErrScreeningReviewed), errors.Is(err, compliance.ErrRenewalNotAllowed),
		errors.Is(err, compliance.ErrIdempotencyKeyReuse), errors.Is(err, compliance.ErrRiskDowngradeManualOnly):
		status, code = http.StatusConflict, "conflict"
	case errors.Is(err, compliance.ErrReasonRequired), errors.Is(err, compliance.ErrInvalidArgument),
		errors.Is(err, compliance.ErrIdempotencyKeyRequired), errors.Is(err, compliance.ErrWebhookMalformed),
		errors.Is(err, compliance.ErrWebhookReplay):
		status, code = http.StatusBadRequest, "invalid_request"
	case errors.Is(err, compliance.ErrWebhookSignature):
		status, code = http.StatusUnauthorized, "invalid_signature"
	case errors.Is(err, compliance.ErrKYCDisabled), errors.Is(err, compliance.ErrAMLDisabled),
		errors.Is(err, compliance.ErrScreeningUnavailable), errors.Is(err, compliance.ErrProviderUnavailable):
		status, code = http.StatusServiceUnavailable, "unavailable"
	}
	c.JSON(status, gin.H{"error": code})
}

func (h *ComplianceHandler) unavailable(c *gin.Context) bool {
	if h == nil || h.svc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "unavailable"})
		return true
	}
	return false
}

func (h *ComplianceHandler) actor(c *gin.Context, rid string) (compliance.Actor, bool) {
	uid := currentUserID(c)
	roles, err := h.getRoles(uid.String())
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return compliance.Actor{}, false
	}
	return compliance.Actor{Type: compliance.ActorAdmin, UserID: &uid, Roles: roles, IP: c.ClientIP(), RequestID: rid}, true
}

func parseUUIDParam(c *gin.Context, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param(name))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return uuid.Nil, false
	}
	return id, true
}

// ---------------- user ----------------

type startKYCRequest struct {
	Country          string `json:"country"`
	ResidenceCountry string `json:"residence_country"`
}

// POST /api/kyc/start — acts ONLY on the authenticated user.
func (h *ComplianceHandler) Start(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	var req startKYCRequest
	_ = c.ShouldBindJSON(&req) // body optional
	rid := requestID(c)
	uid := currentUserID(c)
	res, err := h.svc.StartVerification(c.Request.Context(), compliance.StartInput{
		UserID: uid, IdempotencyKey: c.GetHeader("Idempotency-Key"),
		Country: req.Country, ResidenceCountry: req.ResidenceCountry,
		Actor: compliance.Actor{Type: compliance.ActorUser, UserID: &uid, IP: c.ClientIP(), RequestID: rid},
	})
	if err != nil {
		complianceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": res.Case.Status, "case_id": res.Case.ID, "redirect_url": res.RedirectURL, "replayed": res.Replayed})
}

// GET /api/kyc/status — own status only; no user id accepted anywhere.
func (h *ComplianceHandler) Status(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	v, err := h.svc.GetStatus(c.Request.Context(), currentUserID(c))
	if err != nil {
		complianceError(c, err)
		return
	}
	c.JSON(http.StatusOK, v)
}

// ---------------- webhook ----------------

// POST /api/kyc/webhook/:provider — unauthenticated by JWT on purpose; it is
// authenticated by the provider signature. The body is read raw (bounded)
// because the signature covers the exact bytes.
func (h *ComplianceHandler) Webhook(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxWebhookBody+1))
	if err != nil || len(body) > maxWebhookBody {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	headers := make(map[string]string, len(c.Request.Header))
	for k, v := range c.Request.Header {
		if len(v) > 0 {
			headers[strings.ToLower(k)] = v[0]
		}
	}
	res, err := h.svc.ProcessWebhook(c.Request.Context(), c.Param("provider"), headers, body)
	if err != nil {
		complianceError(c, err)
		return
	}
	// Duplicates and ignored events are still 200: the provider must stop retrying.
	c.JSON(http.StatusOK, gin.H{"received": true, "duplicate": res.Duplicate})
}

// ---------------- admin: KYC ----------------

type caseDTO struct {
	ID               uuid.UUID `json:"id"`
	Status           string    `json:"status"`
	Provider         string    `json:"provider,omitempty"`
	Country          string    `json:"country,omitempty"`
	ResidenceCountry string    `json:"residence_country,omitempty"`
	DocumentType     string    `json:"document_type,omitempty"`
	DocumentCountry  string    `json:"document_country,omitempty"`
	SubmittedAt      any       `json:"submitted_at,omitempty"`
	VerifiedAt       any       `json:"verified_at,omitempty"`
	RejectedAt       any       `json:"rejected_at,omitempty"`
	ExpiresAt        any       `json:"expires_at,omitempty"`
	RejectionReason  string    `json:"rejection_reason_code,omitempty"`
	ReviewReason     string    `json:"review_reason_code,omitempty"`
	RiskLevel        string    `json:"risk_level"`
	Superseded       bool      `json:"superseded"`
	Version          int       `json:"version"`
}

func toCaseDTO(k *compliance.KYCCase) caseDTO {
	d := caseDTO{ID: k.ID, Status: string(k.Status), Provider: k.Provider, Country: k.Country, ResidenceCountry: k.ResidenceCountry,
		DocumentType: k.DocumentType, DocumentCountry: k.DocumentCountry, RejectionReason: k.RejectionReasonCode,
		ReviewReason: k.ReviewReasonCode, RiskLevel: string(k.RiskLevel), Superseded: k.SupersededAt != nil, Version: k.Version}
	d.SubmittedAt, d.VerifiedAt, d.RejectedAt, d.ExpiresAt = k.SubmittedAt, k.VerifiedAt, k.RejectedAt, k.ExpiresAt
	return d // provider_reference and document_hash are intentionally NOT exposed
}

// GET /api/admin/kyc/:user_id
func (h *ComplianceHandler) AdminGet(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	uid, ok := parseUUIDParam(c, "user_id")
	if !ok {
		return
	}
	actor, ok := h.actor(c, requestID(c))
	if !ok {
		return
	}
	v, err := h.svc.AdminGet(c.Request.Context(), actor, uid)
	if err != nil {
		complianceError(c, err)
		return
	}
	cases := make([]caseDTO, 0, len(v.Cases))
	for _, k := range v.Cases {
		cases = append(cases, toCaseDTO(k))
	}
	hist := make([]gin.H, 0, len(v.History))
	for _, e := range v.History {
		hist = append(hist, gin.H{"case_id": e.CaseID, "old_status": e.OldStatus, "new_status": e.NewStatus, "actor_type": e.ActorType,
			"actor_user_id": e.ActorUserID, "reason_code": e.ReasonCode, "reason": e.Reason, "request_id": e.RequestID, "created_at": e.CreatedAt})
	}
	holds := make([]gin.H, 0, len(v.ActiveHolds))
	for _, x := range v.ActiveHolds {
		holds = append(holds, gin.H{"id": x.ID, "type": x.Type, "reason_code": x.ReasonCode, "placed_at": x.PlacedAt})
	}
	flags := make([]gin.H, 0, len(v.OpenFlags))
	for _, f := range v.OpenFlags {
		flags = append(flags, gin.H{"id": f.ID, "type": f.Type, "severity": f.Severity, "source": f.Source, "reason_code": f.ReasonCode, "created_at": f.CreatedAt})
	}
	risk := "LOW"
	if v.Risk != nil {
		risk = string(v.Risk.Level)
	}
	c.JSON(http.StatusOK, gin.H{"user_id": v.UserID, "effective_status": v.EffectiveStatus, "risk_level": risk,
		"cases": cases, "history": hist, "active_holds": holds, "open_flags": flags})
}

type transitionRequest struct {
	CaseID     *uuid.UUID `json:"case_id"`
	ReasonCode string     `json:"reason_code"`
	Reason     string     `json:"reason"`
}

func (h *ComplianceHandler) transition(c *gin.Context, to compliance.KYCStatus) {
	if h.unavailable(c) {
		return
	}
	uid, ok := parseUUIDParam(c, "user_id")
	if !ok {
		return
	}
	var req transitionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	actor, ok := h.actor(c, requestID(c))
	if !ok {
		return
	}
	k, err := h.svc.Transition(c.Request.Context(), compliance.TransitionInput{Actor: actor, UserID: uid, CaseID: req.CaseID, To: to,
		ReasonCode: req.ReasonCode, Reason: req.Reason, IdempotencyKey: c.GetHeader("Idempotency-Key")})
	if err != nil {
		complianceError(c, err)
		return
	}
	c.JSON(http.StatusOK, toCaseDTO(k))
}

func (h *ComplianceHandler) AdminReview(c *gin.Context)  { h.transition(c, compliance.KYCInReview) }
func (h *ComplianceHandler) AdminApprove(c *gin.Context) { h.transition(c, compliance.KYCVerified) }
func (h *ComplianceHandler) AdminReject(c *gin.Context)  { h.transition(c, compliance.KYCRejected) }
func (h *ComplianceHandler) AdminSuspend(c *gin.Context) { h.transition(c, compliance.KYCSuspended) }

// ---------------- admin: AML ----------------

type flagRequest struct {
	Type        string `json:"type"`
	Severity    string `json:"severity"`
	ReasonCode  string `json:"reason_code"`
	Description string `json:"description"`
}

// POST /api/admin/kyc/:user_id/flags
func (h *ComplianceHandler) CreateFlag(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	uid, ok := parseUUIDParam(c, "user_id")
	if !ok {
		return
	}
	var req flagRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	actor, ok := h.actor(c, requestID(c))
	if !ok {
		return
	}
	f, err := h.svc.CreateManualFlag(c.Request.Context(), compliance.ManualFlagInput{Actor: actor, UserID: uid,
		Type: compliance.FlagType(req.Type), Severity: compliance.RiskLevel(req.Severity), ReasonCode: req.ReasonCode, Description: req.Description})
	if err != nil {
		complianceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": f.ID, "type": f.Type, "severity": f.Severity, "status": f.Status})
}

type resolveRequest struct {
	Dismiss bool   `json:"dismiss"`
	Reason  string `json:"reason"`
}

// POST /api/admin/compliance/flags/:id/resolve
func (h *ComplianceHandler) ResolveFlag(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	id, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	var req resolveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	actor, ok := h.actor(c, requestID(c))
	if !ok {
		return
	}
	f, err := h.svc.ResolveFlag(c.Request.Context(), actor, id, req.Dismiss, req.Reason, c.GetHeader("Idempotency-Key"))
	if err != nil {
		complianceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": f.ID, "status": f.Status})
}

type holdRequest struct {
	HoldType   string `json:"hold_type"`
	ReasonCode string `json:"reason_code"`
	Reason     string `json:"reason"`
}

// POST /api/admin/kyc/:user_id/holds
func (h *ComplianceHandler) PlaceHold(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	uid, ok := parseUUIDParam(c, "user_id")
	if !ok {
		return
	}
	var req holdRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	actor, ok := h.actor(c, requestID(c))
	if !ok {
		return
	}
	hd, err := h.svc.PlaceHold(c.Request.Context(), compliance.PlaceHoldInput{Actor: actor, UserID: uid, Type: compliance.HoldType(req.HoldType),
		ReasonCode: req.ReasonCode, Reason: req.Reason, IdempotencyKey: c.GetHeader("Idempotency-Key")})
	if err != nil {
		complianceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": hd.ID, "hold_type": hd.Type, "status": hd.Status})
}

// POST /api/admin/compliance/holds/:id/release
func (h *ComplianceHandler) ReleaseHold(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	id, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	var req resolveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	actor, ok := h.actor(c, requestID(c))
	if !ok {
		return
	}
	hd, err := h.svc.ReleaseHold(c.Request.Context(), actor, id, req.Reason, c.GetHeader("Idempotency-Key"))
	if err != nil {
		complianceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": hd.ID, "status": hd.Status})
}

type riskRequest struct {
	Level       string   `json:"level"`
	ReasonCodes []string `json:"reason_codes"`
	Reason      string   `json:"reason"`
}

// POST /api/admin/kyc/:user_id/risk
func (h *ComplianceHandler) SetRisk(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	uid, ok := parseUUIDParam(c, "user_id")
	if !ok {
		return
	}
	var req riskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	actor, ok := h.actor(c, requestID(c))
	if !ok {
		return
	}
	p, err := h.svc.SetRiskLevel(c.Request.Context(), compliance.SetRiskInput{Actor: actor, UserID: uid, Level: compliance.RiskLevel(req.Level),
		ReasonCodes: req.ReasonCodes, Reason: req.Reason, IdempotencyKey: c.GetHeader("Idempotency-Key")})
	if err != nil {
		complianceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"user_id": p.UserID, "risk_level": p.Level})
}

type screenRequest struct {
	Kind string `json:"kind"`
}

// POST /api/admin/kyc/:user_id/screenings
func (h *ComplianceHandler) RequestScreening(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	uid, ok := parseUUIDParam(c, "user_id")
	if !ok {
		return
	}
	var req screenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	actor, ok := h.actor(c, requestID(c))
	if !ok {
		return
	}
	v, err := h.svc.RequestScreening(c.Request.Context(), actor, uid, compliance.ScreeningKind(req.Kind), c.GetHeader("Idempotency-Key"))
	if err != nil {
		complianceError(c, err)
		return
	}
	out := gin.H{"request_id": v.Request.ID, "status": v.Request.Status}
	if v.Result != nil {
		out["result_id"], out["match_status"], out["review_status"] = v.Result.ID, v.Result.Match, v.Result.Review
	}
	c.JSON(http.StatusOK, out)
}

type reviewScreeningRequest struct {
	Confirmed bool   `json:"confirmed"`
	Reason    string `json:"reason"`
}

// POST /api/admin/compliance/screenings/:id/review
func (h *ComplianceHandler) ReviewScreening(c *gin.Context) {
	if h.unavailable(c) {
		return
	}
	id, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	var req reviewScreeningRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	actor, ok := h.actor(c, requestID(c))
	if !ok {
		return
	}
	r, err := h.svc.ReviewScreening(c.Request.Context(), actor, id, req.Confirmed, req.Reason, c.GetHeader("Idempotency-Key"))
	if err != nil {
		complianceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": r.ID, "review_status": r.Review})
}
