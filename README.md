# Korzadi Wallet

Korzadi Wallet is a custodial crypto wallet and P2P trading platform built around a Go backend and a React frontend. The codebase includes authentication, wallet operations, ledger accounting, blockchain deposit/withdrawal flows, P2P offers/orders/escrow, compliance hooks, and admin tooling.

This repository is active and functional for local development and integration testing, but it should not be treated as fully production-ready without a staged validation pass against real infrastructure and security controls.

## 1. Project overview

- Backend: Go + Gin
- Frontend: React 18 + Vite + TypeScript/JavaScript
- Database: PostgreSQL 16
- Cache / rate limiting: Redis 7
- Containerization: Docker Compose
- Primary goal: manage user wallets, internal ledger accounting, blockchain on/off ramps, and P2P transactions

The project combines an internal financial ledger with external blockchain integrations for deposit and withdrawal paths. Security-sensitive areas include private key custody, 2FA, rate limiting, admin authorization, and key rotation.

## 2. Current status

### What is implemented

- User authentication and session flows
- Email verification and password recovery flows
- 2FA enforcement for critical actions
- Wallet balances, transfers, and transaction history
- Internal ledger plus reservation logic for financial operations
- Blockchain deposit monitoring and withdrawal logic
- P2P market flows: offers, orders, escrow, disputes, notifications
- Compliance hooks and admin routes
- Docker-based local environment with PostgreSQL, Redis, backend, and frontend

### What is partially implemented or not yet verified

- Real production validation with live RPC nodes, SMTP, and KMS/HSM
- Final production hardening across all security controls
- Automatic migration/rollback assurance in production environments
- Complete end-to-end validation for every blockchain network and provider integration

### Security posture

The project includes a security review (`SECURITY_REVIEW.md`) and several fixes already landed in code. However, residual risk remains in areas that depend on live infrastructure, environment configuration, and operator operational discipline.

## 3. Architecture

The repository is organized as follows:

```text
korzadi-wallet/
├── backend/                 # Go API and business logic
│   ├── cmd/                 # API, admin, migration, and utility commands
│   ├── internal/
│   │   ├── application/     # Services (auth, wallet, custody, p2p, security, admin)
│   │   ├── domain/          # Domain entities, ports, and rules
│   │   ├── http/            # Handlers, middleware, and router
│   │   └── infrastructure/  # DB, Redis, crypto, blockchain clients, mailer, compliance
│   ├── migrations/          # SQL migration files
│   ├── tests/               # Integration-oriented tests
│   ├── go.mod               # Go module definition
│   └── Dockerfile           # Backend runtime/build image
├── frontend/                # React app
│   ├── src/                 # Pages, components, api calls, styles
│   ├── public/              # Static assets and manifest files
│   ├── package.json         # Frontend dependencies and scripts
│   ├── vite.config.js       # Vite configuration
│   └── Dockerfile           # Frontend image
├── docs/                    # Operational and production checklist docs
├── infrastructure/           # Backup and external infra config
├── .github/workflows/       # CI pipelines
├── .env.example             # Environment template
├── docker-compose.yml        # Main local stack
├── docker-compose.test.yml   # Integration test stack
├── README.md                # Project documentation
├── SECURITY_REVIEW.md        # Security review and risk tracking
├── AGENTS.md                # Repo-specific agent guidance
└── .gitignore
```

## 4. Main capabilities

### Authentication and account security

- JWT-based session flow
- Email verification and password reset
- 2FA enforcement for privileged or sensitive operations
- Admin and compliance role checks

### Wallet and finance

- Internal ledger accounting and reservation tracking
- Transfer and wallet operations
- Crypto deposit monitoring
- Withdrawal authorization and destination controls
- Multi-network blockchain support and token integration hooks

### P2P platform

- Offer creation and matching
- Order lifecycle management
- Escrow and dispute handling
- Notifications and chat-like communication flows

### Compliance and admin

- Admin panels and privileged operational actions
- KYC/compliance integration placeholders and webhook support
- Operational tooling for key generation and maintenance tasks

## 5. Technology stack

### Backend

- Go 1.26.6
- Gin web framework
- PostgreSQL 16
- Redis 7
- go-ethereum and blockchain-related crypto libraries
- JWT and TOTP/WebAuthn support

### Frontend

- React 18
- Vite 5
- React Router
- Lucide icons, QR support, axios
- PWA support enabled via Vite plugin

## 6. Local setup

Requirements:

- Docker + Docker Compose
- Node.js for frontend work outside Docker
- Go toolchain for backend build/test
- Access to package registries and network for dependency installation

Quick start:

```bash
cp .env.example .env
# customize the env file with real secrets before running anything sensitive
docker compose up --build
```

Then access:

- Backend API: http://localhost:8060
- Frontend: http://localhost:5173

## 7. Environment and operational notes

This project expects a real environment file with secrets and infrastructure settings. The repository includes `.env.example`, but `.env` must not be committed.

Critical variables include:

- Database connection and credentials
- JWT secrets
- Redis configuration
- 2FA encryption key
- Custody encryption keys and active key ID
- Blockchain RPC URLs and contract addresses
- SMTP configuration for email flows

Production configuration must be validated carefully because several security-sensitive features depend on real external systems and key material.

## 8. Security summary

The project includes a dedicated review in `SECURITY_REVIEW.md`. The current state is:

- Fixed or improved: private key-to-address validation, key health validation, critical admin and financial route protections
- Partially resolved: fail-closed policies on some paths, best-effort key re-encryption, and startup validation for critical rewriters
- Still requires validation: live KMS/HSM, production RPC testing, provider integration verification, and operational hardening

No claim of full production readiness should be made without completing the checks defined in the security review and the production checklist.

## 9. Production readiness gate

Before production deployment, the platform should verify at least:

- Secret and key rotation strategy is documented and tested
- Critical admin endpoints remain fail-closed under Redis outage or backend failure
- Custody encryption health checks are confirmed in the actual runtime environment
- Private key import and wallet matching logic is tested in staging with real keys
- RPC providers and blockchain watchers are validated against the target network
- Email, KYC, and monitoring systems are working end-to-end
- Backups and restore procedures are tested
- Security review findings are re-checked after any change in auth, custody, or financial flow

## 10. Repository language composition

This repository is primarily a Go backend with a React frontend. The codebase is not a single-language monolith; it mixes:

- Go services and APIs for backend logic, accounting, custody, and infrastructure
- JavaScript/TypeScript/React for the UI layer
- SQL migrations for data schema evolution
- Docker and shell configuration for deployment and local stacking

## 11. Recommended next steps

1. Validate runtime configuration in a staging environment
2. Re-test critical flows with real secrets and production-like network conditions
3. Complete the security review follow-ups and document residual risk
4. Run a production readiness checklist before enabling customer-facing operations
5. Keep the README and security review aligned with the code as the architecture evolves

## 12. Links to project docs

- `SECURITY_REVIEW.md`
- `docs/PRODUCCION_CHECKLIST.md`
- `docs/admin_setup.md`
- `infrastructure/backup/README.md`
- `AGENTS.md`

This repository is suitable for development and controlled integration testing, but it requires disciplined operational review before being trusted with production funds or customer assets.
