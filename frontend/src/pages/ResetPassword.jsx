import { useEffect, useRef, useState } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { AlertCircle, KeyRound } from 'lucide-react';
import api from '../api/client';

const MIN_LEN = 10;

export default function ResetPassword() {
  const [params] = useSearchParams();
  // El token se guarda en memoria y se quita de la URL al instante (historial/referrer).
  const navigate = useNavigate();
  const tokenRef = useRef(params.get('token') || '');
  useEffect(() => {
    if (tokenRef.current) navigate('/reset-password', { replace: true });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const [password, setPassword] = useState('');
  const [confirm, setConfirm] = useState('');
  const [done, setDone] = useState(false);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);

  async function handleSubmit(e) {
    e.preventDefault();
    setError('');
    if (password.length < MIN_LEN) {
      setError(`La contraseña debe tener al menos ${MIN_LEN} caracteres.`);
      return;
    }
    if (password !== confirm) {
      setError('Las contraseñas no coinciden.');
      return;
    }
    setLoading(true);
    try {
      await api.post('/api/auth/reset-password', { token: tokenRef.current, new_password: password });
      tokenRef.current = '';
      setDone(true);
    } catch (err) {
      setError(err.response?.data?.message || 'No se pudo restablecer la contraseña.');
    } finally {
      setLoading(false);
    }
  }

  if (!tokenRef.current && !done) {
    return (
      <div className="auth-screen">
        <div className="auth-card">
          <div className="brand-mark">K</div>
          <h1>Enlace no válido</h1>
          <div className="alert alert-error">
            <AlertCircle size={16} style={{ flexShrink: 0, marginTop: 1 }} />
            <span>El enlace es inválido o ya fue usado. Solicita uno nuevo.</span>
          </div>
          <p className="switch-link"><Link to="/forgot-password">Solicitar un nuevo enlace</Link></p>
        </div>
      </div>
    );
  }

  return (
    <div className="auth-screen">
      <div className="auth-card">
        <div className="brand-mark">K</div>
        <h1>Nueva contraseña</h1>
        {done ? (
          <>
            <div className="kz-ok">Contraseña actualizada. Cerramos tus sesiones abiertas por seguridad.</div>
            <p className="switch-link"><Link to="/login">Iniciar sesión</Link></p>
          </>
        ) : (
          <form onSubmit={handleSubmit}>
            <div className="field">
              <label>Nueva contraseña</label>
              <input type="password" value={password} onChange={(e) => setPassword(e.target.value)}
                placeholder="Mínimo 10 caracteres" autoComplete="new-password" required />
            </div>
            <div className="field">
              <label>Repite la contraseña</label>
              <input type="password" value={confirm} onChange={(e) => setConfirm(e.target.value)}
                autoComplete="new-password" required />
            </div>
            {error && (
              <div className="alert alert-error">
                <AlertCircle size={16} style={{ flexShrink: 0, marginTop: 1 }} />
                <span>{error}</span>
              </div>
            )}
            <button type="submit" disabled={loading} className="btn-primary">
              {loading ? <span className="spinner" /> : <KeyRound size={17} />}
              {loading ? 'Guardando...' : 'Guardar contraseña'}
            </button>
          </form>
        )}
      </div>
    </div>
  );
}
