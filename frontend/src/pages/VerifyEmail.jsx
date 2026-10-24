import { useEffect, useRef, useState } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { AlertCircle, CheckCircle2 } from 'lucide-react';
import api from '../api/client';

// Consume el enlace de verificación de correo (un solo uso). El token se quita
// de la URL de inmediato para que no quede en el historial del navegador.
export default function VerifyEmail() {
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const [state, setState] = useState('loading'); // loading | ok | error
  const [message, setMessage] = useState('');
  const started = useRef(false);

  useEffect(() => {
    if (started.current) return; // StrictMode ejecuta los efectos dos veces en desarrollo
    started.current = true;

    const token = params.get('token');
    navigate('/verify-email', { replace: true });
    if (!token) {
      setState('error');
      setMessage('El enlace no es válido. Solicita uno nuevo desde tu perfil o inicia sesión.');
      return;
    }
    api.post('/api/auth/verify-email', { token })
      .then(() => setState('ok'))
      .catch((err) => {
        setState('error');
        setMessage(err.response?.data?.message || 'No se pudo verificar el correo.');
      });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <div className="auth-screen">
      <div className="auth-card">
        <div className="brand-mark">K</div>
        <h1>Verificación de correo</h1>
        {state === 'loading' && <p className="subtitle">Verificando tu enlace…</p>}
        {state === 'ok' && (
          <>
            <div className="kz-ok" style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
              <CheckCircle2 size={18} /> <span>¡Correo verificado! Tu cuenta quedó activa.</span>
            </div>
            <p className="switch-link"><Link to="/login">Iniciar sesión</Link></p>
          </>
        )}
        {state === 'error' && (
          <>
            <div className="alert alert-error">
              <AlertCircle size={16} style={{ flexShrink: 0, marginTop: 1 }} />
              <span>{message}</span>
            </div>
            <p className="switch-link"><Link to="/login">Volver a iniciar sesión</Link></p>
          </>
        )}
      </div>
    </div>
  );
}
