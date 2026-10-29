package main

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/shopspring/decimal"

	"korzadi-wallet/backend/internal/application/accountaccess"
	appadmin "korzadi-wallet/backend/internal/application/admin"
	appaffiliate "korzadi-wallet/backend/internal/application/affiliate"
	appauth "korzadi-wallet/backend/internal/application/auth"
	bw "korzadi-wallet/backend/internal/application/blockchainwithdrawal"
	"korzadi-wallet/backend/internal/application/bscwithdrawal"
	appcompliance "korzadi-wallet/backend/internal/application/compliance"
	appcrypto "korzadi-wallet/backend/internal/application/crypto"
	appcustody "korzadi-wallet/backend/internal/application/custody"
	appdeposit "korzadi-wallet/backend/internal/application/deposit"
	appfee "korzadi-wallet/backend/internal/application/fee"
	appgasstation "korzadi-wallet/backend/internal/application/gasstation"
	appkorzadivpn "korzadi-wallet/backend/internal/application/integrations/korzadivpn"
	appledger "korzadi-wallet/backend/internal/application/ledger"
	"korzadi-wallet/backend/internal/application/lifecycle"
	appp2p "korzadi-wallet/backend/internal/application/p2p"
	appreconciliation "korzadi-wallet/backend/internal/application/reconciliation"
	apprecovery "korzadi-wallet/backend/internal/application/recovery"
	appsecurity "korzadi-wallet/backend/internal/application/security"
	appprefs "korzadi-wallet/backend/internal/application/security/prefs"
	appsweep "korzadi-wallet/backend/internal/application/sweep"
	apptransfer "korzadi-wallet/backend/internal/application/transfer"
	apptreasury "korzadi-wallet/backend/internal/application/treasury"
	apptwofactor "korzadi-wallet/backend/internal/application/twofactor"
	appwallet "korzadi-wallet/backend/internal/application/wallet"
	appwatcher "korzadi-wallet/backend/internal/application/watcher"
	"korzadi-wallet/backend/internal/domain"
	"korzadi-wallet/backend/internal/http/handlers"
	adminpanel "korzadi-wallet/backend/internal/http/handlers/adminpanel"
	p2phandler "korzadi-wallet/backend/internal/http/handlers/p2p"
	"korzadi-wallet/backend/internal/http/middleware"
	"korzadi-wallet/backend/internal/http/router"
	"korzadi-wallet/backend/internal/infrastructure/assetregistry"
	"korzadi-wallet/backend/internal/infrastructure/authjwt"
	"korzadi-wallet/backend/internal/infrastructure/bscclient"
	"korzadi-wallet/backend/internal/infrastructure/btcclient"
	"korzadi-wallet/backend/internal/infrastructure/btcwallet"
	"korzadi-wallet/backend/internal/infrastructure/cryptutil"
	"korzadi-wallet/backend/internal/infrastructure/didit"
	"korzadi-wallet/backend/internal/infrastructure/evmclient"
	"korzadi-wallet/backend/internal/infrastructure/evmwallet"
	"korzadi-wallet/backend/internal/infrastructure/mailer"
	"korzadi-wallet/backend/internal/infrastructure/migrate"
	"korzadi-wallet/backend/internal/infrastructure/pglock"
	"korzadi-wallet/backend/internal/infrastructure/postgres"
	"korzadi-wallet/backend/internal/infrastructure/providers"
	securityredis "korzadi-wallet/backend/internal/infrastructure/redis"
	"korzadi-wallet/backend/internal/infrastructure/tronclient"
	"korzadi-wallet/backend/internal/infrastructure/tronwallet"
)

func main() {
	_ = godotenv.Load()

	dbURL := mustEnv("DATABASE_URL")
	jwtSecret := mustEnv("JWT_SECRET")
	vpnHMACSecret := envOr("KORZADIVPN_HMAC_SECRET", "")
	twoFactorEncryptionKey := mustEnv("TWO_FACTOR_ENCRYPTION_KEY")
	redisAddr := envOr("REDIS_ADDR", "redis:6379")
	redisPassword := envOr("REDIS_PASSWORD", "")
	port := envOr("PORT", "8060")

	// Claves de custodia: CUSTODY_ENCRYPTION_KEYS="id1=hex,id2=hex" +
	// CUSTODY_ACTIVE_KEY_ID (una activa para cifrar, el resto solo descifra);
	// si no existe, se usa la variable histórica CUSTODY_ENCRYPTION_KEY
	// (id "default"). Los errores nunca incluyen material de clave.
	custodyKeyProvider, kpErr := cryptutil.LoadEnvKeyProvider(os.Getenv)
	if kpErr != nil {
		log.Fatalf("configuración de claves de custodia inválida: %v", kpErr)
	}
	tronNetworkRaw := os.Getenv("TRON_NETWORK")
	tronNetwork := envOr("TRON_NETWORK", "testnet")
	if tronNetworkRaw == "" {
		log.Println("ADVERTENCIA CRÍTICA: TRON_NETWORK no definido explícitamente — se usa 'testnet' por defecto. Para producción debe establecerse TRON_NETWORK=mainnet (o testnet explícitamente) en .env.")
	}
	usdtContract := mustEnv("TRON_USDT_CONTRACT")
	// Fail fast: the configured USDT TRC-20 contract must be on the allow-list.
	// mainnet → ONLY Tether's official contract; testnet → TRON_USDT_TESTNET_ALLOWLIST.
	if err := tronclient.VerifyUSDTContract(tronNetwork, usdtContract, splitCSV(os.Getenv("TRON_USDT_TESTNET_ALLOWLIST"))); err != nil {
		log.Fatalf("TRON_USDT_CONTRACT rechazado: %v", err)
	}

	// Bloque 8 §19: valida los secretos críticos al arrancar, sin nunca
	// imprimir su valor — solo "missing"/"invalid"/"misconfigured" y el
	// nombre de la variable. mustEnv ya garantiza no-vacío para las
	// obligatorias; esto añade el chequeo de longitud mínima / valores
	// placeholder que mustEnv no hacía.
	if err := appsecurity.ValidateSecrets(
		appsecurity.RequiredSecret{Name: "JWT_SECRET", Value: jwtSecret, MinLength: 16},
		appsecurity.RequiredSecret{Name: "TWO_FACTOR_ENCRYPTION_KEY", Value: twoFactorEncryptionKey, MinLength: 16},
		appsecurity.RequiredSecret{Name: "DATABASE_URL", Value: dbURL},
		// A1 FIX: validar secretos adicionales que pueden ser críticos y faltaban
		appsecurity.RequiredSecret{Name: "CUSTODY_ENCRYPTION_KEYS", Value: os.Getenv("CUSTODY_ENCRYPTION_KEYS")},
		appsecurity.RequiredSecret{Name: "REDIS_PASSWORD", Value: redisPassword},
	); err != nil {
		log.Fatalf("configuración de seguridad inválida: %v", err)
	}

	if vpnHMACSecret == "" {
		log.Println("ADVERTENCIA: KORZADIVPN_HMAC_SECRET no definido — el endpoint de integración " +
			"rechazará todas las peticiones hasta que se configure.")
	}
	if tronNetwork == "mainnet" {
		log.Println("⚠️  ADVERTENCIA CRÍTICA: TRON_NETWORK=mainnet — este proceso operará con USDT REAL. " +
			"Confirmar que hubo auditoría de seguridad externa antes de este punto.")
	}

	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		log.Fatalf("no se pudo conectar a postgres: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(context.Background()); err != nil {
		log.Fatalf("postgres no responde: %v", err)
	}

	// Locks entre réplicas (pg_advisory_lock): pool PROPIO y pequeño para que los
	// locks retenidos nunca dejen sin conexiones al pool principal. No usar
	// PgBouncer en modo transaction (rompe los locks de sesión).
	lockPool, err := pglock.NewPool(context.Background(), dbURL, int32(envUint("ADVISORY_LOCK_MAX_CONNS", 8)))
	if err != nil {
		log.Fatalf("no se pudo abrir el pool de locks: %v", err)
	}
	defer lockPool.Close()
	locker := pglock.New(lockPool)

	migrationsDir := envOr("MIGRATIONS_DIR", "/migrations")
	migrateLockWait := time.Duration(envUint("MIGRATE_LOCK_TIMEOUT_SECONDS", 120)) * time.Second
	if err := migrate.RunWithLockTimeout(context.Background(), pool, migrationsDir, migrateLockWait); err != nil {
		log.Fatalf("error aplicando migraciones: %v", err)
	}

	if err := bootstrapSystemAccounts(context.Background(), pool); err != nil {
		log.Fatalf("no se pudo inicializar cuentas de sistema: %v", err)
	}

	encryptor, err := cryptutil.NewEncryptor(twoFactorEncryptionKey)
	if err != nil {
		log.Fatalf("TWO_FACTOR_ENCRYPTION_KEY inválida: %v", err)
	}
	custodyEncryptor := cryptutil.New(custodyKeyProvider)

	// Migración perezosa: al leer un blob legacy (o cifrado con una clave ya
	// no activa) se re-cifra con la clave activa + AAD y se guarda con CAS.
	rekeyRepo := postgres.NewCustodyRekeyRepository(pool)
	custodyEncryptor.RegisterRewriter(cryptutil.SlotCryptoAddresses, rekeyRepo.Rewrite)
	custodyEncryptor.RegisterRewriter(cryptutil.SlotCustodyWallets, rekeyRepo.Rewrite)
	encryptor.RegisterRewriter(cryptutil.SlotTwoFactor, rekeyRepo.Rewrite)

	redisClient := middleware.NewRedisClient(redisAddr, redisPassword)
	if err := middleware.PingRedis(context.Background(), redisClient); err != nil {
		log.Fatalf("no se pudo conectar a Redis (%s): %v", redisAddr, err)
	}

	// TRON balance-reading provider: NodeReal (native full-node API).
	// TronGrid ha sido removido — NodeReal cubre la única función que se
	// usaba (lectura de saldo TRC20 vía triggerconstantcontract).
	nodeRealURL := os.Getenv("TRON_NODEREAL_URL")
	if nodeRealURL == "" {
		log.Fatalf("TRON_NODEREAL_URL no definido (ej. https://open-platform.nodereal.io/<api_key>/tron)")
	}
	tronClient := tronclient.NewNodeRealClient(nodeRealURL)
	// Same node, full API (blocks, logs, receipts, resources, broadcast): used by the
	// TRON deposit watcher, withdrawals, gas station and fee estimator below.
	tronNode := tronclient.NewFullNode(nodeRealURL)
	tronCfg := tronclient.Config{
		ConfirmationsRequired: envUint("TRON_CONFIRMATIONS_REQUIRED", 20),
		MaxFeeLimitSun:        int64(envUint("TRON_MAX_FEE_LIMIT_SUN", 100_000_000)),
	}
	log.Printf("TRON: usando NodeReal como proveedor de lectura de saldo (%s)", tronNetwork)

	// ---- BSC / USDT BEP-20 (Fase 1) ----
	//
	// BSC is optional at boot: if BSC_RPC_URL isn't set, the rest of the
	// wallet (auth, TRON, transfers, etc.) still starts normally, and BSC
	// endpoints return "bsc_not_configured" until it's set up. If it IS
	// set but misconfigured (wrong chain id, unreachable RPC), boot fails
	// loudly rather than silently running a broken deposit pipeline.
	var bscProvider *bscclient.Provider
	var bscRawClient *bscclient.Client
	var bscCfg bscclient.Config
	if os.Getenv("BSC_RPC_URL") != "" {
		var err error
		bscCfg, err = bscclient.LoadConfigFromEnv()
		if err != nil {
			log.Fatalf("configuración de BSC inválida: %v", err)
		}
		bscRawClient, err = bscclient.Dial(context.Background(), bscCfg)
		if err != nil {
			log.Fatalf("no se pudo conectar al RPC de BSC: %v", err)
		}
		bscProvider = bscclient.NewProvider(bscRawClient, bscCfg)

		health, err := bscProvider.HealthCheck(context.Background(), bscCfg.USDTContractAddress)
		if err != nil {
			log.Fatalf("health check inicial de BSC falló (rpc=%v chainOK=%v contractOK=%v): %v",
				health.RPCReachable, health.ChainIDOK, health.ContractOK, err)
		}
		log.Printf("BSC conectado correctamente: chain_id=%d latest_block=%d contrato_usdt_ok=%v",
			health.ActualChainID, health.LatestBlock, health.ContractOK)
	} else {
		log.Println("BSC_RPC_URL no definido — los endpoints de USDT/BSC responderán 'bsc_not_configured' hasta que se configure.")
	}

	userRepo := postgres.NewUserRepository(pool)
	sessionRepo := postgres.NewSessionRepository(pool)
	walletRepo := postgres.NewWalletRepository(pool)
	ledgerRepo := postgres.NewLedgerRepository(pool)
	reservationRepo := postgres.NewReservationRepository(pool)
	// ledgerService is the single Application-layer ledger port (Bloque 2):
	// every money-moving flow uses either ledgerRepo directly (transfers,
	// generic withdrawals, affiliate payouts, KorzadiVPN — unchanged from
	// before this block) or ledgerService (blockchain deposits, BSC
	// withdrawals — previously wired to a separate postgres.LedgerBlockchainPoster
	// that has been removed). Both paths end up calling the exact same
	// LedgerRepository.ExecuteTransaction — there is only one posting engine.
	ledgerService := appledger.NewService(ledgerRepo, reservationRepo)
	transferRepo := postgres.NewTransferRepository(pool)
	depositRepo := postgres.NewDepositRepository(pool)
	auditRepo := postgres.NewAuditRepository(pool)
	twoFactorRepo := postgres.NewTwoFactorRepository(pool)
	affiliateRepo := postgres.NewAffiliateRepository(pool)
	roleRepo := postgres.NewRoleRepository(pool)
	paymentOrderRepo := postgres.NewPaymentOrderRepository(pool)
	notificationRepo := postgres.NewNotificationRepository(pool)
	cryptoAddressRepo := postgres.NewCryptoAddressRepository(pool)
	custodyAuditRepo := postgres.NewCustodyAuditRepository(pool)
	blockchainTransferRepo := postgres.NewBlockchainTransferRepository(pool)
	blockchainSyncStateRepo := postgres.NewBlockchainSyncStateRepository(pool)
	// Fase B: eventos fallidos reintentables, incidentes de depósitos y
	// re-verificación de depósitos acreditados (reorg tras acreditar).
	watcherFailedEventRepo := postgres.NewWatcherFailedEventRepository(pool)
	depositIncidentRepo := postgres.NewDepositIncidentRepository(pool)
	addressToUserResolver := postgres.NewAddressToUserResolver(pool)
	clearingAccountRepo := postgres.NewClearingAccountRepository(pool)
	userWalletAccountRepo := postgres.NewUserWalletAccountRepository(pool)
	blockchainDepositRecorder := postgres.NewBlockchainDepositRecorderRepository(pool)
	// Bloque 4: mismo repositorio, capacidad de lectura — historial de
	// depósitos blockchain (GET /wallet/deposits/crypto).
	blockchainDepositHistory := appdeposit.NewHistoryService(blockchainDepositRecorder)

	paymentProvider := providers.NewSandboxPaymentProvider()

	jwtMgr := authjwt.NewManager(jwtSecret, 15*time.Minute, 30*24*time.Hour)

	authService := appauth.NewService(userRepo, sessionRepo, walletRepo, jwtMgr, auditRepo, notificationRepo, 30*24*time.Hour)
	// Bloque 5: catálogo de assets a nivel Ledger, para validar el asset de
	// una transferencia interna antes de tocar el Ledger.
	ledgerAssetRepo := postgres.NewAssetRepository(pool)
	transferService := apptransfer.NewService(walletRepo, ledgerRepo, transferRepo, ledgerAssetRepo, userRepo, auditRepo, notificationRepo)
	depositService := appdeposit.NewService(walletRepo, depositRepo, ledgerRepo, paymentProvider, auditRepo)
	twoFactorService := apptwofactor.NewService(twoFactorRepo, "Korzadi Wallet", encryptor)
	// Login must fail closed without the 2FA gate and its one-time challenge
	// store: a password alone can never mint a session for a 2FA account.
	authService.WithTwoFactor(twoFactorService).
		WithLoginChallenges(postgres.NewLoginChallengeRepository(pool))

	// ---- Fase C: verificación de email, restablecimiento y cambio de contraseña ----
	// SMTP_HOST/SMTP_PORT/SMTP_USERNAME/SMTP_PASSWORD/SMTP_FROM (+ APP_BASE_URL con la
	// URL pública del frontend). Sin SMTP el backend arranca igual, pero NO se
	// entregan correos: un super_admin puede verificar cuentas a mano
	// (POST /admin/users/:id/verify-email).
	emailSender, smtpConfigured, mailErr := mailer.NewFromEnv()
	if mailErr != nil {
		log.Fatalf("configuración SMTP inválida: %v", mailErr)
	}
	appBaseURL := strings.TrimSpace(os.Getenv("APP_BASE_URL"))
	if !smtpConfigured {
		log.Println("ADVERTENCIA: SMTP_HOST no definido — NO se enviarán correos de verificación ni de restablecimiento de contraseña.")
	}
	if appBaseURL == "" {
		log.Println("ADVERTENCIA: APP_BASE_URL no definido — no se pueden construir los enlaces de los correos.")
	}
	accountService := accountaccess.NewService(accountaccess.Deps{
		Users:    userRepo,
		Tokens:   postgres.NewAccountTokenRepository(pool),
		Sessions: sessionRepo,
		Sender:   emailSender,
		Audit:    auditRepo,
		Notifier: notificationRepo,
		TwoFA:    twoFactorService,
		BaseURL:  appBaseURL,
	})
	// Los correos se envían en segundo plano: el tiempo de respuesta de los endpoints
	// públicos no depende de si la cuenta existe (evita enumerar usuarios por tiempos)
	// y el registro no espera al servidor SMTP.
	accountService.WithBackground(accountaccess.NewBackground(16, 45*time.Second))
	authService.WithEmailVerifier(accountService.BackgroundVerifier())
	// Fase E: avisos al titular (bloqueo de 2FA, uso de códigos de recuperación, cambios).
	twoFactorService.WithAlerter(accountService)

	// ---- Preferencias de seguridad de usuario (PIN, bloqueo, biometría, alertas) ----
	prefsRepo := postgres.NewPrefsRepository(pool)
	prefsSvc := appprefs.NewService(prefsRepo, accountService)

	// ---- Bloque 8: Security / Operational Controls / Limits ----
	// Reuses userRepo (account status), roleRepo (roles/RBAC), auditRepo
	// (audit_logs) and twoFactorService (2FA) as-is — nothing here
	// reimplements authentication, roles, 2FA or the general audit trail;
	// see internal/application/security's package doc for the full list.
	securityCfg, err := appsecurity.LoadConfigFromEnv()
	if err != nil {
		log.Fatalf("configuración de Bloque 8 inválida: %v", err)
	}
	securityPolicyRepo := postgres.NewSecurityPolicyRepository(pool)
	securityLimitRepo := postgres.NewSecurityLimitRepository(pool)
	securityEventRepo := postgres.NewSecurityEventRepository(pool)
	securityFreezeRepo := postgres.NewSecurityFreezeRepository(pool)
	securityDestinationRepo := postgres.NewSecurityDestinationRepository(pool)
	securityCounter := securityredis.NewSecurityCounter(redisClient)

	securityEvents := appsecurity.NewEventLogger(securityEventRepo, auditRepo)
	ledgerService.WithAdjustmentAuthorization(roleRepo, securityEvents)
	securityLimits := appsecurity.NewLimitService(securityLimitRepo, securityEvents)
	securityFreeze := appsecurity.NewFreezeGuard(securityFreezeRepo, securityCfg, securityEvents)
	securityGuard := appsecurity.NewGuard(roleRepo, userRepo, securityEvents)
	securityStepUp := appsecurity.NewStepUpGuard(twoFactorService, securityEvents)
	securityVelocity := appsecurity.NewVelocityTracker(securityCounter, securityCfg.Velocity, securityEvents)
	securityDestination := appsecurity.NewDestinationGuard(securityDestinationRepo, securityEvents)
	securitySvc := appsecurity.NewService(securityPolicyRepo, securityLimits, securityFreeze, securityGuard,
		securityStepUp, securityVelocity, securityDestination, securityEvents, securityFreezeRepo, securityEventRepo)
	// transferService already exists (constructed above) — wire it in now
	// that securitySvc is built. nil-safe by construction: every other
	// service that doesn't call SetSecurity is unaffected.
	transferService.SetSecurity(securitySvc)

	// ---- Bloque 9: KYC / AML / compliance foundation ----
	// Didit KYC provider
	if strings.EqualFold(strings.TrimSpace(os.Getenv("KYC_PROVIDER")), "didit") &&
		strings.TrimSpace(os.Getenv("KYC_WEBHOOK_SECRET")) == "" {
		secret := strings.TrimSpace(os.Getenv("DIDIT_WEBHOOK_SECRET"))
		if secret == "" {
			log.Fatal("KYC_PROVIDER=didit pero DIDIT_WEBHOOK_SECRET no está configurado")
		}
		// Alias interno: no crea una tercera variable secreta en .env.
		os.Setenv("KYC_WEBHOOK_SECRET", secret)
	}

	complianceCfg, err := appcompliance.LoadConfig(os.Getenv)
	if err != nil {
		log.Fatalf("configuración de Bloque 9 (KYC/AML) inválida: %v", err)
	}

	complianceRegistry := appcompliance.NewRegistry()

	if complianceCfg.KYCEnabled &&
		strings.EqualFold(strings.TrimSpace(complianceCfg.KYCProvider), "didit") {

		diditClient, err := didit.New(didit.Config{
			APIKey:        strings.TrimSpace(os.Getenv("DIDIT_API_KEY")),
			WebhookSecret: strings.TrimSpace(os.Getenv("DIDIT_WEBHOOK_SECRET")),
			WorkflowID:    envOr("DIDIT_WORKFLOW_ID", didit.WorkflowID),
			BaseURL:       envOr("DIDIT_BASE_URL", "https://verification.didit.me"),
		})
		if err != nil {
			log.Fatalf("configuración de Didit inválida: %v", err)
		}

		complianceRegistry.RegisterKYC(diditClient)
		log.Printf("KYC provider registrado: %s", diditClient.Name())
	}

	if complianceCfg.KYCEnabled {
		if _, ok := complianceRegistry.KYC(complianceCfg.KYCProvider); !ok {
			log.Fatalf("KYC_ENABLED=true pero el proveedor configurado no está registrado en este build")
		}
	}

	if complianceCfg.SanctionsEnabled {
		if _, ok := complianceRegistry.Screener(appcompliance.ScreenSanctions); !ok {
			log.Fatalf("SANCTIONS_SCREENING_ENABLED=true pero no hay proveedor de sanciones registrado en este build")
		}
	}

	complianceRepo := postgres.NewComplianceRepository(pool)
	complianceSvc := appcompliance.NewService(complianceRepo, complianceCfg, complianceRegistry, auditRepo, userRepo)
	if complianceCfg.KYCEnabled || complianceCfg.AMLEnabled {
		securitySvc.SetCompliance(appcompliance.NewEngine(complianceRepo, complianceCfg))
	}
	complianceRolesFn := func(userID string) ([]string, error) {
		id, perr := uuid.Parse(userID)
		if perr != nil {
			return nil, perr
		}
		return roleRepo.GetRolesForUser(context.Background(), id)
	}

	// ---- P2P Market ----
	p2pRepo := postgres.NewP2PRepository(pool)
	p2pSvc := appp2p.New(p2pRepo, ledgerRepo, reservationRepo, userWalletAccountRepo, walletRepo, notificationRepo, decimal.NewFromInt(100)) // 1% commission

	affiliateService := appaffiliate.NewService(affiliateRepo, walletRepo, ledgerRepo, auditRepo)
	korzadiVPNService := appkorzadivpn.NewService(paymentOrderRepo, walletRepo, ledgerRepo, auditRepo)
	cryptoService := appcrypto.NewService(cryptoAddressRepo, custodyEncryptor, custodyAuditRepo, tronClient, usdtContract, bscProvider, bscCfg)

	// ---- Gas station (Bloque 6B): tops up user deposit addresses with the
	// native coin needed to pay gas, reused as-is from Bloque 1's BSC
	// withdrawal work — only its nonce coordination changed (now
	// PostgreSQL-backed via evmNonceRepo, never a bare PendingNonceAt).
	// Optional at boot: without GAS_STATION_*, EVM withdrawals whose source
	// address lacks native gas simply fail closed with "gas_unavailable"
	// instead of the backend refusing to start.
	evmNonceRepo := postgres.NewEVMNonceRepository(pool)
	addressKeyRepo := postgres.NewCryptoAddressKeyRepository(pool)

	var bscGasStation *bscwithdrawal.GasStation
	if bscProvider != nil {
		gasStationAddr := os.Getenv("GAS_STATION_ADDRESS")
		gasStationEncryptedKey := os.Getenv("GAS_STATION_ENCRYPTED_PRIVATE_KEY")
		if gasStationAddr != "" && gasStationEncryptedKey != "" {
			gasStationDec, err := custodyEncryptor.DecryptDetailed(gasStationEncryptedKey,
				cryptutil.ForEnvSecret("GAS_STATION_ENCRYPTED_PRIVATE_KEY", gasStationAddr))
			if err != nil {
				log.Fatalf("GAS_STATION_ENCRYPTED_PRIVATE_KEY inválida: %v", err)
			}
			if gasStationDec.NeedsReencrypt {
				// Una variable de entorno no se puede reescribir sola.
				log.Println("ADVERTENCIA: GAS_STATION_ENCRYPTED_PRIVATE_KEY está en formato legacy o con una clave " +
					"no activa — regenerar con la imagen de herramientas (ver docs/CUSTODY_KEY_MANAGEMENT.md).")
			}
			gasStationPrivateKey := gasStationDec.Plaintext
			chainID := new(big.Int).SetUint64(bscCfg.ChainID)
			bscGasStation = bscwithdrawal.NewGasStation(bscRawClient, gasStationAddr, gasStationPrivateKey, chainID).
				WithNonceAllocator(domain.NetworkBSC, evmNonceRepo)
			log.Println("BSC: gas station configurada (retiros de BSC podrán cubrir gas automáticamente)")
		} else {
			log.Println("BSC: gas station no configurada — los retiros de BSC solo funcionarán si la dirección de origen ya tiene gas propio")
		}
	}

	// ---- Blockchain deposit service (shared by every network/asset — spec §14) ----
	blockchainDepositService := appdeposit.NewBlockchainDepositService(
		ledgerService, clearingAccountRepo, userWalletAccountRepo, blockchainDepositRecorder,
	)

	// ---- Asset registry (Blockchain/Network/Asset/Contract/Standard/Decimals) ----
	// This is the piece that replaces "BSC hardcodes a single USDT contract"
	// with a real registry: every enabled network's assets (native coin +
	// whichever tokens have a *_CONTRACT env var set) are loaded from the
	// environment and upserted here, then watchers read from this table —
	// never from a client's own Config struct.
	networkAssetRepo := postgres.NewNetworkAssetRepository(pool)

	// Supervisor de workers: apagado ordenado (esperan a que termine lo que
	// están firmando/transmitiendo; ver internal/application/lifecycle).
	sup := lifecycle.New(context.Background())

	// AUDIT FIX: networks with a RUNNING deposit watcher. Only these may issue
	// deposit addresses (see AddressHandler.SetAllowedNetworks) so users are
	// never given an address on a network nobody is watching.
	var activeDepositNetworks []domain.Network

	// ---- BSC deposit watcher (background worker, not an HTTP handler — spec §10) ----
	// BSC now runs on exactly the same EVMDepositWatcher as every other EVM
	// network below — it is no longer a separate implementation, just the
	// first network this backend supported.
	if bscProvider != nil {
		bscAssets, err := assetregistry.LoadEVMNetworkAssets(domain.NetworkBSC, bscCfg.ConfirmationsRequired)
		if err != nil {
			log.Fatalf("configuración de assets de BSC inválida: %v", err)
		}
		if err := networkAssetRepo.UpsertMany(context.Background(), bscAssets); err != nil {
			log.Fatalf("no se pudo registrar los assets de BSC: %v", err)
		}

		bscWatcher := appwatcher.NewEVMDepositWatcher(
			bscProvider,
			addressToUserResolver,
			addressToUserResolver, // also implements WatchedAddressLister
			blockchainTransferRepo,
			blockchainSyncStateRepo,
			blockchainDepositService,
			appwatcher.Config{
				Network:               domain.NetworkBSC,
				Assets:                toWatcherAssets(bscAssets),
				ConfirmationsRequired: bscCfg.ConfirmationsRequired,
				PollInterval:          15 * time.Second,
				MaxBlocksPerScan:      100,
				SafetyLagBlocks:       3,
			},
		)
		bscWatcher.WithFailedEventStore(watcherFailedEventRepo)
		bscWatcher.WithIncidentReporter(depositIncidentRepo)
		bscWatcher.WithPostCreditRecheck(blockchainTransferRepo, 24*time.Hour)
		bscWatcher.WithSingleton(locker)
		sup.Go("watcher-bsc", bscWatcher.Run)
		activeDepositNetworks = append(activeDepositNetworks, domain.NetworkBSC)
		log.Printf("BSC: watcher iniciado con %d asset(s) (%s)", len(bscAssets), assetCodesOf(bscAssets))
	}

	// ---- Other EVM networks (Ethereum, Polygon, Arbitrum, Optimism, Base) ----
	// Each is entirely optional at boot, same pattern as BSC: if
	// {PREFIX}_RPC_URL isn't set, that network is simply skipped and the
	// rest of the wallet is unaffected. This loop is the direct payoff of
	// generalizing the EVM client: bringing up a sixth network took zero
	// new client code, only this loop plus its two env vars.
	//
	// evmProviders collects every dialed provider (BSC included, added
	// below) keyed by network — Bloque 6A's Fee Engine reuses these
	// exact instances to read gas prices, never dialing a second RPC
	// client of its own.
	evmProviders := map[domain.Network]*evmclient.Provider{}
	if bscProvider != nil {
		evmProviders[domain.NetworkBSC] = bscProvider
	}
	for _, netDefaults := range evmclient.KnownEVMNetworks() {
		if netDefaults.Network == domain.NetworkBSC {
			continue // handled above with its withdrawal-flow wiring
		}

		cfg, ok, err := evmclient.LoadConfigFromEnv(netDefaults)
		if err != nil {
			log.Fatalf("configuración de %s inválida: %v", netDefaults.Network, err)
		}
		if !ok {
			log.Printf("%s_RPC_URL no definido — %s no se activará hasta que se configure.", netDefaults.EnvPrefix, netDefaults.Network)
			continue
		}

		client, err := evmclient.Dial(context.Background(), cfg)
		if err != nil {
			log.Fatalf("no se pudo conectar al RPC de %s: %v", netDefaults.Network, err)
		}
		provider := evmclient.NewProvider(client, cfg)
		evmProviders[netDefaults.Network] = provider

		assets, err := assetregistry.LoadEVMNetworkAssets(netDefaults.Network, cfg.ConfirmationsRequired)
		if err != nil {
			log.Fatalf("configuración de assets de %s inválida: %v", netDefaults.Network, err)
		}
		if err := networkAssetRepo.UpsertMany(context.Background(), assets); err != nil {
			log.Fatalf("no se pudo registrar los assets de %s: %v", netDefaults.Network, err)
		}

		// Probe health with whichever token (if any) is configured, purely
		// for the startup log line — a network with no token configured
		// yet (native coin only) still starts normally.
		var probeContract string
		for _, a := range assets {
			if a.Kind == domain.AssetKindToken {
				probeContract = a.ContractAddress
				break
			}
		}
		health, err := provider.HealthCheck(context.Background(), probeContract)
		if err != nil {
			log.Fatalf("health check inicial de %s falló (rpc=%v chainOK=%v): %v", netDefaults.Network, health.RPCReachable, health.ChainIDOK, err)
		}
		log.Printf("%s conectado correctamente: chain_id=%d latest_block=%d", netDefaults.Network, health.ActualChainID, health.LatestBlock)

		watcher := appwatcher.NewEVMDepositWatcher(
			provider,
			addressToUserResolver,
			addressToUserResolver,
			blockchainTransferRepo,
			blockchainSyncStateRepo,
			blockchainDepositService,
			appwatcher.Config{
				Network:               netDefaults.Network,
				Assets:                toWatcherAssets(assets),
				ConfirmationsRequired: cfg.ConfirmationsRequired,
				PollInterval:          15 * time.Second,
				MaxBlocksPerScan:      2000,
				SafetyLagBlocks:       3,
			},
		)
		watcher.WithFailedEventStore(watcherFailedEventRepo)
		watcher.WithIncidentReporter(depositIncidentRepo)
		watcher.WithPostCreditRecheck(blockchainTransferRepo, 24*time.Hour)
		watcher.WithSingleton(locker)
		sup.Go("watcher-"+string(netDefaults.Network), watcher.Run)
		activeDepositNetworks = append(activeDepositNetworks, netDefaults.Network)
		log.Printf("%s: watcher iniciado con %d asset(s) (%s)", netDefaults.Network, len(assets), assetCodesOf(assets))
	}

	// ---- Bloque 3: registro de direcciones (Ethereum, Polygon, Arbitrum,
	// Optimism, Base, Bitcoin). Deliberately independent of whether the
	// Bitcoin RPC node below is configured — generating a deposit address
	// is pure key generation, it needs no live blockchain connection.
	btcAddressNetwork := btcwallet.Mainnet
	if os.Getenv("BTC_NETWORK") == "testnet" {
		btcAddressNetwork = btcwallet.Testnet
	}
	addressService := appwallet.NewAddressService(cryptoAddressRepo, custodyEncryptor, custodyAuditRepo, btcAddressNetwork)
	addressHandler := handlers.NewAddressHandler(addressService)

	// ---- Bitcoin (independent UTXO adapter — spec explicitly requires this NOT share the EVM interface) ----
	var btcProvider *btcclient.Provider
	btcCfg, btcOK, err := btcclient.LoadConfigFromEnv()
	if err != nil {
		log.Fatalf("configuración de Bitcoin inválida: %v", err)
	}
	if btcOK {
		btcRawClient, err := btcclient.Dial(context.Background(), btcCfg)
		if err != nil {
			log.Fatalf("no se pudo conectar al nodo de Bitcoin: %v", err)
		}
		btcProvider = btcclient.NewProvider(btcRawClient, btcCfg)

		health, err := btcProvider.HealthCheck(context.Background())
		if err != nil {
			log.Fatalf("health check inicial de Bitcoin falló: %v", err)
		}
		log.Printf("Bitcoin conectado correctamente: red=%s latest_block=%d", btcCfg.Network, health.LatestBlock)

		if err := networkAssetRepo.UpsertMany(context.Background(), []domain.NetworkAsset{{
			Network:               domain.NetworkBITCOIN,
			AssetCode:             "BTC",
			Kind:                  domain.AssetKindNative,
			Standard:              domain.TokenStandardNone,
			Decimals:              8,
			Enabled:               true,
			ConfirmationsRequired: btcCfg.ConfirmationsRequired,
		}}); err != nil {
			log.Fatalf("no se pudo registrar el asset BTC: %v", err)
		}

		btcWatcher := appwatcher.NewBTCDepositWatcher(
			btcProvider,
			addressToUserResolver,
			blockchainTransferRepo,
			blockchainSyncStateRepo,
			blockchainDepositService,
			"BTC",
			appwatcher.BTCConfig{
				ConfirmationsRequired: btcCfg.ConfirmationsRequired,
				PollInterval:          30 * time.Second,
				MaxBlocksPerScan:      50, // Bitcoin blocks carry far more transactions than a 2000-block EVM log scan — keep this small
				SafetyLagBlocks:       1,
			},
		)
		btcWatcher.WithSingleton(locker)
		sup.Go("watcher-bitcoin", btcWatcher.Run)
		activeDepositNetworks = append(activeDepositNetworks, domain.NetworkBITCOIN)
		log.Println("Bitcoin: watcher iniciado")
	} else {
		log.Println("BTC_RPC_URL no definido — Bitcoin no se activará hasta que se configure.")
	}

	// ---- TRON deposit watcher (Fase TRON) ----
	// Runs the SAME EVMDepositWatcher / blockchain_transfers / BlockchainDepositService
	// as every other network, over tronclient.Provider. TRON is registered in the asset
	// registry and in activeDepositNetworks ONLY when the watcher is active.
	tronWatcherEnabled := os.Getenv("TRON_DEPOSIT_WATCHER_ENABLED") == "true"
	if tronWatcherEnabled {
		tronAssets, err := assetregistry.LoadTronNetworkAssets(usdtContract, tronCfg.ConfirmationsRequired)
		if err != nil {
			log.Fatalf("configuración de assets de TRON inválida: %v", err)
		}
		if err := networkAssetRepo.UpsertMany(context.Background(), tronAssets); err != nil {
			log.Fatalf("no se pudo registrar los assets de TRON: %v", err)
		}
		var tronTokens []domain.NetworkAsset // the watcher only scans TRC-20 deposits
		for _, a := range tronAssets {
			if a.Kind == domain.AssetKindToken {
				tronTokens = append(tronTokens, a)
			}
		}
		tronWatcher := appwatcher.NewEVMDepositWatcher(
			tronclient.NewProvider(tronNode, tronCfg),
			addressToUserResolver,
			addressToUserResolver,
			blockchainTransferRepo,
			blockchainSyncStateRepo,
			blockchainDepositService,
			appwatcher.Config{
				Network:               domain.NetworkTRON,
				Assets:                toWatcherAssets(tronTokens),
				ConfirmationsRequired: tronCfg.ConfirmationsRequired,
				PollInterval:          9 * time.Second,
				MaxBlocksPerScan:      40, // 2 requests per block with events: keep scans small
				SafetyLagBlocks:       2,
			},
		)
		tronWatcher.WithFailedEventStore(watcherFailedEventRepo)
		tronWatcher.WithIncidentReporter(depositIncidentRepo)
		tronWatcher.WithPostCreditRecheck(blockchainTransferRepo, 24*time.Hour)
		tronWatcher.WithSingleton(locker)
		sup.Go("watcher-tron", tronWatcher.Run)
		activeDepositNetworks = append(activeDepositNetworks, domain.NetworkTRON)
		log.Printf("TRON: watcher iniciado con %d asset(s) (%s), %d confirmaciones", len(tronTokens), assetCodesOf(tronTokens), tronCfg.ConfirmationsRequired)
	} else {
		log.Println("TRON_DEPOSIT_WATCHER_ENABLED no es true — TRON no se registra ni emite direcciones de depósito.")
	}

	// ---- Bloque 6A: Universal Fee Engine ----
	// Calculates (never moves) the USDT fee for a future operation —
	// reuses every existing piece of infrastructure (EVM/Bitcoin
	// providers already dialed above, the asset registry, the Ledger's
	// reservation system) rather than building any of its own.
	feeCfg, err := appfee.LoadConfigFromEnv()
	if err != nil {
		log.Fatalf("configuración del Fee Engine inválida: %v", err)
	}

	feeUSDTAsset, feeAssetFound, err := ledgerAssetRepo.Find(context.Background(), "USDT")
	if err != nil {
		log.Fatalf("no se pudo leer el asset USDT para el Fee Engine: %v", err)
	}
	if !feeAssetFound {
		log.Fatalf("el Fee Engine requiere que USDT esté registrado en assets, pero no se encontró")
	}

	feePrimaryPriceProvider, err := appfee.NewCoinGeckoPriceProviderWithAPIKey(
		"https://api.coingecko.com/api/v3",
		nil,
		os.Getenv("FEE_PRICE_API_KEY"),
	)
	if err != nil {
		log.Fatalf("configuración del proveedor de precios del Fee Engine inválida: %v", err)
	}
	feeSecondaryPriceProvider, err := appfee.NewBinancePriceProvider(envOr("FEE_PRICE_SECONDARY_BASE_URL", "https://api.binance.com/api/v3"), nil)
	if err != nil {
		log.Fatalf("configuración del proveedor de respaldo de precios inválida: %v", err)
	}
	feePriceProvider, err := appfee.NewCachedFallbackPriceProvider(feePrimaryPriceProvider, feeSecondaryPriceProvider, feeCfg.PriceCacheTTL, feeCfg.MaxPriceAge)
	if err != nil {
		log.Fatalf("configuración de caché de precios del Fee Engine inválida: %v", err)
	}

	feeEstimators := []appfee.NetworkFeeEstimator{
		appfee.NewEVMFeeEstimator(evmProviders, feeCfg.GasLimitByNetwork),
		appfee.NewBitcoinFeeEstimator(btcProvider, feeCfg.BitcoinTxVBytes, 3),
	}

	if tronWatcherEnabled {
		feeEstimators = append(feeEstimators, appfee.NewTronFeeEstimator(
			tronNode, int64(envUint("TRON_FEE_ENERGY_UNITS", 78_000)), int64(envUint("TRON_FEE_BANDWIDTH_BYTES", 400))))
	}

	feeQuoteRepo := postgres.NewFeeQuoteRepository(pool)
	feeEngine := appfee.NewEngine(
		feeCfg, appfee.NewServiceFeePolicy(feeCfg), feeEstimators, feePriceProvider,
		networkAssetRepo, feeQuoteRepo, "USDT", feeUSDTAsset.Decimals,
	)
	feeHandler := handlers.NewFeeHandler(feeEngine)

	// ---- Bloque 6B: Withdrawal Service (retiros blockchain) ----
	//
	// Reuses everything above rather than re-implementing it: the same EVM
	// providers/RPC clients the watchers use (evmProviders), the same Asset
	// Registry, the same Fee Engine (feeEngine) and its ReservationService,
	// the same Ledger/Reservations, and the existing custody key store +
	// decryptor. Bitcoin stays DISABLED — no runtime is registered for it
	// and its Signer half unconditionally refuses (DisabledBitcoinSigner).
	feeReservationSvc := appfee.NewReservationService(ledgerService, walletRepo, feeQuoteRepo)

	blockchainWithdrawalRepo := postgres.NewBlockchainWithdrawalRepository(pool)
	// Camino B: the signer first looks for an ACTIVE custody HOT wallet at the
	// source address (withdrawals), then falls back to the user-scoped deposit
	// address key (sweeps).
	withdrawalKeys := bw.HotFirstKeyResolver{Hot: postgres.NewCustodyWalletRepository(pool), User: addressKeyRepo}
	evmSigner := bw.NewEVMSigner(withdrawalKeys, custodyEncryptor)
	// One Signer for the withdrawal service, routed by network family.
	withdrawalSigner := bw.FamilySigner{EVM: evmSigner, Tron: bw.NewTronSigner(withdrawalKeys, custodyEncryptor)}

	// ---- Gas station (Bloque 7.2): custody_wallets-backed automatic
	// refill, replacing the env-var-only bscGasStation above for whichever
	// network(s) an operator has actually registered a GAS custody wallet
	// (wallet_type=GAS, Bloque 7.1) plus an ENABLED gas_station_policies
	// row for. Registering that wallet/policy is an operator action (via
	// application/custody.Service + postgres.GasStationPolicyRepository,
	// no HTTP endpoint is wired for either in this block — see final
	// report), so until that is done a network behaves EXACTLY as before:
	// Gas stays nil and withdrawals whose source lacks native gas fail
	// closed with "gas_unavailable", never silently retrying forever.
	//
	// bscGasStation (env vars) is deliberately still checked FIRST for BSC
	// and takes precedence while configured, so an existing production
	// deployment is completely unaffected by this block — migrating BSC to
	// the new mechanism means unsetting GAS_STATION_* and registering a
	// custody_wallets GAS row instead.
	custodyWalletRepo := postgres.NewCustodyWalletRepository(pool)
	gasPolicyRepo := postgres.NewGasStationPolicyRepository(pool)
	gasRefillRepo := postgres.NewGasStationRefillRepository(pool)

	withdrawalNetworks := map[domain.Network]bw.NetworkRuntime{}
	for network, provider := range evmProviders {
		chain := evmclient.NewWithdrawalChain(provider.Client(), provider.Config())

		var gas bw.GasTopUp
		switch {
		case network == domain.NetworkBSC && bscGasStation != nil:
			gas = bscGasStation
		default:
			gasSvc, configured, err := newCustodyGasStation(context.Background(), network, custodyWalletRepo, gasPolicyRepo, gasRefillRepo, evmNonceRepo, chain, networkAssetRepo, custodyEncryptor, custodyAuditRepo)
			if err != nil {
				log.Fatalf("gas station (%s): %v", network, err)
			}
			if configured {
				gas = gasSvc
				log.Printf("%s: gas station configurada vía custody_wallets (Bloque 7.2)", network)
			} else {
				log.Printf("%s: gas station no configurada (sin wallet GAS activa + política habilitada en custody_wallets) — los retiros solo funcionarán si la dirección de origen ya tiene gas propio", network)
			}
		}
		withdrawalNetworks[network] = bw.NetworkRuntime{Chain: chain, Gas: gas}
	}

	withdrawalCfg, err := bw.LoadConfigFromEnv()
	if err != nil {
		log.Fatalf("configuración de retiros (Bloque 6B) inválida: %v", err)
	}
	if disabled := bw.DisabledAssets(); len(disabled) > 0 {
		log.Printf("retiros: los siguientes assets tienen límites incompletos (falta MAX o DAILY) y permanecerán deshabilitados: %v", disabled)
	}
	if withdrawalCfg.Enabled && !withdrawalCfg.MainnetEnabled {
		for network := range withdrawalNetworks {
			if evmclient.IsMainnetChainID(network, evmProviders[network].Config().ChainID) {
				log.Fatalf("retiros: WITHDRAWALS_ENABLED=true pero WITHDRAWALS_MAINNET_ENABLED no está activado y %s está configurado con un chain id de mainnet (%d) — fail closed", network, evmProviders[network].Config().ChainID)
			}
		}
	}

	// TRON withdrawals (opt-in). The EVM-only maps above (withdrawalNetworks) are left
	// untouched so sweeps / treasury / reconciliation never see a TRON runtime.
	allWithdrawalNetworks := make(map[domain.Network]bw.NetworkRuntime, len(withdrawalNetworks)+1)
	for n, rt := range withdrawalNetworks {
		allWithdrawalNetworks[n] = rt
	}
	if os.Getenv("TRON_WITHDRAWALS_ENABLED") == "true" {
		if !tronWatcherEnabled {
			log.Fatalf("TRON_WITHDRAWALS_ENABLED=true requiere TRON_DEPOSIT_WATCHER_ENABLED=true (TRON solo se registra con el watcher activo)")
		}
		if tronNetwork == "mainnet" && withdrawalCfg.Enabled && !withdrawalCfg.MainnetEnabled {
			log.Fatalf("retiros: TRON_NETWORK=mainnet pero WITHDRAWALS_MAINNET_ENABLED no está activado — fail closed")
		}
		tronChain := tronclient.NewWithdrawalChain(tronNode, tronCfg)
		var tronGas bw.GasTopUp
		gasSvc, configured, err := newTronCustodyGasStation(context.Background(), tronChain, custodyWalletRepo, gasPolicyRepo, gasRefillRepo, networkAssetRepo, custodyEncryptor, custodyAuditRepo)
		if err != nil {
			log.Fatalf("gas station (TRON): %v", err)
		}
		if configured {
			tronGas = gasSvc
			log.Println("TRON: gas station configurada vía custody_wallets (top-up de TRX para Energy/Bandwidth)")
		} else {
			log.Println("TRON: gas station no configurada — los retiros solo funcionarán si la HOT ya tiene TRX/recursos suficientes")
		}
		allWithdrawalNetworks[domain.NetworkTRON] = bw.NetworkRuntime{Resource: tronChain, Gas: tronGas}
		log.Println("TRON: retiros TRC-20 habilitados")
	}

	withdrawalSvc, err := bw.NewService(bw.Deps{
		Store:        blockchainWithdrawalRepo,
		Nonces:       evmNonceRepo,
		Ledger:       ledgerService,
		UserAccounts: userWalletAccountRepo,
		Clearing:     clearingAccountRepo,
		Fees:         feeEngine,
		FeeReserver:  feeReservationSvc,
		Quotes:       feeQuoteRepo,
		Users:        userRepo,
		Wallets:      walletRepo,
		LedgerAssets: ledgerAssetRepo,
		Registry:     networkAssetRepo,
		Addresses:    addressKeyRepo,
		// Camino B: withdrawals are paid from the custody HOT wallet of each
		// network/asset, never from a user deposit address.
		HotWallets: custodyWalletRepo,
		Signer:     withdrawalSigner,
		Networks:   allWithdrawalNetworks,
		Config:     withdrawalCfg,
		// Fase D: controles del Bloque 8 en los retiros (destino nuevo con espera,
		// velocidad, límites con reserva, aprobación manual y revalidación antes
		// de firmar) y aviso al titular.
		Security: securitySvc,
		Alerter:  accountService,
		// Varias réplicas: la sección "comprobar saldo + firmar" de cada HOT se
		// serializa con pg_advisory_lock (network+address).
		Locks:       locker,
		HotLockWait: time.Duration(envUint("HOT_LOCK_WAIT_SECONDS", 30)) * time.Second,
	})
	if err != nil {
		log.Fatalf("no se pudo inicializar el servicio de retiros: %v", err)
	}
	withdrawalWorker := bw.NewWorker(withdrawalSvc)
	sup.Go("withdrawal-worker", withdrawalWorker.Run)
	log.Printf("retiros: worker iniciado (enabled=%v mainnet_enabled=%v redes=%d)",
		withdrawalCfg.Enabled, withdrawalCfg.MainnetEnabled, len(withdrawalNetworks))

	// ---- Bloque 7.3: Sweeps ----
	//
	// Moves funds ALREADY credited to a user (via the deposit flow above)
	// from their deposit address to a TREASURY custody wallet (7.1). Reuses
	// EXACTLY the same instances the withdrawal service above was built
	// with — same EVM chains, same Gas Station per network, same nonce
	// store, same signer, same asset registry, same custody wallet/audit
	// repositories — nothing here is a second signer, nonce manager, gas
	// station or custody registry. See internal/application/sweep for the
	// full design and docs/SWEEPS_7_3.md for the block report.
	sweepRepo := postgres.NewSweepRepository(pool)
	sweepNetworks := map[domain.Network]appsweep.NetworkRuntime{}
	for network, rt := range withdrawalNetworks {
		sweepNetworks[network] = appsweep.NetworkRuntime{Chain: rt.Chain, Gas: rt.Gas}
	}

	sweepCfg, err := appsweep.LoadConfigFromEnv()
	if err != nil {
		log.Fatalf("configuración de sweeps (Bloque 7.3) inválida: %v", err)
	}
	if sweepCfg.Enabled && !sweepCfg.MainnetEnabled {
		for network := range sweepNetworks {
			if evmclient.IsMainnetChainID(network, evmProviders[network].Config().ChainID) {
				log.Fatalf("sweeps: SWEEPS_ENABLED=true pero SWEEPS_MAINNET_ENABLED no está activado y %s está configurado con un chain id de mainnet (%d) — fail closed", network, evmProviders[network].Config().ChainID)
			}
		}
	}

	sweepSvc, err := appsweep.NewService(appsweep.Deps{
		Store:      sweepRepo,
		Candidates: sweepRepo,
		Networks:   sweepNetworks,
		Nonces:     evmNonceRepo,
		Signer:     evmSigner,
		Registry:   networkAssetRepo,
		Wallets:    postgres.NewSweepCustodyWalletRegistry(custodyWalletRepo),
		Audit:      custodyAuditRepo,
		Config:     sweepCfg,
		IsMainnetChain: func(network domain.Network) bool {
			provider, ok := evmProviders[network]
			return ok && evmclient.IsMainnetChainID(network, provider.Config().ChainID)
		},
	})
	if err != nil {
		log.Fatalf("no se pudo inicializar el servicio de sweeps: %v", err)
	}
	sweepWorker := appsweep.NewWorker(sweepSvc).WithLocker(locker)
	sup.Go("sweep-worker", sweepWorker.Run)
	log.Printf("sweeps: worker iniciado (enabled=%v mainnet_enabled=%v redes=%d)",
		sweepCfg.Enabled, sweepCfg.MainnetEnabled, len(sweepNetworks))

	// ---- Bloque 7.4: Treasury / Rebalancing ----
	//
	// Moves funds BETWEEN a HOT and a TREASURY custody wallet Korzadi
	// already controls for one (network, asset_code) — reusing exactly the
	// same per-network Chain/Gas runtimes, the same nonce store, the same
	// custody wallet registry and the same custody audit log the
	// withdrawal/sweep wiring above already built. See
	// internal/application/treasury for the full design and
	// docs/TREASURY_REBALANCING_7_4.md for the block report. Policy CRUD
	// (register/list/enable a policy) has no HTTP endpoint wired in this
	// block either — same "internal, operator-only for now" posture 7.2's
	// gas_station_policies already has — so a fresh deployment behaves
	// exactly as before until an operator explicitly registers and enables
	// a treasury policy.
	treasuryPolicyRepo := postgres.NewTreasuryPolicyRepository(pool)
	treasuryRebalanceRepo := postgres.NewTreasuryRebalanceRepository(pool)
	treasuryNetworks := map[domain.Network]apptreasury.NetworkRuntime{}
	for network, rt := range withdrawalNetworks {
		treasuryNetworks[network] = apptreasury.NetworkRuntime{Chain: rt.Chain, Gas: rt.Gas}
	}

	treasuryCfg, err := apptreasury.LoadConfigFromEnv()
	if err != nil {
		log.Fatalf("configuración de treasury/rebalancing (Bloque 7.4) inválida: %v", err)
	}
	if treasuryCfg.Enabled && !treasuryCfg.MainnetEnabled {
		for network := range treasuryNetworks {
			if evmclient.IsMainnetChainID(network, evmProviders[network].Config().ChainID) {
				log.Fatalf("treasury: TREASURY_REBALANCE_ENABLED=true pero TREASURY_REBALANCE_MAINNET_ENABLED no está activado y %s está configurado con un chain id de mainnet (%d) — fail closed", network, evmProviders[network].Config().ChainID)
			}
		}
	}

	treasurySvc, err := apptreasury.NewService(apptreasury.Deps{
		Policies:   treasuryPolicyRepo,
		Rebalances: treasuryRebalanceRepo,
		Wallets:    postgres.NewTreasuryWalletRegistry(custodyWalletRepo),
		Networks:   treasuryNetworks,
		Nonces:     evmNonceRepo,
		Registry:   networkAssetRepo,
		Decryptor:  custodyEncryptor,
		Audit:      custodyAuditRepo,
		Config:     treasuryCfg,
		IsMainnetChain: func(network domain.Network) bool {
			provider, ok := evmProviders[network]
			return ok && evmclient.IsMainnetChainID(network, provider.Config().ChainID)
		},
	})
	if err != nil {
		log.Fatalf("no se pudo inicializar el servicio de treasury/rebalancing: %v", err)
	}
	treasuryWorker := apptreasury.NewWorker(treasurySvc).WithLocker(locker)
	sup.Go("treasury-worker", treasuryWorker.Run)
	log.Printf("treasury: worker iniciado (enabled=%v mainnet_enabled=%v redes=%d)",
		treasuryCfg.Enabled, treasuryCfg.MainnetEnabled, len(treasuryNetworks))

	// ---- Bloque 7.5: Reconciliation ----
	//
	// Read-only: compares live on-chain balances, this codebase's OWN
	// tracked movements (sweeps/treasury_rebalances/gas_station_refills)
	// and the Ledger's user totals — never modifies a balance. See
	// internal/application/reconciliation and
	// docs/RECONCILIATION_7_5.md.
	reconciliationRepo := postgres.NewReconciliationRepository(pool)
	reconciliationMovements := postgres.NewReconciliationMovementsRepository(pool)
	reconciliationNetworks := map[domain.Network]appreconciliation.NetworkRuntime{}
	for network, rt := range withdrawalNetworks {
		reconciliationNetworks[network] = appreconciliation.NetworkRuntime{Chain: rt.Chain}
	}

	reconciliationCfg, err := appreconciliation.LoadConfigFromEnv()
	if err != nil {
		log.Fatalf("configuración de reconciliation (Bloque 7.5) inválida: %v", err)
	}

	reconciliationSvc, err := appreconciliation.NewService(appreconciliation.Deps{
		Store:     reconciliationRepo,
		Movements: reconciliationMovements,
		Wallets:   postgres.NewReconciliationWalletSource(custodyWalletRepo),
		Networks:  reconciliationNetworks,
		Registry:  networkAssetRepo,
		Ledger:    postgres.NewLedgerFundsRepository(pool),
		Audit:     custodyAuditRepo,
		Config:    reconciliationCfg,
	})
	if err != nil {
		log.Fatalf("no se pudo inicializar el servicio de reconciliation: %v", err)
	}
	reconciliationWorker := appreconciliation.NewWorker(reconciliationSvc).WithLocker(locker)
	sup.Go("reconciliation-worker", reconciliationWorker.Run)
	log.Printf("reconciliation: worker iniciado (enabled=%v intervalo=%s redes=%d)",
		reconciliationCfg.Enabled, reconciliationCfg.PollInterval, len(reconciliationNetworks))

	// ---- Bloque 7.6: Recovery / Exceptions ----
	//
	// Cross-cutting supervisor over Gas Station (7.2), Sweeps (7.3) and
	// Treasury/Rebalancing (7.4) — never a second transaction engine. It
	// only clears a stale LOCK (handing a row back to its own subsystem's
	// worker) or flags needs_review; it never signs or broadcasts. See
	// internal/application/recovery and docs/RECOVERY_7_6.md. Detection
	// always runs even with RECOVERY_ENABLED=false (spec 7.6.2) — only the
	// unstick/escalate actions are gated.
	recoveryCfg, err := apprecovery.LoadConfigFromEnv()
	if err != nil {
		log.Fatalf("configuración de recovery (Bloque 7.6) inválida: %v", err)
	}
	recoverySvc, err := apprecovery.NewService(apprecovery.Deps{
		Stores: []apprecovery.RecoverableStore{
			postgres.NewRecoverableSweeps(pool),
			postgres.NewRecoverableTreasuryRebalances(pool),
			postgres.NewRecoverableGasStationRefills(pool),
		},
		Incidents: postgres.NewRecoveryRepository(pool),
		Audit:     custodyAuditRepo,
		Config:    recoveryCfg,
	})
	if err != nil {
		log.Fatalf("no se pudo inicializar el servicio de recovery: %v", err)
	}
	recoveryWorker := apprecovery.NewWorker(recoverySvc).WithLocker(locker)
	sup.Go("recovery-worker", recoveryWorker.Run)

	// Bloque 9 worker (KYC expiration + screening retry). Idempotent and
	// multi-instance safe; only started when explicitly enabled.
	if complianceCfg.WorkersEnabled {
		sup.Go("compliance-worker", appcompliance.NewWorker(complianceSvc, complianceCfg.WorkerInterval).Run)
	}

	// P2P Market: expiration worker (runs every 5 minutes by default)
	sup.Go("p2p-expiration", func(ctx context.Context) {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if cnt, err := p2pSvc.ExpireOrders(ctx, 100); err != nil {
					log.Printf("p2p expiration error: %v", err)
				} else if cnt > 0 {
					log.Printf("p2p: %d orden(es) expirada(s) y escrow devuelto", cnt)
				}
			}
		}
	})
	log.Printf("recovery: worker iniciado (enabled=%v intervalo=%s — detección siempre activa)",
		recoveryCfg.Enabled, recoveryCfg.PollInterval)

	// AUDIT FIX: manual (sandbox) confirmation of fiat deposits is OFF unless
	// explicitly enabled, and can never be enabled against TRON mainnet.
	fiatSandboxConfirm := os.Getenv("FIAT_SANDBOX_CONFIRM_ENABLED") == "true"
	if fiatSandboxConfirm {
		if tronNetwork == "mainnet" {
			log.Fatalf("FIAT_SANDBOX_CONFIRM_ENABLED=true no está permitido con TRON_NETWORK=mainnet — fail closed")
		}
		log.Println("ADVERTENCIA: FIAT_SANDBOX_CONFIRM_ENABLED=true — los depósitos fiat sandbox pueden confirmarse manualmente (solo para entornos no productivos).")
	}

	addressHandler.SetAllowedNetworks(activeDepositNetworks)
	log.Printf("redes habilitadas para depósitos cripto: %v (TRON solo si TRON_DEPOSIT_WATCHER_ENABLED=true)", activeDepositNetworks)

	// Operaciones admin sensibles (super_admin + 2FA + auditoría): resolver retiros
	// needs_review y registrar wallets HOT/GAS/TREASURY con sus políticas. Las wallets
	// se GENERAN en el servidor; importar una clave por API solo con
	// CUSTODY_ADMIN_KEY_IMPORT_ENABLED=true.
	custodyAdminSvc := appcustody.NewService(custodyWalletRepo, custodyEncryptor, custodyAuditRepo, registryCheck{repo: networkAssetRepo})
	adminRepo := postgres.NewAdminRepository(pool)
	adminAudit := appadmin.NewAuditLogger(adminRepo)
	adminSvc := appadmin.NewService(adminRepo, encryptor, emailSender, appBaseURL)
	adminOps := handlers.NewAdminOpsHandler(withdrawalSvc, custodyAdminSvc, treasurySvc, gasPolicyRepo, adminAudit, generateCustodyKey).
		AllowKeyImport(os.Getenv("CUSTODY_ADMIN_KEY_IMPORT_ENABLED") == "true").
		WithHotWalletReader(func(ctx context.Context, network, address string) (string, string, error) {
			switch network {
			case "TRON":
				p := tronclient.NewProvider(tronNode, tronCfg)
				usdt, err := p.TokenBalance(ctx, address, tronclient.USDTContractMainnet, 6)
				if err != nil {
					return "", "", fmt.Errorf("TRON USDT: %w", err)
				}
				native, err := p.NativeBalance(ctx, address)
				if err != nil {
					return "", "", fmt.Errorf("TRON TRX: %w", err)
				}
				return usdt.String(), native.String(), nil

			case "ETHEREUM", "BSC":
				var p *evmclient.Provider
				for n, candidate := range evmProviders {
					if string(n) == network {
						p = candidate
						break
					}
				}
				if p == nil {
					return "", "", fmt.Errorf("proveedor EVM no configurado para %s", network)
				}

				contract := os.Getenv("USDT_" + network + "_CONTRACT")
				decimals := int32(6)
				if contract == "" && network == "ETHEREUM" {
					contract = "0xdAC17F958D2ee523a2206206994597C13D831ec7"
				}
				if contract == "" && network == "BSC" {
					contract = "0x55d398326f99059fF775485246999027B3197955"
					decimals = 18
				}
				if contract == "" {
					return "", "", fmt.Errorf("contrato USDT no configurado para %s", network)
				}

				usdt, err := p.TokenBalance(ctx, address, contract, decimals)
				if err != nil {
					return "", "", fmt.Errorf("%s USDT: %w", network, err)
				}
				native, err := p.NativeBalance(ctx, address)
				if err != nil {
					return "", "", fmt.Errorf("%s saldo nativo: %w", network, err)
				}
				return usdt.String(), native.String(), nil

			default:
				return "", "", fmt.Errorf("red no soportada: %s", network)
			}
		})

	depositHandler := handlers.NewDepositHandler(depositService)
	destinationAdminHandler := handlers.NewDestinationAdminHandler(securityDestination, auditRepo)
	adminPanel := adminpanel.New(adminSvc, adminOps, securitySvc, p2pSvc, twoFactorService,
		!strings.EqualFold(envOr("ADMIN_COOKIE_SECURE", "true"), "false")).
		WithDepositConfirm(depositHandler, fiatSandboxConfirm).
		WithDestinationAdmin(destinationAdminHandler)
	authHandler := handlers.NewAuthHandler(authService).
		SetCookieSecure(!strings.EqualFold(envOr("AUTH_COOKIE_SECURE", "false"), "false"))

	h := router.Handlers{
		AdminOps:           adminOps,
		AdminPanel:         adminPanel,
		Auth:               authHandler,
		Account:            handlers.NewAccountHandler(accountService),
		DestinationAdmin:   destinationAdminHandler,
		Wallet:             handlers.NewWalletHandler(walletRepo, ledgerRepo, transferService, feePriceProvider),
		Addresses:          addressHandler,
		Deposit:            depositHandler,
		BlockchainDeposits: handlers.NewBlockchainDepositHandler(blockchainDepositHistory),
		Fees:               feeHandler,
		Withdrawal:         handlers.NewBlockchainWithdrawalHandler(withdrawalSvc),
		TwoFactor:          handlers.NewTwoFactorHandler(twoFactorService, userRepo).WithPasswordConfirmer(accountService).WithAudit(auditRepo).WithSessions(sessionRepo),
		SecurityPrefs:      handlers.NewSecurityPrefsHandler(prefsSvc, accountService),
		Affiliate:          handlers.NewAffiliateHandler(affiliateService),
		Admin:              handlers.NewAdminHandler(pool, userRepo, auditRepo, roleRepo),
		KorzadiVPN:         handlers.NewKorzadiVPNHandler(korzadiVPNService),
		Notification:       handlers.NewNotificationHandler(notificationRepo),
		Crypto:             handlers.NewCryptoHandler(cryptoService),
		Security:           handlers.NewSecurityHandler(securitySvc),
		Compliance:         handlers.NewComplianceHandler(complianceSvc, complianceRolesFn),
		P2P:                p2phandler.New(p2pSvc),
		DB:                 pool,
		Redis:              redisClient,
		HealthBSC: func(ctx context.Context) error {
			if bscProvider == nil {
				return fmt.Errorf("BSC no configurado")
			}
			health, err := bscProvider.HealthCheck(ctx, bscCfg.USDTContractAddress)
			if err != nil {
				return err
			}
			if !health.RPCReachable || !health.ChainIDOK {
				return fmt.Errorf("BSC no saludable")
			}
			return nil
		},
		HealthTRON: func(ctx context.Context) error {
			provider := tronclient.NewProvider(tronNode, tronCfg)
			health, err := provider.HealthCheck(ctx, usdtContract)
			if err != nil {
				return err
			}
			if !health.RPCReachable {
				return fmt.Errorf("TRON no alcanzable")
			}
			return nil
		},
	}

	cfg := router.Config{
		JWTManager:    jwtMgr,
		TwoFactorSvc:  twoFactorService,
		VPNHMACSecret: vpnHMACSecret,
		GetRolesForUser: func(userID string) ([]string, error) {
			id, err := uuid.Parse(userID)
			if err != nil {
				return nil, err
			}
			return roleRepo.GetRolesForUser(context.Background(), id)
		},
		AllowedOrigins:  parseAllowedOrigins(envOr("ALLOWED_ORIGINS", "http://localhost:5173")),
		RedisClient:     redisClient,
		SecuritySvc:     securitySvc,
		SecurityCounter: securityCounter,
		SecurityCfg:     securityCfg,

		FiatSandboxConfirmEnabled: fiatSandboxConfirm,
		TrustedProxies:            parseCSV(os.Getenv("TRUSTED_PROXIES")),
	}

	engine := router.New(h, cfg)

	server := &http.Server{
		Addr:              ":" + port,
		Handler:           engine,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("Korzadi Wallet backend escuchando en :%s (TRON_NETWORK=%s)", port, tronNetwork)

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("error al iniciar servidor: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Señal de apagado recibida: deteniendo HTTP y esperando a los workers...")
	// 1) HTTP: deja de aceptar y espera a las peticiones en curso.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("apagado HTTP forzado tras timeout: %v", err)
	}
	// 2) Workers: no toman trabajo nuevo y esperan a que termine lo que están
	// firmando/transmitiendo, hasta SHUTDOWN_WORKER_TIMEOUT_SECONDS (por defecto 60).
	workerTimeout := time.Duration(envUint("SHUTDOWN_WORKER_TIMEOUT_SECONDS", 60)) * time.Second
	if sup.Shutdown(workerTimeout) {
		log.Println("workers detenidos limpiamente")
	} else {
		log.Println("ADVERTENCIA: algunos workers se cancelaron por timeout; su estado está en PostgreSQL y se retomará por lease")
	}
	// 3) Los pools (defer pool.Close / lockPool.Close) se cierran al salir de main,
	// cuando ya no queda ningún worker usando la base de datos.
	log.Println("Backend detenido correctamente.")
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("variable de entorno requerida no definida: %s", key)
	}
	return v
}

// envUint reads a non-negative integer env var, falling back to def when unset
// or invalid (a bad value is logged, never silently trusted).
func envUint(key string, def uint64) uint64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		log.Printf("%s inválido (%q): se usa el valor por defecto %d", key, v, def)
		return def
	}
	return n
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func parseCSV(csv string) []string {
	var out []string
	for _, v := range strings.Split(csv, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func parseAllowedOrigins(csv string) map[string]bool {
	result := map[string]bool{}
	for _, o := range strings.Split(csv, ",") {
		o = strings.TrimSpace(o)
		if o != "" {
			result[o] = true
		}
	}
	return result
}

func bootstrapSystemAccounts(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, first_name, last_name, status, email_verified)
		VALUES ('00000000-0000-0000-0000-000000000001', 'system@korzadi.internal', 'disabled', 'System', 'Platform', 'active', true)
		ON CONFLICT (id) DO NOTHING;

		INSERT INTO wallets (id, user_id, status)
		VALUES ('00000000-0000-0000-0000-000000000002', '00000000-0000-0000-0000-000000000001', 'active')
		ON CONFLICT (id) DO NOTHING;

		INSERT INTO wallet_accounts (id, wallet_id, asset_code)
		VALUES ('00000000-0000-0000-0000-000000000003', '00000000-0000-0000-0000-000000000002', 'USD')
		ON CONFLICT (id) DO NOTHING;
	`)
	return err
}

// toWatcherAssets adapts the asset registry's domain.NetworkAsset rows into
// the watcher package's own EVMAsset shape — kept as separate types so the
// watcher package doesn't need to depend on the full AssetRegistry
// interface, only on the small slice it's handed at construction time.
func toWatcherAssets(assets []domain.NetworkAsset) []appwatcher.EVMAsset {
	out := make([]appwatcher.EVMAsset, 0, len(assets))
	for _, a := range assets {
		out = append(out, appwatcher.EVMAsset{
			AssetCode:       a.AssetCode,
			Kind:            a.Kind,
			ContractAddress: a.ContractAddress,
			Decimals:        a.Decimals,
		})
	}
	return out
}

func assetCodesOf(assets []domain.NetworkAsset) string {
	codes := make([]string, 0, len(assets))
	for _, a := range assets {
		codes = append(codes, a.AssetCode)
	}
	return strings.Join(codes, ", ")
}

// newCustodyGasStation builds a Bloque 7.2 gas station for `network` IF AND
// ONLY IF an active custody_wallets GAS wallet for its native asset exists
// AND has an enabled gas_station_policies row — otherwise it returns
// (nil, false, nil) so the caller leaves NetworkRuntime.Gas nil, preserving
// exactly today's fail-closed "gas_unavailable" behavior for a network no
// operator has configured yet (see the call site's comment for why this
// check happens at boot rather than unconditionally wiring the service in).
// newTronCustodyGasStation mirrors newCustodyGasStation for TRON: it is only
// "configured" when an ACTIVE GAS custody wallet for TRX AND an ENABLED policy exist.
func newTronCustodyGasStation(
	ctx context.Context,
	chain *tronclient.WithdrawalChain,
	wallets *postgres.CustodyWalletRepository,
	policies *postgres.GasStationPolicyRepository,
	refills *postgres.GasStationRefillRepository,
	assets *postgres.NetworkAssetRepository,
	decryptor *cryptutil.Encryptor,
	audit *postgres.CustodyAuditRepository,
) (*appgasstation.TronService, bool, error) {
	wallet, err := wallets.FindActive(ctx, "GAS", string(domain.NetworkTRON), "TRX")
	if err != nil {
		return nil, false, fmt.Errorf("buscar wallet GAS activa: %w", err)
	}
	if wallet == nil {
		return nil, false, nil
	}
	policy, err := policies.Get(ctx, wallet.ID)
	if err != nil {
		return nil, false, fmt.Errorf("leer política de gas station: %w", err)
	}
	if policy == nil || !policy.Enabled {
		return nil, false, nil
	}
	svc, err := appgasstation.NewTronService(appgasstation.TronDeps{
		Network: domain.NetworkTRON, Wallets: wallets, Policies: policies, Refills: refills,
		Assets: assets, Chain: chain, Decryptor: decryptor, Audit: audit,
	})
	if err != nil {
		return nil, false, err
	}
	return svc, true, nil
}

func newCustodyGasStation(
	ctx context.Context,
	network domain.Network,
	wallets *postgres.CustodyWalletRepository,
	policies *postgres.GasStationPolicyRepository,
	refills *postgres.GasStationRefillRepository,
	nonces *postgres.EVMNonceRepository,
	chain *evmclient.WithdrawalChain,
	assets *postgres.NetworkAssetRepository,
	decryptor *cryptutil.Encryptor,
	audit *postgres.CustodyAuditRepository,
) (*appgasstation.Service, bool, error) {
	enabledAssets, err := assets.ListEnabled(ctx, network)
	if err != nil {
		return nil, false, fmt.Errorf("leer asset registry: %w", err)
	}
	var nativeAssetCode string
	for _, a := range enabledAssets {
		if a.Kind == domain.AssetKindNative {
			nativeAssetCode = a.AssetCode
			break
		}
	}
	if nativeAssetCode == "" {
		return nil, false, nil // native asset not registered yet for this network
	}

	wallet, err := wallets.FindActive(ctx, "GAS", string(network), nativeAssetCode)
	if err != nil {
		return nil, false, fmt.Errorf("buscar wallet GAS activa: %w", err)
	}
	if wallet == nil {
		return nil, false, nil
	}

	policy, err := policies.Get(ctx, wallet.ID)
	if err != nil {
		return nil, false, fmt.Errorf("leer política de gas station: %w", err)
	}
	if policy == nil || !policy.Enabled {
		return nil, false, nil
	}

	svc, err := appgasstation.NewService(appgasstation.Deps{
		Network: network, Wallets: wallets, Policies: policies, Refills: refills,
		Nonces: nonces, Chain: chain, Assets: assets, Decryptor: decryptor, Audit: audit,
	})
	if err != nil {
		return nil, false, err
	}
	return svc, true, nil
}

// registryCheck adapta network_assets al validador del servicio de custodia.
type registryCheck struct {
	repo *postgres.NetworkAssetRepository
}

func (r registryCheck) IsEnabled(ctx context.Context, network domain.Network, assetCode string) (bool, error) {
	na, found, err := r.repo.Get(ctx, network, assetCode)
	if err != nil {
		return false, err
	}
	return found && na.Enabled, nil
}

// generateCustodyKey crea una clave nueva EN EL SERVIDOR para una wallet de
// custodia; la clave solo se usa para cifrarla y nunca se devuelve ni se registra.
func generateCustodyKey(addressType string) (address, privateKeyHex string, err error) {
	switch addressType {
	case "evm":
		kp, err := evmwallet.GenerateKeyPair()
		if err != nil {
			return "", "", err
		}
		return kp.Address, kp.PrivateKeyHex, nil
	case "tron":
		kp, err := tronwallet.GenerateKeyPair()
		if err != nil {
			return "", "", err
		}
		return kp.Address, kp.PrivateKeyHex, nil
	default:
		return "", "", fmt.Errorf("address_type no soportado para generar: %q", addressType)
	}
}
