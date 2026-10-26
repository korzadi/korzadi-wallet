/* KZ_INTEGRATION
 * Puente entre los disenos originales y la API real de Korzadi Wallet.
 * - Landing: login/registro reales.
 * - App: guard de sesion, saldos, transacciones, P2P, chat, 2FA y perfil.
 * No altera CSS ni markup; solo reemplaza los datos demo en tiempo de ejecucion.
 */
(function () {
  'use strict';

  var ACCESS_KEY = 'korzadi_access_token';
  var REFRESH_KEY = 'korzadi_refresh_token';
  var USER_KEY = 'korzadi_user';
  var ACTIVE_ORDER_KEY = 'kz_active_order_id';

  var KZ = {
    offers: [],
    visibleOffers: [],
    order: null,
    selectedOffer: null,
    pendingTransfer: null,
    sending: false,
    twoFACallback: null,
    twoFAError: '',
    setup: null,
    codes: null,
    recoveryRemaining: 0,
    recvAddress: '',
    usdt: 0,
    msgKey: '',
    msgOrderId: '',
    msgBusy: false,
    orderBusy: false,
    balancesPromise: null,
    txPromise: null,
    offersPromise: null,
    ordersPromise: null,
    tfaPromise: null,
    disputeReasons: {},
    loginChallenge: '',
    loginRecovery: false,
    loginVerifyBusy: false,
    loginSubmitBusy: false,
    loginFlow: 0
  };
  window.KZ = KZ;

  /* ------------------------------------------------------------------ */
  /* Sesion                                                              */
  /* ------------------------------------------------------------------ */

  function storeGet(key) {
    try { return window.localStorage.getItem(key) || ''; } catch (e) { return ''; }
  }
  function storeSet(key, value) {
    try { window.localStorage.setItem(key, value); } catch (e) {}
  }
  function storeDel(key) {
    try { window.localStorage.removeItem(key); } catch (e) {}
  }
  function getToken() { return storeGet(ACCESS_KEY); }
  function getRefresh() { return storeGet(REFRESH_KEY); }

  function getUser() {
    var raw = storeGet(USER_KEY);
    if (!raw) return null;
    try { return JSON.parse(raw); } catch (e) { return null; }
  }

  function getUserId() {
    var user = getUser();
    if (user && user.id) return String(user.id);
    var token = getToken();
    if (!token) return '';
    try {
      var part = token.split('.')[1].replace(/-/g, '+').replace(/_/g, '/');
      while (part.length % 4) part += '=';
      var payload = JSON.parse(window.atob(part));
      return String(payload.uid || '');
    } catch (e) {
      return '';
    }
  }

  function setSession(data) {
    if (!data) return;
    if (data.access_token) storeSet(ACCESS_KEY, data.access_token);
    if (data.refresh_token) storeSet(REFRESH_KEY, data.refresh_token);
    if (data.user) storeSet(USER_KEY, JSON.stringify(data.user));
  }

  function clearSession() {
    storeDel(ACCESS_KEY);
    storeDel(REFRESH_KEY);
    storeDel(USER_KEY);
    storeDel(ACTIVE_ORDER_KEY);
  }

  function goLanding() {
    if (window.location.pathname !== '/') window.location.replace('/');
  }

  /* ------------------------------------------------------------------ */
  /* API                                                                 */
  /* ------------------------------------------------------------------ */

  var refreshPromise = null;

  function apiUrl(path) { return '/api' + path; }

  function peekJson(res) {
    return res.clone().json().catch(function () { return null; });
  }

  function isStepUpError(data) {
    var code = data && data.error;
    return code === 'step_up_required' || code === 'step_up_invalid' || code === '2fa_required';
  }

  async function parseResponse(res) {
    var text = await res.text();
    var data = null;
    if (text) {
      try { data = JSON.parse(text); } catch (e) { data = text; }
    }
    if (!res.ok) {
      var err = new Error('HTTP ' + res.status);
      err.status = res.status;
      err.data = data;
      if (data && typeof data === 'object') {
        err.code = data.error || data.code || '';
        if (data.message) err.serverMessage = data.message;
      }
      throw err;
    }
    return data;
  }

  async function refreshTokens() {
    if (refreshPromise) return refreshPromise;
    refreshPromise = (async function () {
      var token = getRefresh();
      if (!token) throw new Error('No hay refresh token');
      var res = await window.fetch(apiUrl('/auth/refresh'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ refresh_token: token })
      });
      var data = await parseResponse(res);
      if (!data || !data.access_token) throw new Error('Respuesta de refresh invalida');
      setSession(data);
      return data;
    })();
    try {
      return await refreshPromise;
    } finally {
      refreshPromise = null;
    }
  }

  async function api(path, options) {
    var opts = options || {};
    var headers = Object.assign({}, opts.headers || {});
    if (opts.auth !== false) {
      var token = getToken();
      if (token) headers.Authorization = 'Bearer ' + token;
    }
    var init = { method: opts.method || 'GET', headers: headers };
    if (opts.body !== undefined) {
      headers['Content-Type'] = 'application/json';
      init.body = JSON.stringify(opts.body);
    }

    var res = await window.fetch(apiUrl(path), init);

    if (res.status === 401 && opts.auth !== false) {
      var authError = await peekJson(res);
      if (!isStepUpError(authError)) {
        if (!opts.retry && getRefresh()) {
          try {
            await refreshTokens();
            return api(path, Object.assign({}, opts, { retry: true }));
          } catch (e) {
            clearSession();
            if (opts.redirect !== false) goLanding();
            throw e;
          }
        }
        clearSession();
        if (opts.redirect !== false) goLanding();
      }
    }

    return parseResponse(res);
  }

  /* ------------------------------------------------------------------ */
  /* Formato y utilidades                                                */
  /* ------------------------------------------------------------------ */

  function esc(value) {
    return String(value === null || value === undefined ? '' : value).replace(/[&<>"']/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
    });
  }

  function num(value) {
    var n = Number(value);
    return isFinite(n) ? n : 0;
  }

  function money(value) {
    var n = num(value);
    return n.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 });
  }

  function amt(value) {
    var n = num(value);
    return n.toLocaleString('en-US', { maximumFractionDigits: 8 });
  }

  function trimNum(value, digits) {
    var n = num(value);
    return n.toLocaleString('en-US', { maximumFractionDigits: digits || 6 });
  }

  function dec(value) {
    var n = num(value);
    if (!isFinite(n)) return '0';
    var s = n.toFixed(8);
    s = s.replace(/\.?0+$/, '');
    return s || '0';
  }

  function val(id) {
    var el = document.getElementById(id);
    return el ? String(el.value || '').trim() : '';
  }

  function idemKey() {
    if (window.crypto && typeof window.crypto.randomUUID === 'function') return window.crypto.randomUUID();
    return 'kz-' + Date.now() + '-' + Math.random().toString(36).slice(2, 12);
  }

  function fmtWhen(iso) {
    var d = new Date(iso);
    if (isNaN(d.getTime())) return '-';
    var now = new Date();
    var hm = d.toLocaleTimeString('es', { hour: '2-digit', minute: '2-digit' });
    if (d.toDateString() === now.toDateString()) return 'Hoy · ' + hm;
    var yesterday = new Date(now.getTime() - 86400000);
    if (d.toDateString() === yesterday.toDateString()) return 'Ayer · ' + hm;
    return d.toLocaleDateString('es', { day: '2-digit', month: '2-digit', year: 'numeric' });
  }

  function fmtTime(iso) {
    var d = new Date(iso);
    if (isNaN(d.getTime())) return '';
    return d.toLocaleTimeString('es', { hour: '2-digit', minute: '2-digit' });
  }

  function isOn(section) {
    var el = document.getElementById(section);
    return !!(el && el.classList.contains('on'));
  }

  var META = {
    USDT: { name: 'Tether', color: '#26A17B', symbol: '₮', type: 'stable' },
    USDC: { name: 'USD Coin', color: '#2775CA', symbol: '$', type: 'stable' },
    USD: { name: 'Dolar', color: '#16B364', symbol: '$', type: 'stable' },
    DAI: { name: 'Dai', color: '#F5AC37', symbol: '◈', type: 'stable' },
    BTC: { name: 'Bitcoin', color: '#F7931A', symbol: '₿', type: 'crypto' },
    ETH: { name: 'Ethereum', color: '#627EEA', symbol: 'Ξ', type: 'crypto' },
    BNB: { name: 'BNB', color: '#F3BA2F', symbol: 'B', type: 'crypto' },
    TRX: { name: 'TRON', color: '#EF0027', symbol: 'T', type: 'crypto' },
    MATIC: { name: 'Polygon', color: '#8247E5', symbol: 'M', type: 'crypto' },
    POL: { name: 'Polygon', color: '#8247E5', symbol: 'M', type: 'crypto' },
    ARB: { name: 'Arbitrum', color: '#28A0F0', symbol: 'A', type: 'crypto' },
    OP: { name: 'Optimism', color: '#FF0420', symbol: 'O', type: 'crypto' },
    SOL: { name: 'Solana', color: '#9945FF', symbol: 'S', type: 'crypto' },
    LTC: { name: 'Litecoin', color: '#345D9D', symbol: 'Ł', type: 'crypto' }
  };

  function assetMeta(code) {
    return META[code] || { name: code, color: '#3B6BFF', symbol: code.slice(0, 3), type: 'crypto' };
  }

  var ERROR_MESSAGES = {
    invalid_credentials: 'Correo o contrasena incorrectos.',
    email_taken: 'Ese correo ya esta registrado.',
    account_locked: 'La cuenta esta bloqueada temporalmente. Intentalo mas tarde.',
    invalid_2fa_code: 'El codigo 2FA no es valido.',
    '2fa_required': 'Se requiere un codigo 2FA.',
    '2fa_locked': 'Demasiados intentos. Espera antes de volver a intentar.',
    'invalid_2fa_challenge': 'La verificacion 2FA expiro o ya fue utilizada. Inicia sesion de nuevo.',
    '2fa_challenge_expired': 'La verificacion 2FA expiro. Inicia sesion de nuevo.',
    '2fa_unavailable': 'No se pudo verificar el estado de 2FA. Intentalo mas tarde.',
    '2fa_already_enabled': 'El 2FA ya esta activado en esta cuenta.',
    '2fa_not_enabled': 'El 2FA no esta activado en esta cuenta.',
    '2fa_not_setup': 'Primero inicia la configuracion de 2FA.',
    step_up_required: 'Esta operacion requiere un codigo 2FA.',
    step_up_invalid: 'El codigo 2FA no es valido.',
    invalid_request: 'Revisa los datos e intentalo de nuevo.',
    internal_error: 'El servidor no pudo completar la operacion. Intentalo mas tarde.',
    unauthorized: 'Tu sesion expiro. Inicia sesion de nuevo.',
    invalid_token: 'Tu sesion expiro. Inicia sesion de nuevo.',
    invalid_amount: 'El monto no es valido.',
    insufficient_funds: 'Saldo insuficiente.',
    insufficient_balance: 'Saldo insuficiente.',
    below_minimum: 'El monto esta por debajo del minimo.',
    above_maximum: 'El monto supera el maximo.',
    daily_limit_exceeded: 'Superaste el limite diario.',
    daily_count_exceeded: 'Superaste el numero de operaciones diarias.',
    monthly_limit_exceeded: 'Superaste el limite mensual.',
    monthly_count_exceeded: 'Superaste el numero de operaciones mensuales.',
    rate_limited: 'Demasiadas solicitudes. Intentalo mas tarde.',
    duplicate_operation: 'Esta operacion ya fue procesada.',
    operation_unavailable: 'La operacion no esta disponible en este momento.',
    manual_approval_required: 'La operacion requiere aprobacion manual.',
    compliance_restricted: 'La operacion no esta disponible por restricciones de cumplimiento.',
    authorization_denied: 'La operacion no esta autorizada.',
    new_destination_cooldown: 'El destino es nuevo; espera antes de volver a enviar.',
    destination_blocked: 'El destino esta bloqueado.',
    user_not_found: 'No encontramos una cuenta con ese correo o ID.',
    self_transfer: 'No puedes transferirte a ti mismo.',
    weak_password: 'La contrasena debe tener entre 10 y 128 caracteres y no puede ser tu correo.',
    invalid_current_password: 'La contrasena actual es incorrecta.',
    same_password: 'La nueva contrasena debe ser distinta de la actual.',
    email_already_registered: 'Ese correo ya esta registrado.',
    invalid_refresh: 'La sesion expiro. Inicia sesion de nuevo.',
    refresh_expired: 'La sesion expiro. Inicia sesion de nuevo.',
    refresh_reuse_detected: 'La sesion fue revocada. Inicia sesion de nuevo.',
    bsc_not_configured: 'Los depositos por BNB Smart Chain no estan configurados. Contacta a soporte.',
    tron_deposits_unavailable: 'Los depositos por TRON estan temporalmente deshabilitados.'
  };

  function friendlyError(err) {
    if (!err) return 'No se pudo completar la operacion. Intentalo de nuevo.';
    if (err.name === 'TypeError') return 'No se pudo conectar con el servidor. Revisa tu conexion.';
    var code = err.code;
    if (code && ERROR_MESSAGES[code]) return ERROR_MESSAGES[code];
    var data = err.data;
    if (data && typeof data === 'object') {
      if (data.message && data.message !== 'error interno') return data.message;
      if (typeof data.error === 'string' && !/^[a-z0-9_]+$/.test(data.error)) return data.error;
    }
    if (err.serverMessage) return err.serverMessage;
    if (err.message && !/^HTTP \d+$/.test(err.message) && err.message.indexOf('No hay refresh') === -1) {
      return err.message;
    }
    return 'No se pudo completar la operacion. Intentalo de nuevo.';
  }

  async function copyText(text) {
    text = String(text || '');
    if (!text) { if (window.toast) window.toast('Nada que copiar'); return; }
    try {
      if (navigator.clipboard && navigator.clipboard.writeText) {
        await navigator.clipboard.writeText(text);
      } else {
        throw new Error('sin clipboard');
      }
      if (window.toast) window.toast('Copiado');
    } catch (e) {
      var area = document.createElement('textarea');
      area.value = text;
      area.setAttribute('readonly', '');
      area.style.position = 'fixed';
      area.style.opacity = '0';
      document.body.appendChild(area);
      area.select();
      try { document.execCommand('copy'); if (window.toast) window.toast('Copiado'); }
      catch (err) { if (window.toast) window.toast('No se pudo copiar'); }
      document.body.removeChild(area);
    }
  }

  window.kzCopy = function (kind) {
    if (kind === 'address') copyText(KZ.recvAddress);
    else if (kind === 'secret') copyText(KZ.setup && KZ.setup.secret);
    else if (kind === 'codes') copyText((KZ.codes || []).join('\n'));
    else if (kind === 'ref') copyText(KZ.order && KZ.order.rawId);
  };

  /* ------------------------------------------------------------------ */
  /* Navegacion                                                          */
  /* ------------------------------------------------------------------ */

  var ROUTES = {
    '/dashboard': 'inicio',
    '/assets': 'activos',
    '/p2p': 'p2p',
    '/p2p/create': 'crear',
    '/p2p/order': 'orden',
    '/transactions': 'tx',
    '/profile': 'perfil',
    '/security': 'seguridad'
  };

  var PATHS = {};
  Object.keys(ROUTES).forEach(function (path) { PATHS[ROUTES[path]] = path; });

  var TITLES = {
    inicio: 'Inicio',
    activos: 'Activos',
    p2p: 'Mercado P2P',
    crear: 'Crear oferta',
    orden: 'Orden',
    tx: 'Transacciones',
    perfil: 'Perfil',
    seguridad: 'Seguridad'
  };

  var originalGo = null;

  function sectionFromPath(path) {
    if (ROUTES[path]) return ROUTES[path];
    if (path.indexOf('/p2p/') === 0) return 'p2p';
    return null;
  }

  function pathFor(section) { return PATHS[section] || '/dashboard'; }

  function wireNavigation() {
    if (typeof window.go !== 'function') return;
    originalGo = window.go;

    window.go = function (section) {
      originalGo(section);
      var target = pathFor(section);
      if (window.location.pathname !== target) {
        window.history.pushState({ kz: section }, '', target);
      }
      document.title = 'Korzadi · ' + (TITLES[section] || section);
      afterNavigate(section);
    };

    window.addEventListener('popstate', function () {
      var section = sectionFromPath(window.location.pathname) || 'inicio';
      originalGo(section);
      document.title = 'Korzadi · ' + (TITLES[section] || section);
      afterNavigate(section);
    });
  }

  function afterNavigate(section) {
    if (section === 'p2p') {
      loadOffers();
      if (window.rao) window.rao();
    } else if (section === 'orden') {
      if (KZ.order && window.ror) window.ror();
      loadOrders().then(function () {
        if (KZ.order && window.ror) window.ror();
        if (isOn('orden')) loadMessages(false);
      });
    } else if (section === 'seguridad') {
      if (window.rs) window.rs();
      load2FAStatus();
    } else if (section === 'crear') {
      updateCreateBalance();
      if (window.sum) window.sum();
    } else if (section === 'perfil') {
      setProfile();
      loadKYCStatus();
    } else if (section === 'seguridad') {
      if (window.rs) window.rs();
      load2FAStatus();
      loadKYCStatus();
    }
  }

  function unregisterOldServiceWorker() {
    if (!('serviceWorker' in navigator)) return;
    navigator.serviceWorker.getRegistrations().then(function (regs) {
      regs.forEach(function (reg) { reg.unregister(); });
    }).catch(function () {});
  }

  function ready(fn) {
    if (document.readyState === 'loading') {
      document.addEventListener('DOMContentLoaded', fn, { once: true });
    } else {
      fn();
    }
  }

  /* ------------------------------------------------------------------ */
  /* Landing: login / registro                                           */
  /* ------------------------------------------------------------------ */

  function showAuthStep(id) {
    ['s1', 's2', 's3'].forEach(function (step) {
      var el = document.getElementById(step);
      if (el) el.hidden = step !== id;
    });
  }

  function showLandingNotice(text) {
    var modal = document.getElementById('md');
    if (!modal || !text) return;
    modal.classList.add('on');
    document.body.style.overflow = 'hidden';
    var err = document.getElementById('er');
    if (err) err.textContent = text;
    setTimeout(function () {
      var email = document.getElementById('e');
      if (email) email.focus();
    }, 60);
  }

  function initLanding() {
    var form = document.getElementById('f');
    if (!form) return;
    form.onsubmit = null;
    form.addEventListener('submit', function (ev) {
      ev.preventDefault();
      submitAuth();
    });

    // Al cerrar o reabrir el modal se descarta cualquier challenge pendiente.
    if (typeof window.cl === 'function') {
      var originalClose = window.cl;
      window.cl = function () {
        resetLoginChallenge();
        return originalClose.apply(this, arguments);
      };
    }
    if (typeof window.op === 'function') {
      var originalOpen = window.op;
      window.op = function () {
        resetLoginChallenge();
        return originalOpen.apply(this, arguments);
      };
    }

    var notice = '';
    try {
      notice = window.sessionStorage.getItem('kz_notice') || '';
      if (notice) window.sessionStorage.removeItem('kz_notice');
    } catch (e) {}
    if (notice) showLandingNotice(notice);
  }

  async function submitAuth() {
    var isRegister = false;
    var tab2 = document.getElementById('t2');
    if (tab2 && tab2.classList.contains('on')) isRegister = true;

    var name = val('n');
    var email = val('e').toLowerCase();
    var password = document.getElementById('p') ? document.getElementById('p').value : '';
    var confirm = document.getElementById('p2') ? document.getElementById('p2').value : '';
    var error = '';

    if (isRegister && name.length < 2) error = 'Escribe tu nombre completo.';
    else if (!/^\S+@\S+\.\S+$/.test(email)) error = 'Ingresa un correo valido.';
    else if (password.length < 10) error = 'La contrasena debe tener al menos 10 caracteres.';
    else if (isRegister && password !== confirm) error = 'Las contrasenas no coinciden.';

    var errEl = document.getElementById('er');
    if (errEl) errEl.textContent = error;
    if (error) return;

    var btn = document.getElementById('sb');
    var old = btn ? btn.textContent : '';
    if (btn) { btn.disabled = true; btn.textContent = 'Procesando...'; }

    if (KZ.loginSubmitBusy) return;
    KZ.loginSubmitBusy = true;
    var flow = resetLoginChallenge();
    try {
      var body;
      var path;
      if (isRegister) {
        var parts = name.split(/\s+/);
        var first = parts.shift() || name;
        var last = parts.join(' ') || first;
        body = {
          email: email,
          password: password,
          first_name: first,
          last_name: last
        };
        path = '/auth/register';
      } else {
        body = { email: email, password: password };
        path = '/auth/login';
      }

      var data = await api(path, { method: 'POST', body: body, auth: false });
      if (flow !== KZ.loginFlow) return;

      if (!isRegister && data && data.two_factor_required) {
        if (!data.challenge_token) throw new Error('El servidor no inicio la verificacion 2FA.');
        // Ningun token se guarda antes de verificar el segundo factor.
        clearSession();
        KZ.loginChallenge = data.challenge_token;
        var inputs = document.querySelectorAll('#cd input');
        for (var i = 0; i < inputs.length; i++) inputs[i].value = '';
        showAuthStep('s2');
        setTimeout(function () { if (inputs[0]) inputs[0].focus(); }, 60);
        return;
      }

      if (!data || !data.access_token) throw new Error('El servidor no devolvio una sesion valida.');
      setSession(data);

      var msg = document.getElementById('ms');
      if (msg) {
        msg.textContent = isRegister
          ? 'Tu cuenta fue creada. Revisa tu correo para verificarla y entra a tu wallet.'
          : 'Sesion iniciada como ' + email + '.';
      }
      showLandingSuccess();
    } catch (e) {
      if (flow === KZ.loginFlow && errEl) errEl.textContent = friendlyError(e);
    } finally {
      KZ.loginSubmitBusy = false;
      if (btn) { btn.disabled = false; btn.textContent = old; }
    }
  }

  function resetLoginChallenge() {
    KZ.loginFlow += 1;
    KZ.loginChallenge = '';
    KZ.loginRecovery = false;
    KZ.loginVerifyBusy = false;
    var rec = document.getElementById('kzrecovery');
    var cd = document.getElementById('cd');
    var p = document.getElementById('s2p');
    var toggle = document.getElementById('kztoggle');
    var er2 = document.getElementById('er2');
    if (rec) { rec.hidden = true; rec.value = ''; }
    if (cd) cd.hidden = false;
    if (p) p.textContent = 'Ingresa el codigo de 6 digitos de tu app de autenticacion.';
    if (toggle) toggle.textContent = 'Usar codigo de recuperacion';
    if (er2) er2.textContent = '';
    return KZ.loginFlow;
  }

  function showLandingSuccess() {
    showAuthStep('s3');
    var goBtn = document.querySelector('#s3 .bt');
    if (goBtn) {
      goBtn.onclick = function () { window.location.href = '/dashboard'; };
      goBtn.textContent = 'Ir a mi wallet';
    }
    var modal = document.querySelector('.mc');
    if (modal) modal.scrollTop = 0;
  }

  window.kzToggleRecovery = function () {
    KZ.loginRecovery = !KZ.loginRecovery;
    var rec = document.getElementById('kzrecovery');
    var cd = document.getElementById('cd');
    var p = document.getElementById('s2p');
    var toggle = document.getElementById('kztoggle');
    var er2 = document.getElementById('er2');
    if (rec) { rec.hidden = !KZ.loginRecovery; rec.value = ''; }
    if (cd) cd.hidden = KZ.loginRecovery;
    if (p) p.textContent = KZ.loginRecovery
      ? 'Introduce uno de tus codigos de recuperacion.'
      : 'Ingresa el codigo de 6 digitos de tu app de autenticacion.';
    if (toggle) toggle.textContent = KZ.loginRecovery ? 'Usar codigo de la app' : 'Usar codigo de recuperacion';
    if (er2) er2.textContent = '';
    if (KZ.loginRecovery && rec) rec.focus();
  };

  window.kzCancelLogin2FA = function () {
    resetLoginChallenge();
    showAuthStep('s1');
  };

  window.ver = async function () {
    var er2 = document.getElementById('er2');
    if (!KZ.loginChallenge) {
      if (er2) er2.textContent = 'La verificacion expiro. Inicia sesion de nuevo.';
      return;
    }
    if (KZ.loginVerifyBusy) return;
    var rec = document.getElementById('kzrecovery');
    var code;
    if (KZ.loginRecovery) {
      code = rec ? String(rec.value || '').trim() : '';
      if (!code) { if (er2) er2.textContent = 'Ingresa tu codigo de recuperacion.'; return; }
    } else {
      code = Array.prototype.map.call(document.querySelectorAll('#cd input'), function (el) { return el.value; }).join('');
      if (code.length !== 6) { if (er2) er2.textContent = 'Ingresa los 6 digitos.'; return; }
    }
    KZ.loginVerifyBusy = true;
    var flow = KZ.loginFlow;
    if (er2) er2.textContent = '';
    try {
      var data = await api('/auth/login/2fa', {
        method: 'POST',
        auth: false,
        body: { challenge_token: KZ.loginChallenge, code: code }
      });
      if (flow !== KZ.loginFlow) return;
      if (!data || !data.access_token) throw new Error('El servidor no devolvio una sesion valida.');
      setSession(data);
      KZ.loginChallenge = '';
      var msg = document.getElementById('ms');
      if (msg) msg.textContent = 'Sesion verificada correctamente.';
      showLandingSuccess();
    } catch (e) {
      if (flow !== KZ.loginFlow) return;
      if (e && (e.code === 'invalid_2fa_challenge' || e.code === '2fa_challenge_expired' || e.code === '2fa_required' || e.code === 'invalid_refresh')) {
        resetLoginChallenge();
        showAuthStep('s1');
        var er = document.getElementById('er');
        if (er) er.textContent = friendlyError(e);
        return;
      }
      if (er2) er2.textContent = friendlyError(e);
      var inputs = document.querySelectorAll('#cd input');
      if (!KZ.loginRecovery) {
        for (var i = 0; i < inputs.length; i++) inputs[i].value = '';
        if (inputs[0]) inputs[0].focus();
      }
    } finally {
      KZ.loginVerifyBusy = false;
    }
  };

  /* ------------------------------------------------------------------ */
  /* App: datos                                                          */
  /* ------------------------------------------------------------------ */

  async function loadBalances() {
    if (KZ.balancesPromise) return KZ.balancesPromise;
    KZ.balancesPromise = (async function () {
      try {
        var data = await api('/wallet/balances');
        var list = (data && data.balances) || [];
        var accounts = [];

        list.forEach(function (item) {
          var code = String(item.asset || '').toUpperCase();
          var balance = num(item.balance);
          var value = item.valuation_available ? num(item.value_usdt) : 0;
          var meta = assetMeta(code);
          accounts.push([code, meta.name, meta.color, meta.symbol, amt(balance), value, meta.type, balance]);
        });

        accounts.sort(function (a, b) {
          if (b[5] !== a[5]) return b[5] - a[5];
          if (b[7] !== a[7]) return b[7] - a[7];
          return a[0].localeCompare(b[0]);
        });

        window.AS = accounts;
        window.TOT = num(data && data.total_usdt);
        KZ.usdt = 0;
        accounts.forEach(function (a) { if (a[0] === 'USDT') KZ.usdt = a[7]; });

        if (window.lst && window.ar) {
          window.lst('l1', accounts.slice(0, 3), window.ar, 'No tienes activos todavia.');
        }
        if (window.ra) window.ra();
        if (window.cu) window.cu();
        updateCreateBalance();
      } catch (e) {
        if (e.status !== 401) {
          var l1 = document.getElementById('l1');
          var l3 = document.getElementById('l3');
          if (l1) l1.innerHTML = '<div class="em">No se pudieron cargar tus saldos.</div>';
          if (l3) l3.innerHTML = '<div class="em">No se pudieron cargar tus saldos.</div>';
        }
      } finally {
        KZ.balancesPromise = null;
      }
    })();
    return KZ.balancesPromise;
  }

  async function loadTransactions() {
    if (KZ.txPromise) return KZ.txPromise;
    KZ.txPromise = (async function () {
      try {
        var data = await api('/wallet/transactions');
        var list = (data && data.transactions) || [];
        list.sort(function (a, b) {
          return new Date(b.created_at).getTime() - new Date(a.created_at).getTime();
        });

        var rows = list.map(function (entry) {
          var raw = String(entry.amount || '0').trim();
          var negative = raw.charAt(0) === '-';
          var absolute = negative ? raw.slice(1) : raw;
          var asset = String(entry.asset || '');
          return [
            (negative ? 'Envio de ' : 'Ingreso de ') + asset,
            fmtWhen(entry.created_at),
            (negative ? '-' : '+') + amt(absolute) + ' ' + asset,
            negative ? 'send' : 'recv'
          ];
        });

        window.TX = rows;
        if (window.lst && window.tr) {
          window.lst('l2', rows.slice(0, 3), window.tr, 'Todavia no tienes movimientos.');
        }
        if (window.rt) {
          var pressed = document.querySelector('#c5 .ch[aria-pressed="true"]');
          window.rt(pressed ? pressed.dataset.f : 'all');
        }
      } catch (e) {
        if (e.status !== 401) {
          var l2 = document.getElementById('l2');
          var l5 = document.getElementById('l5');
          if (l2) l2.innerHTML = '<div class="em">No se pudieron cargar tus movimientos.</div>';
          if (l5) l5.innerHTML = '<div class="em">No se pudieron cargar tus movimientos.</div>';
        }
      } finally {
        KZ.txPromise = null;
      }
    })();
    return KZ.txPromise;
  }

  async function loadOffers() {
    if (KZ.offersPromise) return KZ.offersPromise;
    KZ.offersPromise = (async function () {
      try {
        var data = await api('/p2p/offers?asset=USDT&side=sell');
        KZ.offers = Array.isArray(data) ? data : [];
        if (window.ro) window.ro();
      } catch (e) {
        if (e.status !== 401) {
          var l4 = document.getElementById('l4');
          if (l4) l4.innerHTML = '<div class="em">No se pudieron cargar las ofertas.</div>';
        }
      } finally {
        KZ.offersPromise = null;
      }
    })();
    return KZ.offersPromise;
  }

  async function loadOrders() {
    if (KZ.ordersPromise) return KZ.ordersPromise;
    KZ.ordersPromise = (async function () {
      try {
        var data = await api('/p2p/orders');
        var list = Array.isArray(data) ? data : [];
        var active = list.filter(function (o) {
          return ['created', 'escrow_locked', 'payment_marked', 'disputed'].indexOf(o.status) !== -1;
        });
        var stored = storeGet(ACTIVE_ORDER_KEY);
        var chosen = null;
        for (var i = 0; i < active.length; i++) {
          if (String(active[i].id) === stored) { chosen = active[i]; break; }
        }
        if (!chosen && active.length) chosen = active[0];

        if (chosen) {
          storeSet(ACTIVE_ORDER_KEY, String(chosen.id));
          setOrder(mapOrder(chosen));
        } else {
          storeDel(ACTIVE_ORDER_KEY);
          if (!KZ.order) setOrder(null);
        }

        if (window.rao) window.rao();
        if (isOn('orden') && window.ror) window.ror();
      } catch (e) {
        if (e.status !== 401) {
          var ol = document.getElementById('ol');
          if (ol && isOn('orden')) ol.innerHTML = '<div class="card em">No se pudo cargar la orden.</div>';
        }
      } finally {
        KZ.ordersPromise = null;
      }
    })();
    return KZ.ordersPromise;
  }

  async function loadMessages(force) {
    if (!KZ.order || KZ.msgBusy) return;
    var orderId = KZ.order.rawId;
    var cm = document.getElementById('cm');
    if (!cm) return;
    KZ.msgBusy = true;
    try {
      var data = await api('/p2p/orders/' + orderId + '/messages');
      if (!KZ.order || KZ.order.rawId !== orderId) return;
      var list = Array.isArray(data) ? data : [];
      list.sort(function (a, b) {
        return new Date(a.created_at).getTime() - new Date(b.created_at).getTime();
      });
      var key = KZ.order.state + '|' + orderId + '|' + list.map(function (m) { return m.id; }).join(',');
      if (!force && key === KZ.msgKey) return;
      KZ.msgKey = key;

      cm.innerHTML = '';
      addSystemNode('Orden #' + String(orderId).slice(0, 8) + ' · ' + labelFor(KZ.order));
      var uid = getUserId();
      list.forEach(function (m) {
        var mine = String(m.sender_id) === uid;
        addMessageNode(mine ? 'me' : 'them', m.message || '', m.created_at);
      });
      if (KZ.order.state === 'disp') {
        addSystemNode('Disputa abierta. Soporte revisara el caso.');
      }
    } catch (e) {
      if (e.status === 403 || e.status === 404) {
        addSystemNode('No tienes acceso a esta orden.');
      }
    } finally {
      KZ.msgBusy = false;
    }
  }

  async function load2FAStatus() {
    if (KZ.tfaPromise) return KZ.tfaPromise;
    KZ.tfaPromise = (async function () {
      try {
        var data = await api('/security/2fa/status');
        if (window.SEC) window.SEC.tfa = !!(data && data.enabled);
        KZ.recoveryRemaining = num(data && data.recovery_codes_remaining);
        if (isOn('seguridad') && window.rs) window.rs();
      } catch (e) {
        if (e.status !== 401 && isOn('seguridad') && window.toast) {
          window.toast('No se pudo consultar el estado de 2FA');
        }
      } finally {
        KZ.tfaPromise = null;
      }
    })();
    return KZ.tfaPromise;
  }

  async function loadKYCStatus() {
    if (KZ.kycPromise) return KZ.kycPromise;
    KZ.kycPromise = (async function () {
      try {
        var data = await api('/kyc/status');
        var statusStr = String(data && (data.status || data.kyc_status || '') || '').toLowerCase();
        KZ.kycStatus = statusStr || 'not_started';
        if (isOn('perfil') && window.setProfile) window.setProfile();
        if (isOn('seguridad') && window.rs) window.rs();
      } catch (e) {
        KZ.kycStatus = 'not_started';
      } finally {
        KZ.kycPromise = null;
      }
    })();
    return KZ.kycPromise;
  }

  async function loadPreferences() {
    try {
      var data = await api('/security/prefs');
      if (window.SEC) {
        window.SEC.pin = !!(data && data.pin_enabled);
        window.SEC.lock = !!(data && data.app_lock_enabled);
        window.SEC.bio = !!(data && data.biometric_enabled);
        window.SEC.mail = !!(data && data.email_alerts_enabled);
      }
      if (isOn('seguridad') && window.rs) window.rs();
      if (isOn('perfil') && window.setProfile) window.setProfile();
    } catch (e) {}
  }

  /* ------------------------------------------------------------------ */
  /* App: pedidos                                                        */
  /* ------------------------------------------------------------------ */

  function setOrder(order) {
    var previousId = KZ.order ? KZ.order.rawId : '';
    KZ.order = order;
    if (!order || order.rawId !== previousId) {
      KZ.msgKey = '';
      KZ.msgOrderId = '';
    }
  }

  function stateFor(status, role, paidAt) {
    if (status === 'released') return 'done';
    if (status === 'cancelled' || status === 'expired') return 'cancel';
    if (status === 'disputed') return 'disp';
    if (status === 'payment_marked') return role === 'buy' ? 'wait' : 'review';
    if (status === 'escrow_locked' || status === 'created') return role === 'buy' ? 'pay' : 'wait';
    return 'cancel';
  }

  function previousState(status, role, paidAt) {
    if (paidAt) return role === 'sell' ? 'review' : 'wait';
    return role === 'buy' ? 'pay' : 'wait';
  }

  function mapOrder(o) {
    var uid = getUserId();
    var role = String(o.buyer_id) === uid ? 'buy' : 'sell';
    var counterparty = role === 'buy' ? o.seller_id : o.buyer_id;
    var short = String(counterparty || '').slice(0, 8);
    var state = stateFor(o.status, role, o.paid_at);
    var prev = previousState(o.status, role, o.paid_at);
    var disputeReason = KZ.disputeReasons[String(o.id)] || '';
    var disputeId = o.dispute_id ? 'D-' + String(o.dispute_id).slice(0, 8) : '';

    return {
      rawId: String(o.id),
      raw: o,
      role: role,
      o: ['Usuario ' + short, 'U', 0, 0, num(o.price_per_unit), '', [o.payment_method || ''], o.fiat_currency],
      asset: o.asset_code || 'USDT',
      usdt: num(o.amount),
      rate: num(o.price_per_unit),
      cur: o.fiat_currency || 'CUP',
      total: num(o.fiat_amount),
      method: o.payment_method || '',
      instructions: o.payment_instructions || '',
      state: state,
      prev: prev,
      end: new Date(o.expires_at).getTime(),
      proof: null,
      disp: o.dispute_id ? {
        id: disputeId,
        reason: disputeReason,
        step: 2
      } : null,
      rated: 0,
      res: 0
    };
  }

  function labelFor(o) {
    var buyer = o.role === 'buy';
    var map = {
      pay: 'Esperando tu pago',
      wait: buyer ? 'Verificando pago' : 'Esperando pago',
      review: 'Verifica el pago',
      disp: 'En disputa',
      done: 'Completada',
      cancel: 'Cancelada'
    };
    return map[o.state] || '';
  }

  function stageFor(o) {
    if (o.state === 'cancel') return -1;
    if (o.state === 'done') return 4;
    var s = o.state === 'disp' ? (o.prev || 'wait') : o.state;
    return o.role === 'buy' ? (s === 'pay' ? 1 : 2) : (s === 'wait' ? 1 : 2);
  }

  function stepsHtml() {
    var o = KZ.order;
    var stage = stageFor(o);
    var disputed = o.state === 'disp';
    var labels = ['Creada', 'Pago', 'Confirmacion', 'Liberada'];
    return '<div class="card" style="padding:16px 12px"><div style="display:flex;align-items:flex-start">' +
      labels.map(function (label, i) {
        var done = stage > i;
        var active = stage === i;
        var color = disputed && active ? 'var(--am)' : 'var(--bl)';
        return (i ? '<div style="flex:1;height:2px;margin-top:13px;background:' + (stage >= i ? color : 'var(--ln)') + ';transition:.5s"></div>' : '') +
          '<div style="width:68px;text-align:center"><div class="av" style="margin:0 auto;width:28px;height:28px;font-size:12px;' +
          (done ? 'background:var(--gn);color:#06251B' : active ? 'background:' + color + ';color:#fff' : 'background:var(--s2);color:var(--mu)') +
          '">' + (done ? '✓' : i + 1) + '</div><div style="font-size:11px;margin-top:6px;color:' +
          (done || active ? 'var(--tx)' : 'var(--mu)') + '">' + label + '</div></div>';
      }).join('') + '</div></div>';
  }

  function summaryHtml() {
    var o = KZ.order;
    var buyer = o.role === 'buy';
    return '<div class="card" style="padding:14px 16px"><div style="display:flex;align-items:center;gap:10px;margin-bottom:6px">' +
      '<div class="co" style="background:linear-gradient(135deg,#1A63F0,#0B3BB0)">U</div>' +
      '<div style="flex:1"><b>' + esc(o.o[0]) + '</b><div style="color:var(--mu);font-size:12px">Sin reputacion</div></div>' +
      '<span class="pill">' + (buyer ? 'Compras' : 'Vendes') + '</span></div>' +
      '<div class="r2"><span>Cantidad</span><b>' + amt(o.usdt) + ' ' + esc(o.asset) + '</b></div>' +
      '<div class="r2"><span>Tasa</span><b>' + esc(o.cur) + ' ' + trimNum(o.rate, 6) + '</b></div>' +
      '<div class="r2"><span>' + (buyer ? 'Total a pagar' : 'Total a recibir') + '</span><b style="font-size:16px">' +
      esc(o.cur) + ' ' + money(o.total) + '</b></div>' +
      '<div style="display:flex;gap:8px;align-items:center;color:var(--gn);font-size:12px;margin-top:8px">' +
      (window.I ? window.I('shield') : '') + 'USDT retenidos en escrow de Korzadi</div></div>';
  }

  function paymentHtml() {
    var o = KZ.order;
    var buyer = o.role === 'buy';
    var ref = 'KZ-' + o.rawId.slice(0, 8);
    var instructions = o.instructions
      ? '<div style="font-size:12px;color:var(--mu);margin-top:6px;line-height:1.5;white-space:pre-wrap">' + esc(o.instructions) + '</div>'
      : '<div style="font-size:12px;color:var(--mu);margin-top:6px">' + (buyer ? 'El vendedor compartira los datos por el chat.' : 'Configura tus instrucciones en la oferta.') + '</div>';

    return '<div class="card" style="padding:14px 16px"><b style="font-size:14px">' +
      (buyer ? 'Datos de pago del vendedor' : 'Tus datos de cobro') + '</b>' +
      '<div class="r2"><span>Metodo</span><b>' + esc(o.method || '-') + '</b></div>' +
      '<div class="r2"><span>Referencia</span><b>' + ref + ' <button class="ch" style="min-height:44px;padding:0 12px;font-size:12px" onclick="kzCopy(\'ref\')">Copiar</button></b></div>' +
      instructions +
      '<div style="font-size:12px;color:var(--mu);margin-top:6px">Incluye la referencia en el concepto del pago.</div></div>';
  }

  function proofNote() {
    var o = KZ.order;
    if (o.role === 'buy') {
      return '<div class="card" style="padding:14px"><b style="font-size:14px">Comprobante</b><div style="font-size:12px;color:var(--mu);margin-top:6px;line-height:1.5">La carga de comprobantes aun no esta disponible. Marca el pago cuando lo hayas enviado; el vendedor verificara tu pago.</div></div>';
    }
    return '<div class="card" style="padding:14px"><b style="font-size:14px">Comprobante</b><div style="font-size:12px;color:var(--mu);margin-top:6px;line-height:1.5">El comprador marco el pago. Verifica tu cuenta; el comprobante no se adjunta en esta interfaz.</div></div>';
  }

  function infoCard(title, detail) {
    return '<div class="card" style="padding:16px;display:flex;gap:14px;align-items:center"><span class="sp2"></span><div><b style="font-size:14px">' +
      esc(title) + '</b><div style="color:var(--mu);font-size:13px;margin-top:4px;line-height:1.5">' + detail + '</div></div></div>';
  }

  function actionsHtml() {
    var o = KZ.order;
    var state = o.state;
    var buyer = o.role === 'buy';

    if (state === 'pay') {
      return paymentHtml() + proofNote() +
        '<div class="er" id="oe" role="alert"></div>' +
        '<button class="btn" onclick="ipay()">Ya realice el pago</button>' +
        '<div class="g2"><button class="btn o" onclick="sheet(\'cnl\')">Cancelar orden</button><button class="btn o" disabled>Disputa</button></div>' +
        '<div style="font-size:12px;color:var(--mu);text-align:center">Podras abrir una disputa despues de marcar el pago.</div>';
    }

    if (state === 'wait' && buyer) {
      return infoCard('Pago enviado', 'El vendedor esta verificando tu pago. Te avisaremos apenas libere los USDT.') +
        '<button class="btn o" onclick="sheet(\'disp\')">Abrir disputa</button>';
    }

    if (state === 'wait') {
      return infoCard('Esperando el pago del comprador', 'Tus USDT estan protegidos en escrow. Cuando el comprador pague, veras el cambio de estado aqui.') +
        paymentHtml() +
        '<div class="g2"><button class="btn o" onclick="sheet(\'cnl\')">Cancelar orden</button><button class="btn o" disabled>Disputa</button></div>';
    }

    if (state === 'review') {
      return '<div class="card" style="padding:14px"><b style="font-size:14px">El comprador marco el pago</b>' +
        '<div style="font-size:13px;color:var(--mu);margin-top:8px;line-height:1.5">Verifica que recibiste <b style="color:var(--tx)">' +
        esc(o.cur) + ' ' + money(o.total) + '</b> en tu cuenta antes de liberar los USDT. No hay comprobante adjunto en esta interfaz.</div></div>' +
        '<button class="btn" onclick="sheet(\'rel\')">Liberar USDT</button>' +
        '<button class="btn o" onclick="sheet(\'disp\')">Abrir disputa</button>';
    }

    if (state === 'disp') return disputeHtml();
    if (state === 'done') return doneHtml();

    return '<div class="card" style="padding:24px;text-align:center"><h3 style="font-size:20px">Orden cancelada</h3>' +
      '<p style="color:var(--mu);font-size:14px;margin-top:8px;line-height:1.5">Los USDT retenidos en escrow volvieron a su dueno.</p></div>' +
      '<button class="btn" onclick="go(\'p2p\')">Volver al P2P</button>';
  }

  function disputeHtml() {
    var o = KZ.order;
    var d = o.disp || {};
    var reason = d.reason ? 'Motivo: ' + esc(d.reason) + '. ' : '';
    var id = d.id || 'abierta';
    return '<div class="card" style="padding:16px;border-color:var(--am)">' +
      '<div style="display:flex;justify-content:space-between;align-items:center"><b style="color:var(--am)">Disputa ' + esc(id) + '</b><span class="pill">En revision</span></div>' +
      '<div style="color:var(--mu);font-size:13px;margin:8px 0;line-height:1.5">' + reason +
      'Los USDT permanecen congelados en escrow hasta que soporte resuelva.</div>' +
      '<div style="display:flex;gap:12px;align-items:center;padding:8px 0"><div class="av" style="width:26px;height:26px;font-size:12px;flex:none;background:var(--gn);color:#06251B">✓</div><span style="font-size:14px">Disputa abierta</span></div>' +
      '<div style="display:flex;gap:12px;align-items:center;padding:8px 0"><div class="av" style="width:26px;height:26px;font-size:12px;flex:none;background:var(--am);color:#1a1203">•</div><span style="font-size:14px">Revision de soporte</span></div>' +
      '<div style="display:flex;gap:12px;align-items:center;padding:8px 0"><div class="av" style="width:26px;height:26px;font-size:12px;flex:none;background:var(--s2);color:var(--mu)">•</div><span style="font-size:14px;color:var(--mu)">Resolucion</span></div></div>' +
      '<div class="card" style="padding:14px"><b style="font-size:14px">Evidencia</b><div style="font-size:12px;color:var(--mu);margin-top:6px;line-height:1.5">La carga de archivos aun no esta disponible. Envia los detalles por el chat de la orden.</div></div>' +
      '<div style="font-size:12px;color:var(--mu);text-align:center">La resolucion la realiza soporte; no esta disponible desde la app.</div>';
  }

  function doneHtml() {
    var o = KZ.order;
    var buyer = o.role === 'buy';
    var detail = buyer
      ? 'Recibiste ' + amt(o.usdt) + ' ' + esc(o.asset) + ' en tu wallet.'
      : 'Liberaste ' + amt(o.usdt) + ' ' + esc(o.asset) + ' y tu pago de ' + esc(o.cur) + ' ' + money(o.total) + ' fue confirmado.';
    return '<div class="card" style="padding:24px;text-align:center">' +
      '<div class="okc"><svg width="40" height="40" viewBox="0 0 24 24" aria-hidden="true"><path class="dr" pathLength="1" d="M5 12.5l4.5 4.5L19 7.5" fill="none" stroke="#fff" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round"/></svg></div>' +
      '<h3 style="font-size:20px">Orden completada</h3>' +
      '<p style="color:var(--mu);font-size:14px;margin-top:8px;line-height:1.5">' + detail + '</p>' +
      '<div style="margin-top:14px;font-size:12px;color:var(--mu);line-height:1.5">La calificacion de usuarios aun no esta disponible.</div></div>' +
      '<div class="g2"><button class="btn o" onclick="go(\'p2p\')">Volver al P2P</button><button class="btn" onclick="go(\'tx\')">Ver transacciones</button></div>';
  }

  function renderOrder() {
    var ol = document.getElementById('ol');
    var ot = document.getElementById('ot');
    var os = document.getElementById('os');
    var cn = document.getElementById('cn');
    var ci = document.getElementById('ci');
    var cb = document.getElementById('cb');
    if (!ol) return;

    if (!KZ.order) {
      ol.innerHTML = '<div class="card em">No tienes ordenes activas.</div>';
      if (ot) ot.textContent = 'Orden';
      if (os) { os.textContent = ''; os.style.color = ''; }
      if (cn) cn.textContent = '-';
      if (ci) ci.disabled = true;
      if (cb) cb.disabled = true;
      return;
    }

    var o = KZ.order;
    var color = o.state === 'done' ? 'var(--gn)' : o.state === 'disp' ? 'var(--am)' : o.state === 'cancel' ? 'var(--rd)' : 'var(--cy)';
    if (ot) ot.textContent = 'Orden #' + o.rawId.slice(0, 8);
    if (os) { os.textContent = labelFor(o); os.style.color = color; }
    if (cn) cn.textContent = o.o[0];

    var closed = o.state === 'cancel' || o.state === 'done';
    if (ci) {
      ci.disabled = closed;
      ci.placeholder = closed ? 'La orden esta cerrada' : 'Escribe un mensaje';
    }
    if (cb) cb.disabled = closed;

    ol.innerHTML = '<div style="display:grid;gap:12px">' + stepsHtml() + summaryHtml() + actionsHtml() + '</div>';
    if (window.ic) window.ic(ol);
  }

  function renderActiveOrder() {
    var slot = document.getElementById('ao');
    if (!slot) return;
    var o = KZ.order;
    if (!o || o.state === 'done' || o.state === 'cancel') { slot.innerHTML = ''; return; }
    slot.innerHTML = '<button class="card" style="width:100%;text-align:left;padding:14px 16px;margin-bottom:12px;border-color:var(--cy);color:var(--tx);display:flex;gap:12px;align-items:center;cursor:pointer;font:inherit;min-height:56px" onclick="go(\'orden\')">' +
      '<span class="sp2"></span><span style="flex:1"><b style="font-size:14px">Orden activa #' + o.rawId.slice(0, 8) + '</b>' +
      '<span style="display:block;color:var(--mu);font-size:12px;margin-top:2px">' + labelFor(o) + ' · con ' + esc(o.o[0]) + '</span></span>' +
      (window.I ? window.I('chev') : '') + '</button>';
    if (window.ic) window.ic(slot);
  }

  async function refreshOrder(silent) {
    if (!KZ.order || KZ.orderBusy) return;
    var id = KZ.order.rawId;
    var previousState = KZ.order.state;
    KZ.orderBusy = true;
    try {
      var data = await api('/p2p/orders/' + id);
      if (!KZ.order || KZ.order.rawId !== id) return;
      var order = mapOrder(data);
      setOrder(order);
      if (isOn('orden') && window.ror) window.ror();
      if (window.rao) window.rao();
      if ((order.state === 'done' || order.state === 'cancel') && previousState !== order.state) {
        loadBalances();
        loadTransactions();
      }
    } catch (e) {
      if (!silent) throw e;
      if (e.status === 403 || e.status === 404) {
        storeDel(ACTIVE_ORDER_KEY);
        setOrder(null);
        if (window.ror) window.ror();
        if (window.rao) window.rao();
      }
    } finally {
      KZ.orderBusy = false;
    }
  }

  async function markPaid() {
    if (!KZ.order) return;
    var errEl = document.getElementById('oe');
    try {
      await api('/p2p/orders/' + KZ.order.rawId + '/pay', { method: 'POST' });
      if (window.toast) window.toast('Pago marcado. Esperando confirmacion.');
      await refreshOrder(true);
      await loadMessages(true);
    } catch (e) {
      if (errEl) errEl.textContent = friendlyError(e);
      else if (window.toast) window.toast(friendlyError(e));
    }
  }

  async function releaseOrder() {
    if (!KZ.order) return;
    try {
      await api('/p2p/orders/' + KZ.order.rawId + '/release', { method: 'POST' });
      if (window.toast) window.toast('USDT liberados');
      await refreshOrder(true);
      await loadMessages(true);
      loadBalances();
      loadTransactions();
    } catch (e) {
      if (window.toast) window.toast(friendlyError(e));
    }
  }

  async function cancelOrder() {
    if (!KZ.order) return;
    try {
      await api('/p2p/orders/' + KZ.order.rawId + '/cancel', { method: 'POST' });
      if (window.toast) window.toast('Orden cancelada');
      await refreshOrder(true);
      await loadMessages(true);
      loadBalances();
      loadTransactions();
    } catch (e) {
      if (window.toast) window.toast(friendlyError(e));
    }
  }

  async function submitDispute() {
    var desc = val('dd');
    var reason = window.DR || '';
    var errEl = document.getElementById('de');
    var error = '';
    if (!reason) error = 'Elige el motivo de la disputa.';
    else if (desc.length < 10) error = 'Describe lo ocurrido (minimo 10 caracteres).';
    if (errEl) errEl.textContent = error;
    if (error || !KZ.order) return;

    var orderId = KZ.order.rawId;
    try {
      var d = await api('/p2p/orders/' + orderId + '/dispute', {
        method: 'POST',
        body: { reason: reason, description: desc }
      });
      KZ.disputeReasons[orderId] = reason;
      if (KZ.order && KZ.order.rawId === orderId) {
        KZ.order.prev = KZ.order.state;
        KZ.order.state = 'disp';
        KZ.order.disp = { id: d && d.id ? 'D-' + String(d.id).slice(0, 8) : 'abierta', reason: reason, step: 2 };
        if (window.ror) window.ror();
        if (window.rao) window.rao();
      }
      if (window.cs) window.cs();
      if (window.toast) window.toast('Disputa enviada');
      await refreshOrder(true);
      await loadMessages(true);
    } catch (e) {
      if (errEl) errEl.textContent = friendlyError(e);
    }
  }

  async function sendMessage() {
    if (!KZ.order) return;
    if (KZ.order.state === 'cancel' || KZ.order.state === 'done') return;
    var input = document.getElementById('ci');
    if (!input) return;
    var text = String(input.value || '').trim();
    if (!text) return;
    input.value = '';
    try {
      await api('/p2p/orders/' + KZ.order.rawId + '/messages', {
        method: 'POST',
        body: { message: text }
      });
      await loadMessages(true);
    } catch (e) {
      input.value = text;
      if (window.toast) window.toast(friendlyError(e));
    }
  }

  function addMessageNode(who, text, iso) {
    var cm = document.getElementById('cm');
    if (!cm) return;
    var node = document.createElement('div');
    node.className = 'bb ' + (who === 'me' ? 'me' : 'th');
    node.innerHTML = '<span>' + esc(text) + '</span><time>' + esc(fmtTime(iso)) + '</time>';
    cm.appendChild(node);
    cm.scrollTop = cm.scrollHeight;
  }

  function addSystemNode(text) {
    var cm = document.getElementById('cm');
    if (!cm) return;
    var node = document.createElement('div');
    node.className = 'bb sy';
    node.innerHTML = '<span>' + esc(text) + '</span>';
    cm.appendChild(node);
    cm.scrollTop = cm.scrollHeight;
  }

  function tickOrder() {
    var el = document.querySelector('#orden #tm');
    if (!el) return;
    var o = KZ.order;
    var running = o && (o.state === 'pay' || (o.role === 'sell' && o.state === 'wait'));
    if (!running) { el.textContent = ''; return; }
    var left = o.end - Date.now();
    if (left <= 0) { el.textContent = '⏱ 00:00'; return; }
    var seconds = Math.floor(left / 1000);
    var mm = ('0' + Math.floor(seconds / 60)).slice(-2);
    var ss = ('0' + (seconds % 60)).slice(-2);
    el.textContent = '⏱ ' + mm + ':' + ss;
  }

  function pollOrder() {
    if (!isOn('orden') || !KZ.order) return;
    if (KZ.order.state === 'done' || KZ.order.state === 'cancel') return;
    refreshOrder(true);
    loadMessages(false);
  }

  /* ------------------------------------------------------------------ */
  /* App: P2P                                                            */
  /* ------------------------------------------------------------------ */

  function renderOffers() {
    var slot = document.getElementById('l4');
    if (!slot) return;

    if (window.sd === 'sell') {
      slot.innerHTML = '<div class="card em" style="line-height:1.6">La venta P2P aun no esta habilitada en esta interfaz. Puedes comprar USDT tomando ofertas de venta.</div>';
      return;
    }

    var uid = getUserId();
    var method = window.mf || 'all';
    var fiat = window.cm || 'all';
    var list = (KZ.offers || []).filter(function (o) {
      if (String(o.user_id) === uid) return false;
      if (num(o.available_amount) <= 0) return false;
      if (fiat !== 'all' && String(o.fiat_currency) !== fiat) return false;
      if (method !== 'all') {
        var methods = Array.isArray(o.payment_methods) ? o.payment_methods : [];
        var found = methods.some(function (m) { return String(m).indexOf(method) !== -1; });
        if (!found) return false;
      }
      return true;
    });

    KZ.visibleOffers = list;
    if (!list.length) {
      slot.innerHTML = '<div class="card em" style="line-height:1.6">No hay ofertas disponibles para este filtro. Si eres vendedor, publica una oferta desde esta misma seccion.</div>';
      return;
    }

    slot.innerHTML = list.map(function (o, i) {
      var name = 'Usuario ' + String(o.user_id || '').slice(0, 8);
      var methods = Array.isArray(o.payment_methods) ? o.payment_methods : [];
      var pills = methods.map(function (m) { return '<span class="pill">' + esc(m) + '</span>'; }).join('');
      return '<div class="card" style="padding:16px;animation:up .6s both;animation-delay:' + (i * 80) + 'ms">' +
        '<div style="display:flex;align-items:center;gap:10px"><div class="co" style="background:linear-gradient(135deg,#1A63F0,#0B3BB0)">U</div>' +
        '<div style="flex:1"><b>' + esc(name) + '</b><div style="color:var(--mu);font-size:12px">Sin reputacion</div></div>' +
        '<span class="pill">⏱ ' + num(o.payment_time_limit_minutes) + ' min</span></div>' +
        '<div style="margin:14px 0 4px;font-size:24px;font-weight:800">' + esc(o.fiat_currency) + ' ' + money(o.price_per_unit) +
        ' <small style="font-size:12px;font-weight:500;color:var(--mu)">por USDT</small></div>' +
        '<div style="font-size:12px;color:var(--mu)">Limite: ' + amt(o.min_amount) + ' – ' + amt(o.max_amount) + ' USDT · Disponible: ' + amt(o.available_amount) + ' USDT</div>' +
        '<div style="display:flex;gap:6px;flex-wrap:wrap;margin:10px 0 14px">' + pills + '</div>' +
        '<button class="btn" style="width:100%" onclick="sheet(\'take\',' + i + ')">Comprar USDT</button></div>';
    }).join('');
  }

  function offerSheet(index) {
    var o = KZ.visibleOffers[index];
    if (!o) return '<div class="card em">La oferta ya no esta disponible.</div>';
    KZ.selectedOffer = o;
    var min = num(o.min_amount);
    var max = Math.min(num(o.max_amount), num(o.available_amount));
    var methods = Array.isArray(o.payment_methods) ? o.payment_methods : [];
    var options = methods.map(function (m) {
      return '<option value="' + esc(m) + '">' + esc(m) + '</option>';
    }).join('') || '<option value="">Sin metodo especificado</option>';

    return '<h3 style="font-size:20px">Comprar USDT</h3>' +
      '<p style="color:var(--mu);font-size:13px;margin:6px 0 0">con <b style="color:var(--tx)">Usuario ' + esc(String(o.user_id).slice(0, 8)) + '</b> · Sin reputacion</p>' +
      '<label class="lb" for="ta">Monto en USDT</label>' +
      '<input class="fi" id="ta" type="number" inputmode="decimal" placeholder="Entre ' + amt(min) + ' y ' + amt(max) + '" oninput="tsum()">' +
      '<div class="sm"><div>Tasa<b>' + esc(o.fiat_currency) + ' ' + money(o.price_per_unit) + '</b></div><div>Pagaras<b id="tt2">—</b></div></div>' +
      '<label class="lb" for="tmd">Metodo de pago</label><select class="fi" id="tmd">' + options + '</select>' +
      '<div style="display:flex;gap:8px;color:var(--gn);font-size:12px;margin:14px 0;align-items:center">' +
      (window.I ? window.I('shield') : '') + 'Los USDT quedan en escrow hasta confirmar el pago. Tienes ' + num(o.payment_time_limit_minutes) + ' min para pagar.</div>' +
      '<div class="er" id="te" role="alert"></div>' +
      '<button class="btn" style="width:100%;margin-top:6px" onclick="tgo()">Crear orden</button>';
  }

  function takeAmountTotal() {
    var o = KZ.selectedOffer;
    var el = document.getElementById('tt2');
    if (!o || !el) return;
    var amount = num(val('ta'));
    el.textContent = amount > 0 ? o.fiat_currency + ' ' + money(amount * num(o.price_per_unit)) : '—';
  }

  async function takeOrder() {
    var o = KZ.selectedOffer;
    var errEl = document.getElementById('te');
    var error = '';
    if (!o) error = 'La oferta ya no esta disponible.';
    if (!error) {
      var amount = num(val('ta'));
      var min = num(o.min_amount);
      var max = Math.min(num(o.max_amount), num(o.available_amount));
      if (!(amount > 0)) error = 'Ingresa el monto en USDT.';
      else if (amount < min || amount > max) error = 'El monto debe estar entre ' + amt(min) + ' y ' + amt(max) + ' USDT.';
      else if (KZ.order && ['pay', 'wait', 'review', 'disp'].indexOf(KZ.order.state) !== -1) {
        error = 'Ya tienes una orden activa. Completa o cancela la orden actual antes de crear otra.';
      }
    }
    if (error) { if (errEl) errEl.textContent = error; return; }

    var button = document.querySelector('#spc .btn');
    if (button) button.disabled = true;
    try {
      var created = await api('/p2p/orders', {
        method: 'POST',
        headers: { 'Idempotency-Key': idemKey() },
        body: {
          offer_id: String(o.id),
          amount: dec(val('ta')),
          payment_method: val('tmd')
        }
      });
      setOrder(mapOrder(created));
      storeSet(ACTIVE_ORDER_KEY, String(created.id));
      KZ.msgKey = '';
      var cm = document.getElementById('cm');
      if (cm) cm.innerHTML = '';
      if (window.cs) window.cs();
      if (window.toast) window.toast('Orden creada');
      window.go('orden');
      loadMessages(true);
    } catch (e) {
      if (errEl) errEl.textContent = friendlyError(e);
      if (button) button.disabled = false;
    }
  }

  function updateCreateBalance() {
    var s4 = document.getElementById('s4');
    if (s4) s4.textContent = amt(KZ.usdt);
    var side = document.getElementById('sd');
    if (side) {
      var buy = side.querySelector('option[value="buy"]');
      if (buy) buy.disabled = true;
      if (side.value === 'buy') side.value = 'sell';
    }
  }

  function updateCreateSummary() {
    var price = num(val('pr'));
    var total = num(val('tt'));
    var fiat = val('fm');
    var s1 = document.getElementById('s1');
    var s2 = document.getElementById('s2');
    var s3 = document.getElementById('s3');
    var s4 = document.getElementById('s4');
    var wn = document.getElementById('wn');
    if (s1) s1.textContent = price ? fiat + ' ' + money(price) : '—';
    if (s2) s2.textContent = total ? amt(total) : '—';
    if (s3) s3.textContent = price && total ? fiat + ' ' + money(price * total) : '—';
    if (s4) s4.textContent = amt(KZ.usdt);
    if (wn) {
      if (total > KZ.usdt) wn.textContent = 'El total supera tu saldo disponible de ' + amt(KZ.usdt) + ' USDT.';
      else wn.textContent = '';
    }
  }

  async function publishOffer() {
    var price = num(val('pr'));
    var min = num(val('mn'));
    var max = num(val('mx'));
    var total = num(val('tt'));
    var fiat = val('fm');
    var sideEl = document.getElementById('sd');
    var side = sideEl ? sideEl.value : 'sell';
    var terms = document.getElementById('tc');
    var errEl = document.getElementById('ee');
    var error = '';

    if (side === 'buy') error = 'Por ahora solo puedes publicar ofertas de venta.';
    else if (!(price > 0)) error = 'Ingresa el precio por unidad.';
    else if (!(total > 0)) error = 'Ingresa el total a ofrecer.';
    else if (!(min > 0) || min > max || max > total) error = 'Revisa los limites: minimo <= maximo <= total.';
    else if (total > KZ.usdt) error = 'El total supera tu saldo disponible de ' + amt(KZ.usdt) + ' USDT.';
    else if (!document.querySelector('#mt .ch[aria-pressed="true"]')) error = 'Elige al menos un metodo de pago.';
    else if (!terms || !terms.checked) error = 'Debes aceptar los terminos y condiciones.';

    if (errEl) errEl.textContent = error;
    if (error) return;

    var methods = [];
    document.querySelectorAll('#mt .ch[aria-pressed="true"]').forEach(function (el) { methods.push(el.textContent); });
    var provinces = [];
    document.querySelectorAll('#pa .ch[aria-pressed="true"]').forEach(function (el) { provinces.push(el.textContent); });
    var timeEl = document.querySelector('#crear #tm');
    var minutes = parseInt(timeEl ? timeEl.value : '30', 10);
    if (!isFinite(minutes) || minutes <= 0) minutes = 30;

    var button = document.querySelector('#crear .btn:not(.o)');
    if (button) button.disabled = true;
    try {
      await api('/p2p/offers', {
        method: 'POST',
        body: {
          asset_code: 'USDT',
          fiat_currency: fiat,
          price_per_unit: dec(price),
          min_amount: dec(min),
          max_amount: dec(max),
          total_amount: dec(total),
          side: 'sell',
          payment_methods: methods,
          country_restrictions: provinces,
          payment_time_limit_minutes: minutes,
          instructions: val('ins'),
          terms: ''
        }
      });
      if (window.toast) window.toast('Oferta publicada (visible para otros usuarios)');
      window.go('p2p');
    } catch (e) {
      if (errEl) errEl.textContent = friendlyError(e);
    } finally {
      if (button) button.disabled = false;
    }
  }

  /* ------------------------------------------------------------------ */
  /* App: enviar / recibir                                              */
  /* ------------------------------------------------------------------ */

  function renderSend() {
    var rows = (window.AS || []).filter(function (a) { return a[7] > 0; });
    var options = rows.map(function (a) {
      return '<option value="' + esc(a[0]) + '">' + esc(a[0]) + ' · ' + esc(a[4]) + '</option>';
    }).join('');
    if (!options) options = '<option value="USDT">USDT</option>';

    var first = rows[0];
    var available = first ? first[7] : 0;
    var availableLabel = first ? first[0] : 'USDT';

    return '<h3 style="font-size:20px">Enviar</h3>' +
      '<p style="color:var(--mu);font-size:12px;margin:6px 0 12px;line-height:1.5">Transferencia interna a otro usuario de Korzadi. No se envia a una direccion on-chain.</p>' +
      '<label class="lb" for="kzasset">Activo</label><select class="fi" id="kzasset" onchange="kzSendAsset()">' + options + '</select>' +
      '<label class="lb" for="kzto">Correo o ID de usuario</label><input class="fi" id="kzto" autocomplete="off" placeholder="correo@ejemplo.com">' +
      '<label class="lb" for="kzamount">Monto</label><input class="fi" id="kzamount" type="number" inputmode="decimal" placeholder="0.00">' +
      '<div style="color:var(--mu);font-size:12px;margin:8px 0 16px">Disponible: <b id="kzavail" style="color:var(--tx)">' + amt(available) + ' ' + esc(availableLabel) + '</b></div>' +
      '<label class="lb" for="kznote">Nota (opcional)</label><input class="fi" id="kznote" maxlength="140" placeholder="Motivo de la transferencia">' +
      '<div class="er" id="kzerr" role="alert"></div>' +
      '<button class="btn" style="width:100%;margin-top:6px" onclick="kzSend()">Continuar</button>';
  }

  function updateSendAvailable() {
    var selected = val('kzasset') || 'USDT';
    var row = (window.AS || []).filter(function (a) { return a[0] === selected; })[0];
    var el = document.getElementById('kzavail');
    if (el) el.textContent = amt(row ? row[7] : 0) + ' ' + selected;
  }

  function ask2FA(callback, errorText) {
    KZ.twoFACallback = callback;
    if (window.sheet) window.sheet('kz2fa');
    if (errorText) {
      setTimeout(function () {
        var el = document.getElementById('kz2faerr');
        if (el) el.textContent = errorText;
      }, 0);
    }
  }

  async function doTransfer(code) {
    if (!KZ.pendingTransfer || KZ.sending) return;
    KZ.sending = true;
    var pending = KZ.pendingTransfer;
    var headers = { 'Idempotency-Key': pending.key };
    if (code) headers['X-2FA-Code'] = code;

    try {
      await api('/wallet/transfers', {
        method: 'POST',
        headers: headers,
        body: {
          recipient: pending.recipient,
          asset: pending.asset,
          amount: pending.amount,
          note: pending.note
        }
      });
      KZ.pendingTransfer = null;
      KZ.sending = false;
      if (window.cs) window.cs();
      if (window.toast) window.toast('Transferencia enviada');
      loadBalances();
      loadTransactions();
    } catch (e) {
      KZ.sending = false;
      if (e.code === 'step_up_required' || e.code === 'step_up_invalid') {
        ask2FA(function (nextCode) { doTransfer(nextCode); }, e.code === 'step_up_invalid' ? friendlyError(e) : '');
        return;
      }
      var errEl = document.getElementById('kzerr');
      if (errEl) errEl.textContent = friendlyError(e);
      else if (window.toast) window.toast(friendlyError(e));
    }
  }

  async function sendTransfer() {
    var recipient = val('kzto');
    var asset = val('kzasset') || 'USDT';
    var amount = num(val('kzamount'));
    var note = val('kznote');
    var errEl = document.getElementById('kzerr');
    var row = (window.AS || []).filter(function (a) { return a[0] === asset; })[0];
    var available = row ? row[7] : 0;
    var error = '';

    if (!recipient) error = 'Ingresa el correo o ID del destinatario.';
    else if (!(amount > 0)) error = 'Ingresa un monto valido.';
    else if (amount > available) error = 'El monto supera tu saldo disponible (' + amt(available) + ' ' + asset + ').';

    if (errEl) errEl.textContent = error;
    if (error) return;

    KZ.pendingTransfer = {
      recipient: recipient,
      asset: asset,
      amount: dec(amount),
      note: note,
      key: idemKey()
    };
    await doTransfer('');
  }

  function renderReceive() {
    setTimeout(loadReceive, 0);
    return '<h3 style="font-size:20px">Recibir USDT</h3>' +
      '<p style="color:var(--mu);font-size:12px;margin:6px 0 12px;line-height:1.5">Deposito en BNB Smart Chain (BEP-20).</p>' +
      '<div id="kzrecv"><div class="em">Obteniendo direccion...</div></div>';
  }

  async function loadReceive() {
    var slot = document.getElementById('kzrecv');
    if (!slot) return;
    try {
      var data = await api('/wallet/crypto/usdt/bsc/address');
      KZ.recvAddress = data && data.address ? String(data.address) : '';
      slot.innerHTML = '<div style="background:var(--s2);border-radius:16px;padding:14px;word-break:break-all;text-align:center;font-family:ui-monospace,monospace;font-size:13px;line-height:1.6">' +
        esc(KZ.recvAddress) + '</div>' +
        '<div style="color:var(--mu);font-size:12px;margin:12px 0;line-height:1.5">' + esc(data && data.notice ? data.notice : 'Envia unicamente USDT (BEP-20) por BNB Smart Chain.') + '</div>' +
        '<div style="display:flex;gap:8px;align-items:center;color:var(--gn);font-size:12px;margin-bottom:12px">' +
        (window.I ? window.I('shield') : '') + 'Red: BNB Smart Chain (BEP-20)</div>' +
        '<button class="btn" style="width:100%" onclick="kzCopy(\'address\')">Copiar direccion</button>' +
        '<div style="font-size:11px;color:var(--mu);text-align:center;margin-top:10px">El codigo QR se anadira cuando este disponible.</div>';
      if (window.ic) window.ic(slot);
    } catch (e) {
      slot.innerHTML = '<div class="er">' + esc(friendlyError(e)) + '</div>' +
        '<div style="font-size:12px;color:var(--mu);line-height:1.5;margin-top:10px">Los depositos por TRON estan temporalmente deshabilitados. Si el problema continua, contacta a soporte.</div>';
    }
  }

  function renderMore() {
    var actions = [
      ['down', 'Depositar', 'cs();sheet(\'recv\')'],
      ['send', 'Retirar', 'cs();toast(\'Los retiros on-chain aun no estan integrados en esta interfaz.\')'],
      ['swap', 'Convertir', 'cs();toast(\'La conversion no esta disponible.\')'],
      ['tx', 'Historial', 'cs();go(\'tx\')']
    ];
    return '<h3 style="font-size:20px;margin-bottom:6px">Mas acciones</h3>' + actions.map(function (a) {
      return '<button class="to" onclick="' + a[2] + '"><span data-i="' + a[0] + '"></span><b>' + a[1] + '</b></button>';
    }).join('');
  }

  /* ------------------------------------------------------------------ */
  /* App: seguridad                                                      */
  /* ------------------------------------------------------------------ */

  function begin2FA() {
    setTimeout(async function () {
      var slot = document.getElementById('kztfa');
      if (!slot) return;
      try {
        var data = await api('/security/2fa/setup', { method: 'POST' });
        if (window.SEC) window.SEC.tfa = false;
        KZ.setup = data;
        if (data.qr_code) {
          slot.innerHTML = '<p style="color:var(--mu);font-size:13px;margin:8px 0;line-height:1.5">Escanea este codigo con Google Authenticator o Authy. Si prefieres, usa la clave manual.</p>' +
            '<div style="text-align:center;margin:10px 0"><img src="' + esc(data.qr_code) + '" alt="QR 2FA" style="width:200px;height:200px;border-radius:12px;background:#fff;padding:8px;"></div>' +
            '<div class="card" style="padding:12px;margin:10px 0"><b style="font-size:13px">Clave manual</b>' +
            '<div style="word-break:break-all;font-family:ui-monospace,monospace;font-size:13px;margin-top:6px">' + esc(data.secret) + '</div>' +
            '<button class="btn o" style="width:100%;margin-top:8px;min-height:44px" onclick="kzCopy(\'secret\')">Copiar clave</button></div>' +
            '<label class="lb" for="kztfacode">Codigo de 6 digitos</label>' +
            '<input class="fi" id="kztfacode" inputmode="numeric" maxlength="6" placeholder="000000" style="text-align:center;font-size:22px;letter-spacing:6px">' +
            '<div class="er" id="kztfaerr" role="alert"></div>' +
            '<button class="btn" style="width:100%;margin-top:6px" onclick="kzConfirm2FA()">Activar 2FA</button>';
        } else {
          slot.innerHTML = '<p style="color:var(--mu);font-size:13px;margin:8px 0;line-height:1.5">Anade esta cuenta en Google Authenticator o Authy con la clave manual. El codigo QR no esta disponible en esta interfaz.</p>' +
            '<div class="card" style="padding:12px;margin:10px 0"><b style="font-size:13px">Clave manual</b>' +
            '<div style="word-break:break-all;font-family:ui-monospace,monospace;font-size:13px;margin-top:6px">' + esc(data.secret) + '</div>' +
            '<button class="btn o" style="width:100%;margin-top:8px;min-height:44px" onclick="kzCopy(\'secret\')">Copiar clave</button></div>' +
            '<label class="lb" for="kztfacode">Codigo de 6 digitos</label>' +
            '<input class="fi" id="kztfacode" inputmode="numeric" maxlength="6" placeholder="000000" style="text-align:center;font-size:22px;letter-spacing:6px">' +
            '<div class="er" id="kztfaerr" role="alert"></div>' +
            '<button class="btn" style="width:100%;margin-top:6px" onclick="kzConfirm2FA()">Activar 2FA</button>';
        }
      } catch (e) {
        slot.innerHTML = '<div class="er">' + esc(friendlyError(e)) + '</div>';
        if (e.code === '2fa_already_enabled') load2FAStatus();
      }
    }, 0);
  }

  async function confirm2FA() {
    var code = val('kztfacode');
    var errEl = document.getElementById('kztfaerr');
    if (!/^\d{6}$/.test(code)) {
      if (errEl) errEl.textContent = 'Ingresa los 6 digitos de tu app.';
      return;
    }
    try {
      await api('/security/2fa/confirm', { method: 'POST', body: { code: code } });
      KZ.codes = (KZ.setup && KZ.setup.recovery_codes) || [];
      KZ.setup = null;
      if (window.SEC) window.SEC.tfa = true;
      if (window.cs) window.cs();
      if (window.rs) window.rs();
      if (window.toast) window.toast('2FA activado');
      if (window.sheet) window.sheet('codes');
    } catch (e) {
      if (errEl) errEl.textContent = friendlyError(e);
    }
  }

  async function disable2FA() {
    var password = val('kzoffpass');
    var code = val('kzoffcode');
    var errEl = document.getElementById('kzofferr');
    if (!password) { if (errEl) errEl.textContent = 'Ingresa tu contrasena actual.'; return; }
    if (!code) { if (errEl) errEl.textContent = 'Ingresa el codigo 2FA.'; return; }
    try {
      await api('/security/2fa/disable', { method: 'POST', body: { password: password, code: code } });
      if (window.SEC) window.SEC.tfa = false;
      KZ.codes = null;
      KZ.recoveryRemaining = 0;
      if (window.cs) window.cs();
      if (window.rs) window.rs();
      if (window.toast) window.toast('2FA desactivado');
    } catch (e) {
      if (errEl) errEl.textContent = friendlyError(e);
    }
  }

  async function regenerateCodes() {
    var password = val('kzregenpass');
    var code = val('kzregencode');
    var errEl = document.getElementById('kzregenerr');
    if (!password) { if (errEl) errEl.textContent = 'Ingresa tu contrasena actual.'; return; }
    if (!code) { if (errEl) errEl.textContent = 'Ingresa el codigo 2FA.'; return; }
    try {
      var data = await api('/security/2fa/recovery-codes/regenerate', {
        method: 'POST',
        body: { password: password, code: code }
      });
      KZ.codes = (data && data.recovery_codes) || [];
      if (window.cs) window.cs();
      if (window.sheet) window.sheet('codes');
      if (window.toast) window.toast('Codigos regenerados');
    } catch (e) {
      if (errEl) errEl.textContent = friendlyError(e);
    }
  }

  async function changePassword() {
    var current = document.getElementById('p0') ? document.getElementById('p0').value : '';
    var next = document.getElementById('p1') ? document.getElementById('p1').value : '';
    var confirm = document.getElementById('p2') ? document.getElementById('p2').value : '';
    var code = document.getElementById('p3') ? document.getElementById('p3').value : '';
    var errEl = document.getElementById('te');
    var error = '';

    if (!current) error = 'Ingresa tu contrasena actual.';
    else if (next.length < 10) error = 'La nueva contrasena debe tener al menos 10 caracteres.';
    else if (next !== confirm) error = 'Las contrasenas no coinciden.';
    else if (window.SEC && window.SEC.tfa && !code) error = 'Tu cuenta tiene 2FA activo; ingresa el codigo.';

    if (errEl) errEl.textContent = error;
    if (error) return;

    try {
      await api('/account/change-password', {
        method: 'POST',
        body: {
          current_password: current,
          new_password: next,
          two_fa_code: code
        }
      });
      try { window.sessionStorage.setItem('kz_notice', 'Contrasena actualizada. Inicia sesion de nuevo.'); } catch (e) {}
      clearSession();
      window.location.replace('/');
    } catch (e) {
      if (errEl) errEl.textContent = friendlyError(e);
    }
  }

  function twoFAForm() {
    return '<h3 style="font-size:20px">Activar 2FA</h3>' +
      '<div id="kztfa" class="em">Generando secreto...</div>';
  }

  function kycSheet() {
    var status = KZ.kycStatus || 'not_started';
    var labels = {
      'not_started': ['Sin verificar', 'Verifica tu identidad para aumentar tus límites y retirar fondos.'],
      'pending': ['En revisión', 'Estamos revisando tus datos.'],
      'in_review': ['En revisión', 'Estamos revisando tus datos.'],
      'verified': ['Verificado', 'Tu identidad ya está verificada.'],
      'approved': ['Verificado', 'Tu identidad ya está verificada.'],
      'rejected': ['Rechazado', 'No pudimos verificar tu identidad. Puedes intentarlo de nuevo.'],
      'declined': ['Rechazado', 'No pudimos verificar tu identidad. Puedes intentarlo de nuevo.'],
      'expired': ['Vencido', 'Tu verificación expiró. Puedes renovarla.']
    };
    var info = labels[status] || ['—', ''];
    var verified = status === 'verified' || status === 'approved';
    var inReview = status === 'pending' || status === 'in_review';
    var countries = window.DIDIT_COUNTRIES || window.KZ_COUNTRIES || [
      { code: 'AR', name: 'Argentina' }, { code: 'CO', name: 'Colombia' }, { code: 'EC', name: 'Ecuador' }, { code: 'VE', name: 'Venezuela' }, { code: 'CL', name: 'Chile' },
      { code: 'PE', name: 'Perú' }, { code: 'BO', name: 'Bolivia' }, { code: 'MX', name: 'México' }, { code: 'ES', name: 'España' }, { code: 'US', name: 'Estados Unidos' }
    ];
    var options = countries.map(function (c) { return '<option value="' + esc(c.code) + '">' + esc(c.name) + '</option>'; }).join('');
    return '<h3 style="font-size:20px">Verificación KYC</h3>' +
      '<div class="card" style="padding:14px 16px;margin-top:10px">' +
      '<div style="display:flex;align-items:center;gap:10px"><div class="co" style="background:' + (verified ? 'var(--gn)' : 'var(--am)') + '">' + (verified ? '✓' : 'K') + '</div>' +
      '<div><b style="font-size:14px">' + esc(info[0]) + '</b><div style="color:var(--mu);font-size:13px">' + esc(info[1]) + '</div></div></div>' +
      (verified || inReview ? '<div style="font-size:12px;color:var(--mu);margin-top:8px">Estado actual: <b style="color:' + (verified ? 'var(--gn)' : 'var(--am)') + '">' + esc(info[0]) + '</b></div>' : '') +
      (!verified && !inReview ? '<label class="lb" for="kyc_country">País de emisión del documento</label><select class="fi" id="kyc_country">' + options + '</select>' : '') +
      (!verified && !inReview ? '<label class="lb" for="kyc_residence">País de residencia</label><select class="fi" id="kyc_residence">' + options + '</select>' : '') +
      '<div class="er" id="kyce" role="alert"></div>' +
      (!verified && !inReview ? '<button class="btn" style="width:100%;margin-top:6px" onclick="kzKycStart()">Iniciar verificación</button>' : '') +
      '</div>';
  }

  function kycStart() {
    var country = document.getElementById('kyc_country') ? document.getElementById('kyc_country').value : '';
    var residence = document.getElementById('kyc_residence') ? document.getElementById('kyc_residence').value : '';
    var errEl = document.getElementById('kyce');
    if (!country) { if (errEl) errEl.textContent = 'Selecciona al menos tu país.'; return; }
    api('/kyc/start', { method: 'POST', headers: { 'Idempotency-Key': idemKey() }, body: { country: country, residence_country: residence } })
      .then(function (data) {
        if (data && data.redirect_url) { window.location.href = data.redirect_url; return; }
        loadKYCStatus();
        if (window.cs) window.cs();
        if (window.toast) window.toast('Verificación iniciada');
      })
      .catch(function (e) { if (errEl) errEl.textContent = friendlyError(e); });
  }

  if (window.SH) window.SH.kyc = kycSheet;

  if (window.SH) window.SH.kyc = kycSheet;

  function kzPinSheet() {
    var enabled = !!(window.SEC && window.SEC.pin);
    return enabled ? '<h3 style="font-size:20px">PIN de seguridad</h3>' +
      '<p style="color:var(--mu);font-size:13px;margin:8px 0 14px;line-height:1.5">Tu PIN protege transferencias, pagos y retiros. Para eliminarlo, ingresa tu PIN actual.</p>' +
      '<label class="lb" for="kzpinverify">PIN actual</label><input class="fi" id="kzpinverify" inputmode="numeric" maxlength="16" placeholder="4-16 dígitos" style="text-align:center;font-size:18px;letter-spacing:4px">' +
      '<label class="lb" for="kzpinpw">Contraseña actual</label><input class="fi" id="kzpinpw" type="password" autocomplete="current-password">' +
      '<div class="er" id="kzpindelerr" role="alert"></div>' +
      '<button class="btn" style="width:100%;margin-top:6px;background:none;color:var(--rd);border-color:var(--rd)" onclick="kzPinDisable()">Eliminar PIN</button>' :
      '<h3 style="font-size:20px">Crear PIN de seguridad</h3>' +
      '<p style="color:var(--mu);font-size:13px;margin:8px 0 0">Elige un PIN de 4 a 16 dígitos para proteger operaciones sensibles.</p>' +
      '<label class="lb" for="kzpinnew">Nuevo PIN</label><input class="fi" id="kzpinnew" inputmode="numeric" maxlength="16" placeholder="4-16 dígitos" style="font-size:18px;text-align:center">' +
      '<label class="lb" for="kzpinconf">Confirmar PIN</label><input class="fi" id="kzpinconf" inputmode="numeric" maxlength="16" placeholder="4-16 dígitos" style="font-size:18px;text-align:center">' +
      '<label class="lb" for="kzpinpwnew">Contraseña actual</label><input class="fi" id="kzpinpwnew" type="password" autocomplete="current-password">' +
      '<div class="er" id="kzpinnewerr" role="alert"></div>' +
      '<button class="btn" style="width:100%;margin-top:6px" onclick="kzPinSetup()">Crear PIN</button>';
  }
  if (window.SH) {
    window.SH.tpin = function () {
      window.SH.kzpin = kzPinSheet;
      return kzPinSheet();
    };
  }

  function kzPinSetup() {
    var pin = document.getElementById('kzpinnew') ? document.getElementById('kzpinnew').value.trim() : '';
    var conf = document.getElementById('kzpinconf') ? document.getElementById('kzpinconf').value.trim() : '';
    var pw = document.getElementById('kzpinpwnew') ? document.getElementById('kzpinpwnew').value : '';
    var errEl = document.getElementById('kzpinnewerr');
    var error = '';
    if (!pin || pin.length < 4 || pin.length > 16) error = 'El PIN debe tener entre 4 y 16 dígitos y solo números.';
    else if (pin !== conf) error = 'Los PIN no coinciden.';
    else if (!pw) error = 'Ingresa tu contraseña actual.';
    if (errEl) errEl.textContent = error;
    if (error) return;
    api('/security/pin/setup', { method: 'POST', body: { pin: pin, current_password: pw }, auth: false })
      .then(function (data) {
        if (window.SEC) window.SEC.pin = true;
        loadPreferences();
        if (window.cs) window.cs();
        if (window.toast) window.toast('PIN creado');
      })
      .catch(function (e) { if (errEl) errEl.textContent = friendlyError(e); });
  }

  function kzPinDisable() {
    var pin = document.getElementById('kzpinverify') ? document.getElementById('kzpinverify').value.trim() : '';
    var pw = document.getElementById('kzpinpw') ? document.getElementById('kzpinpw').value : '';
    var errEl = document.getElementById('kzpindelerr');
    var error = '';
    if (!pin) error = 'Ingresa tu PIN actual.';
    else if (!pw) error = 'Ingresa tu contraseña actual.';
    if (errEl) errEl.textContent = error;
    if (error) return;
    api('/security/pin/disable', { method: 'POST', body: { current_password: pw } })
      .catch(function (e) { if (errEl) errEl.textContent = friendlyError(e); })
      .then(function () {
        if (window.SEC) { window.SEC.pin = false; window.SEC.lock = false; window.SEC.bio = false; }
        loadPreferences();
        if (window.cs) window.cs();
        if (window.toast) window.toast('PIN eliminado');
      });
  }

  function twoFAOffForm() {
    return '<h3 style="font-size:20px">Desactivar 2FA</h3>' +
      '<p style="color:var(--mu);font-size:13px;margin:8px 0 14px;line-height:1.5">Tu cuenta quedara menos protegida. Ingresa tu contrasena actual y el codigo de 6 digitos para confirmar.</p>' +
      '<label class="lb" for="kzoffpass">Contrasena actual</label><input class="fi" id="kzoffpass" type="password" autocomplete="current-password">' +
      '<label class="lb" for="kzoffcode">Codigo 2FA</label><input class="fi" id="kzoffcode" inputmode="numeric" maxlength="12" autocomplete="one-time-code" placeholder="000000">' +
      '<div class="er" id="kzofferr" role="alert"></div>' +
      '<button class="btn" style="width:100%;margin-top:6px;background:none;color:var(--rd);border-color:var(--rd)" onclick="kzDoDisable()">Desactivar 2FA</button>';
  }

  function codesSheet() {
    var codes = KZ.codes || [];
    if (!codes.length) {
      return '<h3 style="font-size:20px">Codigos de recuperacion</h3>' +
        '<p style="color:var(--mu);font-size:13px;margin:8px 0;line-height:1.5">Por seguridad, los codigos solo se muestran al activar o regenerar el 2FA. Puedes generar un juego nuevo.</p>' +
        '<div class="g2"><button class="btn o" onclick="sheet(\'regen\')">Regenerar</button><button class="btn" onclick="cs()">Cerrar</button></div>';
    }
    return '<h3 style="font-size:20px">Codigos de recuperacion</h3>' +
      '<p style="color:var(--mu);font-size:13px;margin:8px 0 0;line-height:1.5">Guardalos ahora; no se volveran a mostrar. Cada codigo sirve una sola vez.</p>' +
      '<div style="display:grid;grid-template-columns:1fr 1fr;gap:8px;margin:14px 0">' +
      codes.map(function (c) { return '<div class="card" style="padding:10px;text-align:center;font-family:ui-monospace,monospace;font-size:13px;letter-spacing:1px">' + esc(c) + '</div>'; }).join('') +
      '</div><div class="g2"><button class="btn o" onclick="sheet(\'regen\')">Regenerar</button><button class="btn" onclick="kzCopy(\'codes\')">Copiar</button></div>';
  }

  function regenerateSheet() {
    return '<h3 style="font-size:20px">Regenerar codigos</h3>' +
      '<p style="color:var(--mu);font-size:13px;margin:8px 0 14px;line-height:1.5">Los codigos anteriores dejaran de funcionar.</p>' +
      '<label class="lb" for="kzregenpass">Contrasena actual</label><input class="fi" id="kzregenpass" type="password" autocomplete="current-password">' +
      '<label class="lb" for="kzregencode">Codigo 2FA</label><input class="fi" id="kzregencode" inputmode="numeric" maxlength="12" autocomplete="one-time-code" placeholder="000000">' +
      '<div class="er" id="kzregenerr" role="alert"></div>' +
      '<button class="btn" style="width:100%;margin-top:6px" onclick="kzDoRegen()">Regenerar</button>';
  }

  function passwordSheet() {
    var tfa = window.SEC && window.SEC.tfa;
    return '<h3 style="font-size:20px">Cambiar contrasena</h3>' +
      '<label class="lb" for="p0">Contrasena actual</label><input class="fi" id="p0" type="password" autocomplete="current-password">' +
      '<label class="lb" for="p1">Nueva contrasena</label><input class="fi" id="p1" type="password" autocomplete="new-password" oninput="pwm()">' +
      '<div style="display:flex;align-items:center;gap:10px;margin-top:8px"><div style="flex:1;height:6px;border-radius:4px;background:var(--ln);overflow:hidden"><div id="sbr" style="height:100%;width:0;transition:.4s"></div></div><small id="sl" style="color:var(--mu);width:48px"></small></div>' +
      '<label class="lb" for="p2">Confirmar nueva contrasena</label><input class="fi" id="p2" type="password" autocomplete="new-password">' +
      (tfa ? '<label class="lb" for="p3">Codigo 2FA</label><input class="fi" id="p3" inputmode="numeric" maxlength="12" autocomplete="one-time-code" placeholder="000000"><div style="color:var(--mu);font-size:12px;margin-top:4px">Tu cuenta tiene 2FA activo.</div>' : '') +
      '<div class="er" id="te" role="alert"></div>' +
      '<button class="btn" style="width:100%;margin-top:6px" onclick="pwo()">Cambiar contrasena</button>';
  }

  function twoFAPromptSheet() {
    return '<h3 style="font-size:20px">Verificacion 2FA</h3>' +
      '<p style="color:var(--mu);font-size:13px;margin:8px 0 14px;line-height:1.5">Esta operacion requiere un codigo de tu app autenticadora.</p>' +
      '<input class="fi" id="kz2facode" inputmode="numeric" maxlength="12" autocomplete="one-time-code" placeholder="000000" style="text-align:center;font-size:22px;letter-spacing:6px">' +
      '<div class="er" id="kz2faerr" role="alert"></div>' +
      '<button class="btn" style="width:100%;margin-top:6px" onclick="kzSubmit2FA()">Verificar</button>';
  }

  /* ------------------------------------------------------------------ */
  /* App: perfil / logout                                                */
  /* ------------------------------------------------------------------ */

  function initialsFor(user) {
    if (!user) return 'U';
    var first = String(user.first_name || '').trim();
    var last = String(user.last_name || '').trim();
    var initials = (first.charAt(0) + last.charAt(0)).toUpperCase();
    if (initials) return initials;
    var email = String(user.email || '').trim();
    return (email.slice(0, 2) || 'U').toUpperCase();
  }

  function setProfile() {
    var user = getUser();
    var name = user ? [user.first_name, user.last_name].filter(Boolean).join(' ') : 'Usuario';
    var email = user && user.email ? user.email : '-';
    var initials = initialsFor(user);

    var headerAvatar = document.querySelector('header.hd .av');
    if (headerAvatar) headerAvatar.textContent = initials;

    var profile = document.getElementById('perfil');
    if (!profile) return;
    var avatar = profile.querySelector('.av');
    if (avatar) avatar.textContent = initials;
    var info = avatar ? avatar.parentElement : null;
    if (info) {
      var nameEl = info.querySelector('b');
      if (nameEl) nameEl.textContent = name || 'Usuario';
      var emailEl = info.querySelector('div[style*="color"]');
      if (emailEl) emailEl.textContent = email;
    }

    var kycStatusStr = KZ.kycStatus || 'not_started';
    var kycPill = document.querySelector('#mn2 .pill');
    if (kycPill) {
      var kycLabel = kycStatusStr === 'verified' || kycStatusStr === 'approved' ? 'Verificado' : (kycStatusStr === 'pending' || kycStatusStr === 'in_review' ? 'En revisión' : (kycStatusStr === 'rejected' || kycStatusStr === 'declined' ? 'Rechazado' : 'Pendiente'));
      var kycColor = (kycStatusStr === 'verified' || kycStatusStr === 'approved') ? 'var(--gn)' : ((kycStatusStr === 'rejected' || kycStatusStr === 'declined') ? 'var(--rd)' : 'var(--am)');
      kycPill.textContent = kycLabel;
      kycPill.style.color = kycColor;
    }
  }

  async function logout() {
    var refresh = getRefresh();
    try {
      if (refresh) {
        await api('/auth/logout', {
          method: 'POST',
          body: { refresh_token: refresh },
          auth: false,
          redirect: false
        });
      }
    } catch (e) {}
    clearSession();
    window.location.replace('/');
  }

  function wireLogout() {
    document.querySelectorAll('.nb.lo').forEach(function (button) {
      button.onclick = function (ev) {
        if (ev) ev.preventDefault();
        logout();
      };
    });
    var profile = document.getElementById('perfil');
    if (profile) {
      profile.querySelectorAll('button.btn').forEach(function (button) {
        if (button.textContent.indexOf('Cerrar') !== -1) {
          button.onclick = function (ev) {
            if (ev) ev.preventDefault();
            logout();
          };
        }
      });
    }
  }

  /* ------------------------------------------------------------------ */
  /* App: instalacion de overrides                                       */
  /* ------------------------------------------------------------------ */

  function installAppOverrides() {
    window.OR = null;
    window.AS = [];
    window.TX = [];
    window.OF = [];
    window.TOT = 0;
    window.TK = null;
    window.DR = '';
    window.RC = [];
    if (window.SEC) {
      window.SEC.tfa = false;
      window.SEC.pin = null;
      window.SEC.lock = false;
      window.SEC.bio = false;
      window.SEC.mail = false;
    }

    ['l1', 'l2', 'l3', 'l4', 'l5'].forEach(function (id) {
      var el = document.getElementById(id);
      if (el) el.innerHTML = '<div class="em">Cargando...</div>';
    });
    var total = document.getElementById('tot');
    if (total) total.textContent = '$—';
    var s4 = document.getElementById('s4');
    if (s4) s4.textContent = '—';
    var activeSlot = document.getElementById('ao');
    if (activeSlot) activeSlot.innerHTML = '';
    var chat = document.getElementById('cm');
    if (chat) chat.innerHTML = '';
    var orderTitle = document.getElementById('ot');
    if (orderTitle) orderTitle.textContent = 'Orden';
    var orderStatus = document.getElementById('os');
    if (orderStatus) orderStatus.textContent = '';
    var counterparty = document.getElementById('cn');
    if (counterparty) counterparty.textContent = '-';

    /* Pedido */
    window.lbl = labelFor;
    window.stg = stageFor;
    window.steps = stepsHtml;
    window.sumry = summaryHtml;
    window.paydet = paymentHtml;
    window.proofUI = function () { return proofNote(); };
    window.acts = actionsHtml;
    window.dispUI = disputeHtml;
    window.doneUI = doneHtml;
    window.ror = renderOrder;
    window.rao = renderActiveOrder;
    window.ipay = markPaid;
    window.finish = releaseOrder;
    window.ocan = cancelOrder;
    window.dcan = function () { if (window.toast) window.toast('Cancelar una disputa no esta disponible. Contacta a soporte.'); };
    window.dsub = submitDispute;
    window.send = sendMessage;
    window.qc = function (text) {
      var input = document.getElementById('ci');
      if (input) input.value = text || '';
      sendMessage();
    };
    window.pfile = function () { if (window.toast) window.toast('La carga de archivos aun no esta disponible.'); };

    if (window.SH) {
      window.SH.take = offerSheet;
      window.SH.cnl = function () {
        var o = KZ.order;
        if (!o) return '<div class="card em">No tienes una orden activa.</div>';
        return '<h3 style="font-size:20px">Cancelar la orden?</h3>' +
          '<p style="color:var(--mu);font-size:13px;margin:8px 0 16px;line-height:1.5">Si ya enviaste el pago no canceles: abre una disputa.</p>' +
          '<div class="g2"><button class="btn o" onclick="cs()">Volver</button>' +
          '<button class="btn" style="background:none;color:var(--rd);border-color:var(--rd)" onclick="cs();ocan()">Cancelar orden</button></div>';
      };
      window.SH.rel = function () {
        var o = KZ.order;
        if (!o) return '<div class="card em">No tienes una orden activa.</div>';
        return '<h3 style="font-size:20px">Liberar USDT</h3>' +
          '<p style="color:var(--mu);font-size:13px;margin:8px 0 16px;line-height:1.5">Libera solo si ya recibiste <b style="color:var(--tx)">' +
          esc(o.cur) + ' ' + money(o.total) + '</b> en tu cuenta. Esta accion no se puede deshacer.</p>' +
          '<div class="g2"><button class="btn o" onclick="cs()">Todavia no</button>' +
          '<button class="btn" onclick="cs();finish()">Si, liberar</button></div>';
      };
      window.SH.disp = function () {
        window.DR = '';
        var reasons = window.DRS || ['No recibi el pago', 'No liberaron los USDT', 'Monto incorrecto', 'Otro motivo'];
        return '<h3 style="font-size:20px">Abrir disputa</h3>' +
          '<p style="color:var(--mu);font-size:13px;margin:8px 0 0;line-height:1.5">Soporte revisara el chat y los movimientos. Los USDT quedan congelados en escrow hasta resolver.</p>' +
          '<div class="chs" id="dr" style="flex-wrap:wrap;overflow:visible;margin-top:14px">' +
          reasons.map(function (r) { return '<button class="ch" aria-pressed="false" data-f="' + esc(r) + '" onclick="dsel(this)">' + esc(r) + '</button>'; }).join('') +
          '</div><label class="lb" for="dd">Describe lo ocurrido</label>' +
          '<textarea class="fi" id="dd" placeholder="Cuentanos que paso..."></textarea>' +
          '<div class="er" id="de" role="alert"></div>' +
          '<button class="btn" style="width:100%;margin-top:6px" onclick="dsub()">Enviar disputa</button>';
      };
      window.SH.tfa = function () {
        begin2FA();
        return twoFAForm();
      };
      window.SH.tfaoff = twoFAOffForm;
      window.SH.codes = codesSheet;
      window.SH.regen = regenerateSheet;
      window.SH.pw = passwordSheet;
      window.SH.send = renderSend;
      window.SH.recv = renderReceive;
      window.SH.more = renderMore;
      window.SH.kz2fa = twoFAPromptSheet;
    }

    window.t2fa = function () {
      if (window.SEC && window.SEC.tfa) window.sheet('tfaoff');
      else window.sheet('tfa');
    };
    window.tmail = function () {
      var current = !!(window.SEC && window.SEC.mail);
      api('/security/prefs', { method: 'POST', body: { email_alerts_enabled: !current }, auth: false }).catch(function () {});
      window.SEC.mail = !current;
      loadPreferences();
      if (window.toast) window.toast(current ? 'Alertas desactivadas' : 'Alertas por correo activadas');
      if (window.rs) window.rs();
    };
    window.tlock = function () {
      if (!window.SEC || !window.SEC.pin) {
        if (window.toast) window.toast('Primero crea tu PIN de seguridad');
        window.tpin();
        return;
      }
      api('/security/prefs', { method: 'POST', body: { app_lock_enabled: !(window.SEC && window.SEC.lock) }, auth: false }).catch(function () {});
      window.SEC.lock = !(window.SEC && window.SEC.lock);
      if (window.SEC && !window.SEC.lock) window.SEC.bio = false;
      loadPreferences();
      if (window.toast) window.toast((window.SEC && window.SEC.lock) ? 'Bloqueo de la app activado' : 'Bloqueo de la app desactivado');
      if (window.rs) window.rs();
    };
    window.tbio = function () {
      if (!window.SEC || !window.SEC.lock) {
        if (window.toast) window.toast('Activa antes el bloqueo de la app');
        return;
      }
      api('/security/prefs', { method: 'POST', body: { biometric_enabled: !(window.SEC && window.SEC.bio) }, auth: false }).catch(function () {});
      window.SEC.bio = !(window.SEC && window.SEC.bio);
      loadPreferences();
      if (window.toast) window.toast((window.SEC && window.SEC.bio) ? 'Biometría activada' : 'Biometría desactivada');
      if (window.rs) window.rs();
    };
    window.tpin = function () {
      if (window.SEC && window.SEC.pin) {
        window.sheet('kzpin');
      } else {
        window.sheet('kzpin');
      }
    };
    window.pwo = changePassword;

    window.tsum = takeAmountTotal;
    window.tgo = takeOrder;
    window.ro = renderOffers;
    window.pub = publishOffer;
    window.sum = updateCreateSummary;

    ['pr', 'tt'].forEach(function (id) {
      var el = document.getElementById(id);
      if (el) el.oninput = updateCreateSummary;
    });
    var sideSelect = document.getElementById('sd');
    if (sideSelect) sideSelect.onchange = updateCreateSummary;
    var fiatSelect = document.getElementById('fm');
    if (fiatSelect) fiatSelect.onchange = updateCreateSummary;

    window.kzSend = sendTransfer;
    window.kzSendAsset = updateSendAvailable;
    window.kzConfirm2FA = confirm2FA;
    window.kzDoDisable = disable2FA;
    window.kzDoRegen = regenerateCodes;
    window.kzSubmit2FA = function () {
      var code = val('kz2facode');
      var errEl = document.getElementById('kz2faerr');
      if (!/^[A-Za-z0-9-]{6,12}$/.test(code)) {
        if (errEl) errEl.textContent = 'Ingresa el codigo de tu app.';
        return;
      }
      var callback = KZ.twoFACallback;
      KZ.twoFACallback = null;
      if (window.cs) window.cs();
      if (callback) {
        try { callback(code); } catch (e) {}
      }
    };

    var originalRs = window.rs;
    if (typeof originalRs === 'function') {
      window.rs = function () {
        originalRs();
        document.querySelectorAll('#sc2 .pill').forEach(function (pill) {
          if (pill.textContent.indexOf('Nivel') !== -1 || pill.textContent.indexOf('Pendiente') !== -1 || pill.textContent.indexOf('Verificado') !== -1 || pill.textContent.indexOf('Rechazado') !== -1) {
            var statusStr = KZ.kycStatus || 'not_started';
            var label = statusStr === 'verified' || statusStr === 'approved' ? 'Verificado' : (statusStr === 'pending' || statusStr === 'in_review' ? 'En revisión' : (statusStr === 'rejected' || statusStr === 'declined' ? 'Rechazado' : 'Pendiente'));
            var color = (statusStr === 'verified' || statusStr === 'approved') ? 'var(--gn)' : ((statusStr === 'rejected' || statusStr === 'declined') ? 'var(--rd)' : 'var(--am)');
            pill.textContent = label;
            pill.style.color = color;
          }
        });
        document.querySelectorAll('#sc2 .sx small').forEach(function (small) {
          if (small.textContent.indexOf('iniciar sesi') !== -1) {
            small.textContent = 'Codigo de 6 digitos para operaciones sensibles y retiros.';
          }
        });
      };
    }
  }

  /* ------------------------------------------------------------------ */
  /* Arranque                                                            */
  /* ------------------------------------------------------------------ */

  function initApp() {
    if (!getToken()) {
      goLanding();
      return;
    }

    installAppOverrides();
    wireNavigation();
    setProfile();
    wireLogout();

    var initial = sectionFromPath(window.location.pathname) || 'inicio';
    if (originalGo) {
      originalGo(initial);
      document.title = 'Korzadi · ' + (TITLES[initial] || initial);
      afterNavigate(initial);
    } else if (window.go) {
      window.go(initial);
    }

    loadBalances();
    loadTransactions();
    loadOrders();
    load2FAStatus();
    loadPreferences();
    loadKYCStatus();

    window.setInterval(tickOrder, 1000);
    window.setInterval(pollOrder, 8000);
  }

  ready(function () {
    unregisterOldServiceWorker();
    if (document.getElementById('app')) {
      initApp();
    } else if (document.getElementById('f')) {
      initLanding();
    }
  });
})();
