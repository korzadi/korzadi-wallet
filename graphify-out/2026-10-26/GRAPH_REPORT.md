# Graph Report - korzadi-wallet  (2026-10-26)

## Corpus Check
- 492 files · ~398,388 words
- Verdict: corpus is large enough that graph structure adds value.
- Unclassified: 13 file(s) not represented in the graph (top: .css 5, (none) 4, .history 1)

## Summary
- 6149 nodes · 25892 edges · 200 communities (178 shown, 22 thin omitted)
- Extraction: 96% EXTRACTED · 4% INFERRED · 0% AMBIGUOUS · INFERRED: 1080 edges (avg confidence: 0.85)
- Token cost: 0 input · 0 output

## Graph Freshness
- Built from commit: `e829afed`
- Run `git rev-parse HEAD` and compare to check if the graph is stale.
- Run `graphify update .` after code changes (no API cost).

## Community Hubs (Navigation)
- backend/cmd/api/main.go
- testing.T
- Network
- newEnv
- RoleRepository
- DestinationStatus
- main
- Check
- kz-integration.js
- github.com/google/uuid.UUID
- Actor
- Client
- Item
- Service
- Repo
- AdminOpsHandler
- Sweep
- context.Context
- newHarness
- GenerateKeyPair
- btcclient/client.go
- newTronGasEnv
- testPool
- compliance_routes_test.go
- Withdrawal
- transfer/service_test.go
- Service
- btcwallet/keypair_test.go
- currentUserID
- time.Duration
- github.com/shopspring/decimal.Decimal
- aider chat started at 2026-10-09 12:21:08
- .Sign
- Chain
- korzadi-wallet/backend/internal/infrastructure/tronwallet/tx.go
- Price
- EVMDepositWatcher
- User
- Rebalance
- LoadConfigFromEnv
- watcher_resilience_test.go
- AAD
- memTx
- Korzadi Wallet
- WalletLayout
- Chain
- Service
- LedgerTransaction
- Bloque 7.3 -- Sweeps -- Informe final
- korzadi-wallet/backend/internal/infrastructure/tronclient/fullnode.go
- sync.Mutex
- accountaccess/service_test.go
- custody/service_test.go
- GasStationRefill
- AuditLogger
- package.json
- Service
- newTronHarness
- Service
- engine_test.go
- insertSweepEvent
- main.jsx
- newTronHarness
- MemStore
- evm_deposit_watcher_test.go
- Service
- NetworkAsset
- newHarness
- EVMDepositWatcher
- Bloque 8 — Security / Controles Operativos / Límites
- aider chat started at 2026-10-09 12:43:52
- newHarness
- OperationType
- twofactor/service_test.go
- BlockchainTransfer
- CustodyWallet
- BlockchainDepositRecord
- aider chat started at 2026-10-09 12:28:57
- Deps
- github.com/gin-gonic/gin.Context
- ComplianceHandler
- Service
- Flag
- KYCCase
- Hold
- Bloque 9 — KYC / AML / Compliance foundation
- NewVelocityTracker
- NewService
- complianceTx
- Bloque 7.7 — Tests / Auditoría del Bloque 7
- ParseSignedTx
- AdminAdvanced.jsx
- AdminHandler
- buildProvider
- newRig
- FreezeStatus
- TwoFactorHandler
- scanTreasuryRebalance
- ForCryptoAddress
- Landing.jsx
- fakeStore
- NetworkFeeEstimate
- harness
- validate_test.go
- time.Time
- NewDestinationGuard
- Deps
- fakeStore
- Bloque 7.4 — Treasury / Rebalancing
- broadcast.go
- newTronDepositEnv
- Bloque 7.6 — Recovery / Exceptions
- acctRouter
- Bloque 7.5 — Reconciliation
- .Sign
- WatcherFailedEventRepository
- Gestión de claves de custodia: cifrado v1, rotación y herramientas
- Locker
- Memory
- blockchain_confirm_test.go
- LoadEVMNetworkAssets
- newH2FAEnv
- WithdrawalChain
- ValidateAddress
- BuildTRC20TransferRaw
- Endurecimiento para varias réplicas
- .broadcastResourceStep
- StatusHistory
- Fase TRON: depósitos y retiros TRC-20 (USDT)
- NewLimitService
- Deps
- Fase TRON: depósitos y retiros TRC-20 (USDT)
- Fase C — Verificación de email, restablecimiento y cambio de contraseña
- acctUsers
- Fase E — 2FA endurecido
- user.js
- Store
- fakeNonces
- .broadcastResourceStep
- AuthHandler
- AGENTS.md
- Service
- ValidateSecrets
- .processClaimed
- GasStationPolicy
- Dependencias nuevas requeridas (Fase 1 — BSC)
- ReserveInput
- Service
- auth-E2E-01-login-y-persistencia-de-sesión/error-context.md
- dashboard-assets-E2E-02-panel-saldos-y-activos-EVM-TRON/error-context.md
- Service
- newTestUser
- New
- Transactions.jsx
- BlockchainWithdrawalHandler
- RiskProfile
- Backups fuera del VPS
- NewSecurityCounter
- diditCountries.js
- EnvKeyProvider
- .QueryRow
- Background
- Affiliate
- .authorizeCreate
- GasStation
- LoadConfigFromEnv
- Deposit
- StubKYCProvider
- RunWithLockTimeout
- .EstimateNetworkFee
- call
- github.com/gin-gonic/gin.Engine
- mailer_test.go
- AddressHandler
- main
- env
- Event
- NewFeeHandler
- .Sign
- ScreeningResult
- .Sign
- Withdrawal
- .FindEncryptedKeyWithAAD
- doIdem
- NewRedisRateLimiter
- PaymentOrder
- NewWorker
- Security.jsx
- fakeSigner
- blockchainwithdrawal/security_test.go
- fakeSigner
- blockingSender
- newFakeAssetValidator
- RestrictionError
- BlockRangeBeyondHeadError
- korzadi-wallet/backend/internal/domain/resource_chain.go
- korzadi-wallet/backend

## God Nodes (most connected - your core abstractions)
1. `Network` - 230 edges
2. `main()` - 181 edges
3. `main()` - 171 edges
4. `newEnv()` - 62 edges
5. `testPool()` - 57 edges
6. `User` - 53 edges
7. `currentUserID()` - 52 edges
8. `TransactionReceipt` - 51 edges
9. `KYCCase` - 50 edges
10. `newHarness()` - 48 edges

## Surprising Connections (you probably didn't know these)
- `newTronDepositEnv()` --calls--> `NewEVMDepositWatcher()`  [INFERRED]
  korzadi-wallet/backend/internal/application/watcher/tron_deposit_e2e_test.go → backend/internal/application/watcher/evm_deposit_watcher.go
- `newTronDepositEnv()` --calls--> `newFakeTransferStore()`  [INFERRED]
  korzadi-wallet/backend/internal/application/watcher/tron_deposit_e2e_test.go → backend/internal/application/watcher/evm_deposit_watcher_test.go
- `newTronDepositEnv()` --calls--> `newFakeSyncState()`  [INFERRED]
  korzadi-wallet/backend/internal/application/watcher/tron_deposit_e2e_test.go → backend/internal/application/watcher/evm_deposit_watcher_test.go
- `hexAddr()` --calls--> `tronAddressToHex()`  [INFERRED]
  korzadi-wallet/backend/internal/infrastructure/tronclient/fullnode.go → backend/internal/infrastructure/tronclient/base58.go
- `NewFullNode()` --calls--> `NewNodeRealClient()`  [INFERRED]
  korzadi-wallet/backend/internal/infrastructure/tronclient/fullnode.go → backend/internal/infrastructure/tronclient/nodereal.go

## Import Cycles
- None detected.

## Communities (200 total, 22 thin omitted)

### Community 0 - "backend/cmd/api/main.go"
Cohesion: 0.07
Nodes (48): newFakeLedgerAssets(), TronSignParams, coinGeckoSimplePriceResponse, BitcoinUTXO, BroadcastError, bareStore, fakeLedgerAssets, simpleErr (+40 more)

### Community 1 - "testing.T"
Cohesion: 0.02
Nodes (151): NewBackground(), slowEnv(), TestBackground_DropsWhenFull_AndRecoversFromPanic(), TestBackground_TaskTimeoutCancelsContext(), TestBackgroundVerifier_ReturnsImmediately_AndSends(), TestForgotPassword_Async_ResponseTimeIndependentOfSMTP(), TestResendVerification_Async_ResponseTimeIndependentOfSMTP(), hotHarness() (+143 more)

### Community 2 - "Network"
Cohesion: 0.03
Nodes (29): newFakeNonces(), Service, AddressBook, ClearingAccounts, HotWalletBook, NonceStore, tronKeys, newFakeNonces() (+21 more)

### Community 3 - "newEnv"
Cohesion: 0.12
Nodes (48): TestFlag_AutomatedAndAMLDisabled(), TestFlag_CreateDedupeResolve(), TestHold_NotOnSelfAndInvalidType(), TestHold_PlaceReleaseAffectsAuthorizationOnly(), TestRisk_CriticalPlacesInternalHold(), TestRisk_Transitions(), TestConcurrency_ApproveRejectRace(), TestConcurrency_DoubleStart() (+40 more)

### Community 5 - "DestinationStatus"
Cohesion: 0.12
Nodes (12): secDestKey(), DestinationGuard, DestinationStatus, HashDestination(), destKey(), newFakeDestinationRepo(), TestDestinationGuard_NewKnownBlocked(), TestMigration0019_BackfillHash_MatchesGoHash() (+4 more)

### Community 6 - "main"
Cohesion: 0.04
Nodes (100): bootstrapSystemAccounts(), envOr(), envUint(), main(), mustEnv(), parseAllowedOrigins(), parseCSV(), splitCSV() (+92 more)

### Community 7 - "Check"
Cohesion: 0.06
Nodes (26): DefaultConfig(), envBool(), LoadConfigFromEnv(), FundCategoryForWalletType(), TestFundCategoryForWalletType(), NewService(), strPtr(), weiToDecimalForAsset() (+18 more)

### Community 8 - "kz-integration.js"
Cohesion: 0.06
Nodes (96): actionsHtml(), addMessageNode(), addSystemNode(), afterNavigate(), amt(), api(), apiUrl(), ask2FA() (+88 more)

### Community 9 - "github.com/google/uuid.UUID"
Cohesion: 0.01
Nodes (80): authMemory, authSessionRepo, authUserRepo, authWalletRepo, Service, fakeLedger, newFakeAccounts(), newFakeClearing() (+72 more)

### Community 10 - "Actor"
Cohesion: 0.11
Nodes (14): cleanCountry(), Service, actorID(), sortCasesNewestFirst(), CanTransition(), validateAdminTransition(), ValidateTransition(), Actor (+6 more)

### Community 11 - "Client"
Cohesion: 0.07
Nodes (23): backoffDuration(), bounded1559FeeCaps(), Dial(), Client, isTransient(), redactURLErr(), TestBackoffDuration_RespectsMax(), TestBlockRangeErrorForObservedHeadIsTyped() (+15 more)

### Community 12 - "Item"
Cohesion: 0.05
Nodes (28): DefaultConfig(), envBool(), LoadConfigFromEnv(), TestIsLockExpired(), isLockExpired(), NewService(), insertRecoveryEvent(), scanRecoverableItems() (+20 more)

### Community 13 - "Service"
Cohesion: 0.15
Nodes (11): amountReservationReason(), feeReservationReason(), CreateInput, CreateResult, Service, isInsufficientFunds(), sanitizeCause(), TestValidateTronDestination() (+3 more)

### Community 14 - "Repo"
Cohesion: 0.06
Nodes (15): Repo, itoa(), CreateOfferInput, CreateOrderInput, Dispute, DisputeStatus, Message, noopNotifier (+7 more)

### Community 15 - "AdminOpsHandler"
Cohesion: 0.09
Nodes (21): CustodyWallet, ListFilter, RegisterInput, Status, badRequest(), gasPolicyView(), AdminOpsHandler, NewAdminOpsHandler() (+13 more)

### Community 16 - "Sweep"
Cohesion: 0.13
Nodes (12): TestSanitizeCause_TruncatesAndStaysSingleLine(), Backoff(), classifyBroadcastError(), Service, isRevertError(), gasOwner(), Service, nonceOwner() (+4 more)

### Community 17 - "context.Context"
Cohesion: 0.01
Nodes (59): newFakeChain(), EVMChain, GasTopUp, NetworkRuntime, ResolveGasFeeParams(), gasStub, fakeTronParams, fakeChain (+51 more)

### Community 18 - "newHarness"
Cohesion: 0.11
Nodes (36): containsSubstring(), TestAdvance_ResumeAfterRestart_UsesPersistedSignedTxWithoutResigning(), TestAudit_LogsAcrossLifecycle(), TestBroadcastStep_AmbiguousNeverAutoRebroadcasts(), TestBroadcastStep_RejectedFailsClosed(), TestBroadcastStep_Success(), TestComplete_NeverTouchesLedger(), TestConfirmStep_ReachesRequiredConfirmations_Completes() (+28 more)

### Community 19 - "GenerateKeyPair"
Cohesion: 0.06
Nodes (38): TronSigner, NewTronSigner(), mustTronKP(), TestTronSigner_RefusesNativeAndEVMRequests(), TestTronSigner_RefusesWrongSourceKey(), newFakeWallets(), tronGasEnv, newTronGasEnv() (+30 more)

### Community 20 - "btcclient/client.go"
Cohesion: 0.07
Nodes (26): asRPCError(), btcToSats(), Dial(), sign(), TestBtcToSats_ExactConversion(), TestIsTransient_NotFoundIsNotRetried(), TestIsTransient_RPCWarmupIsRetried(), TestLoadConfigFromEnv_Configured() (+18 more)

### Community 21 - "newTronGasEnv"
Cohesion: 0.23
Nodes (12): NewWithdrawalChain(), newFakeWallets(), newTronGasEnv(), TestTronGas_AmbiguousBroadcastIsReconciledNotResent(), TestTronGas_DefinitiveRejectionMarksFailed(), TestTronGas_DisabledAndMissingWallet(), TestTronGas_LostTxExpiresAndIsPlannedAgain(), TestTronGas_NeverLeaksPrivateKey() (+4 more)

### Community 22 - "testPool"
Cohesion: 0.11
Nodes (60): New(), NewAccountTokenRepository(), NewAssetRepository(), UserWalletAccountRepository, NewUserWalletAccountRepository(), NewLedgerRepository(), NewP2PRepository(), New() (+52 more)

### Community 23 - "compliance_routes_test.go"
Cohesion: 0.17
Nodes (29): Service, sanitizeMeta(), Body(), Sign(), sign(), TestPrivacy_ErrorsAndLogsDoNotLeak(), WebhookResult, TestWebhook_InvalidSignature() (+21 more)

### Community 24 - "Withdrawal"
Cohesion: 0.08
Nodes (15): CanTransition(), Status, Withdrawal, Event, Store, Update, insertWithdrawalEvent(), scanBlockchainWithdrawal() (+7 more)

### Community 25 - "transfer/service_test.go"
Cohesion: 0.05
Nodes (49): LedgerAssets, newFakeLedgerAssetsAdapter(), decimalPlaces(), LedgerAsset, Service, sameTransferRequest(), newFakeAssets(), newFakeLedger() (+41 more)

### Community 26 - "Service"
Cohesion: 0.10
Nodes (9): DefaultConfig(), envBool(), LoadConfigFromEnv(), actorOrSystem(), Service, NewService(), sanitizeCause(), Config (+1 more)

### Community 27 - "btcwallet/keypair_test.go"
Cohesion: 0.10
Nodes (24): NewAddressService(), toPublicAddress(), AddressFromPublicKey(), encodeSegwitAddress(), GenerateKeyPair(), Network, hash160(), TestAddressFromPublicKey_DeterministicForSameKey() (+16 more)

### Community 28 - "currentUserID"
Cohesion: 0.09
Nodes (14): AccountHandler, AffiliateHandler, parseAndValidateAmount(), respondDomainError(), BlockchainDepositHandler, toDepositResponse(), CryptoHandler, publicHealthError() (+6 more)

### Community 29 - "time.Duration"
Cohesion: 0.15
Nodes (11): NewRateLimiter(), backoff(), classifyBroadcastError(), broadcastClass, Service, isFinalFailure(), nonceOwner(), TestClassifyTronBroadcastErrors() (+3 more)

### Community 30 - "github.com/shopspring/decimal.Decimal"
Cohesion: 0.02
Nodes (56): InFlightReader, ToRawUnits(), ValidateAmount(), toWei(), toRawUnits(), BlockchainProvider, OnChainNativeTransferEvent, OnChainTransferEvent (+48 more)

### Community 31 - "aider chat started at 2026-10-09 12:21:08"
Cohesion: 0.06
Nodes (33): 1. BNB Smart Chain — BEP-20., , 2. Ethereum — ERC-20., , , , 3. TRON — TRC-20, únicamente cuando el contrato oficial del activo esté verificado.,  (+25 more)

### Community 32 - ".Sign"
Cohesion: 0.12
Nodes (23): Decryptor, DisabledBitcoinSigner, EVMSigner, KeyResolver, SignedTx, Signer, SignRequest, NewEVMSigner() (+15 more)

### Community 33 - "Chain"
Cohesion: 0.09
Nodes (9): addr20(), fromHex41(), account, block, Chain, tx, New(), topicAddr() (+1 more)

### Community 34 - "korzadi-wallet/backend/internal/infrastructure/tronwallet/tx.go"
Cohesion: 0.15
Nodes (27): AddressFromBytes21(), AddressToBytes21(), allZero(), BuildTRC20TransferRaw(), BuildTRXTransferRaw(), ParsedTx, TRC20TransferParams, TRXTransferParams (+19 more)

### Community 35 - "Price"
Cohesion: 0.10
Nodes (25): NewBinancePriceProvider(), errorsJoin(), NewCachedFallbackPriceProvider(), validatePrice(), validCachedPrice(), CoinGeckoPriceProvider, minTimestamp(), NewCoinGeckoPriceProviderWithAPIKey() (+17 more)

### Community 36 - "EVMDepositWatcher"
Cohesion: 0.08
Nodes (9): Config, EVMDepositWatcher, DepositIncident, DepositIncidentKind, FailedDepositEvent, CreditedTransferLister, FailedEventStore, fakeIncidents (+1 more)

### Community 37 - "User"
Cohesion: 0.07
Nodes (10): fakeUsers, newFakeUsers(), UserReader, User, UserStatus, UserRepository, fakeUsers, UserReader (+2 more)

### Community 38 - "Rebalance"
Cohesion: 0.15
Nodes (14): classifyBroadcastError(), Service, insufficientCode(), insufficientErr(), isRevertError(), TestClassifyBroadcastError(), TestClassifyBroadcastError_Nil(), TestIsRevertError() (+6 more)

### Community 39 - "LoadConfigFromEnv"
Cohesion: 0.50
Nodes (5): DefaultConfig(), envBool(), LoadConfigFromEnv(), parsePositiveDecimal(), Config

### Community 40 - "watcher_resilience_test.go"
Cohesion: 0.20
Nodes (23): newFakeEVMProvider(), credited(), fakeTransferStore, newFakeFailedStore(), newRecheckWatcher(), newRWatcher(), rEvent(), rRow() (+15 more)

### Community 41 - "AAD"
Cohesion: 0.11
Nodes (11): KeySourceResolver, tronDec, AAD, Encryptor, Decrypted, Rewriter, Encryptor, fakeEncryptor (+3 more)

### Community 42 - "memTx"
Cohesion: 0.07
Nodes (6): ScreeningRequest, WebhookEvent, WebhookProcessing, scanScreenReq(), ScreeningRequestStatus, memTx

### Community 43 - "Korzadi Wallet"
Cohesion: 0.07
Nodes (28): Checklist de producción — Korzadi Wallet, Criterio de estado, Dependencias externas pendientes, Estado por componente, Migraciones P2P, P2P Market (Completado), Requisitos antes de habilitar producción, Riesgos técnicos (+20 more)

### Community 44 - "WalletLayout"
Cohesion: 0.11
Nodes (33): walletApi, WalletLayout(), getUser(), Deposits(), STATUS, COUNTRIES, KYC(), LABELS (+25 more)

### Community 45 - "Chain"
Cohesion: 0.10
Nodes (8): addr20(), fromHex41(), account, block, Chain, tx, New(), topicAddr()

### Community 46 - "Service"
Cohesion: 0.13
Nodes (9): generateRecoveryCodes(), Service, matchStep(), normalizeRecovery(), recoveryHash(), TestRecoveryHash_Formats(), Alerter, Repository (+1 more)

### Community 47 - "LedgerTransaction"
Cohesion: 0.05
Nodes (55): NewService(), TestSettleReservations_DelegatesToSettler(), TestSettleReservations_FailsClosedWithoutSettler(), TestSettleReservations_PropagatesRepositoryErrorUnwrapped(), TestSettleReservations_RejectsInvalidInputBeforeRepository(), validSettlement(), newFakeRepo(), newFakeReservations() (+47 more)

### Community 48 - "Bloque 7.3 -- Sweeps -- Informe final"
Cohesion: 0.08
Nodes (23): 0. Estado de la verificacion (leer primero), 10. Como se evita el doble sweep, 11. Como se integra Gas Station, 12. Como se gestiona el nonce, 13. Como se firma, 14. Como se hace broadcast, 15. Como se manejan los broadcasts ambiguos, 16. Estados del sweep (+15 more)

### Community 49 - "korzadi-wallet/backend/internal/infrastructure/tronclient/fullnode.go"
Cohesion: 0.07
Nodes (22): decodeNodeMessage(), AccountResources, BlockRef, blockResp, FullNode, TxInfo, TxLog, hexAddr() (+14 more)

### Community 50 - "sync.Mutex"
Cohesion: 0.20
Nodes (4): notifRec, fakeEventRepo, fakeAudit, fakeAuditLogger

### Community 51 - "accountaccess/service_test.go"
Cohesion: 0.12
Nodes (30): auditRec, env, fake2FA, fakeSessions, recSender, sentMail, Service, newEnv() (+22 more)

### Community 52 - "custody/service_test.go"
Cohesion: 0.16
Nodes (25): newFakeRepository(), newTestService(), TestGet_ReturnsSameDataAsRegistered(), TestList_FiltersByWalletType(), TestRegister_AuditEventNeverContainsPrivateKey(), TestRegister_CreatesGasWallet(), TestRegister_CreatesHotWallet(), TestRegister_CreatesTreasuryWallet() (+17 more)

### Community 53 - "GasStationRefill"
Cohesion: 0.03
Nodes (27): newCustodyGasStation(), newTronCustodyGasStation(), NewService(), fakeRefills, newFakeRefills(), TestNewService_RejectsNonEVMNetwork(), classifyTronBroadcast(), tronBroadcastClass (+19 more)

### Community 54 - "AuditLogger"
Cohesion: 0.20
Nodes (21): Service, NewService(), Service, NewService(), Service, NewService(), DestinationGuard, NewService() (+13 more)

### Community 55 - "package.json"
Cohesion: 0.06
Nodes (31): dependencies, axios, lucide-react, qrcode.react, react, react-dom, react-router-dom, devDependencies (+23 more)

### Community 56 - "Service"
Cohesion: 0.10
Nodes (17): TestTwoInstances_ConcurrentWorkers_NeverOverCommitHotWallet(), backoff(), amountReservationReason(), feeReservationReason(), CreateInput, CreateResult, Service, isFinalFailure() (+9 more)

### Community 57 - "newTronHarness"
Cohesion: 0.19
Nodes (19): fakeLedger, tronHarness, hasEvent(), newTronHarness(), reservationStatus(), TestTron_AmbiguousBroadcast_AcceptedByNode_NeverSentTwice(), TestTron_AmbiguousBroadcast_Lost_ResendsSameBytesBeforeExpiry(), TestTron_FeeSpikeBeyondQuote_FailsBeforeSigning() (+11 more)

### Community 58 - "Service"
Cohesion: 0.06
Nodes (23): FakeKYCProvider, NewFakeKYCProvider(), CreateVerificationInput, CreateVerificationResult, ProviderEvent, ProviderStatus, NewRegistry(), Service (+15 more)

### Community 59 - "engine_test.go"
Cohesion: 0.05
Nodes (54): newFakeAddresses(), newFakeFeeEngine(), FeeEngine, harness, tronFees, NewEngine(), baseConfig(), freshPrice() (+46 more)

### Community 60 - "insertSweepEvent"
Cohesion: 0.12
Nodes (11): CanTransition(), nullIfEmpty(), insertSweepEvent(), scanSweep(), sweepExecer, SweepRepository, sweepRowScanner, Event (+3 more)

### Community 61 - "main.jsx"
Cohesion: 0.08
Nodes (24): affiliateAPI, api, authAPI, cryptoAPI, feeAPI, idempotencyKey(), kycAPI, notificationAPI (+16 more)

### Community 62 - "newTronHarness"
Cohesion: 0.19
Nodes (19): fakeLedger, tronHarness, hasEvent(), newTronHarness(), reservationStatus(), TestTron_AmbiguousBroadcast_AcceptedByNode_NeverSentTwice(), TestTron_AmbiguousBroadcast_Lost_ResendsSameBytesBeforeExpiry(), TestTron_FeeSpikeBeyondQuote_FailsBeforeSigning() (+11 more)

### Community 63 - "MemStore"
Cohesion: 0.14
Nodes (6): currentCases(), MemStore, newState(), RiskHistoryEntry, idemRow, state

### Community 64 - "evm_deposit_watcher_test.go"
Cohesion: 0.29
Nodes (17): NewEVMDepositWatcher(), fakeTransferStore, newFakeSyncState(), newFakeTransferStore(), TestEVMDepositWatcher_ConfirmedTransferStillMonitoredForReorg(), TestEVMDepositWatcher_DetectsTokenTransferToManagedAddress(), TestEVMDepositWatcher_DuplicateEventNeverProcessedTwice(), TestEVMDepositWatcher_FailedFutureRangeRetryDoesNotAdvanceCheckpoint() (+9 more)

### Community 65 - "Service"
Cohesion: 0.11
Nodes (12): Deps, SessionRevoker, TwoFAChecker, UserStore, Service, hashToken(), NewService(), newToken() (+4 more)

### Community 66 - "NetworkAsset"
Cohesion: 0.07
Nodes (22): registryCheck, assetCodesOf(), toWatcherAssets(), toWatcherAssets(), newFakeRegistry(), fakeAssets, newFakeRegistry(), Config (+14 more)

### Community 67 - "newHarness"
Cohesion: 0.15
Nodes (25): generateCustodyKey(), GenerateKeyPair(), keyPairFromPrivateKey(), TestGenerateKeyPair_NeverProducesDuplicateAddresses(), TestGenerateKeyPair_PrivateKeyRoundTrips(), TestGenerateKeyPair_ProducesChecksummedAddress(), TestValidateAddress_RejectsBadChecksum(), VerifySender() (+17 more)

### Community 68 - "EVMDepositWatcher"
Cohesion: 0.08
Nodes (19): NewBTCDepositWatcher(), satsToBTC(), newFakeUTXOProvider(), TestBTCDepositWatcher_ConfirmsAndCreditsAfterEnoughConfirmations(), TestBTCDepositWatcher_DetectsOutputToManagedAddress(), TestBTCDepositWatcher_ReorgMarksTransferReorged(), TestBTCDepositWatcher_RetriesCreditAfterTransientFailure(), TestBTCDepositWatcher_TwoOutputsSameTxAreBothTracked() (+11 more)

### Community 69 - "Bloque 8 — Security / Controles Operativos / Límites"
Cohesion: 0.10
Nodes (20): 0. Qué NO reimplementa este bloque, 10. Kill switches (spec §20), 11. Emergency freeze (spec §21), 12. Concurrencia (spec §22–§23), 13. API administrativa, 14. Amenazas cubiertas, 15. Amenazas fuera de alcance de este bloque, 16. Estado de build/test (+12 more)

### Community 70 - "aider chat started at 2026-10-09 12:43:52"
Cohesion: 0.10
Nodes (20): , , , , , , , 1. Evidencia de los archivos y funciones que intervienen. (+12 more)

### Community 71 - "newHarness"
Cohesion: 0.18
Nodes (23): planRefill(), bnb(), newHarness(), TestEnsureGasFor_AuditsConfirmedRefill(), TestEnsureGasFor_AuditsFailedRefill(), TestEnsureGasFor_BelowMinimum_Refills(), TestEnsureGasFor_BelowRequired_Refills(), TestEnsureGasFor_BroadcastAmbiguous_NeverAutoRetries() (+15 more)

### Community 72 - "OperationType"
Cohesion: 0.13
Nodes (10): Policy, ValidatePolicyShape(), TestValidatePolicyShape(), OperationType, categoryFor(), nullableInt(), secPolicies, SecurityFreezeRepository (+2 more)

### Community 73 - "twofactor/service_test.go"
Cohesion: 0.15
Nodes (25): NewService(), newEnabledEnv(), TestAdminReset_DeletesCredentials_AndAlerts(), TestBeginSetup_RecoveryCodes_Are80BitsAndDistinct(), TestBeginSetup_RefusesWhenAlreadyEnabled(), TestConfirm_ConsumesCode_NoImmediateReplayForWithdrawal(), TestConfirm_WithoutSetup_StableError(), TestConfirm_WrongCode_RejectedAndCounted() (+17 more)

### Community 74 - "BlockchainTransfer"
Cohesion: 0.16
Nodes (5): keyOf(), BlockchainTransfer, BlockchainTransferRepository, fakeFailedRow, fakeFailedStore

### Community 75 - "CustodyWallet"
Cohesion: 0.06
Nodes (18): fakeWallets, CustodyWallet, CustodyWalletFilter, CustodyWalletRepository, scanCustodyWallet(), fakeRepository, Repository, WalletRegistry (+10 more)

### Community 76 - "BlockchainDepositRecord"
Cohesion: 0.15
Nodes (16): BlockchainDepositRecord, HistoryService, NewHistoryService(), newFakeDepositReader(), sampleRecord(), TestGetForUser_ReturnsErrDepositNotFound_WhenBelongsToAnotherUser(), TestGetForUser_ReturnsErrDepositNotFound_WhenIDDoesNotExist(), TestGetForUser_ReturnsRecordForOwner() (+8 more)

### Community 77 - "aider chat started at 2026-10-09 12:28:57"
Cohesion: 0.10
Nodes (20): , 1. Archivos concretos encontrados., 2. Estado real de cada una de las seis combinaciones de activo y red., 3. Errores demostrables y pruebas existentes., 4. Correcciones mínimas necesarias, por prioridad., 5. Comandos de verificación seguros., ,  (+12 more)

### Community 78 - "Deps"
Cohesion: 0.10
Nodes (3): Deps, NewService(), TestNewService_RejectsNonEVMNetwork()

### Community 79 - "github.com/gin-gonic/gin.Context"
Cohesion: 0.14
Nodes (7): getUserID(), New(), pageParams(), SecurityHandler, parseOptionalDecimal(), twoFAHeader(), Handler

### Community 80 - "ComplianceHandler"
Cohesion: 0.28
Nodes (6): complianceError(), ComplianceHandler, parseUUIDParam(), requestID(), toCaseDTO(), caseDTO

### Community 81 - "Service"
Cohesion: 0.09
Nodes (9): Service, isRevertError(), ResourceChain, sunToTRX(), Service, gasOwner(), nonceOwner(), WeiToDecimal() (+1 more)

### Community 82 - "Flag"
Cohesion: 0.15
Nodes (9): boolStr(), Service, Flag, scanFlag(), FlagSource, FlagStatus, FlagType, ManualFlagInput (+1 more)

### Community 83 - "KYCCase"
Cohesion: 0.09
Nodes (5): Tx, KYCCase, collectCases(), Store, ComplianceRepository

### Community 84 - "Hold"
Cohesion: 0.18
Nodes (9): claim(), hashParts(), validReason(), Hold, HoldType, scanHold(), HoldStatus, PlaceHoldInput (+1 more)

### Community 85 - "Bloque 9 — KYC / AML / Compliance foundation"
Cohesion: 0.11
Nodes (17): 10. Auditoría, 11. Privacidad y cifrado, 12. Retención (política — **no** inventar plazos), 13. Configuración, 14. API, 15. Concurrencia y workers, 16. Límites del sistema y qué depende de terceros, 1. Arquitectura (+9 more)

### Community 86 - "NewVelocityTracker"
Cohesion: 0.15
Nodes (16): DefaultConfig(), defaultRateLimits(), defaultVelocity(), envBool(), Config, RateLimitRule, LoadConfigFromEnv(), parseRateLimitRule() (+8 more)

### Community 87 - "NewService"
Cohesion: 0.15
Nodes (10): actorOrSystem(), NewService(), toPublic(), ValidateAddress(), ValidateAddressType(), validateEVMAddress(), ValidateStatus(), ValidateWalletType() (+2 more)

### Community 88 - "complianceTx"
Cohesion: 0.21
Nodes (4): isUniqueViolation(), nullString(), complianceTx, dbq

### Community 89 - "Bloque 7.7 — Tests / Auditoría del Bloque 7"
Cohesion: 0.12
Nodes (15): 0. Aviso honesto de entorno (léase antes que todo lo demás), 10. Veredicto final, 1. Qué se pudo ejecutar en este entorno, 2. Resumen por bloque, 3. Tests escritos en este entorno, 4. Matriz de pruebas (spec 7.7) — estado real, 5. Seguridad (7.7), 6. Concurrencia (7.7) (+7 more)

### Community 90 - "ParseSignedTx"
Cohesion: 0.14
Nodes (17): AddressFromBytes21(), allZero(), ParseSignedTx(), pbReadVarint(), pbWalk(), SignRaw(), buildSigned(), mustKP() (+9 more)

### Community 91 - "AdminAdvanced.jsx"
Cohesion: 0.15
Nodes (18): clearTokens(), Admin(), logout(), date(), routes, shortId(), Status(), Table() (+10 more)

### Community 92 - "AdminHandler"
Cohesion: 0.16
Nodes (4): AdminHandler, parsePagination(), emailVerifierStore, RoleAssigner

### Community 93 - "buildProvider"
Cohesion: 0.13
Nodes (19): buildProvider(), envOr(), LoadConfigFromEnv(), looksLikeEVMAddress(), clearBSCEnv(), TestLoadConfigFromEnv_InvalidContractFormat(), TestLoadConfigFromEnv_MissingContract(), TestLoadConfigFromEnv_MissingRPCURL() (+11 more)

### Community 94 - "newRig"
Cohesion: 0.29
Nodes (15): NewMemStore(), idem(), idem2fa(), newRig(), TestRoutes_AdminViewDoesNotLeakSecretsOrProviderRefs(), TestRoutes_HoldFlow(), TestRoutes_IDOR(), TestRoutes_ManualReviewFlowAndRBACSplit() (+7 more)

### Community 95 - "FreezeStatus"
Cohesion: 0.23
Nodes (4): FreezeStatus, newFakeFreezeRepo(), secFreezes, fakeFreezeRepo

### Community 96 - "TwoFactorHandler"
Cohesion: 0.14
Nodes (7): LockedError, TwoFactorHandler, respondTwoFAError(), respondTwoFALocked(), PasswordConfirmer, sessionRevoker, twoFAActionRequest

### Community 97 - "scanTreasuryRebalance"
Cohesion: 0.11
Nodes (11): derefString(), insertTreasuryRebalanceEvent(), scanTreasuryRebalance(), treasuryRebalanceExecer, TreasuryRebalanceRepository, treasuryRebalanceRowScanner, Direction, Event (+3 more)

### Community 98 - "ForCryptoAddress"
Cohesion: 0.22
Nodes (22): ForCryptoAddress(), ForCustodyWallet(), ForTable(), ForTwoFactor(), NormalizeAddress(), IsLegacy(), New(), legacyEncrypt() (+14 more)

### Community 99 - "Landing.jsx"
Cohesion: 0.11
Nodes (24): getAccessToken(), showAsset(), Assets(), money(), qty(), STABLES, coin(), COINS (+16 more)

### Community 100 - "fakeStore"
Cohesion: 0.17
Nodes (3): keyOf(), newFakeStore(), fakeStore

### Community 101 - "NetworkFeeEstimate"
Cohesion: 0.11
Nodes (12): MultiNetworkFeeEstimator, TronFeeEstimator, tronParamsSource, NewTronFeeEstimator(), TestMultiNetworkFeeEstimatorRoutesByNetwork(), TestTronFeeEstimator_EnergyBandwidthToTRX(), TestTronFeeEstimator_Rejections(), BitcoinFeeEstimator (+4 more)

### Community 102 - "harness"
Cohesion: 0.14
Nodes (4): Candidate, CandidateSource, fakeCandidates, harness

### Community 103 - "validate_test.go"
Cohesion: 0.13
Nodes (20): CreatePolicyInput, Policy, PolicyFilter, d(), TestValidatePolicyFields_BoundaryEqualities(), TestValidatePolicyFields_HotEqualsTreasury(), TestValidatePolicyFields_InvalidNetworkOrAsset(), TestValidatePolicyFields_MaxAmountNotPositive() (+12 more)

### Community 104 - "time.Time"
Cohesion: 0.12
Nodes (10): clock, fakeTokens, tokenRow, DestinationCooldownError, AccountTokenPurpose, AccountTokenRepository, asCooldown(), fakeBody (+2 more)

### Community 105 - "NewDestinationGuard"
Cohesion: 0.26
Nodes (10): cdKey(), newCDRepo(), TestCheckWithCooldown_AfterCooldown_Allows(), TestCheckWithCooldown_DisabledOrUnsupported_NoWait(), TestCheckWithCooldown_KnownAllowlistedAndBlocked(), TestCheckWithCooldown_NewDestination_RegistersAndWaits(), TestCheckWithCooldown_RepoError_FailsClosed(), TestDestinationSetStatus_ValidatesAndAllowlistBypassesCooldown() (+2 more)

### Community 107 - "fakeStore"
Cohesion: 0.19
Nodes (3): inflightKey(), newFakeStore(), fakeStore

### Community 108 - "Bloque 7.4 — Treasury / Rebalancing"
Cohesion: 0.15
Nodes (12): 1. Qué es y qué no es, 2. Arquitectura — qué se reutiliza, 3.1 Política (7.4.1), 3.2 Detección (7.4.2, `EvaluatePolicies` / `evaluatePolicy`), 3.3 Ejecución (`process.go`, mismo esqueleto que Sweeps 7.3), 3. Flujo, 4. Persistencia (7.4.3), 5. Worker (7.4.4) (+4 more)

### Community 110 - "newTronDepositEnv"
Cohesion: 0.12
Nodes (13): tronDepositEnv, newTronDepositEnv(), TestTronDeposit_DetectedConfirmedCreditedOnce(), TestTronDeposit_IgnoresUnmanagedAddressesAndOtherTokens(), TestTronDeposit_ReorgBeforeConfirmationNeverCredits(), Config, NewProvider(), tronDepositEnv (+5 more)

### Community 111 - "Bloque 7.6 — Recovery / Exceptions"
Cohesion: 0.18
Nodes (10): 1. Qué es y qué no es, 2. Cobertura de la lista mínima del spec, 3. Arquitectura, 4. Persistencia, 5. Kill switch (7.6.2) — con una diferencia deliberada, 6. Auditoría (7.6.3), 7. Resolución (nunca automática), 8. Límites conocidos / fuera de alcance (+2 more)

### Community 112 - "acctRouter"
Cohesion: 0.28
Nodes (11): acctPost(), acctRouter(), errCode(), TestChangePassword_Success_And2FA(), TestChangePassword_WrongCurrent_Is400Not401(), TestForgotPassword_IdenticalResponse_KnownVsUnknown(), TestPublicEndpoints_RejectMalformedInput(), TestResendVerification_IdenticalResponse() (+3 more)

### Community 113 - "Bloque 7.5 — Reconciliation"
Cohesion: 0.20
Nodes (9): 1. Qué es y qué no es, 2. Por qué existe un "baseline" en el leg 1, 3. Arquitectura — qué se reutiliza, 4. Estados, 5. Auditoría, 6. Worker, 7. Idempotencia y concurrencia, 8. Límites conocidos / fuera de alcance (+1 more)

### Community 114 - ".Sign"
Cohesion: 0.18
Nodes (11): Decryptor, DisabledBitcoinSigner, EVMSigner, KeyResolver, KeySourceResolver, SignedTx, Signer, SignRequest (+3 more)

### Community 116 - "Gestión de claves de custodia: cifrado v1, rotación y herramientas"
Cohesion: 0.22
Nodes (8): 1. Formato de cifrado, 2. Configuración multi-clave, 3. Migración perezosa, 4. Procedimiento de rotación, 5. Imágenes: producción vs herramientas, 6. KeyProvider (KMS/HSM), 7. Revisión de fugas (logs/errores), Gestión de claves de custodia: cifrado v1, rotación y herramientas

### Community 117 - "Locker"
Cohesion: 0.10
Nodes (13): Locker, RunExclusive(), SingletonKey(), TestRunExclusive_NilLockerRunsDirectly(), TestRunExclusive_OnlyOneInstanceRuns(), NewWorker(), NewWorker(), NewWorker() (+5 more)

### Community 119 - "blockchain_confirm_test.go"
Cohesion: 0.15
Nodes (22): RecordCompletedInput, NewBlockchainDepositService(), newFakeLedger(), newFakeRecorder(), newService(), sampleConfirmedTransfer(), TestConfirmBlockchainDeposit_ConcurrentWorkers_OnlyOneCredit(), TestConfirmBlockchainDeposit_CreditsOnce() (+14 more)

### Community 120 - "LoadEVMNetworkAssets"
Cohesion: 0.22
Nodes (9): loadDecimals(), LoadEVMNetworkAssets(), looksLikeEVMAddress(), TestLoadEVMNetworkAssets_InvalidContractFormatIsRejected(), TestLoadEVMNetworkAssets_NativeAlwaysPresent(), TestLoadEVMNetworkAssets_TokenOnlyRegisteredIfContractSet(), TestLoadEVMNetworkAssets_TwoTokensSameNetworkDoNotMix(), TestLoadEVMNetworkAssets_ValidatesOfficialUSDTAndDecimals() (+1 more)

### Community 121 - "newH2FAEnv"
Cohesion: 0.18
Nodes (14): NewTwoFactorHandler(), newH2FAEnv(), nextTOTP(), TestTwoFAAdminReset(), TestTwoFADisable_MalformedBody_And_NoPasswordConfirmer(), TestTwoFADisable_RequiresPasswordAndCode(), TestTwoFAMiddleware_Locked_Returns429WithRetryAfter(), TestTwoFAMiddleware_RequiredInvalidValidReplay() (+6 more)

### Community 122 - "WithdrawalChain"
Cohesion: 0.05
Nodes (15): ChainRef, ResourceQuote, ceilPct(), computeQuote(), TRC20TransferParam(), TestComputeQuoteLimits(), isRevertError(), ResourceChain (+7 more)

### Community 123 - "ValidateAddress"
Cohesion: 0.25
Nodes (8): AddressFromPrivateKeyHex(), addressFromPublicKeyBytes(), base58CheckEncode(), doubleSHA256(), TestKeyPairConsistency(), TestValidateAddressRejectsGarbage(), ValidateAddress(), ParsedTx

### Community 124 - "BuildTRC20TransferRaw"
Cohesion: 0.36
Nodes (9): AddressToBytes21(), BuildTRC20TransferRaw(), BuildTRXTransferRaw(), TRC20TransferParams, TRXTransferParams, pbBytes(), pbVarint(), pbVarintRaw() (+1 more)

### Community 125 - "Endurecimiento para varias réplicas"
Cohesion: 0.25
Nodes (7): 1. Locks entre procesos (`pg_advisory_lock`), 2. Migraciones, 3. Apagado ordenado, 4. Rate limiter Redis, 5. Endpoints admin (super_admin + 2FA `X-2FA-Code` + auditoría, incluidos los rechazos), Endurecimiento para varias réplicas, Límites conocidos

### Community 126 - ".broadcastResourceStep"
Cohesion: 0.20
Nodes (6): classifyBroadcastError(), broadcastClass, NetworkRuntime, observer, Service, TestClassifyTronBroadcastErrors()

### Community 127 - "StatusHistory"
Cohesion: 0.29
Nodes (4): StatusHistory, sv(), ActorType, AdminKYCView

### Community 128 - "Fase TRON: depósitos y retiros TRC-20 (USDT)"
Cohesion: 0.25
Nodes (7): BLOQUEADO (requiere un nodo real o una decisión externa), Diseño y decisiones de seguridad, Fase TRON: depósitos y retiros TRC-20 (USDT), Puesta en marcha (orden recomendado), Qué se reutilizó (sin duplicar), Riesgos y límites conocidos, Variables de entorno

### Community 129 - "NewLimitService"
Cohesion: 0.27
Nodes (10): NewLimitService(), newFakeLimiter(), TestLimitService_CommitThenReleaseFails(), TestLimitService_ConcurrentReservationsRespectDailyLimit(), TestLimitService_MinMaxEnforced(), TestLimitService_ReleaseReturnsAllowance(), TestLimitService_ReplayProtection(), usageKey() (+2 more)

### Community 130 - "Deps"
Cohesion: 0.15
Nodes (6): DefaultConfig(), envBool(), LoadConfigFromEnv(), NewService(), Config, Deps

### Community 131 - "Fase TRON: depósitos y retiros TRC-20 (USDT)"
Cohesion: 0.25
Nodes (7): BLOQUEADO (requiere un nodo real o una decisión externa), Diseño y decisiones de seguridad, Fase TRON: depósitos y retiros TRC-20 (USDT), Puesta en marcha (orden recomendado), Qué se reutilizó (sin duplicar), Riesgos y límites conocidos, Variables de entorno

### Community 132 - "Fase C — Verificación de email, restablecimiento y cambio de contraseña"
Cohesion: 0.33
Nodes (5): Configuración (variables de entorno del backend), Endpoints, Fase C — Verificación de email, restablecimiento y cambio de contraseña, Garantías, Pendiente fuera de esta fase

### Community 133 - "acctUsers"
Cohesion: 0.21
Nodes (8): adminRouter(), adminUser(), newAcctUsers(), TestManualVerifyEmail(), TestUnblockUser_OnlyBlockedAccounts(), TestUnblockUser_RequiresReason(), TestUnblockUser_RespectsEmailVerification_AndAudits(), acctUsers

### Community 134 - "Fase E — 2FA endurecido"
Cohesion: 0.33
Nodes (5): Efectos que debes conocer, Endpoints, Fase E — 2FA endurecido, Pendiente fuera de esta fase, Qué cambió

### Community 135 - "user.js"
Cohesion: 0.20
Nodes (15): Coin(), COINS, nav, getNetwork(), NETWORKS, setNetwork(), SOON_NETWORKS, Buy() (+7 more)

### Community 136 - "Store"
Cohesion: 0.20
Nodes (3): Event, Store, Update

### Community 138 - ".broadcastResourceStep"
Cohesion: 0.26
Nodes (3): NetworkRuntime, observer, Service

### Community 140 - "AGENTS.md"
Cohesion: 0.40
Nodes (4): Comandos relevantes, Desarrollo local, Estructura, Notas clave

### Community 141 - "Service"
Cohesion: 0.09
Nodes (25): EmailVerifier, Claims, TestService_Login_ActiveUser(), TestService_Logout_RevokesSession(), TestService_Refresh_RotatesToken(), TestService_Register_CreatesWalletAndAccount(), AuthResult, RegisterInput (+17 more)

### Community 142 - "ValidateSecrets"
Cohesion: 0.40
Nodes (5): looksLikePlaceholder(), ValidateSecrets(), contains(), TestValidateSecrets_AllGood(), TestValidateSecrets_NeverLeaksValue()

### Community 143 - ".processClaimed"
Cohesion: 0.40
Nodes (4): Backoff(), TestBackoff_ClampsLowAttempts(), TestBackoff_Monotonic(), Service

### Community 144 - "GasStationPolicy"
Cohesion: 0.08
Nodes (15): fakeAudit, fakeDecryptor, fakePolicies, harness, newFakePolicies(), GasStationPolicy, GasStationPolicyRepository, scanGasStationPolicy() (+7 more)

### Community 145 - "Dependencias nuevas requeridas (Fase 1 — BSC)"
Cohesion: 0.40
Nodes (4): Cerrado en esta iteración, Dependencias nuevas requeridas (Fase 1 — BSC), Pendiente antes de producción, Qué pude validar aquí y qué no

### Community 146 - "ReserveInput"
Cohesion: 0.19
Nodes (6): Reservation, ReserveInput, isLimitRejection(), secLimiter, secRes, Limiter

### Community 147 - "Service"
Cohesion: 0.07
Nodes (24): NewGuard(), NewEventLogger(), NewFreezeGuard(), TestEventLogger_StripsSecretLikeMetadata(), TestGuard_CheckOwnership_IDOR(), TestGuard_RequireActiveAccount(), TestGuard_RequireRole(), TestStepUpGuard_RequireForAmount() (+16 more)

### Community 148 - "auth-E2E-01-login-y-persistencia-de-sesión/error-context.md"
Cohesion: 0.40
Nodes (4): Error details, Instructions, Test info, Test source

### Community 149 - "dashboard-assets-E2E-02-panel-saldos-y-activos-EVM-TRON/error-context.md"
Cohesion: 0.40
Nodes (4): Error details, Instructions, Test info, Test source

### Community 150 - "Service"
Cohesion: 0.14
Nodes (9): Service, NewService(), key(), newFakeStore(), Config, CryptoAddress, CryptoAddressRepository, TRC20BalanceReader (+1 more)

### Community 151 - "newTestUser"
Cohesion: 0.67
Nodes (4): NewSecurityDestinationRepository(), newTestUser(), TestDestinationRepo_AdminAllowlistAndBlock(), TestDestinationRepo_PendingCooldownLifecycle()

### Community 152 - "New"
Cohesion: 0.30
Nodes (12): TwoFAVerifyMiddleware(), ServerToServerHMAC(), AuthRequired(), CORSConfig(), IdempotencyKeyRequired(), RequireRole(), BodyLimit(), OperationGuard() (+4 more)

### Community 153 - "Transactions.jsx"
Cohesion: 0.83
Nodes (3): fmtDate(), isBuy(), Transactions()

### Community 154 - "BlockchainWithdrawalHandler"
Cohesion: 0.24
Nodes (9): ParseAmount(), TestHTTPStatusFor_Cooldown(), HTTPStatusFor(), BlockchainWithdrawalHandler, maskAddress(), queryInt(), respondWithdrawalError(), toWithdrawalDTO() (+1 more)

### Community 155 - "RiskProfile"
Cohesion: 0.23
Nodes (3): RiskProfile, scanCase(), scanRisk()

### Community 156 - "Backups fuera del VPS"
Cohesion: 0.50
Nodes (3): Activación, Backups fuera del VPS, Verificación periódica

### Community 160 - "EnvKeyProvider"
Cohesion: 0.18
Nodes (6): open(), TestProviderConfigErrorsNeverEchoKeys(), aeadFromHex(), NewEnvKeyProvider(), EnvKeyProvider, KeyProvider

### Community 161 - ".QueryRow"
Cohesion: 0.29
Nodes (6): classifyMissingReservation(), lockUsageRow(), periodKeys(), queryPolicy(), pgxQuerier, SecurityLimitRepository

### Community 163 - "Background"
Cohesion: 0.24
Nodes (3): Background, BackgroundVerifier, Service

### Community 164 - "Affiliate"
Cohesion: 0.18
Nodes (4): Repository, generateCode(), Affiliate, AffiliateRepository

### Community 166 - ".authorizeCreate"
Cohesion: 0.21
Nodes (3): Service, limitReferenceID(), EvaluateThresholds()

### Community 167 - "GasStation"
Cohesion: 0.26
Nodes (4): NewGasStation(), hexToHash32ForWithdrawal(), GasStation, NonceAllocator

### Community 168 - "LoadConfigFromEnv"
Cohesion: 0.24
Nodes (11): decimalEnv(), durationEnv(), int64Env(), LoadConfigFromEnv(), clearFeeEnv(), TestLoadConfigFromEnv_CustomValues(), TestLoadConfigFromEnv_RejectsExcessiveVolatilityBuffer(), TestLoadConfigFromEnv_RejectsMinGreaterThanMax() (+3 more)

### Community 170 - "Deposit"
Cohesion: 0.08
Nodes (11): Deposit, TransactionStatus, Withdrawal, DepositRepository, NewWithdrawalRepository(), NewStubCryptoProvider(), CreateDepositInput, CryptoProvider (+3 more)

### Community 172 - "StubKYCProvider"
Cohesion: 0.17
Nodes (4): KYCVerificationStatus, NewStubKYCProvider(), KYCProvider, StubKYCProvider

### Community 173 - "RunWithLockTimeout"
Cohesion: 0.16
Nodes (12): apply(), discardConn(), Run(), RunWithLockTimeout(), discard(), KeyID(), New(), NewPool() (+4 more)

### Community 174 - ".EstimateNetworkFee"
Cohesion: 0.21
Nodes (7): MultiNetworkFeeEstimator, TronFeeEstimator, tronParamsSource, NewTronFeeEstimator(), TestMultiNetworkFeeEstimatorRoutesByNetwork(), TestTronFeeEstimator_EnergyBandwidthToTRX(), TestTronFeeEstimator_Rejections()

### Community 177 - "call"
Cohesion: 0.27
Nodes (9): call(), newOps(), TestCreateTreasuryPolicy_AlwaysDisabledAndAudited(), TestRegisterWallet_GeneratesServerSide_NeverLeaksKey(), TestRegisterWallet_ImportAllowedWhenEnabled(), TestRegisterWallet_ImportDisabledByDefault(), TestResolveWithdrawal_FailureIsAudited(), TestResolveWithdrawal_ShortReasonRejected() (+1 more)

### Community 178 - "github.com/gin-gonic/gin.Engine"
Cohesion: 0.24
Nodes (11): setupTestRouter(), hmacRouter(), sign(), TestServerToServerHMAC_EmptySecret_FailsClosed(), TestServerToServerHMAC_ValidAndInvalid(), adminOpsEngine(), do(), TestAdminOps_NoStepUp_RoutesNotRegistered() (+3 more)

### Community 179 - "mailer_test.go"
Cohesion: 0.15
Nodes (14): BuildMessage(), maskEmail(), NewLogSender(), NewSMTPSender(), fakeSMTP(), TestBuildMessage_HeadersAndBase64Body(), TestBuildMessage_RejectsHeaderInjection(), TestLogSender_DoesNotFailAndMasksEmail() (+6 more)

### Community 185 - "AddressHandler"
Cohesion: 0.19
Nodes (12): SupportedNetworks(), TestSupportedNetworks_DoesNotIncludeTRONOrBSC(), AddressHandler, NewAddressHandler(), supportedNetworkNames(), toResponse(), newNetRouter(), TestActiveNetworks_ListsOnlyWatchedNetworks() (+4 more)

### Community 190 - "main"
Cohesion: 0.20
Nodes (8): main(), processTable(), reencryptGasStationEnv(), report(), selectTables(), CustodyRekeyRepository, stats, RekeyRow

### Community 191 - "env"
Cohesion: 0.08
Nodes (18): FakeAudit, FakeScreener, Config, LoadConfig(), TestEffectiveStatus(), Decide(), Engine, NewEngine() (+10 more)

### Community 194 - "Event"
Cohesion: 0.19
Nodes (7): Event, normalizeKey(), sanitizeMetadata(), NewSecurityEventRepository(), nullOperation(), SecurityEventRepository, EventType

### Community 196 - "NewFeeHandler"
Cohesion: 0.21
Nodes (10): FeeHandler, NewFeeHandler(), respondFeeError(), TestFeeHandlerQuoteLookupAndProviderFailureAreSafe(), TestFeeHandlerRejectsMalformedFinancialRequestsBeforeCallingEngine(), toEstimateFeeResponse(), TestFeeHandler_Estimate_Validation(), TestFeeHandler_GetQuote_NotFound() (+2 more)

### Community 198 - ".Sign"
Cohesion: 0.39
Nodes (3): FamilySigner, validateTronSignRequest(), verifySignedTron()

### Community 201 - "ScreeningResult"
Cohesion: 0.20
Nodes (6): Service, ScreeningResult, scanScreenRes(), ReviewStatus, ScreeningKind, ScreeningOutcomeView

### Community 206 - ".Sign"
Cohesion: 0.39
Nodes (3): FamilySigner, validateTronSignRequest(), verifySignedTron()

### Community 211 - "Withdrawal"
Cohesion: 0.40
Nodes (3): CanTransition(), Status, Withdrawal

### Community 220 - "doIdem"
Cohesion: 0.40
Nodes (5): doIdem(), idemRouter(), TestIdempotencyKeyRequired_AcceptsValid(), TestIdempotencyKeyRequired_Missing(), TestIdempotencyKeyRequired_RejectsInvalid()

### Community 221 - "NewRedisRateLimiter"
Cohesion: 0.18
Nodes (8): NewRedisRateLimiter(), deadRedis(), doGET(), TestRedisRateLimiter_DefaultFailOpen_WhenRedisDown(), TestRedisRateLimiter_FailClosed_WhenRedisDown(), TestSecurityRateLimit_FailClosedOperations(), AtomicIncr(), RedisRateLimiter

### Community 223 - "PaymentOrder"
Cohesion: 0.23
Nodes (5): PaymentOrder, ChargeInput, ChargeResult, OrderRepository, PaymentOrderRepository

### Community 233 - "Security.jsx"
Cohesion: 0.80
Nodes (4): ChangePasswordCard(), RecoveryCodesBlock(), Security(), TwoFactorManageCard()

### Community 238 - "blockchainwithdrawal/security_test.go"
Cohesion: 0.14
Nodes (23): maskAddress(), newSecDest(), newSecEnv(), newSecLimiter(), TestMaskAddress(), TestSecurity_BlockedDestination_Rejected(), TestSecurity_Cancel_ReleasesLimitReservation(), TestSecurity_Completed_CommitsLimitAndMarksDestinationKnown() (+15 more)

## Knowledge Gaps
- **16 isolated node(s):** `korzadi-wallet/backend`, `Service`, `Service`, `Service`, `harness` (+11 more)
  These have ≤1 connection - possible missing edges or undocumented components. (Counts symbols only; 706 node(s) total have ≤1 connection when file, concept and rationale nodes are included.)
- **22 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `Network` connect `Network` to `backend/cmd/api/main.go`, `Deps`, `main`, `Check`, `fakeNonces`, `github.com/google/uuid.UUID`, `Client`, `.broadcastResourceStep`, `Service`, `AdminOpsHandler`, `Sweep`, `context.Context`, `Withdrawal`, `Service`, `btcwallet/keypair_test.go`, `github.com/shopspring/decimal.Decimal`, `.Sign`, `EVMDepositWatcher`, `.authorizeCreate`, `GasStation`, `LoadConfigFromEnv`, `AAD`, `Rebalance`, `.EstimateNetworkFee`, `GasStationRefill`, `Service`, `AddressHandler`, `engine_test.go`, `evm_deposit_watcher_test.go`, `NetworkAsset`, `EVMDepositWatcher`, `BlockchainTransfer`, `CustodyWallet`, `BlockchainDepositRecord`, `Deps`, `Service`, `Withdrawal`, `NewService`, `.FindEncryptedKeyWithAAD`, `buildProvider`, `fakeStore`, `NetworkFeeEstimate`, `harness`, `validate_test.go`, `Deps`, `fakeStore`, `.Sign`, `WatcherFailedEventRepository`, `newFakeAssetValidator`, `blockchain_confirm_test.go`, `LoadEVMNetworkAssets`, `WithdrawalChain`, `.broadcastResourceStep`?**
  _High betweenness centrality (0.030) - this node is a cross-community bridge._
- **What connects `korzadi-wallet/backend`, `Service`, `Service` to the rest of the system?**
  _16 weakly-connected nodes found - possible documentation gaps or missing edges._
- **Should `backend/cmd/api/main.go` be split into smaller, more focused modules?**
  _Cohesion score 0.07131709374670463 - nodes in this community are weakly interconnected._
- **Why does `provisionDepositAddress()` connect `main` to `backend/cmd/api/main.go`, `testing.T`, `Network`, `github.com/google/uuid.UUID`, `context.Context`?**
  _High betweenness centrality (0.012) - this node is a cross-community bridge._
- **Should `testing.T` be split into smaller, more focused modules?**
  _Cohesion score 0.02064940894807721 - nodes in this community are weakly interconnected._
- **Why does `Deps` connect `Deps` to `backend/cmd/api/main.go`, `Network`, `NetworkAsset`, `time.Time`, `Service`, `Service`?**
  _High betweenness centrality (0.012) - this node is a cross-community bridge._
- **Should `Network` be split into smaller, more focused modules?**
  _Cohesion score 0.03221288515406162 - nodes in this community are weakly interconnected._