package router

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	appsecurity "korzadi-wallet/backend/internal/application/security"
	"korzadi-wallet/backend/internal/application/twofactor"
	"korzadi-wallet/backend/internal/http/handlers"
	p2phandler "korzadi-wallet/backend/internal/http/handlers/p2p"
	"korzadi-wallet/backend/internal/http/middleware"
	"korzadi-wallet/backend/internal/infrastructure/authjwt"
)

type Handlers struct {
	Auth               *handlers.AuthHandler
	Account            *handlers.AccountHandler          // verificación de email y contraseña (puede ser nil)
	DestinationAdmin   *handlers.DestinationAdminHandler // aprobar/bloquear direcciones de retiro (puede ser nil)
	Wallet             *handlers.WalletHandler
	Addresses          *handlers.AddressHandler
	Deposit            *handlers.DepositHandler
	BlockchainDeposits *handlers.BlockchainDepositHandler
	Withdrawal         *handlers.BlockchainWithdrawalHandler
	Fees               *handlers.FeeHandler
	TwoFactor          *handlers.TwoFactorHandler
	Affiliate          *handlers.AffiliateHandler
	Admin              *handlers.AdminHandler
	KorzadiVPN         *handlers.KorzadiVPNHandler
	Notification       *handlers.NotificationHandler
	Crypto             *handlers.CryptoHandler
	// Security is Bloque 8's admin surface (freezes, policies, security
	// events) — may be nil if this deployment hasn't wired Bloque 8 yet,
	// in which case its routes are simply not registered (see New below).
	Security *handlers.SecurityHandler
	// Compliance is Bloque 9 (KYC/AML). Nil = routes not registered.
	Compliance *handlers.ComplianceHandler
	// AdminOps (opcional): operaciones admin sensibles (super_admin + 2FA + auditoría).
	AdminOps *handlers.AdminOpsHandler
	// P2P: Peer-to-peer market
	P2P *p2phandler.Handler
}

type Config struct {
	JWTManager      *authjwt.Manager
	TwoFactorSvc    *twofactor.Service
	VPNHMACSecret   string
	GetRolesForUser func(userID string) ([]string, error)
	AllowedOrigins  map[string]bool
	RedisClient     *redis.Client

	// SecuritySvc drives Bloque 8's OperationGuard (kill switch/emergency
	// freeze) middleware. Nil is fully supported — OperationGuard becomes
	// a no-op (see middleware.OperationGuard) so a deployment can adopt
	// Bloque 8 without this ever being a hard requirement to boot.
	SecuritySvc *appsecurity.Service
	// SecurityCounter backs the per-user rate limiting in
	// middleware.SecurityRateLimit (spec §8); typically
	// redis.NewSecurityCounter(redisClient). Nil disables it (no-op).
	SecurityCounter appsecurity.Counter
	// SecurityCfg supplies the rate-limit rules/enabled flag SecurityRateLimit reads.
	SecurityCfg appsecurity.Config

	// FiatSandboxConfirmEnabled registers the admin-only sandbox deposit
	// confirmation route. Default false (fail closed).
	FiatSandboxConfirmEnabled bool
	// TrustedProxies lists the proxy IPs/CIDRs whose X-Forwarded-For header
	// may be trusted for c.ClientIP(). Empty/nil = trust none (the header is
	// ignored), so per-IP rate limits cannot be evaded by spoofing it.
	TrustedProxies []string
}

func New(h Handlers, cfg Config) *gin.Engine {
	r := gin.New()
	// AUDIT FIX: by default gin trusts every proxy, which lets any client
	// spoof X-Forwarded-For and dodge the per-IP rate limiters (login, 2FA,
	// withdrawals...). Trust only explicitly configured proxies.
	if err := r.SetTrustedProxies(cfg.TrustedProxies); err != nil {
		panic("router: TRUSTED_PROXIES inválido: " + err.Error())
	}
	r.Use(gin.Recovery(), gin.Logger(), middleware.CORSConfig(cfg.AllowedOrigins), middleware.BodyLimit(1<<20))

	// login/registro/2FA y operaciones financieras (incl. retiros) fallan CERRADOS si Redis cae.
	authLimiter := middleware.NewRedisRateLimiter(cfg.RedisClient, 10, time.Minute).FailClosed()
	financialLimiter := middleware.NewRedisRateLimiter(cfg.RedisClient, 30, time.Minute).FailClosed()
	integrationLimiter := middleware.NewRedisRateLimiter(cfg.RedisClient, 60, time.Minute)
	cryptoReadLimiter := middleware.NewRedisRateLimiter(cfg.RedisClient, 120, time.Minute)
	cryptoWriteLimiter := middleware.NewRedisRateLimiter(cfg.RedisClient, 20, time.Minute)

	api := r.Group("/api")
	{
		auth := api.Group("/auth")
		auth.Use(authLimiter.Middleware())
		{
			auth.POST("/register", h.Auth.Register)
			auth.POST("/login", middleware.SecurityRateLimit(cfg.SecurityCounter, cfg.SecurityCfg, "login"), h.Auth.Login)
			auth.POST("/refresh", h.Auth.Refresh)
			auth.POST("/logout", h.Auth.Logout)
			if h.Account != nil {
				auth.POST("/verify-email", h.Account.VerifyEmail)
				auth.POST("/resend-verification", h.Account.ResendVerification)
				auth.POST("/forgot-password", h.Account.ForgotPassword)
				auth.POST("/reset-password", h.Account.ResetPassword)
			}
		}

		authed := api.Group("")
		authed.Use(middleware.AuthRequired(cfg.JWTManager))
		{
			if h.Account != nil {
				authed.POST("/account/change-password", authLimiter.Middleware(), h.Account.ChangePassword)
			}
			wallet := authed.Group("/wallet")
			{
				wallet.GET("/balances", h.Wallet.Balances)
				wallet.GET("/transactions", h.Wallet.Transactions)
				wallet.GET("/transfers", h.Wallet.ListTransfers)
				wallet.GET("/transfers/:id", h.Wallet.GetTransfer)

				financial := wallet.Group("")
				financial.Use(financialLimiter.Middleware(), middleware.IdempotencyKeyRequired())
				{
					financial.POST("/transfers",
						middleware.SecurityRateLimit(cfg.SecurityCounter, cfg.SecurityCfg, "transfer"),
						middleware.OperationGuard(cfg.SecuritySvc, appsecurity.OpTransfer),
						h.Wallet.CreateTransfer)
					financial.POST("/deposits", h.Deposit.Create)
					// AUDIT FIX (CRITICAL): POST /deposits/:id/confirm used to live here,
					// reachable by ANY authenticated user, so a user could create a
					// deposit and confirm it themselves to mint ledger funds. It now
					// exists only as an admin-gated, explicitly-enabled sandbox route
					// (see /admin/deposits/:id/confirm below).
					financial.POST("/withdrawals",
						middleware.SecurityRateLimit(cfg.SecurityCounter, cfg.SecurityCfg, "withdrawal"),
						middleware.OperationGuard(cfg.SecuritySvc, appsecurity.OpWithdrawal),
						handlers.TwoFAVerifyMiddleware(cfg.TwoFactorSvc), h.Withdrawal.Create)
				}
				// Bloque 6B: lectura de retiros blockchain — ya filtra por
				// ownership en SQL, no requiere Idempotency-Key.
				wallet.GET("/withdrawals", h.Withdrawal.List)
				wallet.GET("/withdrawals/:id", h.Withdrawal.Get)

				cryptoGroup := wallet.Group("/crypto/usdt")
				cryptoGroup.Use(cryptoReadLimiter.Middleware())
				{
					// TRON (existing)
					cryptoGroup.GET("/address", h.Crypto.GetDepositAddress)
					cryptoGroup.GET("/onchain-balance", h.Crypto.GetOnChainBalance)

					// BSC / BEP-20 (new, Fase 1) — namespaced under /bsc so the
					// TRON routes above keep their existing paths unchanged for
					// any client already integrated against them.
					bsc := cryptoGroup.Group("/bsc")
					{
						bsc.GET("/address", h.Crypto.GetBSCDepositAddress)
						bsc.GET("/onchain-balance", h.Crypto.GetBSCOnChainBalance)
					}
				}

				// Bloque 3: registro genérico de direcciones para Ethereum,
				// Polygon, Arbitrum, Optimism, Base y Bitcoin. TRON y BSC
				// siguen sirviéndose desde /wallet/crypto/usdt arriba — no
				// duplicados acá.
				// Redes que el cliente puede ofrecer para depósitos ahora mismo
				// (solo las que tienen watcher activo).
				wallet.GET("/networks", h.Addresses.ActiveNetworks)

				addresses := wallet.Group("/addresses")
				{
					addresses.GET("", cryptoReadLimiter.Middleware(), h.Addresses.List)
					addresses.POST("", cryptoWriteLimiter.Middleware(), h.Addresses.Create)
				}

				// Bloque 4: historial de depósitos blockchain (solo lectura).
				// Namespaced bajo /deposits/crypto para no chocar con las
				// rutas fiat existentes (POST /wallet/deposits, arriba) —
				// son dos flujos de dinero distintos con modelos de dominio
				// distintos, nunca deben compartir la misma ruta.
				cryptoDeposits := wallet.Group("/deposits/crypto")
				cryptoDeposits.Use(cryptoReadLimiter.Middleware())
				{
					cryptoDeposits.GET("", h.BlockchainDeposits.List)
					cryptoDeposits.GET("/:id", h.BlockchainDeposits.Get)
				}

				// Bloque 6A: Universal Fee Engine. Estimating a fee never
				// touches the Ledger's balance (only calculates and
				// persists a quote row), so it doesn't need the
				// 'financial' group's Idempotency-Key requirement above.
				fees := wallet.Group("/fees")
				{
					fees.POST("/estimate", cryptoWriteLimiter.Middleware(), h.Fees.Estimate)
					fees.GET("/quotes/:id", cryptoReadLimiter.Middleware(), h.Fees.GetQuote)
				}
			}

			security := authed.Group("/security/2fa")
			security.Use(authLimiter.Middleware(), middleware.SecurityRateLimit(cfg.SecurityCounter, cfg.SecurityCfg, "2fa")) // AUDIT FIX: 2FA confirm was unthrottled
			{
				security.POST("/setup", h.TwoFactor.Setup)
				security.POST("/confirm", h.TwoFactor.Confirm)
				security.GET("/status", h.TwoFactor.Status)
				security.POST("/disable", h.TwoFactor.Disable)
				security.POST("/recovery-codes/regenerate", h.TwoFactor.RegenerateRecoveryCodes)
			}

			affiliateGroup := authed.Group("/affiliate")
			{
				affiliateGroup.GET("", h.Affiliate.Me)
				affiliateGroup.POST("", h.Affiliate.Enroll)
				affiliateGroup.POST("/referrals", h.Affiliate.RegisterReferral)
			}

			notifications := authed.Group("/notifications")
			{
				notifications.GET("", h.Notification.List)
				notifications.POST("/:id/read", h.Notification.MarkRead)
			}

			admin := authed.Group("/admin")
			admin.Use(middleware.RequireRole(cfg.GetRolesForUser, "admin", "super_admin"))
			{
				admin.GET("/users", h.Admin.ListUsers)
				admin.POST("/users/:id/block", h.Admin.BlockUser)
				admin.GET("/transactions", h.Admin.ListTransactions)
				admin.GET("/audit", h.Admin.ListAudit)

				// Sandbox-only manual confirmation of fiat deposits. Fail-closed:
				// not registered unless FIAT_SANDBOX_CONFIRM_ENABLED=true, and even
				// then only for admin/super_admin. Must stay disabled in production
				// (a real payment provider confirms via a signed webhook instead).
				if cfg.FiatSandboxConfirmEnabled {
					admin.POST("/deposits/:id/confirm", h.Deposit.Confirm)
				}

				// Bloque 8: emergency freeze, security policies, security
				// events. Same admin/super_admin gate as the rest of
				// /admin above — no new privilege level introduced.
				if h.Security != nil {
					secAdmin := admin.Group("/security")
					{
						secAdmin.GET("/freezes", h.Security.ListFreezes)
						secAdmin.POST("/freezes", h.Security.SetFreeze)
						secAdmin.GET("/policies", h.Security.ListPolicies)
						secAdmin.POST("/policies", h.Security.UpsertPolicy)
						secAdmin.GET("/events", h.Security.ListEvents)
					}
				}

				if cfg.TwoFactorSvc != nil {
					RegisterAdminOpsRoutes(admin, h.AdminOps, cfg.GetRolesForUser, handlers.TwoFAVerifyMiddleware(cfg.TwoFactorSvc))
				}

				superAdminOnly := admin.Group("")
				superAdminOnly.Use(middleware.RequireRole(cfg.GetRolesForUser, "super_admin"))
				{
					superAdminOnly.POST("/users/:id/unblock", h.Admin.UnblockUser)
					superAdminOnly.POST("/users/:id/verify-email", h.Admin.ManualVerifyEmail)
					superAdminOnly.POST("/users/:id/reset-2fa", h.TwoFactor.AdminReset)
					if h.DestinationAdmin != nil {
						superAdminOnly.POST("/security/destinations", h.DestinationAdmin.Set)
					}
					superAdminOnly.POST("/users/:id/roles", h.Admin.AssignRole)
				}
			}
		}

		// Bloque 9: KYC/AML. JWT + dedicated compliance roles + step-up 2FA;
		// the webhook is signature-authenticated and sits outside JWT.
		if h.Compliance != nil {
			var stepUp gin.HandlerFunc
			if cfg.TwoFactorSvc != nil {
				stepUp = handlers.TwoFAVerifyMiddleware(cfg.TwoFactorSvc)
			}
			RegisterComplianceRoutes(api, authed, h.Compliance, cfg.GetRolesForUser, stepUp, integrationLimiter.Middleware())
		}

		// P2P Market
		if h.P2P != nil {
			p2p := authed.Group("/p2p")
			{
				p2p.GET("/offers", h.P2P.ListOffers)
				p2p.POST("/offers", h.P2P.CreateOffer)
				p2p.GET("/offers/mine", h.P2P.MyOffers)
				p2p.GET("/offers/:id", h.P2P.GetOffer)
				p2p.PATCH("/offers/:id", h.P2P.UpdateOffer)
				p2p.POST("/orders", h.P2P.CreateOrder)
				p2p.GET("/orders", h.P2P.MyOrders)
				p2p.GET("/orders/:id", h.P2P.GetOrder)
				p2p.POST("/orders/:id/pay", h.P2P.MarkPaid)
				p2p.POST("/orders/:id/release", h.P2P.Release)
				p2p.POST("/orders/:id/cancel", h.P2P.Cancel)
				p2p.POST("/orders/:id/dispute", h.P2P.OpenDispute)
				p2p.GET("/orders/:id/messages", h.P2P.Messages)
				p2p.POST("/orders/:id/messages", h.P2P.SendMessage)
			}
		}

		integrations := api.Group("/integrations")
		integrations.Use(integrationLimiter.Middleware())
		{
			vpn := integrations.Group("/korzadivpn")
			vpn.Use(middleware.ServerToServerHMAC(cfg.VPNHMACSecret))
			{
				vpn.POST("/payment", h.KorzadiVPN.Pay)
			}
		}
	}

	// Admin P2P routes (compliance_admin role required)
	if h.P2P != nil && h.Compliance != nil {
		var stepUp gin.HandlerFunc
		if cfg.TwoFactorSvc != nil {
			stepUp = handlers.TwoFAVerifyMiddleware(cfg.TwoFactorSvc)
		}
		adminP2P := api.Group("/admin/p2p")
		adminP2P.Use(middleware.RequireRole(cfg.GetRolesForUser, "compliance_admin"))
		if stepUp != nil {
			adminP2P.Use(stepUp)
		}
		{
			adminP2P.GET("/orders", h.P2P.AdminListOrders)
			adminP2P.GET("/orders/:id", h.P2P.AdminGetOrder)
			adminP2P.POST("/orders/:id/resolve", h.P2P.AdminResolveDispute)
			adminP2P.GET("/offers", h.P2P.AdminListOffers)
			adminP2P.POST("/offers/:id/suspend", h.P2P.AdminSuspendOffer)
		}
	}

	healthHandler := func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) }
	r.GET("/health", healthHandler)
	r.HEAD("/health", healthHandler)
	r.GET("/health/blockchain/bsc", h.Crypto.GetBSCHealth)

	return r
}
