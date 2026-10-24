import { useState } from 'react';
import { useNavigate, Link } from 'react-router-dom';
import { Wallet, AlertCircle } from 'lucide-react';
import api, { setTokens } from '../api/client';

export default function Login() {
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  const navigate = useNavigate();

  async function handleSubmit(e) {
    e.preventDefault();
    setError('');
    setLoading(true);
    try {
      const { data } = await api.post('/api/auth/login', { email, password });
      localStorage.setItem('korzadi_user', JSON.stringify(data.user || {}));
      setTokens(
        data.access_token || data.accessToken || data.token,
        data.refresh_token || data.refreshToken || ''
      );
      navigate('/dashboard');
    } catch (err) {
      setError(err.response?.data?.message || 'No se pudo iniciar sesión');
    } finally {
      setLoading(false);
    }
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
