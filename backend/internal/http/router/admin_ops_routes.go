package router

import (
	"github.com/gin-gonic/gin"

	"korzadi-wallet/backend/internal/http/handlers"
	"korzadi-wallet/backend/internal/http/middleware"
)

// RegisterAdminOpsRoutes monta las operaciones administrativas sensibles bajo
// `admin` (ya autenticado). TODAS exigen:
//  1. rol super_admin (no basta admin),
//  2. 2FA de paso (header X-2FA-Code) — stepUp es obligatorio: sin él las rutas
//     NO se registran (falla cerrado),
//  3. auditoría de cada acción (la hace el handler).
func RegisterAdminOpsRoutes(admin *gin.RouterGroup, h *handlers.AdminOpsHandler,
	getRoles func(userID string) ([]string, error), stepUp gin.HandlerFunc) {
	if h == nil || stepUp == nil || getRoles == nil {
		return
	}
	g := admin.Group("")
	g.Use(middleware.RequireRole(getRoles, "super_admin"))
	g.Use(stepUp)
	{
		g.GET("/withdrawals/review", h.ListWithdrawalReviews)
		g.POST("/withdrawals/:id/resolve", h.ResolveWithdrawal)

		g.GET("/custody/wallets", h.ListWallets)
		g.GET("/hot-wallet/balances", h.HotWalletBalances)
		g.POST("/custody/wallets", h.RegisterWallet)
		g.POST("/custody/wallets/:id/status", h.SetWalletStatus)

		g.GET("/treasury/policies", h.ListTreasuryPolicies)
		g.POST("/treasury/policies", h.CreateTreasuryPolicy)
		g.POST("/treasury/policies/:id/enabled", h.SetTreasuryPolicyEnabled)

		g.GET("/gas-station/policies/:wallet_id", h.GetGasPolicy)
		g.PUT("/gas-station/policies/:wallet_id", h.PutGasPolicy)
	}
}
