import { useState } from 'react';
import { Link } from 'react-router-dom';
import { AlertCircle, Mail } from 'lucide-react';
import api from '../api/client';

export default function ForgotPassword() {
  const [email, setEmail] = useState('');
  const [sent, setSent] = useState(false);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);

  async function handleSubmit(e) {
    e.preventDefault();
    setError('');
    setLoading(true);
    try {
      await api.post('/api/auth/forgot-password', { email: email.trim() });
      setSent(true); // misma respuesta exista o no la cuenta
    } catch (err) {
      setError(err.response?.data?.message || 'No se pudo procesar la solicitud. Inténtalo de nuevo.');
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="auth-screen">
      <div className="auth-card">
        <div className="brand-mark">K</div>
        <h1>Recuperar contraseña</h1>
        {sent ? (
          <>
            <div className="kz-ok">
              Si la cuenta existe, te enviamos un correo con el enlace para restablecer tu contraseña.
              El enlace vence en 30 minutos.
            </div>
            <p className="switch-link"><Link to="/login">Volver a iniciar sesión</Link></p>
          </>
        ) : (
          <>
            <p className="subtitle">Te enviaremos un enlace para crear una nueva contraseña</p>
            <form onSubmit={handleSubmit}>
              <div className="field">
                <label>Correo electrónico</label>
                <input type="email" value={email} onChange={(e) => setEmail(e.target.value)}
                  placeholder="tu@correo.com" autoComplete="email" required />
              </div>
              {error && (
                <div className="alert alert-error">
                  <AlertCircle size={16} style={{ flexShrink: 0, marginTop: 1 }} />
                  <span>{error}</span>
                </div>
              )}
              <button type="submit" disabled={loading} className="btn-primary">
                {loading ? <span className="spinner" /> : <Mail size={17} />}
                {loading ? 'Enviando...' : 'Enviar enlace'}
              </button>
            </form>
            <p className="switch-link"><Link to="/login">Volver a iniciar sesión</Link></p>
          </>
        )}
      </div>
    </div>
  );
}
