import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { ChevronRight, ShieldCheck } from 'lucide-react';
import { walletApi } from '../api/wallet';
import api, { clearTokens } from '../api/client';
import WalletLayout from '../components/WalletLayout';

function ChangePasswordCard() {
  const navigate = useNavigate();
  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');
  const [again, setAgain] = useState('');
  const [twoFa, setTwoFa] = useState('');
  const [err, setErr] = useState('');
  const [busy, setBusy] = useState(false);

  const submit = async () => {
    setErr('');
    if (next.length < 10) { setErr('La nueva contraseña debe tener al menos 10 caracteres.'); return; }
    if (next !== again) { setErr('Las contraseñas nuevas no coinciden.'); return; }
    setBusy(true);
    try {
      await api.post('/api/account/change-password', {
        current_password: current, new_password: next, two_fa_code: twoFa,
      });
      // El backend cerró todas las sesiones: hay que iniciar sesión de nuevo.
      clearTokens();
      navigate('/login');
    } catch (e) {
      setErr(e.response?.data?.message || 'No se pudo cambiar la contraseña.');
    } finally { setBusy(false); }
  };

  return (
    <div className="kz-card">
      <strong>Cambiar contraseña</strong>
      <input className="kz-input" style={{ marginTop: 12 }} type="password" autoComplete="current-password"
        placeholder="Contraseña actual" value={current} onChange={(e) => setCurrent(e.target.value)} />
      <input className="kz-input" style={{ marginTop: 8 }} type="password" autoComplete="new-password"
        placeholder="Nueva contraseña (mín. 10 caracteres)" value={next} onChange={(e) => setNext(e.target.value)} />
      <input className="kz-input" style={{ marginTop: 8 }} type="password" autoComplete="new-password"
        placeholder="Repite la nueva contraseña" value={again} onChange={(e) => setAgain(e.target.value)} />
      <input className="kz-input" style={{ marginTop: 8 }} inputMode="numeric" maxLength={6}
        placeholder="Código 2FA (si lo tienes activado)" value={twoFa} onChange={(e) => setTwoFa(e.target.value.replace(/\D/g, ''))} />
      {err && <div className="kz-err" style={{ marginTop: 8 }}>{err}</div>}
      <button className="kz-btn" style={{ marginTop: 12 }} onClick={submit} disabled={busy || !current || !next || !again}>
        {busy ? 'Guardando…' : 'Cambiar contraseña'}
      </button>
      <div className="kz-hint" style={{ marginTop: 8 }}>Al cambiarla cerraremos tus sesiones en todos los dispositivos.</div>
    </div>
  );
}

function RecoveryCodesBlock({ codes }) {
  return (
    <div style={{ margin: '12px 0' }}>
      <div className="kz-hint">Códigos de recuperación: guárdalos ahora en un lugar seguro. Cada uno sirve una sola vez y no se volverán a mostrar.</div>
      {codes.map((c) => (<div className="kz-addr" key={c} style={{ marginTop: 6 }}><span>{c}</span></div>))}
    </div>
  );
}

// Gestión del 2FA ya activo: regenerar códigos de recuperación o desactivarlo.
// Ambas acciones exigen la contraseña y un código 2FA (de la app o de recuperación).
function TwoFactorManageCard({ remaining, onRemaining, onDisabled }) {
  const [password, setPassword] = useState('');
  const [code, setCode] = useState('');
  const [codes, setCodes] = useState(null);
  const [err, setErr] = useState('');
  const [busy, setBusy] = useState(false);

  const run = async (action) => {
    setErr(''); setCodes(null); setBusy(true);
    try {
      if (action === 'disable') {
        await walletApi.disable2FA(password, code.trim());
        onDisabled();
      } else {
        const r = await walletApi.regenerateRecoveryCodes(password, code.trim());
        setCodes(r.data?.recovery_codes || []);
        onRemaining((r.data?.recovery_codes || []).length);
      }
      setPassword(''); setCode('');
    } catch (e) {
      setErr(e.response?.data?.message || e.response?.data?.error || 'No se pudo completar la acción.');
    } finally { setBusy(false); }
  };

  return (
    <div className="kz-card">
      <strong>Administrar 2FA</strong>
      <div className="kz-hint" style={{ marginTop: 6 }}>
        Códigos de recuperación disponibles: {remaining}{remaining <= 2 ? ' — quedan pocos, genera nuevos.' : ''}
      </div>
      <input className="kz-input" style={{ marginTop: 12 }} type="password" autoComplete="current-password"
        placeholder="Tu contraseña" value={password} onChange={(e) => setPassword(e.target.value)} />
      <input className="kz-input" style={{ marginTop: 8 }} maxLength={24} autoComplete="one-time-code"
        placeholder="Código 2FA o código de recuperación" value={code} onChange={(e) => setCode(e.target.value)} />
      {err && <div className="kz-err" style={{ marginTop: 8 }}>{err}</div>}
      {codes && <RecoveryCodesBlock codes={codes} />}
      <button className="kz-btn" style={{ marginTop: 12 }} disabled={busy || !password || !code} onClick={() => run('regen')}>
        Generar códigos de recuperación nuevos
      </button>
      <button className="kz-btn" style={{ marginTop: 8, background: 'transparent', border: '1px solid currentColor' }}
        disabled={busy || !password || !code}
        onClick={() => { if (window.confirm('Sin 2FA no podrás retirar fondos hasta volver a activarlo. ¿Desactivarlo?')) run('disable'); }}>
        Desactivar 2FA
      </button>
    </div>
  );
}

export default function Security() {
  const [setup, setSetup] = useState(null);
  const [code, setCode] = useState('');
  const [enabled, setEnabled] = useState(false);
  const [remaining, setRemaining] = useState(0);
  const [msg, setMsg] = useState('');
  const [error, setError] = useState('');

  useEffect(() => {
    walletApi.get2FAStatus()
      .then((r) => { setEnabled(!!r.data?.enabled); setRemaining(r.data?.recovery_codes_remaining ?? 0); })
      .catch((e) => { console.error("Frontend error:", e); });
  }, []);

  const start = async () => {
    setError(''); setMsg('');
    try { const r = await walletApi.setup2FA(); setSetup(r.data?.result || r.data); }
    catch (e) { setError(e.response?.data?.error || 'No se pudo iniciar la configuración de 2FA'); }
  };
  const confirm = async () => {
    setError('');
    try { await walletApi.confirm2FA(code); setEnabled(true); setRemaining(Array.isArray(setup?.recovery_codes) ? setup.recovery_codes.length : 10); setSetup(null); setCode(''); setMsg('2FA activado correctamente.'); }
    catch (e) { setError(e.response?.data?.error || 'Código inválido'); }
  };

  const soon = [['PIN de la aplicación', 'Usa un PIN para acceder'], ['Biometría', 'Huella o reconocimiento facial'], ['Gestión de dispositivos', 'Dispositivos autorizados']];

  return (
    <WalletLayout title="Seguridad" tabs={false}>
      <div className="kz-card" style={{ padding: '0 14px' }}>
        <div className="kz-row">
          <div className="main"><strong>Autenticación de dos factores (2FA)</strong><small>Protege tu cuenta con 2FA</small></div>
          <button className={`kz-switch ${enabled ? 'on' : ''}`} onClick={() => !enabled && !setup && start()} aria-label="Activar 2FA" />
        </div>
        {soon.map(([t, s]) => (
          <div className="kz-row" key={t}>
            <div className="main"><strong>{t}</strong><small>{s}</small></div>
            <span className="kz-badge soon">Pronto</span>
          </div>
        ))}
      </div>

      {setup && (
        <div className="kz-card">
          <strong>Escanea o copia este secreto en tu app autenticadora</strong>
          <div className="kz-addr" style={{ margin: '12px 0' }}><span>{setup.secret || setup.secret_key || setup.totp_secret || '—'}</span></div>
          {Array.isArray(setup.recovery_codes) && <RecoveryCodesBlock codes={setup.recovery_codes} />}
          <input className="kz-input" style={{ marginTop: 12 }} inputMode="numeric" maxLength={6} placeholder="Código de 6 dígitos" value={code} onChange={(e) => setCode(e.target.value.replace(/\D/g, ''))} />
          <button className="kz-btn" style={{ marginTop: 12 }} onClick={confirm} disabled={code.length < 6}>Confirmar 2FA</button>
        </div>
      )}
      {enabled && !setup && (
        <TwoFactorManageCard remaining={remaining} onRemaining={setRemaining}
          onDisabled={() => { setEnabled(false); setRemaining(0); setMsg('2FA desactivado. Actívalo de nuevo para poder retirar fondos.'); }} />
      )}
      <ChangePasswordCard />
      {msg && <div className="kz-ok">{msg}</div>}
      {error && <div className="kz-err">{error}</div>}
      <div className="kz-note"><ShieldCheck size={18} style={{ flex: 'none' }} />Tu seguridad es nuestra prioridad. Nunca compartas tu contraseña ni tu código 2FA.</div>
    </WalletLayout>
  );
}
