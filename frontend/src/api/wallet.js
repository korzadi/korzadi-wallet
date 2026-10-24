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
};
