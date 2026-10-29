import { useCallback, useEffect, useRef, useState } from 'react';
import { Navigate, useNavigate } from 'react-router-dom';
import api, { clearTokens, hasSession, setTokens } from '../api/client';
import { getUser, showAsset } from '../lib/user';
import './landing.css';

const money = (v, d = 2) => Number(v || 0).toLocaleString('en-US', {
  minimumFractionDigits: d,
  maximumFractionDigits: d,
});
const qty = (v) => Number(v || 0).toLocaleString('en-US', { maximumFractionDigits: 8 });

const COINS = {
  USDT: ['#26A17B', '₮'], BTC: ['#F7931A', '₿'], ETH: ['#627EEA', 'Ξ'],
  BNB: ['#F3BA2F', 'B'], TRX: ['#EF0027', 'T'], USDC: ['#2775CA', '$'], USD: ['#1468FF', '$'],
};
const coin = (code) => {
  const c = String(code || '').toUpperCase();
  return COINS[c] || ['#1468FF', (c || '?')[0]];
};

const INITIAL_DATA = {
  total: 0,
  assets: [],
  ops: [],
  offers: [],
  demo: { total: false, ops: false, offers: false },
};

function KzMark({ size = 22 }) {
  return (
    <svg width={size} height={size} aria-hidden="true">
      <use href="#kz" />
    </svg>
  );
}

function CountUp({ value, duration = 1800 }) {
  const [current, setCurrent] = useState(0);
  useEffect(() => {
    let raf = 0;
    const target = Number(value) || 0;
    const start = performance.now();
    const tick = (now) => {
      const p = Math.min((now - start) / duration, 1);
      setCurrent(target * (1 - Math.pow(1 - p, 3)));
      if (p < 1) raf = requestAnimationFrame(tick);
    };
    raf = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(raf);
  }, [value, duration]);
  return <span className="cnt">{money(current)}</span>;
}

function Reveal({ children, delay = 0 }) {
  const ref = useRef(null);
  useEffect(() => {
    const el = ref.current;
    if (!el || typeof IntersectionObserver === 'undefined') return undefined;
    const io = new IntersectionObserver((entries) => {
      entries.forEach((entry) => {
        if (entry.isIntersecting) {
          el.classList.add('in');
          io.unobserve(el);
        }
      });
    }, { threshold: 0.15 });
    io.observe(el);
    return () => io.disconnect();
  }, []);
  return (
    <div ref={ref} className="rv" style={delay ? { transitionDelay: `${delay}s` } : undefined}>
      {children}
    </div>
  );
}

function useLandingData() {
  const [data, setData] = useState(INITIAL_DATA);
  const [loadError, setLoadError] = useState('');

  useEffect(() => {
    let alive = true;

    async function load() {
      if (!hasSession()) return;

      const next = {
        ...INITIAL_DATA,
        demo: { total: true, ops: true, offers: true },
      };

      try {
        const { data: body } = await api.get('/api/wallet/balances');
        const balances = body?.balances || [];
        next.total = Number(body?.total_usdt || 0);
        next.assets = balances.map((a) => {
          const [color, glyph] = coin(a.asset);
          return {
            code: showAsset(a.asset),
            name: showAsset(a.asset),
            value: `$${money(a.value_usdt)}`,
            amount: `${qty(a.balance)} ${showAsset(a.asset)}`,
            color,
            glyph,
          };
        });
        next.demo.total = false;
      } catch (e) {
        setLoadError('No se pudo cargar los datos de balance.');
        // Sin datos simulados; se mantiene vacío.
      }

      try {
        const { data: body } = await api.get('/api/wallet/transactions');
        const list = body?.transactions || [];
        next.ops = list.slice(0, 3).map((e) => {
          const [color, glyph] = coin(e.asset);
          const n = Number(e.amount || 0);
          const when = e.created_at
            ? new Date(e.created_at).toLocaleDateString('es', { day: '2-digit', month: 'short' })
            : '';
          return {
            title: `${showAsset(e.asset)} · ${n < 0 ? 'Envío' : 'Recepción'}`,
            when,
            amount: `${n > 0 ? '+' : ''}${qty(n)} ${showAsset(e.asset)}`,
            color,
            glyph,
            sign: n < 0 ? '#F87171' : '#34D399',
          };
        });

      } catch (e) {
        setLoadError('No se pudo cargar las transacciones recientes.');
        // No hay datos simulados; se mantiene vacío.
      }

      try {
        const { data: body } = await api.get('/api/p2p/offers');
        const list = Array.isArray(body) ? body : [];
        next.offers = list.slice(0, 3).map((o) => {
          const [color, glyph] = coin(o.asset_code);
          return {
            trader: `${o.asset_code} · ${o.side === 'sell' ? 'Venta' : 'Compra'}`,
            rate: `${qty(o.price_per_unit)} ${o.fiat_currency} / ${o.asset_code}`,
            limit: `Límite: ${qty(o.min_amount)} – ${qty(o.max_amount)} ${o.fiat_currency}`,
            color,
            glyph,
          };
        });

      } catch (e) {
        setLoadError('No se pudo cargar las ofertas P2P.');
        // Sin datos simulados; se mantiene vacío.
      }

      if (alive) setData(next);
    }

    load();
    return () => { alive = false; };
  }, []);

  return { data, loadError };
}

export default function Landing() {
  const navigate = useNavigate();
  const { data, loadError } = useLandingData();
  const user = getUser();
  const userName = user.name || 'Alexander';
  const [authenticatedAtMount] = useState(() => hasSession());

  const [modalOpen, setModalOpen] = useState(false);
  const [mode, setMode] = useState(1);
  const [step, setStep] = useState('s1');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [showPassword, setShowPassword] = useState(false);
  const [firstName, setFirstName] = useState('');
  const [lastName, setLastName] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [mfaError, setMfaError] = useState('');
  const [mfaBusy, setMfaBusy] = useState(false);
  const [successMsg, setSuccessMsg] = useState('');
  const [requiresLogin, setRequiresLogin] = useState(false);
  const [code, setCode] = useState(['', '', '', '', '', '']);
  const [challengeToken, setChallengeToken] = useState('');
  const [useRecovery, setUseRecovery] = useState(false);
  const [recoveryCode, setRecoveryCode] = useState('');
  const [pulseIdx, setPulseIdx] = useState(null);

  const emailRef = useRef(null);
  const codeRefs = useRef([]);
  const lastFocus = useRef(null);
  const flowId = useRef(0);
  const loginInFlight = useRef(false);
  const verifyInFlight = useRef(false);

  useEffect(() => {
    if (!data.ops.length) return undefined;
    const timer = setInterval(() => {
      setPulseIdx(Math.floor(Math.random() * data.ops.length));
    }, 2500);
    return () => clearInterval(timer);
  }, [data.ops.length]);

  function clearChallenge() {
    flowId.current += 1;
    setChallengeToken('');
    setCode(['', '', '', '', '', '']);
    setRecoveryCode('');
    setUseRecovery(false);
    setMfaError('');
  }

  const closeModal = useCallback(() => {
    flowId.current += 1;
    setChallengeToken('');
    setCode(['', '', '', '', '', '']);
    setRecoveryCode('');
    setUseRecovery(false);
    setMfaError('');
    setModalOpen(false);
    document.body.style.overflow = '';
    if (lastFocus.current && typeof lastFocus.current.focus === 'function') {
      lastFocus.current.focus();
    }
  }, []);

  useEffect(() => {
    const onKey = (e) => { if (e.key === 'Escape') closeModal(); };
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('keydown', onKey);
      document.body.style.overflow = '';
    };
  }, [closeModal]);

  function scrollToId(hash) {
    if (!hash || hash === '#') {
      window.scrollTo({ top: 0, behavior: 'smooth' });
      return;
    }
    const el = document.getElementById(hash.slice(1));
    if (el) el.scrollIntoView({ behavior: 'smooth' });
  }

  function openModal(nextMode) {
    clearChallenge();
    lastFocus.current = document.activeElement;
    setMode(nextMode);
    setStep('s1');
    setError('');
    setSuccessMsg('');
    setModalOpen(true);
    document.body.style.overflow = 'hidden';
    setTimeout(() => emailRef.current?.focus(), 60);
  }

  function switchTab(nextMode) {
    clearChallenge();
    setMode(nextMode);
    setStep('s1');
    setError('');
    setSuccessMsg('');
  }

  function persistSession(body) {
    const accessToken = body?.access_token || body?.accessToken || body?.token || '';
    if (!accessToken) {
      throw new Error('El servidor no devolvió una sesión válida.');
    }
    clearTokens();
    localStorage.setItem('korzadi_user', JSON.stringify(body?.user || {}));
    setTokens(accessToken);
  }

  async function handleSubmit(e) {
    e.preventDefault();
    if (loginInFlight.current) return;
    setError('');
    const mail = email.trim();

    if (mode === 1) {
      if (!/^\S+@\S+\.\S+$/.test(mail)) { setError('Ingresa un correo válido.'); return; }
      if (!password) { setError('Ingresa tu contraseña.'); return; }
    } else {
      if (firstName.trim().length < 2) { setError('Escribe tu nombre.'); return; }
      if (lastName.trim().length < 2) { setError('Escribe tu apellido.'); return; }
      if (!/^\S+@\S+\.\S+$/.test(mail)) { setError('Ingresa un correo válido.'); return; }
      if (password.length < 10) { setError('La contraseña debe tener al menos 10 caracteres.'); return; }
      if (password !== confirmPassword) { setError('Las contraseñas no coinciden.'); return; }
    }

    loginInFlight.current = true;
    setBusy(true);
    const flow = ++flowId.current;
    try {
      if (mode === 1) {
        const { data: body } = await api.post('/api/auth/login', { email: mail, password });
        if (flow !== flowId.current) return;

        if (body?.two_factor_required) {
          if (!body.challenge_token) {
            throw new Error('El servidor no inició la verificación 2FA.');
          }
          // No se guarda ninguna sesión ni token antes del segundo factor.
          clearTokens();
          setChallengeToken(body.challenge_token);
          setCode(['', '', '', '', '', '']);
          setRecoveryCode('');
          setUseRecovery(false);
          setMfaError('');
          setStep('s2');
          setTimeout(() => codeRefs.current[0]?.focus(), 60);
        } else {
          persistSession(body);
          setRequiresLogin(false);
          setChallengeToken('');
          setSuccessMsg('Sesión verificada correctamente.');
          setStep('s3');
        }
      } else {
        const { data: body } = await api.post('/api/auth/register', {
          first_name: firstName.trim(),
          last_name: lastName.trim(),
          email: mail,
          password,
        });
        if (flow !== flowId.current) return;
        if (body?.access_token) {
          persistSession(body);
          setSuccessMsg('Tu cuenta fue creada correctamente.');
        } else {
          setRequiresLogin(true);
          setSuccessMsg('Tu cuenta fue creada. Revisa tu correo y verifica la cuenta antes de iniciar sesión.');
        }
        setStep('s3');
      }
    } catch (err) {
      if (flow === flowId.current) {
        const detail = err?.response?.data;
        setError(detail?.message || detail?.error || err?.message || 'No se pudo completar la operación.');
      }
    } finally {
      loginInFlight.current = false;
      setBusy(false);
    }
  }

  async function handleVerify() {
    if (verifyInFlight.current || !challengeToken) return;
    setMfaError('');
    const value = useRecovery ? recoveryCode.trim() : code.join('');
    if (!value) {
      setMfaError(useRecovery ? 'Ingresa tu código de recuperación.' : 'Ingresa los 6 dígitos.');
      return;
    }
    if (!useRecovery && value.length !== 6) { setMfaError('Ingresa los 6 dígitos.'); return; }

    verifyInFlight.current = true;
    setMfaBusy(true);
    const flow = flowId.current;
    try {
      const { data: body } = await api.post('/api/auth/login/2fa', {
        challenge_token: challengeToken,
        code: value,
      });
      if (flow !== flowId.current) return;
      persistSession(body);
      setRequiresLogin(false);
      setChallengeToken('');
      setSuccessMsg('Sesión verificada correctamente.');
      setStep('s3');
    } catch (err) {
      if (flow !== flowId.current) return;
      const detail = err?.response?.data || {};
      if (detail.error === '2fa_locked') {
        setMfaError(`Demasiados intentos. Intenta de nuevo en ${detail.retry_after_seconds || 60}s.`);
      } else if (detail.error === 'invalid_2fa_code') {
        setMfaError('El código 2FA es incorrecto.');
      } else if (detail.error === 'invalid_2fa_challenge' || detail.error === '2fa_challenge_expired' || detail.error === '2fa_required') {
        clearChallenge();
        setStep('s1');
        setError('Tu verificación 2FA expiró o ya fue utilizada. Inicia sesión de nuevo.');
      } else if (detail.error === 'account_locked') {
        setMfaError('La cuenta está bloqueada temporalmente.');
      } else if (!err?.response) {
        setMfaError('No se pudo conectar con el servidor. Revisa tu conexión.');
      } else {
        setMfaError(detail.message || detail.error || 'No se pudo verificar el código.');
      }
      if (!useRecovery) {
        setCode(['', '', '', '', '', '']);
        codeRefs.current[0]?.focus();
      }
    } finally {
      verifyInFlight.current = false;
      setMfaBusy(false);
    }
  }

  function cancelTwoFactor() {
    clearChallenge();
    setStep('s1');
    setError('');
  }

  function setCodeDigit(index, raw) {
    const digits = String(raw || '').replace(/\D/g, '');
    setCode((prev) => {
      const next = [...prev];
      next[index] = digits.slice(-1);
      return next;
    });
    if (digits && index < 5) setTimeout(() => codeRefs.current[index + 1]?.focus(), 0);
  }

  function onCodeKeyDown(index, e) {
    if (e.key === 'Backspace' && !code[index] && index > 0) {
      e.preventDefault();
      setCode((prev) => {
        const next = [...prev];
        next[index - 1] = '';
        return next;
      });
      codeRefs.current[index - 1]?.focus();
    }
    if (e.key === 'Enter') handleVerify();
  }

  function onCodePaste(index, e) {
    const text = (e.clipboardData?.getData('text') || '').replace(/\D/g, '').slice(0, 6);
    if (!text) return;
    e.preventDefault();
    const next = ['', '', '', '', '', ''];
    text.split('').forEach((digit, i) => { next[i] = digit; });
    setCode(next);
    codeRefs.current[Math.min(text.length, 5)]?.focus();
  }

  function goToWallet() {
    closeModal();
    navigate(requiresLogin ? '/login' : '/dashboard');
  }

  function openOffer() {
    if (hasSession()) navigate('/p2p');
    else openModal(1);
  }

  if (authenticatedAtMount) return <Navigate to="/dashboard" replace />;

  return (
    <div className="landing">
      <svg width="0" height="0" style={{ position: 'absolute' }} aria-hidden="true">
        <defs>
          <linearGradient id="kg" x1="0" y1="0" x2="1" y2="1">
            <stop offset="0" stopColor="#3FD0FF" />
            <stop offset="1" stopColor="#1F4DFF" />
          </linearGradient>
          <symbol id="kz" viewBox="0 0 44 44">
            <path fill="url(#kg)" d="M2 2h9v40H2zM14 26L31 2h12L22 30zM17 25l26 17H30L14 34z" />
          </symbol>
        </defs>
      </svg>

      <nav>
        <a className="logo" href="#" onClick={(e) => { e.preventDefault(); scrollToId('#'); }}>
          <svg width="38" height="38" aria-hidden="true"><use href="#kz" /></svg>Korzadi
        </a>
        <div className="links">
          <a className="on" href="#" onClick={(e) => { e.preventDefault(); scrollToId('#'); }}>Inicio</a>
          <a href="#feat" onClick={(e) => { e.preventDefault(); scrollToId('#feat'); }}>Wallet</a>
          <a href="#p2p" onClick={(e) => { e.preventDefault(); scrollToId('#p2p'); }}>P2P</a>
          <a href="#feat" onClick={(e) => { e.preventDefault(); scrollToId('#feat'); }}>Historial</a>
          <a href="#seg" onClick={(e) => { e.preventDefault(); scrollToId('#seg'); }}>Soporte</a>
        </div>
        <div className="nr">
          <button className="bt" type="button" onClick={() => openModal(1)}>Iniciar sesión</button>
          <button className="bt f" type="button" onClick={() => openModal(2)}>Crear cuenta</button>
        </div>
      </nav>

      <section className="hero">
        <div className="orb" style={{ width: 340, height: 340, right: -60, top: 20 }} />
        <div className="orb" style={{ width: 220, height: 220, left: '30%', bottom: -80, animationDelay: '-3s' }} />

        <div style={{ position: 'relative', zIndex: 3 }}>
          <div className="pill u">
            <svg className="ic" style={{ width: 18, height: 18 }} viewBox="0 0 24 24"><path d="M12 3l8 3v6c0 5-3.5 8-8 9-4.5-1-8-4-8-9V6z" /></svg>
            Wallet Custodial + P2P
          </div>
          <h1 className="u" style={{ animationDelay: '.1s' }}>
            <svg width="72" height="72" aria-hidden="true"><use href="#kz" /></svg>Korzadi
          </h1>
          <h2 className="u" style={{ animationDelay: '.2s' }}>Tu dinero, en <span className="gr">tus manos.</span></h2>
          <p className="sub u" style={{ animationDelay: '.3s' }}>
            Compra, vende, envía y recibe criptomonedas de forma rápida, segura y sencilla.
          </p>
          <div className="tri u" style={{ animationDelay: '.4s' }}>
            <div>
              <svg className="ic" viewBox="0 0 24 24"><path d="M12 3l8 3v6c0 5-3.5 8-8 9-4.5-1-8-4-8-9V6zM9 12l2 2 4-4" /></svg>
              <span><b>Custodia Segura</b>Tus fondos protegidos con tecnología de nivel bancario.</span>
            </div>
            <div>
              <svg className="ic" viewBox="0 0 24 24"><path d="M13 2L4 14h7l-1 8 9-12h-7z" /></svg>
              <span><b>P2P Rápido</b>Compra y vende entre usuarios, con múltiples métodos de pago.</span>
            </div>
            <div>
              <svg className="ic" viewBox="0 0 24 24"><path d="M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18zM3 12h18M12 3c3 3 3 15 0 18M12 3c-3 3-3 15 0 18" /></svg>
              <span><b>Global</b>Opera en múltiples redes y países desde un solo lugar.</span>
            </div>
          </div>
          <div className="cta u" style={{ animationDelay: '.5s' }}>
            <button className="bt f" type="button" onClick={() => openModal(2)}>Comenzar ahora →</button>
            <button className="bt" type="button" onClick={() => scrollToId('#p2p')}>▶ Ver cómo funciona</button>
          </div>
        </div>

        <div className="stage u" style={{ animationDelay: '.3s' }}>
          <div className="lap">
            <div className="sd">
              <div className="on">Inicio</div><div>Wallet</div><div>P2P</div>
              <div>Historial</div><div>Seguridad</div><div>Configuración</div>
            </div>
            <div>
              <b style={{ fontSize: 15 }}>Hola, {userName}</b>
              <div style={{ color: 'var(--mut)' }}>Bienvenido a Korzadi</div>
              <div style={{ margin: '8px 0', display: 'flex', justifyContent: 'space-between', alignItems: 'end' }}>
                <div>
                  Saldo total (USD)
                  {data.demo.total && <span className="demo-badge">Ejemplo</span>}
                  <div className="bal">$<CountUp value={data.total} /></div>
                  <div className="up">{data.demo.total ? '↑ +2.45% (24h)' : 'Actualizado'}</div>
                </div>
                <svg width="150" height="46" viewBox="0 0 150 46">
                  <polyline className="dr" pathLength="1" points="0,38 22,30 42,34 62,22 84,26 106,12 130,18 150,4" fill="none" stroke="#33B5FF" strokeWidth="2" />
                </svg>
              </div>
              <div className="pb"><span>Depositar</span><span>Enviar</span><span>Recibir</span></div>
              <b style={{ display: 'block', marginTop: 10 }}>
                Operaciones recientes
                {data.demo.ops && <span className="demo-badge">Ejemplo</span>}
              </b>
              <div id="ops">
                {data.ops.map((op, i) => (
                  <div className={`row ${pulseIdx === i ? 'pulse' : ''}`} key={`${op.title}-${i}`}>
                    <div className="co" style={{ background: op.color }}>{op.glyph}</div>
                    <div><b>{op.title}</b><small>{op.when}</small></div>
                    <div className="r" style={{ color: op.sign }}><b>{op.amount}</b></div>
                  </div>
                ))}
                {!data.ops.length && (
                  <div style={{ color: 'var(--mut)', fontSize: 10, padding: '10px 0' }}>Sin operaciones todavía.</div>
                )}
              </div>
            </div>
          </div>

          <div className="ph">
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
              <span style={{ display: 'flex', gap: 6, alignItems: 'center', fontWeight: 700, fontSize: 14 }}>
                <KzMark size={22} />Korzadi
              </span>
              <span style={{ color: 'var(--mut)' }}>9:41</span>
            </div>
            <div style={{ marginTop: 12, background: '#0B1B4A', borderRadius: 12, padding: 10 }}>
              <span style={{ color: 'var(--mut)' }}>
                Saldo total (USD)
                {data.demo.total && <span className="demo-badge">Ejemplo</span>}
              </span>
              <div className="bal">$<CountUp value={data.total} /></div>
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'end' }}>
                <span className="up">{data.demo.total ? '↑ +2.45% (24h)' : 'Actualizado'}</span>
                <svg width="90" height="34" viewBox="0 0 90 34">
                  <polyline className="dr" pathLength="1" points="0,28 14,22 28,26 42,14 58,18 72,8 90,3" fill="none" stroke="#33B5FF" strokeWidth="2" />
                </svg>
              </div>
            </div>
            <div className="acts">
              <div><i />Depositar</div><div><i />Enviar</div><div><i />Recibir</div><div><i />P2P</div>
            </div>
            <b>
              Mis activos
              {data.demo.total && <span className="demo-badge">Ejemplo</span>}
            </b>
            <div id="as">
              {data.assets.map((a) => (
                <div className="row" key={`${a.code}-${a.amount}`}>
                  <div className="co" style={{ background: a.color }}>{a.glyph}</div>
                  <div><b>{a.code}</b><small>{a.name}</small></div>
                  <div className="r"><b>{a.value}</b><small>{a.amount}</small></div>
                </div>
              ))}
              {!data.assets.length && (
                <div style={{ color: 'var(--mut)', fontSize: 10, padding: '10px 0' }}>Sin activos todavía.</div>
              )}
            </div>
          </div>
        </div>
      </section>

      <section className="strip" id="feat">
        {[
          ['Custodia Segura', 'Fondos protegidos con tecnología de nivel bancario, multifirma y monitoreo 24/7.', 'M12 3l8 3v6c0 5-3.5 8-8 9-4.5-1-8-4-8-9V6zM9 12l2 2 4-4'],
          ['P2P Rápido y Confiable', 'Compra y vende cripto directo entre usuarios, con múltiples métodos de pago.', 'M16 20v-2a4 4 0 0 0-4-4H7a4 4 0 0 0-4 4v2M9.5 10a3.5 3.5 0 1 0 0-7 3.5 3.5 0 0 0 0 7M21 20v-2a4 4 0 0 0-3-3.9M16 3.1a3.5 3.5 0 0 1 0 6.8'],
          ['Transacciones Instantáneas', 'Envía y recibe cripto en minutos, sin complicaciones.', 'M13 2L4 14h7l-1 8 9-12h-7z'],
          ['Múltiples Criptomonedas', 'Soporta las principales redes y activos digitales.', 'M12 5c4.4 0 8 1 8 2.5S16.4 10 12 10 4 9 4 7.5 7.6 5 12 5zM4 7.5v4C4 13 7.6 14 12 14s8-1 8-2.5v-4M4 11.5v4C4 17 7.6 18 12 18s8-1 8-2.5v-4'],
          ['Disponible en Todo Momento', 'App móvil y versión web. Tu wallet, donde la necesites.', 'M8 2h8a2 2 0 0 1 2 2v16a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2zM11 18h2'],
          ['Soporte 24/7', 'Un equipo siempre listo para ayudarte.', 'M4 14v-2a8 8 0 0 1 16 0v2M4 14h3v5H5a1 1 0 0 1-1-1zM20 14h-3v5h2a1 1 0 0 0 1-1z'],
        ].map(([title, text, path], i) => (
          <Reveal key={title} delay={i * 0.1}>
            <svg className="ic" viewBox="0 0 24 24"><path d={path} /></svg>
            <b>{title}</b>{text}
          </Reveal>
        ))}
      </section>

      <section className="lt" id="p2p">
        <Reveal>
          <h3>P2P sin límites</h3>
          <p style={{ fontSize: 17 }}>Conecta con miles de usuarios, elige el mejor precio y paga como prefieras.</p>
          <ul className="ck">
            <li><i>✓</i>Compra y vende USDT, BTC, ETH y más</li>
            <li><i>✓</i>Múltiples métodos de pago (banco, tarjeta, efectivo, etc.)</li>
            <li><i>✓</i>Sistema de escrow para mayor seguridad</li>
            <li><i>✓</i>Calificaciones y reputación de usuarios</li>
          </ul>
          <button className="bt f" type="button" style={{ minHeight: 48, borderRadius: 30, padding: '0 30px' }} onClick={() => openModal(1)}>
            Ir al P2P →
          </button>
        </Reveal>

        <Reveal>
          <div className="duo">
            <div className="ph a">
              <b>
                P2P
                {data.demo.offers && <span className="demo-badge">Ejemplo</span>}
              </b>
              <div className="pb"><span>Comprar</span><span style={{ background: '#12307f' }}>Vender</span></div>
              <div id="of">
                {data.offers.map((offer, i) => (
                  <div className="of" key={`${offer.trader}-${i}`}>
                    <div className="co" style={{ background: offer.color }}>{offer.glyph}</div>
                    <div><b>{offer.trader}</b><small>{offer.rate}</small><small>{offer.limit}</small></div>
                    <button type="button" onClick={openOffer}>Comprar</button>
                  </div>
                ))}
                {!data.offers.length && (
                  <div style={{ color: 'var(--mut)', fontSize: 10, padding: 8 }}>No hay ofertas por ahora.</div>
                )}
              </div>
            </div>
            <div className="ph b" style={{ textAlign: 'center' }}>
              <div className="ok">
                <svg width="40" height="40" viewBox="0 0 24 24">
                  <path pathLength="1" d="M5 12.5l4.5 4.5L19 7.5" fill="none" stroke="#fff" strokeWidth="2.6" strokeLinecap="round" strokeLinejoin="round" />
                </svg>
              </div>
              <b style={{ fontSize: 15 }}>¡Compra exitosa!</b>
              <div style={{ color: 'var(--mut)', margin: '6px 0 18px' }}>Has recibido 200.00 USDT</div>
              <div className="pb"><span style={{ padding: 10 }}>Ver detalle</span></div>
              <div style={{ color: '#5C9BFF', marginTop: 12 }}>Volver al inicio</div>
            </div>
          </div>
        </Reveal>

        <Reveal>
          <div id="seg">
            <div className="logo" style={{ color: 'var(--lk)', fontSize: 44 }}>
              <svg width="52" height="52" aria-hidden="true"><use href="#kz" /></svg>Korzadi
            </div>
            <h4>Seguridad y confianza en cada operación</h4>
            <p>Somos una plataforma custodial, donde tus fondos están protegidos y siempre bajo tu control. Combinamos la libertad del P2P con la seguridad de una wallet profesional.</p>
            <div className="bd">
              <div>
                <svg className="ic" style={{ width: 26, height: 26 }} viewBox="0 0 24 24"><path d="M12 3l8 3v6c0 5-3.5 8-8 9-4.5-1-8-4-8-9V6zM9 12l2 2 4-4" /></svg>
                KYC/AML<br />Verificado
              </div>
              <div>
                <svg className="ic" style={{ width: 26, height: 26 }} viewBox="0 0 24 24"><path d="M6 11h12v9H6zM8 11V8a4 4 0 0 1 8 0v3" /></svg>
                Cifrado de extremo<br />a extremo
              </div>
              <div>
                <svg className="ic" style={{ width: 26, height: 26 }} viewBox="0 0 24 24"><path d="M4 14v-2a8 8 0 0 1 16 0v2M4 14h3v5H5a1 1 0 0 1-1-1zM20 14h-3v5h2a1 1 0 0 0 1-1z" /></svg>
                Soporte 24/7<br />en español
              </div>
            </div>
          </div>
        </Reveal>
      </section>

      <footer>
        {loadError && (
          <div style={{ color: '#F87171', fontSize: 12, marginBottom: 12, textAlign: 'center' }}>
            {loadError}
          </div>
        )}
        <b style={{ display: 'flex', gap: 6, alignItems: 'center', color: 'var(--lk)' }}>
          <svg width="22" height="22" aria-hidden="true"><use href="#kz" /></svg>Korzadi
        </b>
        Más que una wallet, es tu puente al mundo cripto.
      </footer>

      <div className={`md ${modalOpen ? 'on' : ''}`} id="md" role="dialog" aria-modal="true" aria-labelledby="mt">
        <div className="mc">
          <button className="x" type="button" onClick={closeModal} aria-label="Cerrar">✕</button>
          <div className="logo" style={{ fontSize: 22 }}>
            <svg width="30" height="30" aria-hidden="true"><use href="#kz" /></svg>Korzadi
          </div>

          {step === 's1' && (
            <div id="s1">
              <div className="tb">
                <button id="t1" type="button" className={mode === 1 ? 'on' : ''} onClick={() => switchTab(1)}>Iniciar sesión</button>
                <button id="t2" type="button" className={mode === 2 ? 'on' : ''} onClick={() => switchTab(2)}>Crear cuenta</button>
              </div>
              <h3 id="mt" style={{ fontSize: 20 }}>{mode === 1 ? 'Bienvenido de nuevo' : 'Crea tu cuenta'}</h3>

              <form id="f" onSubmit={handleSubmit} noValidate>
                {mode === 2 && (
                  <div id="nm">
                    <label className="fl" htmlFor="n">Nombre</label>
                    <input className="ip" id="n" autoComplete="given-name" value={firstName}
                      onChange={(e) => setFirstName(e.target.value)} />
                    <label className="fl" htmlFor="n2">Apellido</label>
                    <input className="ip" id="n2" autoComplete="family-name" value={lastName}
                      onChange={(e) => setLastName(e.target.value)} />
                  </div>
                )}

                <label className="fl" htmlFor="e">Correo electrónico</label>
                <input className="ip" id="e" type="email" autoComplete="email" placeholder="tu@correo.com"
                  ref={emailRef} value={email} onChange={(e) => setEmail(e.target.value)} />

                <label className="fl" htmlFor="p">Contraseña</label>
                <div style={{ position: 'relative' }}>
                  <input className="ip" id="p" type={showPassword ? 'text' : 'password'}
                    autoComplete={mode === 1 ? 'current-password' : 'new-password'}
                    value={password} onChange={(e) => setPassword(e.target.value)} />
                  <button type="button" id="vw" aria-label="Mostrar contraseña"
                    onClick={() => setShowPassword((v) => !v)}
                    style={{ position: 'absolute', right: 0, top: 0, minWidth: 56, height: 46, background: 'none', border: 0, color: '#33B5FF', fontSize: 13, fontWeight: 600 }}>
                    {showPassword ? 'Ocultar' : 'Ver'}
                  </button>
                </div>

                {mode === 2 && (
                  <div id="cf">
                    <label className="fl" htmlFor="p2">Confirmar contraseña</label>
                    <input className="ip" id="p2" type="password" autoComplete="new-password"
                      value={confirmPassword} onChange={(e) => setConfirmPassword(e.target.value)} />
                  </div>
                )}

                <div className="er" id="er" role="alert">{error}</div>
                <button className="bt f" id="sb" type="submit" disabled={busy}
                  style={{ width: '100%', minHeight: 48, marginTop: 6 }}>
                  {busy ? 'Procesando…' : (mode === 1 ? 'Iniciar sesión' : 'Crear cuenta')}
                </button>
              </form>
            </div>
          )}

          {step === 's2' && (
            <div id="s2">
              <h3 style={{ fontSize: 20, marginTop: 20 }}>Verificación en 2 pasos</h3>
              <p style={{ color: '#A9B8DA', fontSize: 14, marginTop: 8, lineHeight: 1.5 }}>
                {useRecovery
                  ? 'Introduce uno de tus códigos de recuperación.'
                  : 'Ingresa el código de 6 dígitos de tu app de autenticación.'}
              </p>
              {useRecovery ? (
                <input className="ip" type="text" autoComplete="one-time-code" maxLength={19}
                  aria-label="Código de recuperación"
                  value={recoveryCode}
                  onChange={(e) => { setRecoveryCode(e.target.value.toUpperCase()); setMfaError(''); }}
                  onKeyDown={(e) => { if (e.key === 'Enter') handleVerify(); }} />
              ) : (
                <div className="cd" id="cd">
                  {code.map((digit, i) => (
                    <input key={i} className="ip" maxLength={1} inputMode="numeric" aria-label={`Dígito ${i + 1}`}
                      ref={(el) => { codeRefs.current[i] = el; }}
                      value={digit}
                      onChange={(e) => setCodeDigit(i, e.target.value)}
                      onKeyDown={(e) => onCodeKeyDown(i, e)}
                      onPaste={(e) => onCodePaste(i, e)} />
                  ))}
                </div>
              )}
              <div className="er" id="er2" role="alert" style={{ textAlign: 'center' }}>{mfaError}</div>
              <button className="bt f" type="button" onClick={handleVerify} disabled={mfaBusy}
                style={{ width: '100%', minHeight: 48, marginTop: 6 }}>
                {mfaBusy ? 'Verificando…' : 'Verificar'}
              </button>
              <button type="button" onClick={() => { setUseRecovery((v) => !v); setMfaError(''); setCode(['', '', '', '', '', '']); setRecoveryCode(''); }}
                style={{ width: '100%', marginTop: 10, background: 'none', border: 0, color: '#33B5FF', cursor: 'pointer', minHeight: 40 }}>
                {useRecovery ? 'Usar código de la app' : 'Usar un código de recuperación'}
              </button>
              <button type="button" onClick={cancelTwoFactor}
                style={{ width: '100%', marginTop: 2, background: 'none', border: 0, color: '#A9B8DA', cursor: 'pointer', minHeight: 40 }}>
                Volver
              </button>
            </div>
          )}

          {step === 's3' && (
            <div id="s3" style={{ textAlign: 'center' }}>
              <div className="ok" style={{ marginTop: 34 }}>
                <svg width="40" height="40" viewBox="0 0 24 24" aria-hidden="true">
                  <path pathLength="1" d="M5 12.5l4.5 4.5L19 7.5" fill="none" stroke="#fff" strokeWidth="2.6" strokeLinecap="round" strokeLinejoin="round" />
                </svg>
              </div>
              <h3 style={{ fontSize: 20 }}>¡Listo!</h3>
              <p id="ms" style={{ color: '#A9B8DA', margin: '8px 0 18px', fontSize: 14, lineHeight: 1.5 }}>{successMsg}</p>
              <button className="bt f" type="button" onClick={goToWallet} style={{ width: '100%', minHeight: 48 }}>
                {requiresLogin ? 'Iniciar sesión' : 'Ir a mi wallet'}
              </button>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
