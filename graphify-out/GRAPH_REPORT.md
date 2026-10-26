# Graph Report - korzadi-wallet  (2026-10-26)

## Corpus Check
- 505 files · ~410,000 words
- Verdict: corpus is large enough that graph structure adds value.
- Unclassified: 14 file(s) not represented in the graph (top: (none) 5, .css 5, .history 1)

## Summary
- 6312 nodes · 26550 edges · 233 communities (202 shown, 31 thin omitted)
- Extraction: 96% EXTRACTED · 4% INFERRED · 0% AMBIGUOUS · INFERRED: 1109 edges (avg confidence: 0.85)
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
- Reservation
- newHarness
- main
- Service
- kz-integration.js
- context.Context
- Actor
- Client
- Item
- Service
- Repo
- AdminOpsHandler
- time.Duration
- math/big.Int
- newHarness
- GenerateKeyPair
- btcclient/client.go
- newTronGasEnv
- testPool
- policy_test.go
- Withdrawal
- transfer/service_test.go
- Deps
- btcwallet/keypair_test.go
- currentUserID
- Service
- github.com/shopspring/decimal.Decimal
- aider chat started at 2026-10-09 12:21:08
- .Sign
- Chain
- korzadi-wallet/backend/internal/infrastructure/tronwallet/tx.go
- NewCachedFallbackPriceProvider
- EVMDepositWatcher
- User
- Rebalance
- LoadConfigFromEnv
- watcher_resilience_test.go
- AAD
- KYCCase
- Korzadi Wallet
- main.jsx
- Chain
- Service
- LedgerTransaction
- Bloque 7.3 -- Sweeps -- Informe final
- korzadi-wallet/backend/internal/infrastructure/tronclient/fullnode.go
- TransactionReceipt
- accountaccess/service_test.go
- custody/service_test.go
- GasStationRefill
- AuditLogger
- package.json
- Service
- newTronHarness
- compliance/ports.go
- FeeQuote
- Check
- client.js
- newTronHarness
- prefs/service.go
- evm_deposit_watcher_test.go
- Provider
- NetworkAsset
- newHarness
- NewBTCDepositWatcher
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
- ledger/service_test.go
- Provider
- .signAndSend
- Bloque 9 — KYC / AML / Compliance foundation
- NewVelocityTracker
- NewService
- engine_test.go
- Bloque 7.7 — Tests / Auditoría del Bloque 7
- BuildTRC20TransferRaw
- clearTokens
- AdminHandler
- buildProvider
- compliance_routes_test.go
- FreezeStatus
- TwoFactorHandler
- scanTreasuryRebalance
- ForCryptoAddress
- Landing
- fakeStore
- NetworkFeeEstimate
- harness
- validate_test.go
- time.Time
- NewDestinationGuard
- Deps
- fakeStore
- Bloque 7.4 — Treasury / Rebalancing
- Service
- sync.Mutex
- Bloque 7.6 — Recovery / Exceptions
- acctRouter
- Bloque 7.5 — Reconciliation
- .Sign
- WatcherFailedEventRepository
- Gestión de claves de custodia: cifrado v1, rotación y herramientas
- Locker
- didit.go
- blockchain_confirm_test.go
- CustodyWalletRepository
- newH2FAEnv
- WithdrawalChain
- WithdrawalChain
- .signAndSend
- Endurecimiento para varias réplicas
- .prepareResourceTx
- login_2fa_test.go
- Fase TRON: depósitos y retiros TRC-20 (USDT)
- NewLimitService
- Deps
- Fase TRON: depósitos y retiros TRC-20 (USDT)
- Fase C — Verificación de email, restablecimiento y cambio de contraseña
- acctUsers
- Fase E — 2FA endurecido
- user.js
- Store
- FullNode
- .prepareResourceTx
- AuthHandler
- AGENTS.md
- Service
- ValidateSecrets
- .transferEventsInRangeAdaptive
- GasStationPolicy
- Dependencias nuevas requeridas (Fase 1 — BSC)
- fakeEVMProvider
- Service
- EVMChain
- EVMChain
- CryptoAddress
- newTestUser
- New
- KORZADI — INFORME DE AUDITORÍA DE SEGURIDAD
- BlockchainWithdrawalHandler
- NewService
- Backups fuera del VPS
- NewSecurityCounter
- diditCountries.js
- TestBlockchainDeposit_FullFlowOnBSCETHAndTRON
- EnvKeyProvider
- EVMChain
- fakeChain
- Background
- NewReservationService
- Chain
- .authorizeCreate
- GasStation
- LoadConfigFromEnv
- Handlers
- Transfer
- Policy
- StubKYCProvider
- RunWithLockTimeout
- WorkContext
- ReservationSettlementInput
- newEnv
- call
- adminOpsEngine
- @playwright/test
- AdminAdvanced.jsx
- NewHistoryService
- net/http.Client
- Engine
- SecurityHandler
- AddressHandler
- btcclient/client_test.go
- NewBlockchainDepositService
- Price
- .evaluatePolicy
- fakeChain
- FakeAudit
- .ResolveNeedsReview
- ParseSignedTx
- Service
- rpcClient
- FeeHandler
- netEntries
- .Sign
- reconciliation/model_test.go
- rpcError
- harness
- LoadConfigFromEnv
- newUserForTokens
- .EstimateNetworkFee
- .handle
- .Sign
- harness
- recSender
- Config
- hmacRouter
- Status
- nonNeg
- .audit
- mustActive
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
2. `main()` - 185 edges
3. `main()` - 171 edges
4. `newEnv()` - 62 edges
5. `currentUserID()` - 58 edges
6. `testPool()` - 57 edges
7. `User` - 55 edges
8. `TransactionReceipt` - 51 edges
9. `KYCCase` - 50 edges
10. `newHarness()` - 48 edges

## Surprising Connections (you probably didn't know these)
- `Riesgos residuales actuales (post-Ronda 6 — con autorización para todas las correcciones seguras)` --references--> `RequireAuth()`  [INFERRED]
  AUDITORIA_SEGURIDAD.md → frontend/src/main.jsx
- `RONDA 1 — Hallazgos iniciales` --references--> `RequireAuth()`  [INFERRED]
  AUDITORIA_SEGURIDAD.md → frontend/src/main.jsx
- `RONDA 4 — Fecha: 2026-10-26` --references--> `RequireAuth()`  [INFERRED]
  AUDITORIA_SEGURIDAD.md → frontend/src/main.jsx
- `RONDA 5 — Fecha: 2026-10-26` --references--> `RequireAuth()`  [INFERRED]
  AUDITORIA_SEGURIDAD.md → frontend/src/main.jsx
- `RONDA 6 — Fecha: 2026-10-26 (autorización concedida para corregir todos los riesgos corregibles)` --references--> `RequireAuth()`  [INFERRED]
  AUDITORIA_SEGURIDAD.md → frontend/src/main.jsx

## Import Cycles
- None detected.

## Communities (233 total, 31 thin omitted)

### Community 0 - "backend/cmd/api/main.go"
Cohesion: 0.07
Nodes (57): newFakeLedgerAssets(), TronSignParams, coinGeckoSimplePriceResponse, normalizeKey(), sanitizeMetadata(), BitcoinUTXO, BroadcastError, bareStore (+49 more)

### Community 1 - "testing.T"
Cohesion: 0.03
Nodes (107): NewBackground(), slowEnv(), TestBackground_DropsWhenFull_AndRecoversFromPanic(), TestBackground_TaskTimeoutCancelsContext(), TestBackgroundVerifier_ReturnsImmediately_AndSends(), TestForgotPassword_Async_ResponseTimeIndependentOfSMTP(), TestResendVerification_Async_ResponseTimeIndependentOfSMTP(), hotHarness() (+99 more)

### Community 2 - "Network"
Cohesion: 0.04
Nodes (22): newFakeNonces(), Service, HotWalletBook, NonceStore, fakeNonces, newFakeNonces(), newFakeNonces(), BlockchainFamily (+14 more)

### Community 3 - "newEnv"
Cohesion: 0.07
Nodes (67): TestFlag_AutomatedAndAMLDisabled(), TestFlag_CreateDedupeResolve(), TestHold_NotOnSelfAndInvalidType(), TestHold_PlaceReleaseAffectsAuthorizationOnly(), TestRisk_CriticalPlacesInternalHold(), TestRisk_Transitions(), Body(), Sign() (+59 more)

### Community 4 - "Reservation"
Cohesion: 0.05
Nodes (16): fakeLedger, FeeReserver, LedgerPort, Reservation, ReservationStatus, ReservationRepository, fakeFeeReserver, fakeLedgerReservations (+8 more)

### Community 5 - "newHarness"
Cohesion: 0.04
Nodes (48): newFakeAccounts(), newFakeAddresses(), newFakeClearing(), newFakeFeeEngine(), newFakeLedger(), newFakeStore(), newFakeUsers(), newFakeWallets() (+40 more)

### Community 6 - "main"
Cohesion: 0.05
Nodes (95): bootstrapSystemAccounts(), envOr(), envUint(), main(), mustEnv(), parseAllowedOrigins(), parseCSV(), splitCSV() (+87 more)

### Community 7 - "Service"
Cohesion: 0.09
Nodes (15): DefaultConfig(), envBool(), LoadConfigFromEnv(), NewService(), strPtr(), weiToDecimalForAsset(), reconciliationWalletAdapter, Config (+7 more)

### Community 8 - "kz-integration.js"
Cohesion: 0.06
Nodes (104): actionsHtml(), addMessageNode(), addSystemNode(), afterNavigate(), amt(), api(), apiUrl(), ask2FA() (+96 more)

### Community 9 - "context.Context"
Cohesion: 0.01
Nodes (84): authSessionRepo, LoginChallengeRepository, memChallenges, Service, Service, LoginChallenge, newLoginChallenge(), AddressBook (+76 more)

### Community 10 - "Actor"
Cohesion: 0.07
Nodes (25): Service, cleanCountry(), Service, Service, actorID(), claim(), EffectiveStatus(), sortCasesNewestFirst() (+17 more)

### Community 11 - "Client"
Cohesion: 0.10
Nodes (10): backoffDuration(), bounded1559FeeCaps(), Dial(), Client, isTransient(), redactURLErr(), TestBackoffDuration_RespectsMax(), TestBounded1559FeeCapsRespectRPCQuote() (+2 more)

### Community 12 - "Item"
Cohesion: 0.07
Nodes (25): DefaultConfig(), envBool(), LoadConfigFromEnv(), TestIsLockExpired(), isLockExpired(), NewService(), scanRecoverableItems(), scanRecoveryIncident() (+17 more)

### Community 13 - "Service"
Cohesion: 0.18
Nodes (8): amountReservationReason(), feeReservationReason(), CreateInput, CreateResult, Service, isInsufficientFunds(), sanitizeCause(), ValidateIdempotencyKey()

### Community 14 - "Repo"
Cohesion: 0.06
Nodes (15): Repo, itoa(), CreateOfferInput, CreateOrderInput, Dispute, DisputeStatus, Message, noopNotifier (+7 more)

### Community 15 - "AdminOpsHandler"
Cohesion: 0.09
Nodes (23): CustodyWallet, ListFilter, RegisterInput, Status, badRequest(), gasPolicyView(), AdminOpsHandler, NewAdminOpsHandler() (+15 more)

### Community 16 - "time.Duration"
Cohesion: 0.11
Nodes (16): TestSanitizeCause_TruncatesAndStaysSingleLine(), Backoff(), classifyBroadcastError(), Service, isRevertError(), gasOwner(), Service, nonceOwner() (+8 more)

### Community 17 - "math/big.Int"
Cohesion: 0.04
Nodes (20): ResolveGasFeeParams(), gasStub, ToRawUnits(), fakeChain, newFakeChain(), toWei(), newFakeChain(), toRawUnits() (+12 more)

### Community 18 - "newHarness"
Cohesion: 0.11
Nodes (37): containsSubstring(), TestAdvance_ResumeAfterRestart_UsesPersistedSignedTxWithoutResigning(), TestAudit_LogsAcrossLifecycle(), TestBroadcastStep_AmbiguousNeverAutoRebroadcasts(), TestBroadcastStep_RejectedFailsClosed(), TestBroadcastStep_Success(), TestComplete_NeverTouchesLedger(), TestConfirmStep_ReachesRequiredConfirmations_Completes() (+29 more)

### Community 19 - "GenerateKeyPair"
Cohesion: 0.04
Nodes (56): TronSigner, NewTronSigner(), mustTronKP(), TestTronSigner_RefusesNativeAndEVMRequests(), TestTronSigner_RefusesWrongSourceKey(), TestValidateTronDestination(), validateTronDestination(), ValidateDestination() (+48 more)

### Community 20 - "btcclient/client.go"
Cohesion: 0.19
Nodes (10): btcToSats(), Dial(), sign(), blockScriptPubKey, blockTx, blockVout, Client, rawOutput (+2 more)

### Community 21 - "newTronGasEnv"
Cohesion: 0.21
Nodes (12): newFakeWallets(), tronGasEnv, newTronGasEnv(), TestTronGas_AmbiguousBroadcastIsReconciledNotResent(), TestTronGas_DefinitiveRejectionMarksFailed(), TestTronGas_DisabledAndMissingWallet(), TestTronGas_LostTxExpiresAndIsPlannedAgain(), TestTronGas_NeverLeaksPrivateKey() (+4 more)

### Community 22 - "testPool"
Cohesion: 0.14
Nodes (52): New(), UserWalletAccountRepository, NewUserWalletAccountRepository(), LedgerRepository, NewLedgerRepository(), New(), NewTransferRepository(), NewUserRepository() (+44 more)

### Community 23 - "policy_test.go"
Cohesion: 0.33
Nodes (12): boolStr(), TestEffectiveStatus(), hashParts(), fakeBody, flagRequest, holdRequest, resolveRequest, reviewScreeningRequest (+4 more)

### Community 24 - "Withdrawal"
Cohesion: 0.08
Nodes (13): CanTransition(), Status, Withdrawal, Event, Store, Update, insertWithdrawalEvent(), scanBlockchainWithdrawal() (+5 more)

### Community 25 - "transfer/service_test.go"
Cohesion: 0.10
Nodes (37): LedgerAssets, newFakeLedgerAssetsAdapter(), LedgerAsset, newFakeAssets(), newFakeLedger(), newFakeTransfers(), newFakeUsers(), newFakeWallets() (+29 more)

### Community 26 - "Deps"
Cohesion: 0.18
Nodes (6): DefaultConfig(), envBool(), LoadConfigFromEnv(), NewService(), Config, Deps

### Community 27 - "btcwallet/keypair_test.go"
Cohesion: 0.07
Nodes (34): NewAddressService(), AddressFromPublicKey(), encodeSegwitAddress(), GenerateKeyPair(), Network, hash160(), TestAddressFromPublicKey_DeterministicForSameKey(), TestAddressFromPublicKey_RejectsUncompressedKey() (+26 more)

### Community 28 - "currentUserID"
Cohesion: 0.09
Nodes (15): TestHTTPStatusFor_Cooldown(), HTTPStatusFor(), AffiliateHandler, parseAndValidateAmount(), respondDomainError(), TestRespondDomainError_Login2FACodes(), BlockchainDepositHandler, toDepositResponse() (+7 more)

### Community 29 - "Service"
Cohesion: 0.22
Nodes (7): classifyBroadcastError(), broadcastClass, Service, isFinalFailure(), nonceOwner(), TestClassifyTronBroadcastErrors(), WeiToDecimal()

### Community 30 - "github.com/shopspring/decimal.Decimal"
Cohesion: 0.02
Nodes (34): Repository, generateCode(), InFlightReader, Decide(), TestDecide(), SatsToBTC(), scaleDown(), TestScaleDown() (+26 more)

### Community 31 - "aider chat started at 2026-10-09 12:21:08"
Cohesion: 0.06
Nodes (33): 1. BNB Smart Chain — BEP-20., , 2. Ethereum — ERC-20., , , , 3. TRON — TRC-20, únicamente cuando el contrato oficial del activo esté verificado.,  (+25 more)

### Community 32 - ".Sign"
Cohesion: 0.11
Nodes (25): Decryptor, DisabledBitcoinSigner, EVMSigner, KeyResolver, SignedTx, Signer, SignRequest, NewEVMSigner() (+17 more)

### Community 33 - "Chain"
Cohesion: 0.10
Nodes (8): addr20(), fromHex41(), account, block, Chain, tx, New(), topicAddr()

### Community 34 - "korzadi-wallet/backend/internal/infrastructure/tronwallet/tx.go"
Cohesion: 0.12
Nodes (31): addressFromPublicKeyBytes(), base58CheckEncode(), doubleSHA256(), ParsedTx, AddressFromBytes21(), AddressToBytes21(), allZero(), BuildTRC20TransferRaw() (+23 more)

### Community 35 - "NewCachedFallbackPriceProvider"
Cohesion: 0.23
Nodes (11): NewCachedFallbackPriceProvider(), CoinGeckoPriceProvider, minTimestamp(), NewCoinGeckoPriceProviderWithAPIKey(), TestBinancePriceProvider_QuoteAndTimestamp(), TestCachedFallbackPriceProvider_CacheFallbackAndFailClosed(), TestCachedFallbackPriceProvider_RejectsStaleProviderQuotes(), TestCachedFallbackPriceProvider_UsesSecondaryWhenPrimaryFails() (+3 more)

### Community 36 - "EVMDepositWatcher"
Cohesion: 0.09
Nodes (10): Config, EVMDepositWatcher, Config, EVMDepositWatcher, CreditedTransferLister, FailedEventStore, IncidentReporter, Stats (+2 more)

### Community 37 - "User"
Cohesion: 0.04
Nodes (18): UserStore, authMemory, authUserRepo, authWalletRepo, Service, hashToken(), newToken(), TestTokens_AreRandomAndLongEnough() (+10 more)

### Community 38 - "Rebalance"
Cohesion: 0.09
Nodes (20): Backoff(), classifyBroadcastError(), Service, insufficientCode(), insufficientErr(), isRevertError(), TestBackoff_ClampsLowAttempts(), TestBackoff_Monotonic() (+12 more)

### Community 39 - "LoadConfigFromEnv"
Cohesion: 0.24
Nodes (9): DefaultConfig(), envBool(), LoadConfigFromEnv(), parsePositiveDecimal(), ValidateAmount(), AssetLimits, Config, ToRawUnits() (+1 more)

### Community 40 - "watcher_resilience_test.go"
Cohesion: 0.14
Nodes (25): credited(), fakeTransferStore, newFakeFailedStore(), newRecheckWatcher(), newRWatcher(), rEvent(), rRow(), TestResilience_Backfill_DoesNotTouchCheckpoint_AndScansInChunks() (+17 more)

### Community 41 - "AAD"
Cohesion: 0.08
Nodes (16): processTable(), report(), tronDec, AAD, Encryptor, CustodyRekeyRepository, Decrypted, Rewriter (+8 more)

### Community 42 - "KYCCase"
Cohesion: 0.03
Nodes (44): currentCases(), MemStore, NewMemStore(), newState(), RiskHistoryEntry, Tx, Flag, Hold (+36 more)

### Community 43 - "Korzadi Wallet"
Cohesion: 0.07
Nodes (28): Checklist de producción — Korzadi Wallet, Criterio de estado, Dependencias externas pendientes, Estado por componente, Migraciones P2P, P2P Market (Completado), Requisitos antes de habilitar producción, Riesgos técnicos (+20 more)

### Community 44 - "main.jsx"
Cohesion: 0.10
Nodes (36): walletApi, WalletLayout(), getUser(), Deposits(), STATUS, COUNTRIES, KYC(), LABELS (+28 more)

### Community 45 - "Chain"
Cohesion: 0.11
Nodes (7): addr20(), account, block, Chain, tx, New(), topicAddr()

### Community 46 - "Service"
Cohesion: 0.14
Nodes (8): generateRecoveryCodes(), Service, matchStep(), normalizeRecovery(), ForTwoFactor(), Alerter, Repository, SetupResult

### Community 47 - "LedgerTransaction"
Cohesion: 0.13
Nodes (18): LedgerTransaction, TransactionType, LedgerEntryInput, verifyReplayPayload(), findByIdempotencyKeyPool(), findByIdempotencyKeyTx(), insertAdjustmentAudit(), postEntriesTx() (+10 more)

### Community 48 - "Bloque 7.3 -- Sweeps -- Informe final"
Cohesion: 0.08
Nodes (23): 0. Estado de la verificacion (leer primero), 10. Como se evita el doble sweep, 11. Como se integra Gas Station, 12. Como se gestiona el nonce, 13. Como se firma, 14. Como se hace broadcast, 15. Como se manejan los broadcasts ambiguos, 16. Estados del sweep (+15 more)

### Community 49 - "korzadi-wallet/backend/internal/infrastructure/tronclient/fullnode.go"
Cohesion: 0.16
Nodes (9): decodeNodeMessage(), BlockRef, blockResp, BroadcastError, FullNode, TxInfo, TxLog, hexAddr() (+1 more)

### Community 50 - "TransactionReceipt"
Cohesion: 0.05
Nodes (13): ProviderHealth, TransactionReceipt, Provider, Provider, Config, Provider, NewProvider(), Chain (+5 more)

### Community 51 - "accountaccess/service_test.go"
Cohesion: 0.14
Nodes (31): auditRec, clock, env, fake2FA, fakeSessions, fakeUsers, notifRec, TwoFAChecker (+23 more)

### Community 52 - "custody/service_test.go"
Cohesion: 0.16
Nodes (25): newFakeRepository(), newTestService(), TestGet_ReturnsSameDataAsRegistered(), TestList_FiltersByWalletType(), TestRegister_AuditEventNeverContainsPrivateKey(), TestRegister_CreatesGasWallet(), TestRegister_CreatesHotWallet(), TestRegister_CreatesTreasuryWallet() (+17 more)

### Community 53 - "GasStationRefill"
Cohesion: 0.11
Nodes (9): fakeRefills, newFakeRefills(), EVMNonceRepository, normalizeNonceAddress(), GasStationRefill, scanGasStationRefill(), RefillStore, fakeRefills (+1 more)

### Community 54 - "AuditLogger"
Cohesion: 0.12
Nodes (26): Deps, SessionRevoker, NewService(), Service, NewService(), Service, NewService(), Service (+18 more)

### Community 55 - "package.json"
Cohesion: 0.07
Nodes (30): dependencies, axios, lucide-react, qrcode.react, react, react-dom, react-router-dom, devDependencies (+22 more)

### Community 56 - "Service"
Cohesion: 0.15
Nodes (11): backoff(), amountReservationReason(), feeReservationReason(), CreateInput, CreateResult, Service, isFinalFailure(), isInsufficientFunds() (+3 more)

### Community 57 - "newTronHarness"
Cohesion: 0.19
Nodes (19): fakeLedger, tronHarness, hasEvent(), newTronHarness(), reservationStatus(), TestTron_AmbiguousBroadcast_AcceptedByNode_NeverSentTwice(), TestTron_AmbiguousBroadcast_Lost_ResendsSameBytesBeforeExpiry(), TestTron_FeeSpikeBeyondQuote_FailsBeforeSigning() (+11 more)

### Community 58 - "compliance/ports.go"
Cohesion: 0.07
Nodes (19): FakeKYCProvider, FakeScreener, NewFakeKYCProvider(), sign(), CreateVerificationInput, CreateVerificationResult, ProviderEvent, ProviderStatus (+11 more)

### Community 59 - "FeeQuote"
Cohesion: 0.06
Nodes (15): FeeEngine, QuoteStore, tronFees, FeeQuote, QuoteStatus, fakeFeeEngine, fakeQuotes, EstimateFeeInput (+7 more)

### Community 60 - "Check"
Cohesion: 0.05
Nodes (25): CanTransition(), derefString(), nullIfEmpty(), insertReconciliationEvent(), scanReconciliationCheck(), insertSweepEvent(), scanSweep(), reconciliationExecer (+17 more)

### Community 61 - "client.js"
Cohesion: 0.10
Nodes (22): affiliateAPI, api, authAPI, cryptoAPI, feeAPI, idempotencyKey(), kycAPI, notificationAPI (+14 more)

### Community 62 - "newTronHarness"
Cohesion: 0.19
Nodes (19): fakeLedger, tronHarness, hasEvent(), newTronHarness(), reservationStatus(), TestTron_AmbiguousBroadcast_AcceptedByNode_NeverSentTwice(), TestTron_AmbiguousBroadcast_Lost_ResendsSameBytesBeforeExpiry(), TestTron_FeeSpikeBeyondQuote_FailsBeforeSigning() (+11 more)

### Community 63 - "prefs/service.go"
Cohesion: 0.12
Nodes (13): base64Decode(), base64Std(), Prefs, Service, hashPin(), NewService(), onlyDigits(), readRandom() (+5 more)

### Community 64 - "evm_deposit_watcher_test.go"
Cohesion: 0.32
Nodes (20): NewEVMDepositWatcher(), fakeTransferStore, newFakeEVMProvider(), newFakeSyncState(), newFakeTransferStore(), TestEVMDepositWatcher_ConfirmedTransferStillMonitoredForReorg(), TestEVMDepositWatcher_DetectsTokenTransferToManagedAddress(), TestEVMDepositWatcher_DuplicateEventNeverProcessedTwice() (+12 more)

### Community 65 - "Provider"
Cohesion: 0.09
Nodes (9): OnChainTransferEvent, Provider, hexToHash32(), transferLogEvents(), parseTransferLog(), topicToAddress(), parseTransferLog(), topicToAddress() (+1 more)

### Community 66 - "NetworkAsset"
Cohesion: 0.09
Nodes (18): assetCodesOf(), toWatcherAssets(), toWatcherAssets(), newFakeRegistry(), fakeAssets, newFakeRegistry(), NetworkAsset, AssetKind (+10 more)

### Community 67 - "newHarness"
Cohesion: 0.16
Nodes (25): generateCustodyKey(), GenerateKeyPair(), keyPairFromPrivateKey(), TestGenerateKeyPair_NeverProducesDuplicateAddresses(), TestGenerateKeyPair_PrivateKeyRoundTrips(), TestGenerateKeyPair_ProducesChecksummedAddress(), TestValidateAddress_RejectsBadChecksum(), KeyPair (+17 more)

### Community 68 - "NewBTCDepositWatcher"
Cohesion: 0.16
Nodes (13): NewBTCDepositWatcher(), satsToBTC(), newFakeUTXOProvider(), TestBTCDepositWatcher_ConfirmsAndCreditsAfterEnoughConfirmations(), TestBTCDepositWatcher_DetectsOutputToManagedAddress(), TestBTCDepositWatcher_ReorgMarksTransferReorged(), TestBTCDepositWatcher_RetriesCreditAfterTransientFailure(), TestBTCDepositWatcher_TwoOutputsSameTxAreBothTracked() (+5 more)

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
Cohesion: 0.05
Nodes (21): Event, Policy, Reservation, ReserveInput, isLimitRejection(), OperationType, categoryFor(), classifyMissingReservation() (+13 more)

### Community 73 - "twofactor/service_test.go"
Cohesion: 0.14
Nodes (27): NewService(), recoveryHash(), newEnabledEnv(), TestAdminReset_DeletesCredentials_AndAlerts(), TestBeginSetup_RecoveryCodes_Are80BitsAndDistinct(), TestBeginSetup_RefusesWhenAlreadyEnabled(), TestConfirm_ConsumesCode_NoImmediateReplayForWithdrawal(), TestConfirm_WithoutSetup_StableError() (+19 more)

### Community 74 - "BlockchainTransfer"
Cohesion: 0.18
Nodes (4): keyOf(), BlockchainTransfer, TransferStatus, TransferStore

### Community 75 - "CustodyWallet"
Cohesion: 0.15
Nodes (7): fakeWallets, CustodyWallet, CustodyWalletFilter, scanCustodyWallet(), fakeRepository, WalletRegistry, fakeWallets

### Community 76 - "BlockchainDepositRecord"
Cohesion: 0.23
Nodes (6): BlockchainDepositRecord, HistoryService, applyNullableDepositFields(), BlockchainDepositReader, fakeDepositReader, BlockchainDepositRecorderRepository

### Community 77 - "aider chat started at 2026-10-09 12:28:57"
Cohesion: 0.10
Nodes (20): , 1. Archivos concretos encontrados., 2. Estado real de cada una de las seis combinaciones de activo y red., 3. Errores demostrables y pruebas existentes., 4. Correcciones mínimas necesarias, por prioridad., 5. Comandos de verificación seguros., ,  (+12 more)

### Community 79 - "github.com/gin-gonic/gin.Context"
Cohesion: 0.12
Nodes (8): AccountHandler, publicHealthError(), TestPublicHealthError_NeverEchoesRawDetail(), getUserID(), New(), pageParams(), twoFAHeader(), Handler

### Community 80 - "ComplianceHandler"
Cohesion: 0.28
Nodes (6): complianceError(), ComplianceHandler, parseUUIDParam(), requestID(), toCaseDTO(), caseDTO

### Community 81 - "Service"
Cohesion: 0.24
Nodes (6): classifyBroadcastError(), broadcastClass, Service, nonceOwner(), TestClassifyTronBroadcastErrors(), WeiToDecimal()

### Community 82 - "ledger/service_test.go"
Cohesion: 0.21
Nodes (21): NewService(), TestSettleReservations_DelegatesToSettler(), TestSettleReservations_FailsClosedWithoutSettler(), TestSettleReservations_PropagatesRepositoryErrorUnwrapped(), TestSettleReservations_RejectsInvalidInputBeforeRepository(), validSettlement(), newFakeRepo(), newFakeReservations() (+13 more)

### Community 83 - "Provider"
Cohesion: 0.09
Nodes (6): BitcoinOutput, BitcoinTransactionStatus, UTXOProvider, Provider, NewProvider(), fakeUTXOProvider

### Community 84 - ".signAndSend"
Cohesion: 0.12
Nodes (6): classifyTronBroadcast(), tronBroadcastClass, TronChain, TronDeps, TronService, ChainRef

### Community 85 - "Bloque 9 — KYC / AML / Compliance foundation"
Cohesion: 0.11
Nodes (17): 10. Auditoría, 11. Privacidad y cifrado, 12. Retención (política — **no** inventar plazos), 13. Configuración, 14. API, 15. Concurrencia y workers, 16. Límites del sistema y qué depende de terceros, 1. Arquitectura (+9 more)

### Community 86 - "NewVelocityTracker"
Cohesion: 0.15
Nodes (16): DefaultConfig(), defaultRateLimits(), defaultVelocity(), envBool(), Config, RateLimitRule, LoadConfigFromEnv(), parseRateLimitRule() (+8 more)

### Community 87 - "NewService"
Cohesion: 0.15
Nodes (10): actorOrSystem(), NewService(), toPublic(), ValidateAddress(), ValidateAddressType(), validateEVMAddress(), ValidateStatus(), ValidateWalletType() (+2 more)

### Community 88 - "engine_test.go"
Cohesion: 0.33
Nodes (22): baseConfig(), freshPrice(), newFakeAssetChecker(), newFakeQuoteStore(), newTestEngine(), TestEstimateFee_ConversionExample(), TestEstimateFee_DisabledAsset_Rejected(), TestEstimateFee_NegativeAmount_Rejected() (+14 more)

### Community 89 - "Bloque 7.7 — Tests / Auditoría del Bloque 7"
Cohesion: 0.12
Nodes (15): 0. Aviso honesto de entorno (léase antes que todo lo demás), 10. Veredicto final, 1. Qué se pudo ejecutar en este entorno, 2. Resumen por bloque, 3. Tests escritos en este entorno, 4. Matriz de pruebas (spec 7.7) — estado real, 5. Seguridad (7.7), 6. Concurrencia (7.7) (+7 more)

### Community 90 - "BuildTRC20TransferRaw"
Cohesion: 0.15
Nodes (20): AddressFromBytes21(), AddressToBytes21(), BuildTRC20TransferRaw(), BuildTRXTransferRaw(), TRC20TransferParams, TRXTransferParams, pbBytes(), pbVarint() (+12 more)

### Community 91 - "clearTokens"
Cohesion: 0.20
Nodes (15): clearTokens(), Admin(), logout(), date(), routes, shortId(), Status(), Table() (+7 more)

### Community 92 - "AdminHandler"
Cohesion: 0.18
Nodes (4): AdminHandler, parsePagination(), emailVerifierStore, RoleAssigner

### Community 93 - "buildProvider"
Cohesion: 0.13
Nodes (19): buildProvider(), envOr(), LoadConfigFromEnv(), looksLikeEVMAddress(), clearBSCEnv(), TestLoadConfigFromEnv_InvalidContractFormat(), TestLoadConfigFromEnv_MissingContract(), TestLoadConfigFromEnv_MissingRPCURL() (+11 more)

### Community 94 - "compliance_routes_test.go"
Cohesion: 0.40
Nodes (14): idem(), idem2fa(), newRig(), TestRoutes_AdminViewDoesNotLeakSecretsOrProviderRefs(), TestRoutes_HoldFlow(), TestRoutes_IDOR(), TestRoutes_ManualReviewFlowAndRBACSplit(), TestRoutes_NormalUserCannotUseAdminEndpoints() (+6 more)

### Community 95 - "FreezeStatus"
Cohesion: 0.18
Nodes (5): FreezeStatus, newFakeFreezeRepo(), TestFreezeGuard_KillSwitchAndFreeze(), secFreezes, fakeFreezeRepo

### Community 96 - "TwoFactorHandler"
Cohesion: 0.13
Nodes (7): LockedError, TwoFactorHandler, respondTwoFAError(), respondTwoFALocked(), PasswordConfirmer, sessionRevoker, twoFAActionRequest

### Community 97 - "scanTreasuryRebalance"
Cohesion: 0.17
Nodes (9): insertTreasuryRebalanceEvent(), scanTreasuryRebalance(), treasuryRebalanceExecer, TreasuryRebalanceRepository, treasuryRebalanceRowScanner, Direction, Event, Status (+1 more)

### Community 98 - "ForCryptoAddress"
Cohesion: 0.17
Nodes (23): ForCryptoAddress(), ForCustodyWallet(), ForTable(), NormalizeAddress(), IsLegacy(), New(), legacyEncrypt(), mustProvider() (+15 more)

### Community 99 - "Landing"
Cohesion: 0.13
Nodes (22): getAccessToken(), coin(), COINS, CountUp(), DEMO, INITIAL_DATA, KzMark(), Landing() (+14 more)

### Community 101 - "NetworkFeeEstimate"
Cohesion: 0.10
Nodes (13): MultiNetworkFeeEstimator, TronFeeEstimator, tronParamsSource, NewTronFeeEstimator(), TestMultiNetworkFeeEstimatorRoutesByNetwork(), TestTronFeeEstimator_EnergyBandwidthToTRX(), TestTronFeeEstimator_Rejections(), EVMFeeEstimator (+5 more)

### Community 102 - "harness"
Cohesion: 0.13
Nodes (4): Service, Candidate, fakeCandidates, harness

### Community 103 - "validate_test.go"
Cohesion: 0.41
Nodes (13): CreatePolicyInput, d(), TestValidatePolicyFields_BoundaryEqualities(), TestValidatePolicyFields_HotEqualsTreasury(), TestValidatePolicyFields_InvalidNetworkOrAsset(), TestValidatePolicyFields_MaxAmountNotPositive(), TestValidatePolicyFields_MinGreaterThanTarget(), TestValidatePolicyFields_NegativeAmounts() (+5 more)

### Community 104 - "time.Time"
Cohesion: 0.05
Nodes (17): fakeTokens, tokenRow, Engine, NewEngine(), DestinationGuard, cdKey(), DestinationStatus, destKey() (+9 more)

### Community 105 - "NewDestinationGuard"
Cohesion: 0.21
Nodes (14): newCDRepo(), TestCheckWithCooldown_AfterCooldown_Allows(), TestCheckWithCooldown_DisabledOrUnsupported_NoWait(), TestCheckWithCooldown_KnownAllowlistedAndBlocked(), TestCheckWithCooldown_NewDestination_RegistersAndWaits(), TestCheckWithCooldown_RepoError_FailsClosed(), TestDestinationSetStatus_ValidatesAndAllowlistBypassesCooldown(), DestinationGuard (+6 more)

### Community 108 - "Bloque 7.4 — Treasury / Rebalancing"
Cohesion: 0.15
Nodes (12): 1. Qué es y qué no es, 2. Arquitectura — qué se reutiliza, 3.1 Política (7.4.1), 3.2 Detección (7.4.2, `EvaluatePolicies` / `evaluatePolicy`), 3.3 Ejecución (`process.go`, mismo esqueleto que Sweeps 7.3), 3. Flujo, 4. Persistencia (7.4.3), 5. Worker (7.4.4) (+4 more)

### Community 109 - "Service"
Cohesion: 0.17
Nodes (8): classifyBroadcastError(), NewService(), TestNewService_RejectsNonEVMNetwork(), AssetRegistry, broadcastClass, Deps, Service, RefillStatus

### Community 110 - "sync.Mutex"
Cohesion: 0.13
Nodes (10): auditEvent, fakeAuditLogger, tronDepositEnv, newTronDepositEnv(), TestTronDeposit_DetectedConfirmedCreditedOnce(), TestTronDeposit_ReorgBeforeConfirmationNeverCredits(), fakeEventRepo, fakeAudit (+2 more)

### Community 111 - "Bloque 7.6 — Recovery / Exceptions"
Cohesion: 0.18
Nodes (10): 1. Qué es y qué no es, 2. Cobertura de la lista mínima del spec, 3. Arquitectura, 4. Persistencia, 5. Kill switch (7.6.2) — con una diferencia deliberada, 6. Auditoría (7.6.3), 7. Resolución (nunca automática), 8. Límites conocidos / fuera de alcance (+2 more)

### Community 112 - "acctRouter"
Cohesion: 0.24
Nodes (11): acctPost(), acctRouter(), TestChangePassword_Success_And2FA(), TestChangePassword_WrongCurrent_Is400Not401(), TestForgotPassword_IdenticalResponse_KnownVsUnknown(), TestPublicEndpoints_RejectMalformedInput(), TestResendVerification_IdenticalResponse(), TestResetPassword_WeakAndInvalidToken() (+3 more)

### Community 113 - "Bloque 7.5 — Reconciliation"
Cohesion: 0.20
Nodes (9): 1. Qué es y qué no es, 2. Por qué existe un "baseline" en el leg 1, 3. Arquitectura — qué se reutiliza, 4. Estados, 5. Auditoría, 6. Worker, 7. Idempotencia y concurrencia, 8. Límites conocidos / fuera de alcance (+1 more)

### Community 114 - ".Sign"
Cohesion: 0.22
Nodes (10): Decryptor, DisabledBitcoinSigner, EVMSigner, KeyResolver, SignedTx, Signer, SignRequest, NewEVMSigner() (+2 more)

### Community 115 - "WatcherFailedEventRepository"
Cohesion: 0.25
Nodes (3): FailedDepositEvent, truncateErr(), WatcherFailedEventRepository

### Community 116 - "Gestión de claves de custodia: cifrado v1, rotación y herramientas"
Cohesion: 0.22
Nodes (8): 1. Formato de cifrado, 2. Configuración multi-clave, 3. Migración perezosa, 4. Procedimiento de rotación, 5. Imágenes: producción vs herramientas, 6. KeyProvider (KMS/HSM), 7. Revisión de fugas (logs/errores), Gestión de claves de custodia: cifrado v1, rotación y herramientas

### Community 117 - "Locker"
Cohesion: 0.08
Nodes (15): Locker, releaser(), RunExclusive(), SingletonKey(), TestRunExclusive_NilLockerRunsDirectly(), TestRunExclusive_OnlyOneInstanceRuns(), NewWorker(), NewWorker() (+7 more)

### Community 118 - "didit.go"
Cohesion: 0.13
Nodes (13): headerValue(), mapStatus(), marshalCanonical(), marshalString(), normalizeReason(), shortenFloats(), sortKeys(), statusToEvent() (+5 more)

### Community 119 - "blockchain_confirm_test.go"
Cohesion: 0.33
Nodes (15): newFakeLedger(), newFakeRecorder(), newService(), sampleConfirmedTransfer(), TestConfirmBlockchainDeposit_ConcurrentWorkers_OnlyOneCredit(), TestConfirmBlockchainDeposit_CreditsOnce(), TestConfirmBlockchainDeposit_DuplicateCallIsIdempotent(), TestConfirmBlockchainDeposit_EntriesAreBalanced() (+7 more)

### Community 120 - "CustodyWalletRepository"
Cohesion: 0.13
Nodes (16): registryCheck, newCustodyGasStation(), newTronCustodyGasStation(), NewTronService(), CustodyWalletRepository, GasStationPolicyRepository, GasStationRefillRepository, NetworkAssetRepository (+8 more)

### Community 121 - "newH2FAEnv"
Cohesion: 0.22
Nodes (15): errCode(), NewTwoFactorHandler(), newH2FAEnv(), nextTOTP(), TestRespondDomainError_TwoFALocked_Is429(), TestTwoFAAdminReset(), TestTwoFADisable_MalformedBody_And_NoPasswordConfirmer(), TestTwoFADisable_RequiresPasswordAndCode() (+7 more)

### Community 122 - "WithdrawalChain"
Cohesion: 0.10
Nodes (7): ResourceQuote, ceilPct(), computeQuote(), WithdrawalChain, NewWithdrawalChain(), TRC20TransferParam(), TestComputeQuoteLimits()

### Community 123 - "WithdrawalChain"
Cohesion: 0.10
Nodes (7): ceilPct(), computeQuote(), WithdrawalChain, AccountResources, nonNeg(), TRC20TransferParam(), TestComputeQuoteLimits()

### Community 124 - ".signAndSend"
Cohesion: 0.15
Nodes (6): classifyTronBroadcast(), tronBroadcastClass, TronChain, TronDeps, TronService, NewTronService()

### Community 125 - "Endurecimiento para varias réplicas"
Cohesion: 0.25
Nodes (7): 1. Locks entre procesos (`pg_advisory_lock`), 2. Migraciones, 3. Apagado ordenado, 4. Rate limiter Redis, 5. Endpoints admin (super_admin + 2FA `X-2FA-Code` + auditoría, incluidos los rechazos), Endurecimiento para varias réplicas, Límites conocidos

### Community 126 - ".prepareResourceTx"
Cohesion: 0.11
Nodes (7): isRevertError(), NetworkRuntime, observer, ResourceChain, Service, sunToTRX(), gasOwner()

### Community 127 - "login_2fa_test.go"
Cohesion: 0.18
Nodes (17): fakeTwoFactor, newActiveAuthUser(), TestComplete2FA_ChallengeSingleUse(), TestComplete2FA_ConcurrentCompletionMintsOneSession(), TestComplete2FA_ExpiredChallenge(), TestComplete2FA_LocksAfterMaxWrongAttempts(), TestComplete2FA_RecoveryCodeAcceptedOnce(), TestComplete2FA_ValidCodeIssuesVerifiedSession() (+9 more)

### Community 128 - "Fase TRON: depósitos y retiros TRC-20 (USDT)"
Cohesion: 0.25
Nodes (7): BLOQUEADO (requiere un nodo real o una decisión externa), Diseño y decisiones de seguridad, Fase TRON: depósitos y retiros TRC-20 (USDT), Puesta en marcha (orden recomendado), Qué se reutilizó (sin duplicar), Riesgos y límites conocidos, Variables de entorno

### Community 129 - "NewLimitService"
Cohesion: 0.27
Nodes (10): NewLimitService(), newFakeLimiter(), TestLimitService_CommitThenReleaseFails(), TestLimitService_ConcurrentReservationsRespectDailyLimit(), TestLimitService_MinMaxEnforced(), TestLimitService_ReleaseReturnsAllowance(), TestLimitService_ReplayProtection(), usageKey() (+2 more)

### Community 130 - "Deps"
Cohesion: 0.18
Nodes (3): NewService(), CandidateSource, Deps

### Community 131 - "Fase TRON: depósitos y retiros TRC-20 (USDT)"
Cohesion: 0.25
Nodes (7): BLOQUEADO (requiere un nodo real o una decisión externa), Diseño y decisiones de seguridad, Fase TRON: depósitos y retiros TRC-20 (USDT), Puesta en marcha (orden recomendado), Qué se reutilizó (sin duplicar), Riesgos y límites conocidos, Variables de entorno

### Community 132 - "Fase C — Verificación de email, restablecimiento y cambio de contraseña"
Cohesion: 0.33
Nodes (5): Configuración (variables de entorno del backend), Endpoints, Fase C — Verificación de email, restablecimiento y cambio de contraseña, Garantías, Pendiente fuera de esta fase

### Community 133 - "acctUsers"
Cohesion: 0.44
Nodes (8): adminRouter(), adminUser(), newAcctUsers(), TestManualVerifyEmail(), TestUnblockUser_OnlyBlockedAccounts(), TestUnblockUser_RequiresReason(), TestUnblockUser_RespectsEmailVerification_AndAudits(), acctUsers

### Community 134 - "Fase E — 2FA endurecido"
Cohesion: 0.33
Nodes (5): Efectos que debes conocer, Endpoints, Fase E — 2FA endurecido, Pendiente fuera de esta fase, Qué cambió

### Community 135 - "user.js"
Cohesion: 0.16
Nodes (20): Coin(), COINS, nav, getNetwork(), NETWORKS, setNetwork(), showAsset(), SOON_NETWORKS (+12 more)

### Community 136 - "Store"
Cohesion: 0.20
Nodes (3): Event, Store, Update

### Community 137 - "FullNode"
Cohesion: 0.18
Nodes (7): decodeNodeMessage(), BlockRef, blockResp, FullNode, TxInfo, TxLog, hexAddr()

### Community 138 - ".prepareResourceTx"
Cohesion: 0.11
Nodes (7): isRevertError(), NetworkRuntime, observer, ResourceChain, Service, sunToTRX(), gasOwner()

### Community 140 - "AGENTS.md"
Cohesion: 0.40
Nodes (4): Comandos relevantes, Desarrollo local, Estructura, Notas clave

### Community 141 - "Service"
Cohesion: 0.12
Nodes (13): EmailVerifier, TwoFactorGate, AuthResult, RegisterInput, Service, NewAuthHandler(), TestAuthHandler_Login_BadRequest(), TestAuthHandler_LoginTwoFactor_BadRequest() (+5 more)

### Community 142 - "ValidateSecrets"
Cohesion: 0.40
Nodes (5): looksLikePlaceholder(), ValidateSecrets(), contains(), TestValidateSecrets_AllGood(), TestValidateSecrets_NeverLeaksValue()

### Community 143 - ".transferEventsInRangeAdaptive"
Cohesion: 0.17
Nodes (13): TestBlockRangeErrorForObservedHeadIsTyped(), blockRangeErrorForObservedHead(), chunkRecipientAddresses(), decodeTransferLog(), Client, isLogLimitError(), buildTransferLog(), TestDecodeTransferLog_SharedAcrossNetworks() (+5 more)

### Community 144 - "GasStationPolicy"
Cohesion: 0.17
Nodes (8): fakePolicies, newFakePolicies(), GasStationPolicy, scanGasStationPolicy(), PolicyStore, noGas, fakePolicies, newFakePolicies()

### Community 145 - "Dependencias nuevas requeridas (Fase 1 — BSC)"
Cohesion: 0.40
Nodes (4): Cerrado en esta iteración, Dependencias nuevas requeridas (Fase 1 — BSC), Pendiente antes de producción, Qué pude validar aquí y qué no

### Community 146 - "fakeEVMProvider"
Cohesion: 0.12
Nodes (4): BlockchainProvider, OnChainNativeTransferEvent, fakeEVMProvider, fakeTransferRange

### Community 147 - "Service"
Cohesion: 0.15
Nodes (22): NewGuard(), NewEventLogger(), NewFreezeGuard(), TestEventLogger_StripsSecretLikeMetadata(), TestGuard_CheckOwnership_IDOR(), TestGuard_RequireActiveAccount(), TestGuard_RequireRole(), TestStepUpGuard_RequireForAmount() (+14 more)

### Community 148 - "EVMChain"
Cohesion: 0.11
Nodes (3): EVMChain, GasTopUp, NetworkRuntime

### Community 149 - "EVMChain"
Cohesion: 0.11
Nodes (3): EVMChain, GasTopUp, NetworkRuntime

### Community 150 - "CryptoAddress"
Cohesion: 0.16
Nodes (8): key(), toPublicAddress(), CryptoAddress, CryptoAddressRepository, Address, AddressStore, AddressType, fakeStore

### Community 151 - "newTestUser"
Cohesion: 0.22
Nodes (8): DestinationCooldownError, NewBlockchainWithdrawalRepository(), NewSecurityDestinationRepository(), asCooldown(), newTestUser(), TestDestinationRepo_AdminAllowlistAndBlock(), TestDestinationRepo_PendingCooldownLifecycle(), TestWithdrawalRepo_LimitReservationRoundTrip()

### Community 152 - "New"
Cohesion: 0.27
Nodes (13): TwoFAVerifyMiddleware(), TestAuthRequiredRejectsLoginChallengeToken(), ServerToServerHMAC(), AuthRequired(), CORSConfig(), IdempotencyKeyRequired(), RequireRole(), BodyLimit() (+5 more)

### Community 153 - "KORZADI — INFORME DE AUDITORÍA DE SEGURIDAD"
Cohesion: 0.15
Nodes (16): Estado del repositorio, Hallazgos confirmados (previos + nuevos), Historial de rondas, KORZADI — INFORME DE AUDITORÍA DE SEGURIDAD, Metadatos, Próximos pasos (post-Ronda 5 — Parada justificada), Resumen ejecutivo, Riesgos residuales actuales (post-Ronda 6 — con autorización para todas las correcciones seguras) (+8 more)

### Community 154 - "BlockchainWithdrawalHandler"
Cohesion: 0.31
Nodes (7): ParseAmount(), BlockchainWithdrawalHandler, maskAddress(), queryInt(), respondWithdrawalError(), toWithdrawalDTO(), withdrawalDTO

### Community 155 - "NewService"
Cohesion: 0.27
Nodes (14): Claims, TestService_Login_ActiveUser(), TestService_Logout_RevokesSession(), TestService_Refresh_RotatesToken(), TestService_Register_CreatesWalletAndAccount(), newMemChallenges(), NewService(), newAuthMemory() (+6 more)

### Community 156 - "Backups fuera del VPS"
Cohesion: 0.50
Nodes (3): Activación, Backups fuera del VPS, Verificación periódica

### Community 159 - "TestBlockchainDeposit_FullFlowOnBSCETHAndTRON"
Cohesion: 0.15
Nodes (16): main(), NewAddressToUserResolver(), NewBlockchainDepositRecorderRepository(), NewBlockchainSyncStateRepository(), NewBlockchainTransferRepository(), NewDepositIncidentRepository(), NewWatcherFailedEventRepository(), provisionDepositAddress() (+8 more)

### Community 160 - "EnvKeyProvider"
Cohesion: 0.18
Nodes (6): open(), TestProviderConfigErrorsNeverEchoKeys(), aeadFromHex(), NewEnvKeyProvider(), EnvKeyProvider, KeyProvider

### Community 161 - "EVMChain"
Cohesion: 0.12
Nodes (3): EVMChain, GasTopUp, NetworkRuntime

### Community 163 - "Background"
Cohesion: 0.16
Nodes (4): Background, BackgroundVerifier, Service, Supervisor

### Community 164 - "NewReservationService"
Cohesion: 0.37
Nodes (12): NewReservationService(), activeQuote(), newFakeLedgerReservations(), newFakeWalletResolver(), TestConsumeFee_MarksConsumed(), TestConsumeFee_WithoutReservation_Rejected(), TestReleaseFee_ReturnsToAvailableAndCancelsQuote(), TestReserveFee_ConcurrentCalls_OnlyOneReservationSurvives() (+4 more)

### Community 165 - "Chain"
Cohesion: 0.13
Nodes (3): Chain, GasTopUp, NetworkRuntime

### Community 166 - ".authorizeCreate"
Cohesion: 0.13
Nodes (6): Service, limitReferenceID(), maskAddress(), TestMaskAddress(), EvaluateThresholds(), isPinCode()

### Community 167 - "GasStation"
Cohesion: 0.26
Nodes (4): NewGasStation(), hexToHash32ForWithdrawal(), GasStation, NonceAllocator

### Community 168 - "LoadConfigFromEnv"
Cohesion: 0.24
Nodes (11): decimalEnv(), durationEnv(), int64Env(), LoadConfigFromEnv(), clearFeeEnv(), TestLoadConfigFromEnv_CustomValues(), TestLoadConfigFromEnv_RejectsExcessiveVolatilityBuffer(), TestLoadConfigFromEnv_RejectsMinGreaterThanMax() (+3 more)

### Community 169 - "Handlers"
Cohesion: 0.25
Nodes (13): Service, NewService(), NewAddressHandler(), newNetRouter(), TestActiveNetworks_ListsOnlyWatchedNetworks(), TestAddressCreate_NetworkWithoutWatcher_Rejected(), TestTronDepositAddress_Unavailable(), CryptoHandler (+5 more)

### Community 170 - "Transfer"
Cohesion: 0.05
Nodes (23): decimalPlaces(), Service, sameTransferRequest(), toView(), transferLedgerKey(), Deposit, TransactionStatus, Transfer (+15 more)

### Community 171 - "Policy"
Cohesion: 0.23
Nodes (6): Policy, PolicyFilter, scanTreasuryPolicy(), noTreasury, treasuryAdmin, treasuryPolicyRowScanner

### Community 172 - "StubKYCProvider"
Cohesion: 0.17
Nodes (4): KYCVerificationStatus, NewStubKYCProvider(), KYCProvider, StubKYCProvider

### Community 173 - "RunWithLockTimeout"
Cohesion: 0.16
Nodes (12): apply(), discardConn(), Run(), RunWithLockTimeout(), discard(), KeyID(), New(), NewPool() (+4 more)

### Community 174 - "WorkContext"
Cohesion: 0.19
Nodes (10): TestTwoInstances_ConcurrentWorkers_NeverOverCommitHotWallet(), NewWorker(), New(), TestShutdown_SoftContextStopsLoop(), TestShutdown_TimeoutForcesHardCancel(), TestShutdown_WaitsForInFlightWork(), TestSupervisor_PanicDoesNotCrash(), TestWorkContext_WithoutSupervisorIgnoresCancel() (+2 more)

### Community 175 - "ReservationSettlementInput"
Cohesion: 0.17
Nodes (5): ReservationSettlementInput, ReservationSettler, fakeAdjustmentRepo, fakeRepo, settlingRepo

### Community 176 - "newEnv"
Cohesion: 0.23
Nodes (8): env, newEnv(), TestBroadcastErrorKinds(), TestFailedTransfersProduceNoEvents(), TestHealthAndBalances(), TestQuoteWithAndWithoutStakedResources(), TestReceiptLifecycleAndConfirmations(), TestReorgChangesReceiptBlockHash()

### Community 177 - "call"
Cohesion: 0.31
Nodes (9): call(), newOps(), TestCreateTreasuryPolicy_AlwaysDisabledAndAudited(), TestRegisterWallet_GeneratesServerSide_NeverLeaksKey(), TestRegisterWallet_ImportAllowedWhenEnabled(), TestRegisterWallet_ImportDisabledByDefault(), TestResolveWithdrawal_FailureIsAudited(), TestResolveWithdrawal_ShortReasonRejected() (+1 more)

### Community 178 - "adminOpsEngine"
Cohesion: 0.53
Nodes (6): adminOpsEngine(), do(), TestAdminOps_NoStepUp_RoutesNotRegistered(), TestAdminOps_Requires2FA(), TestAdminOps_RequiresSuperAdmin(), TestAdminOps_SuperAdminWith2FA_Allowed()

### Community 179 - "@playwright/test"
Cohesion: 0.20
Nodes (4): decodeBase32(), generateTotp(), fakeUser, @playwright/test

### Community 180 - "AdminAdvanced.jsx"
Cohesion: 0.29
Nodes (9): AdminAdvanced(), arr(), errText(), Field(), sections, SelectField(), short(), Table() (+1 more)

### Community 181 - "NewHistoryService"
Cohesion: 0.38
Nodes (10): NewHistoryService(), newFakeDepositReader(), sampleRecord(), TestGetForUser_ReturnsErrDepositNotFound_WhenBelongsToAnotherUser(), TestGetForUser_ReturnsErrDepositNotFound_WhenIDDoesNotExist(), TestGetForUser_ReturnsRecordForOwner(), TestListForUser_CapsExcessivePageSize(), TestListForUser_DefaultsPageSizeWhenUnset() (+2 more)

### Community 182 - "net/http.Client"
Cohesion: 0.24
Nodes (7): NewBinancePriceProvider(), NewFullNodeWithHTTP(), Config, BinancePriceProvider, CoinGeckoPriceProvider, NewCoinGeckoPriceProvider(), NewFullNodeWithHTTP()

### Community 183 - "Engine"
Cohesion: 0.33
Nodes (6): NewEngine(), PriceProvider, AssetSupportChecker, Engine, NetworkFeeEstimator, ServiceFeePolicy

### Community 184 - "SecurityHandler"
Cohesion: 0.31
Nodes (4): ValidatePolicyShape(), TestValidatePolicyShape(), SecurityHandler, parseOptionalDecimal()

### Community 185 - "AddressHandler"
Cohesion: 0.27
Nodes (6): SupportedNetworks(), TestSupportedNetworks_DoesNotIncludeTRONOrBSC(), AddressHandler, supportedNetworkNames(), toResponse(), addressResponse

### Community 186 - "btcclient/client_test.go"
Cohesion: 0.24
Nodes (9): TestBtcToSats_ExactConversion(), TestIsTransient_NotFoundIsNotRetried(), TestIsTransient_RPCWarmupIsRetried(), TestLoadConfigFromEnv_Configured(), TestLoadConfigFromEnv_OptionalWhenUnset(), envOr(), LoadConfigFromEnv(), isTransient() (+1 more)

### Community 187 - "NewBlockchainDepositService"
Cohesion: 0.39
Nodes (6): NewBlockchainDepositService(), BlockchainDepositRecorder, BlockchainDepositService, ClearingAccountResolver, LedgerPoster, UserAccountResolver

### Community 188 - "Price"
Cohesion: 0.36
Nodes (6): errorsJoin(), validatePrice(), validCachedPrice(), CachedFallbackPriceProvider, fakePriceProvider, Price

### Community 189 - ".evaluatePolicy"
Cohesion: 0.28
Nodes (4): boolPtr(), capAmount(), Service, weiToDecimalForAsset()

### Community 193 - "ParseSignedTx"
Cohesion: 0.25
Nodes (6): allZero(), ParseSignedTx(), pbReadVarint(), pbWalk(), TestParseNeverPanicsOnGarbage(), TxID()

### Community 194 - "Service"
Cohesion: 0.48
Nodes (3): Service, NewService(), UserResolver

### Community 195 - "rpcClient"
Cohesion: 0.38
Nodes (4): backoffDuration(), newRPCClient(), redactURLErr(), rpcClient

### Community 196 - "FeeHandler"
Cohesion: 0.32
Nodes (6): FeeHandler, respondFeeError(), TestFeeHandlerQuoteLookupAndProviderFailureAreSafe(), toEstimateFeeResponse(), TestRespondFeeError_DoesNotLeakProviderDetails(), estimateFeeResponse

### Community 197 - "netEntries"
Cohesion: 0.43
Nodes (7): netEntries(), sameNetEntries(), TestNetEntries_DecimalScaleInsensitive(), TestNetEntries_DetectsDifferentPayloads(), TestNetEntries_OrderAndSplitInsensitive(), d(), TestCheckSettlementCoverage()

### Community 198 - ".Sign"
Cohesion: 0.39
Nodes (3): FamilySigner, validateTronSignRequest(), verifySignedTron()

### Community 199 - "reconciliation/model_test.go"
Cohesion: 0.40
Nodes (5): FundCategoryForWalletType(), TestFundCategoryForWalletType(), TestNextStatus_FreshMismatchThenEscalation(), TestNextStatus_MatchAlwaysWins(), FundCategory

### Community 200 - "rpcError"
Cohesion: 0.33
Nodes (3): asRPCError(), rpcError, rpcResponse

### Community 201 - "harness"
Cohesion: 0.40
Nodes (3): fakeAudit, fakeDecryptor, harness

### Community 202 - "LoadConfigFromEnv"
Cohesion: 0.50
Nodes (4): DefaultConfig(), envBool(), LoadConfigFromEnv(), Config

### Community 203 - "newUserForTokens"
Cohesion: 0.60
Nodes (5): NewAccountTokenRepository(), newUserForTokens(), TestAccountTokens_ConcurrentConsume_ExactlyOneWins(), TestAccountTokens_CreateConsume_SingleUse_Expiry(), TestAccountTokens_NewTokenInvalidatesPrevious_AndLastCreatedAt()

### Community 206 - ".Sign"
Cohesion: 0.39
Nodes (3): FamilySigner, validateTronSignRequest(), verifySignedTron()

### Community 207 - "harness"
Cohesion: 0.40
Nodes (3): fakeAudit, fakeDecryptor, harness

### Community 209 - "Config"
Cohesion: 0.67
Nodes (3): Config, LoadConfig(), RetentionPolicy

### Community 210 - "hmacRouter"
Cohesion: 0.67
Nodes (4): hmacRouter(), sign(), TestServerToServerHMAC_EmptySecret_FailsClosed(), TestServerToServerHMAC_ValidAndInvalid()

### Community 220 - "doIdem"
Cohesion: 0.33
Nodes (5): doIdem(), idemRouter(), TestIdempotencyKeyRequired_AcceptsValid(), TestIdempotencyKeyRequired_Missing(), TestIdempotencyKeyRequired_RejectsInvalid()

### Community 221 - "NewRedisRateLimiter"
Cohesion: 0.18
Nodes (8): NewRedisRateLimiter(), deadRedis(), doGET(), TestRedisRateLimiter_DefaultFailOpen_WhenRedisDown(), TestRedisRateLimiter_FailClosed_WhenRedisDown(), TestSecurityRateLimit_FailClosedOperations(), AtomicIncr(), RedisRateLimiter

### Community 223 - "PaymentOrder"
Cohesion: 0.25
Nodes (5): PaymentOrder, NewPaymentOrderRepository(), ChargeResult, OrderRepository, PaymentOrderRepository

### Community 233 - "Security.jsx"
Cohesion: 0.80
Nodes (4): ChangePasswordCard(), RecoveryCodesBlock(), Security(), TwoFactorManageCard()

### Community 238 - "blockchainwithdrawal/security_test.go"
Cohesion: 0.15
Nodes (24): newSecDest(), newSecEnv(), newSecLimiter(), secDestKey(), TestSecurity_BlockedDestination_Rejected(), TestSecurity_Cancel_ReleasesLimitReservation(), TestSecurity_Completed_CommitsLimitAndMarksDestinationKnown(), TestSecurity_CooldownElapsed_AllowsWithdrawal() (+16 more)

## Knowledge Gaps
- **16 isolated node(s):** `korzadi-wallet/backend`, `Service`, `Service`, `Service`, `harness` (+11 more)
  These have ≤1 connection - possible missing edges or undocumented components. (Counts symbols only; 720 node(s) total have ≤1 connection when file, concept and rationale nodes are included.)
- **31 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `Network` connect `Network` to `backend/cmd/api/main.go`, `testing.T`, `Deps`, `main`, `Service`, `context.Context`, `.prepareResourceTx`, `Client`, `Service`, `AdminOpsHandler`, `time.Duration`, `math/big.Int`, `fakeEVMProvider`, `GenerateKeyPair`, `EVMChain`, `EVMChain`, `CryptoAddress`, `Withdrawal`, `Deps`, `github.com/shopspring/decimal.Decimal`, `TestBlockchainDeposit_FullFlowOnBSCETHAndTRON`, `.Sign`, `EVMChain`, `fakeChain`, `EVMDepositWatcher`, `.authorizeCreate`, `GasStation`, `LoadConfigFromEnv`, `Rebalance`, `Policy`, `TransactionReceipt`, `GasStationRefill`, `Engine`, `Service`, `AddressHandler`, `FeeQuote`, `NewBlockchainDepositService`, `Check`, `evm_deposit_watcher_test.go`, `Provider`, `NetworkAsset`, `NewBTCDepositWatcher`, `BlockchainTransfer`, `BlockchainDepositRecord`, `.EstimateNetworkFee`, `Deps`, `Provider`, `.signAndSend`, `NewService`, `engine_test.go`, `buildProvider`, `ForCryptoAddress`, `fakeStore`, `NetworkFeeEstimate`, `harness`, `validate_test.go`, `time.Time`, `Deps`, `fakeStore`, `Service`, `.Sign`, `WatcherFailedEventRepository`, `newFakeAssetValidator`, `CustodyWalletRepository`, `WithdrawalChain`, `WithdrawalChain`, `.signAndSend`, `.prepareResourceTx`?**
  _High betweenness centrality (0.027) - this node is a cross-community bridge._
- **What connects `korzadi-wallet/backend`, `Service`, `Service` to the rest of the system?**
  _16 weakly-connected nodes found - possible documentation gaps or missing edges._
- **Should `backend/cmd/api/main.go` be split into smaller, more focused modules?**
  _Cohesion score 0.06796106979573416 - nodes in this community are weakly interconnected._
- **Why does `Deps` connect `Deps` to `backend/cmd/api/main.go`, `Network`, `newHarness`, `time.Time`, `Service`, `time.Duration`, `Service`, `Locker`, `Service`?**
  _High betweenness centrality (0.021) - this node is a cross-community bridge._
- **Should `testing.T` be split into smaller, more focused modules?**
  _Cohesion score 0.026383526383526385 - nodes in this community are weakly interconnected._
- **Why does `main()` connect `main` to `backend/cmd/api/main.go`, `testing.T`, `NewLimitService`, `Deps`, `newHarness`, `Service`, `Client`, `Item`, `Service`, `ValidateSecrets`, `AdminOpsHandler`, `math/big.Int`, `GenerateKeyPair`, `Service`, `btcclient/client.go`, `testPool`, `newTestUser`, `New`, `Deps`, `NewService`, `btcwallet/keypair_test.go`, `NewSecurityCounter`, `TestBlockchainDeposit_FullFlowOnBSCETHAndTRON`, `.Sign`, `NewCachedFallbackPriceProvider`, `NewReservationService`, `LoadConfigFromEnv`, `GasStation`, `Handlers`, `LoadConfigFromEnv`, `RunWithLockTimeout`, `WorkContext`, `TransactionReceipt`, `NewHistoryService`, `AuditLogger`, `net/http.Client`, `Engine`, `btcclient/client_test.go`, `NewBlockchainDepositService`, `prefs/service.go`, `evm_deposit_watcher_test.go`, `NetworkAsset`, `Service`, `NewBTCDepositWatcher`, `twofactor/service_test.go`, `LoadConfigFromEnv`, `newUserForTokens`, `github.com/gin-gonic/gin.Context`, `Config`, `ledger/service_test.go`, `Provider`, `NewVelocityTracker`, `NewService`, `buildProvider`, `PaymentOrder`, `ForCryptoAddress`, `NewWorker`, `NetworkFeeEstimate`, `time.Time`, `NewDestinationGuard`, `Locker`, `CustodyWalletRepository`, `newH2FAEnv`?**
  _High betweenness centrality (0.015) - this node is a cross-community bridge._
- **Should `Network` be split into smaller, more focused modules?**
  _Cohesion score 0.03990610328638498 - nodes in this community are weakly interconnected._