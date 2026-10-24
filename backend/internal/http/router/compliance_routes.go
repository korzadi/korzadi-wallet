package router

import (
	"github.com/gin-gonic/gin"

	"korzadi-wallet/backend/internal/http/handlers"
	"korzadi-wallet/backend/internal/http/middleware"
)

// RegisterComplianceRoutes mounts Bloque 9's endpoints.
//
//	authed:  group that already enforces JWT (middleware.AuthRequired)
//	api:     the /api group (webhook is OUTSIDE JWT: it is signature-authenticated)
//	stepUp:  2FA step-up for privileged actions (X-2FA-Code); never nil in production
//
// Admin routes do NOT reuse admin/super_admin: they require the dedicated
// compliance_reviewer / compliance_admin roles (separation of duties).
func RegisterComplianceRoutes(api, authed *gin.RouterGroup, h *handlers.ComplianceHandler,
	getRoles func(userID string) ([]string, error), stepUp gin.HandlerFunc, webhookLimiter gin.HandlerFunc) {
	if h == nil {
		return
	}
	if webhookLimiter == nil {
		webhookLimiter = func(c *gin.Context) { c.Next() }
	}
	if stepUp == nil { // fail closed: never expose privileged routes without step-up
		stepUp = func(c *gin.Context) { c.AbortWithStatusJSON(503, gin.H{"error": "unavailable"}) }
	}
	api.POST("/kyc/webhook/:provider", webhookLimiter, h.Webhook)

	kyc := authed.Group("/kyc")
	{
		kyc.GET("/status", h.Status)
		kyc.POST("/start", middleware.IdempotencyKeyRequired(), h.Start)
	}

	reviewer := middleware.RequireRole(getRoles, "compliance_reviewer", "compliance_admin")
	admin := middleware.RequireRole(getRoles, "compliance_admin")
	idem := middleware.IdempotencyKeyRequired()

	ak := authed.Group("/admin/kyc")
	{
		ak.GET("/:user_id", reviewer, h.AdminGet)
		ak.POST("/:user_id/review", reviewer, idem, h.AdminReview)
		ak.POST("/:user_id/flags", reviewer, h.CreateFlag)
		ak.POST("/:user_id/risk", reviewer, stepUp, idem, h.SetRisk)
		ak.POST("/:user_id/screenings", reviewer, idem, h.RequestScreening)
		ak.POST("/:user_id/approve", admin, stepUp, idem, h.AdminApprove)
		ak.POST("/:user_id/reject", admin, stepUp, idem, h.AdminReject)
		ak.POST("/:user_id/suspend", admin, stepUp, idem, h.AdminSuspend)
		ak.POST("/:user_id/holds", admin, stepUp, idem, h.PlaceHold)
	}
	ac := authed.Group("/admin/compliance")
	ac.Use(admin, stepUp, idem)
	{
		ac.POST("/holds/:id/release", h.ReleaseHold)
		ac.POST("/flags/:id/resolve", h.ResolveFlag)
		ac.POST("/screenings/:id/review", h.ReviewScreening)
	}
}
