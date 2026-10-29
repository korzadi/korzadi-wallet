import axios from 'axios';

const API_URL = import.meta.env.VITE_API_URL || '';

export const api = axios.create({
  baseURL: API_URL,
  withCredentials: true,
  headers: {
    'Content-Type': 'application/json',
  },
  timeout: 20000,
});

// El access token vive solo en memoria. El refresh token lo guarda el backend
// en una cookie HttpOnly; nunca se expone a JavaScript.
let accessToken = localStorage.getItem('korzadi_access_token') || '';
if (accessToken) {
  // Migración de sesiones antiguas: el token deja de persistir en disco.
  localStorage.removeItem('korzadi_access_token');
}

export function setTokens(token = '') {
  accessToken = token || '';
  if (accessToken) {
    localStorage.setItem('korzadi_auth', '1');
  }
}

export function clearTokens() {
  accessToken = '';
  localStorage.removeItem('korzadi_access_token');
  localStorage.removeItem('korzadi_refresh_token');
  localStorage.removeItem('korzadi_user');
  localStorage.removeItem('korzadi_auth');
}

export function getAccessToken() {
  return accessToken;
}

export function hasSession() {
  return Boolean(accessToken || localStorage.getItem('korzadi_auth') ||
    localStorage.getItem('korzadi_refresh_token') ||
    localStorage.getItem('korzadi_user'));
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
  // Compatibilidad: si quedó un refresh token de la versión anterior,
  // se envía una sola vez para migrar a la cookie HttpOnly.
  const legacyRefreshToken = localStorage.getItem('korzadi_refresh_token');

  const response = await axios.post(
    `${API_URL}/api/auth/refresh`,
    legacyRefreshToken ? { refresh_token: legacyRefreshToken } : {},
    {
      withCredentials: true,
      headers: {
        'Content-Type': 'application/json',
      },
      timeout: 20000,
    }
  );

  const data = response.data || {};
  const token = data.access_token || data.accessToken || data.token;

  if (!token) {
    throw new Error('Refresh response without access token');
  }

  setTokens(token);
  localStorage.removeItem('korzadi_refresh_token');

  return token;
}

export async function refreshSession() {
  return refreshAccessToken();
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

  loginTwoFactor: (challengeToken, code) =>
    api.post('/api/auth/login/2fa', {
      challenge_token: challengeToken,
      code,
    }),

  refresh: () =>
    axios.post(`${API_URL}/api/auth/refresh`, {}, { withCredentials: true, timeout: 20000 }),

  logout: () =>
    axios.post(`${API_URL}/api/auth/logout`, {}, { withCredentials: true, timeout: 20000 }),
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
