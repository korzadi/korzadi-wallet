import { useRef, useState } from 'react';
import { useNavigate, Link } from 'react-router-dom';
import { Wallet, AlertCircle, ArrowLeft, KeyRound } from 'lucide-react';
import api, { clearTokens, setTokens } from '../api/client';

export default function Login() {
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  const navigate = useNavigate();

  const [step, setStep] = useState('credentials');
  const [challengeToken, setChallengeToken] = useState('');
  const [code, setCode] = useState('');
  const [useRecovery, setUseRecovery] = useState(false);
  const [twoFaError, setTwoFaError] = useState('');
  const [verifying, setVerifying] = useState(false);

  const loginInFlight = useRef(false);
  const verifyInFlight = useRef(false);
  const flowId = useRef(0);

  function resetChallenge(nextStep = 'credentials') {
    flowId.current += 1;
    setChallengeToken('');
    setCode('');
    setUseRecovery(false);
    setTwoFaError('');
    setStep(nextStep);
  }

  function persistSession(data) {
    if (!data?.access_token) {
      throw new Error('El servidor no devolvió una sesión válida.');
    }
    clearTokens();
    localStorage.setItem('korzadi_user', JSON.stringify(data.user || {}));
    setTokens(data.access_token, data.refresh_token || '');
  }

  async function handleSubmit(e) {
    e.preventDefault();
    if (loginInFlight.current) return;
    setError('');
    loginInFlight.current = true;
    setLoading(true);
    const flow = ++flowId.current;
    try {
      const { data } = await api.post('/api/auth/login', { email, password });
      if (flow !== flowId.current) return;

      if (data?.two_factor_required) {
        if (!data.challenge_token) {
          throw new Error('El servidor no inició la verificación 2FA. Inténtalo de nuevo.');
        }
        // Nunca debe quedar una sesión previa mientras el 2FA está pendiente.
        clearTokens();
        setChallengeToken(data.challenge_token);
        setStep('2fa');
        return;
      }

      persistSession(data);
      setChallengeToken('');
      navigate('/dashboard');
    } catch (err) {
      if (flow === flowId.current) {
        setError(err.response?.data?.message || err.response?.data?.error || 'No se pudo iniciar sesión');
      }
    } finally {
      loginInFlight.current = false;
      setLoading(false);
    }
  }

  async function handleVerify(e) {
    e.preventDefault();
    if (verifyInFlight.current || !challengeToken) return;
    const value = useRecovery ? code.trim() : code.replace(/\D/g, '');
    if (!value) {
      setTwoFaError(useRecovery ? 'Ingresa tu código de recuperación.' : 'Ingresa los 6 dígitos.');
      return;
    }
    if (!useRecovery && value.length !== 6) {
      setTwoFaError('Ingresa los 6 dígitos.');
      return;
    }

    setTwoFaError('');
    verifyInFlight.current = true;
    setVerifying(true);
    const flow = flowId.current;
    try {
      const { data } = await api.post('/api/auth/login/2fa', {
        challenge_token: challengeToken,
        code: value,
      });

      // Una respuesta tardía tras cancelar/volver no debe crear la sesión.
      if (flow !== flowId.current) return;
      persistSession(data);
      setChallengeToken('');
      setCode('');
      navigate('/dashboard');
    } catch (err) {
      if (flow !== flowId.current) return;
      const detail = err.response?.data || {};
      if (detail.error === '2fa_locked') {
        setTwoFaError(`Demasiados intentos. Inténtalo de nuevo en ${detail.retry_after_seconds || 60}s.`);
      } else if (detail.error === 'invalid_2fa_code') {
        setTwoFaError('El código 2FA no es correcto.');
        setCode('');
      } else if (detail.error === 'invalid_2fa_challenge' || detail.error === '2fa_challenge_expired' || detail.error === '2fa_required') {
        setError('Tu verificación 2FA expiró o ya fue utilizada. Inicia sesión de nuevo.');
        resetChallenge('credentials');
      } else if (detail.error === 'account_locked') {
        setTwoFaError('La cuenta está bloqueada temporalmente.');
      } else if (!err.response) {
        setTwoFaError('No se pudo conectar con el servidor. Revisa tu conexión.');
      } else {
        setTwoFaError(detail.message || detail.error || 'No se pudo verificar el código.');
      }
    } finally {
      verifyInFlight.current = false;
      setVerifying(false);
    }
  }

  function cancelTwoFactor() {
    resetChallenge('credentials');
    setError('');
  }

  if (step === '2fa') {
    return (
      <div className="auth-screen">
        <div className="auth-card">
          <div className="brand-mark">K</div>
          <h1>Verificación en 2 pasos</h1>
          <p className="subtitle">
            {useRecovery
              ? 'Introduce uno de tus códigos de recuperación.'
              : 'Ingresa el código de 6 dígitos de tu app de autenticación.'}
          </p>
          <form onSubmit={handleVerify}>
            <div className="field">
              <label>{useRecovery ? 'Código de recuperación' : 'Código 2FA'}</label>
              <input
                type="text"
                value={code}
                onChange={(e) => {
                  setCode(useRecovery ? e.target.value.toUpperCase() : e.target.value.replace(/\D/g, ''));
                  setTwoFaError('');
                }}
                placeholder={useRecovery ? 'XXXX-XXXX-XXXX-XXXX' : '000000'}
                inputMode={useRecovery ? 'text' : 'numeric'}
                autoComplete="one-time-code"
                maxLength={useRecovery ? 19 : 6}
                autoFocus
                required
              />
            </div>
            {twoFaError && (
              <div className="alert alert-error">
                <AlertCircle size={16} style={{ flexShrink: 0, marginTop: 1 }} />
                <span>{twoFaError}</span>
              </div>
            )}
            <button type="submit" disabled={verifying} className="btn-primary">
              {verifying ? <span className="spinner" /> : <KeyRound size={17} />}
              {verifying ? 'Verificando...' : 'Verificar'}
            </button>
            <button type="button" className="switch-link" style={{ background: 'none', border: 0, width: '100%', cursor: 'pointer' }}
              onClick={() => { setUseRecovery((v) => !v); setCode(''); setTwoFaError(''); }}>
              {useRecovery ? 'Usar código de la app' : 'Usar un código de recuperación'}
            </button>
            <button type="button" className="switch-link" style={{ background: 'none', border: 0, width: '100%', cursor: 'pointer' }}
              onClick={cancelTwoFactor}>
              <ArrowLeft size={14} style={{ verticalAlign: 'middle', marginRight: 4 }} />
              Volver
            </button>
          </form>
        </div>
      </div>
    );
  }

  return (
    <div className="auth-screen">
      <div className="auth-card">
        <div className="brand-mark">K</div>
        <h1>Bienvenido de nuevo</h1>
        <p className="subtitle">Inicia sesión para continuar</p>
        <form onSubmit={handleSubmit}>
          <div className="field">
            <label>Correo electrónico</label>
            <input type="email" value={email} onChange={(e) => setEmail(e.target.value)}
              placeholder="tu@correo.com" autoComplete="email" required />
          </div>
          <div className="field">
            <label>Contraseña</label>
            <input type="password" value={password} onChange={(e) => setPassword(e.target.value)}
              placeholder="••••••••" autoComplete="current-password" required />
          </div>
          {error && (
            <div className="alert alert-error">
              <AlertCircle size={16} style={{ flexShrink: 0, marginTop: 1 }} />
              <span>{error}</span>
            </div>
          )}
          <button type="submit" disabled={loading} className="btn-primary">
            {loading ? <span className="spinner" /> : <Wallet size={17} />}
            {loading ? 'Ingresando...' : 'Iniciar sesión'}
          </button>
        </form>
        <p className="switch-link"><Link to="/forgot-password">¿Olvidaste tu contraseña?</Link></p>
        <p className="switch-link">¿No tienes cuenta? <Link to="/register">Regístrate</Link></p>
      </div>
    </div>
  );
}
