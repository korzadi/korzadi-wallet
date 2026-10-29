import api, { idempotencyKey } from './client';

export const walletApi = {
  // Wallet
  balances: () =>
    api.get('/api/wallet/balances'),

  transactions: (params = {}) =>
    api.get('/api/wallet/transactions', { params }),

  transfers: (params = {}) =>
    api.get('/api/wallet/transfers', { params }),

  transfer: (data) =>
    api.post('/api/wallet/transfers', data, {
      headers: { 'Idempotency-Key': idempotencyKey() },
    }),

  // Networks / addresses
  networks: () =>
    api.get('/api/wallet/networks'),

  addresses: () =>
    api.get('/api/wallet/addresses'),

  createAddress: (network) =>
    api.post('/api/wallet/addresses', { network }),

  // BSC / USDT BEP-20: endpoint específico del backend
  bscDepositAddress: () =>
    api.get('/api/wallet/crypto/usdt/bsc/address'),

  // Crypto deposits
  cryptoDeposits: (params = {}) =>
    api.get('/api/wallet/deposits/crypto', { params }),

  cryptoDeposit: (id) =>
    api.get(`/api/wallet/deposits/crypto/${id}`),

  // Fees
  estimateFee: (data) =>
    api.post('/api/wallet/fees/estimate', data),

  feeQuote: (id) =>
    api.get(`/api/wallet/fees/quotes/${id}`),

  // Withdrawals
  withdrawals: (params = {}) =>
    api.get('/api/wallet/withdrawals', { params }),

  withdrawal: (id) =>
    api.get(`/api/wallet/withdrawals/${id}`),

  createWithdrawal: (data, twoFactorCode) =>
    api.post('/api/wallet/withdrawals', data, {
      headers: {
        'Idempotency-Key': idempotencyKey(),
        'X-2FA-Code': twoFactorCode,
      },
    }),

  // 2FA
  setup2FA: () =>
    api.post('/api/security/2fa/setup'),

  confirm2FA: (code) =>
    api.post('/api/security/2fa/confirm', { code }),

  get2FAStatus: () =>
    api.get('/api/security/2fa/status'),

  disable2FA: (password, code) =>
    api.post('/api/security/2fa/disable', { password, code }),

  regenerateRecoveryCodes: (password, code) =>
    api.post('/api/security/2fa/recovery-codes/regenerate', { password, code }),

  buy: (data) =>
    api.post('/api/buy', data),

  // Security Preferences (PIN, biometría, gestión de dispositivos)
  getSecurityPrefs: () =>
    api.get('/api/security/prefs'),

  setupPin: (pin, current_password) =>
    api.post('/api/security/pin/setup', { pin, current_password }),

  verifyPin: (pin) =>
    api.post('/api/security/pin/verify', { pin }),

  disablePin: (current_password) =>
    api.post('/api/security/pin/disable', { current_password }),

  updateSecurityPrefs: (data) =>
    api.post('/api/security/prefs', data),

  // KYC
  kycStatus: () =>
    api.get('/api/kyc/status'),

  startKYC: (data = {}) =>
    api.post('/api/kyc/start', data, {
      headers: { 'Idempotency-Key': idempotencyKey() },
    }),

  // Notifications
  notifications: () =>
    api.get('/api/notifications'),

  readNotification: (id) =>
    api.post(`/api/notifications/${id}/read`),

  // Affiliate
  affiliate: () =>
    api.get('/api/affiliate'),

  affiliateEnroll: () =>
    api.post('/api/affiliate'),

  // P2P Market
  p2pOffers: (params = {}) =>
    api.get('/api/p2p/offers', { params }),

  p2pGetOffer: (id) =>
    api.get(`/api/p2p/offers/${id}`),

  p2pMyOffers: () =>
    api.get('/api/p2p/offers/mine'),

  p2pCreateOffer: (data) =>
    api.post('/api/p2p/offers', data, {
      headers: { 'Idempotency-Key': idempotencyKey() },
    }),

  p2pUpdateOffer: (id, data) =>
    api.patch(`/api/p2p/offers/${id}`, data, {
      headers: { 'Idempotency-Key': idempotencyKey() },
    }),

  p2pMyOrders: () =>
    api.get('/api/p2p/orders'),

  p2pCreateOrder: (data) =>
    api.post('/api/p2p/orders', data, {
      headers: { 'Idempotency-Key': idempotencyKey() },
    }),

  p2pGetOrder: (id) =>
    api.get(`/api/p2p/orders/${id}`),

  p2pMarkPaid: (id) =>
    api.post(`/api/p2p/orders/${id}/pay`, {}, {
      headers: { 'Idempotency-Key': idempotencyKey() },
    }),

  p2pRelease: (id) =>
    api.post(`/api/p2p/orders/${id}/release`, {}, {
      headers: { 'Idempotency-Key': idempotencyKey() },
    }),

  p2pCancel: (id) =>
    api.post(`/api/p2p/orders/${id}/cancel`, {}, {
      headers: { 'Idempotency-Key': idempotencyKey() },
    }),

  p2pOpenDispute: (id, data) =>
    api.post(`/api/p2p/orders/${id}/dispute`, data, {
      headers: { 'Idempotency-Key': idempotencyKey() },
    }),

  p2pMessages: (id) =>
    api.get(`/api/p2p/orders/${id}/messages`),

  p2pSendMessage: (id, message) =>
    api.post(`/api/p2p/orders/${id}/messages`, { message }, {
      headers: { 'Idempotency-Key': idempotencyKey() },
    }),

};
