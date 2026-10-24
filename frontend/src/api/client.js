import axios from 'axios';

const API_URL = import.meta.env.VITE_API_URL || '';

export const api = axios.create({
  baseURL: API_URL,
  headers: {
    'Content-Type': 'application/json',
  },
  timeout: 20000,
});

export function setTokens(accessToken, refreshToken = '') {
  if (accessToken) {
    localStorage.setItem('korzadi_access_token', accessToken);
  }

  if (refreshToken) {
    localStorage.setItem('korzadi_refresh_token', refreshToken);
  }
}

export function clearTokens() {
  localStorage.removeItem('korzadi_access_token');
  localStorage.removeItem('korzadi_refresh_token');
  localStorage.removeItem('korzadi_user');
}

export function getAccessToken() {
  return localStorage.getItem('korzadi_access_token');
}

export function idempotencyKey() {
  if (crypto?.randomUUID) {
    return crypto.randomUUID();
  }

  return `${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

// JWT
api.interceptors.request.use((config) => {
  const token = getAccessToken();

  if (token) {
    config.headers.Authorization = `Bearer ${token}`;
  }

  return config;
});

let refreshing = false;
let refreshPromise = null;

async function refreshAccessToken() {
  const refreshToken = localStorage.getItem('korzadi_refresh_token');

  if (!refreshToken) {
    throw new Error('No refresh token');
  }

  const response = await axios.post(
    `${API_URL}/api/auth/refresh`,
    { refresh_token: refreshToken },
    {
      headers: {
        'Content-Type': 'application/json',
      },
      timeout: 20000,
    }
  );

  const data = response.data || {};

  const accessToken =
    data.access_token ||
    data.accessToken ||
    data.token;

  const newRefreshToken =
    data.refresh_token ||
    data.refreshToken ||
    refreshToken;

  if (!accessToken) {
    throw new Error('Refresh response without access token');
  }

  setTokens(accessToken, newRefreshToken);

  return accessToken;
}

api.interceptors.response.use(
  (response) => response,
  async (error) => {
    const original = error.config;

    if (
      !error.response ||
      error.response.status !== 401 ||
      !original ||
      original._retry ||
      original.url?.includes('/api/auth/login') ||
      original.url?.includes('/api/auth/refresh')
    ) {
      return Promise.reject(error);
    }

    original._retry = true;

    try {
      if (!refreshing) {
        refreshing = true;

        refreshPromise = refreshAccessToken()
          .finally(() => {
            refreshing = false;
            refreshPromise = null;
          });
      }

      const token = await refreshPromise;

      original.headers = original.headers || {};
      original.headers.Authorization = `Bearer ${token}`;

      return api(original);
    } catch (refreshError) {
      clearTokens();

      if (window.location.pathname !== '/login') {
        window.location.href = '/login';
      }

      return Promise.reject(refreshError);
    }
  }
);

// =========================
// AUTH
// =========================

export const authAPI = {
  register: (data) =>
    api.post('/api/auth/register', data),

  login: (data) =>
    api.post('/api/auth/login', data),

  refresh: () =>
    api.post('/api/auth/refresh', {
      refresh_token: localStorage.getItem('korzadi_refresh_token'),
    }),

  logout: () =>
    api.post('/api/auth/logout'),
};

// =========================
// WALLET
// =========================

export const walletAPI = {
  balances: () =>
    api.get('/api/wallet/balances'),

  transactions: (params = {}) =>
    api.get('/api/wallet/transactions', { params }),

  transfers: (params = {}) =>
    api.get('/api/wallet/transfers', { params }),

  transfer: (data) =>
    api.post('/api/wallet/transfers', data, {
      headers: {
        'Idempotency-Key': idempotencyKey(),
      },
    }),

  transferById: (id) =>
    api.get(`/api/wallet/transfers/${id}`),

  deposits: (params = {}) =>
    api.get('/api/wallet/deposits', { params }),

  withdrawals: (params = {}) =>
    api.get('/api/wallet/withdrawals', { params }),

  withdrawalById: (id) =>
    api.get(`/api/wallet/withdrawals/${id}`),
};

// =========================
// CRYPTO ADDRESSES
// =========================

export const cryptoAPI = {
  networks: () =>
    api.get('/api/wallet/networks'),

  addresses: () =>
    api.get('/api/wallet/addresses'),

  createAddress: (network) =>
    api.post('/api/wallet/addresses', {
      network,
    }),

  usdtAddress: () =>
    api.get('/api/wallet/crypto/usdt/address'),

  usdtOnchainBalance: () =>
    api.get('/api/wallet/crypto/usdt/onchain-balance'),

  usdtBscAddress: () =>
    api.get('/api/wallet/crypto/usdt/bsc/address'),

  usdtBscOnchainBalance: () =>
    api.get('/api/wallet/crypto/usdt/bsc/onchain-balance'),

  cryptoDeposits: (params = {}) =>
    api.get('/api/wallet/deposits/crypto', { params }),

  cryptoDepositById: (id) =>
    api.get(`/api/wallet/deposits/crypto/${id}`),
};

// =========================
// FEES
// =========================

export const feeAPI = {
  estimate: (data) =>
    api.post('/api/wallet/fees/estimate', data),

  quote: (id) =>
    api.get(`/api/wallet/fees/quotes/${id}`),
};

// =========================
// WITHDRAWALS
// =========================

export const withdrawalAPI = {
  create: (data, twoFACode) =>
    api.post('/api/wallet/withdrawals', data, {
      headers: {
        'Idempotency-Key': idempotencyKey(),
        ...(twoFACode
          ? { 'X-2FA-Code': twoFACode }
          : {}),
      },
    }),

  list: (params = {}) =>
    api.get('/api/wallet/withdrawals', { params }),

  get: (id) =>
    api.get(`/api/wallet/withdrawals/${id}`),
};

// =========================
// 2FA
// =========================

export const twoFactorAPI = {
  setup: () =>
    api.post('/api/security/2fa/setup'),

  confirm: (code) =>
    api.post('/api/security/2fa/confirm', { code }),
};

// =========================
// KYC
// =========================

export const kycAPI = {
  status: () =>
    api.get('/api/kyc/status'),

  start: (data = {}) =>
    api.post('/api/kyc/start', data, {
      headers: {
        'Idempotency-Key': idempotencyKey(),
      },
    }),
};

// =========================
// AFFILIATE
// =========================

export const affiliateAPI = {
  get: () =>
    api.get('/api/affiliate'),

  create: (data) =>
    api.post('/api/affiliate', data),

  referrals: (data) =>
    api.post('/api/affiliate/referrals', data),
};

// =========================
// NOTIFICATIONS
// =========================

export const notificationAPI = {
  list: () =>
    api.get('/api/notifications'),

  markRead: (id) =>
    api.post(`/api/notifications/${id}/read`),
};

export default api;
