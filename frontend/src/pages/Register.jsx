import { useState } from 'react';
import { useNavigate, Link } from 'react-router-dom';
import { UserPlus, AlertCircle } from 'lucide-react';
import api, { setTokens } from '../api/client';

export default function Register() {
  const [form, setForm] = useState({ first_name: '', last_name: '', email: '', password: '' });
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  const navigate = useNavigate();

  function update(field) {
    return (e) => setForm({ ...form, [field]: e.target.value });
  }

  async function handleSubmit(e) {
    e.preventDefault();
    setError('');
    setLoading(true);
    try {
      const { data } = await api.post('/api/auth/register', form);
      localStorage.setItem('korzadi_user', JSON.stringify(data.user || {}));
      setTokens(data.access_token || data.accessToken || data.token, data.refresh_token || data.refreshToken || '');
      navigate('/dashboard');
    } catch (err) {
      setError(err.response?.data?.message || 'No se pudo crear la cuenta');
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="auth-screen">
      <div className="auth-card">
        <div className="brand-mark">K</div>
        <h1>Crea tu cuenta</h1>
        <p className="subtitle">Empieza a usar Korzadi Wallet en segundos</p>
        <form onSubmit={handleSubmit}>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
            <div className="field">
              <label>Nombre</label>
              <input value={form.first_name} onChange={update('first_name')} placeholder="Ana" required />
            </div>
            <div className="field">
              <label>Apellido</label>
              <input value={form.last_name} onChange={update('last_name')} placeholder="Pérez" required />
            </div>
          </div>
          <div className="field">
            <label>Correo electrónico</label>
            <input type="email" value={form.email} onChange={update('email')} placeholder="tu@correo.com" autoComplete="email" required />
          </div>
          <div className="field">
            <label>Contraseña</label>
            <input type="password" minLength={10} value={form.password} onChange={update('password')}
              placeholder="Mínimo 10 caracteres" autoComplete="new-password" required />
          </div>
          {error && (
            <div className="alert alert-error">
              <AlertCircle size={16} style={{ flexShrink: 0, marginTop: 1 }} />
              <span>{error}</span>
            </div>
          )}
          <button type="submit" disabled={loading} className="btn-primary">
            {loading ? <span className="spinner" /> : <UserPlus size={17} />}
            {loading ? 'Creando cuenta...' : 'Crear cuenta'}
          </button>
        </form>
        <p className="switch-link">¿Ya tienes cuenta? <Link to="/login">Inicia sesión</Link></p>
      </div>
    </div>
  );
}
