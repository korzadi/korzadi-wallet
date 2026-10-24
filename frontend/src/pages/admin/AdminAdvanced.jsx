import { useCallback, useEffect, useState } from 'react';
import { Link, useLocation } from 'react-router-dom';
import {
  Activity, ArrowLeft, CheckCircle2, Clock3, LockKeyhole,
  LogOut, Moon, RefreshCw, ShieldCheck, Sun, Wallet,
  AlertTriangle, Menu, X
} from 'lucide-react';
import api, { clearTokens } from '../../api/client';
import './admin.css';
import './admin-advanced.css';

const sections = [
  ['withdrawals', 'Retiros'],
  ['custody', 'Custodia'],
  ['hot-wallet', 'Hot Wallet'],
  ['treasury', 'Tesorería'],
  ['gas', 'Gas Station'],
  ['security', 'Seguridad'],
];

const titles = {
  withdrawals: 'Revisión de retiros',
  custody: 'Wallets de custodia',
  'hot-wallet': 'Hot Wallet',
  treasury: 'Políticas de tesorería',
  gas: 'Gas Station',
  security: 'Centro de seguridad',
};

const errText = (e) =>
  e?.response?.data?.error ||
  e?.response?.data?.message ||
  e?.message ||
  'No se pudo completar la solicitud.';

const arr = (data, key) =>
  Array.isArray(data?.[key]) ? data[key] : [];

const short = (value, n = 14) => {
  const s = String(value ?? '');
  return s.length > n ? `${s.slice(0, n)}…` : s || '—';
};

function Table({ columns, rows, empty = 'No hay registros disponibles.' }) {
  return (
    <div className="admin-table-wrap">
      <table className="admin-table">
        <thead><tr>{columns.map(c => <th key={c.label}>{c.label}</th>)}</tr></thead>
        <tbody>
          {rows.length ? rows.map((row, i) => (
            <tr key={row.id || row.custody_wallet_id || i}>
              {columns.map(c => (
                <td key={c.label}>
                  {c.render ? c.render(row) : String(row[c.key] ?? '—')}
                </td>
              ))}
            </tr>
          )) : (
            <tr><td colSpan={columns.length} className="admin-empty">{empty}</td></tr>
          )}
        </tbody>
      </table>
    </div>
  );
}

function Field({ label, value, onChange, type = 'text', placeholder = '', required = false }) {
  return (
    <label className="aa-field">
      <span>{label}</span>
      <input
        type={type}
        value={value}
        onChange={e => onChange(e.target.value)}
        placeholder={placeholder}
        required={required}
      />
    </label>
  );
}

function SelectField({ label, value, onChange, options }) {
  return (
    <label className="aa-field">
      <span>{label}</span>
      <select value={value} onChange={e => onChange(e.target.value)}>
        {options.map(o => <option key={o[0]} value={o[0]}>{o[1]}</option>)}
      </select>
    </label>
  );
}

export default function AdminAdvanced() {
  const location = useLocation();
  const current = location.pathname.split('/').pop();
  const section = sections.some(s => s[0] === current) ? current : 'withdrawals';

  const [theme, setTheme] = useState(() => {
    try { return localStorage.getItem('korzadi_admin_theme') || 'dark'; }
    catch { return 'dark'; }
  });
  const [mobileOpen, setMobileOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [message, setMessage] = useState('');
  const [twoFA, setTwoFA] = useState('');
  const [reason, setReason] = useState('');
  const [data, setData] = useState({});
  const [busy, setBusy] = useState(false);

  const [walletForm, setWalletForm] = useState({
    network: 'TRON', asset_code: 'USDT', wallet_type: 'HOT',
    address_type: 'tron', reason: 'Crear wallet administrativa',
  });
  const [treasuryForm, setTreasuryForm] = useState({
    network: 'BSC', asset_code: 'BNB',
    hot_custody_wallet_id: '', treasury_custody_wallet_id: '',
    min_balance: '0', target_balance: '0', max_balance: '0',
    rebalance_threshold: '0', max_rebalance_amount: '0', reason: '',
  });
  const [gasForm, setGasForm] = useState({
    wallet_id: '', minimum_balance: '0', target_balance: '0',
    max_refill: '0', enabled: false, reason: '',
  });
  const [policyForm, setPolicyForm] = useState({
    operation: 'withdrawal', asset_code: 'USDT', network: 'BSC',
    min_amount: '0', max_amount: '', daily_limit: '',
    daily_count_limit: '', monthly_limit: '',
    monthly_count_limit: '', step_up_threshold: '',
    manual_approval_threshold: '', enabled: false,
  });
  const [freezeForm, setFreezeForm] = useState({
    operation: 'withdrawals', frozen: true, reason: '',
  });

  useEffect(() => {
    document.documentElement.dataset.adminTheme = theme;
    try { localStorage.setItem('korzadi_admin_theme', theme); } catch {}
  }, [theme]);

  const headers = useCallback(() => {
    const code = twoFA.trim();
    return code ? { 'X-2FA-Code': code } : {};
  }, [twoFA]);

  const request = useCallback(async (method, url, body, with2FA = false) => {
    const config = with2FA ? { headers: headers() } : {};
    if (method === 'get') return api.get(url, config);
    if (method === 'post') return api.post(url, body, config);
    if (method === 'put') return api.put(url, body, config);
    throw new Error('Método no admitido');
  }, [headers]);

  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      let result = {};
      if (section === 'withdrawals') {
        const r = await request('get', '/api/admin/withdrawals/review', undefined, true);
        result.items = arr(r.data, 'items');
      } else if (section === 'custody') {
        const r = await request('get', '/api/admin/custody/wallets', undefined, true);
        result.items = arr(r.data, 'items');
      } else if (section === 'hot-wallet') {
        const r = await request('get', '/api/admin/hot-wallet/balances', undefined, true);
        result.items = arr(r.data, 'items');
      } else if (section === 'treasury') {
        const r = await request('get', '/api/admin/treasury/policies', undefined, true);
        result.items = arr(r.data, 'items');
      } else if (section === 'gas') {
        result.items = [];
      } else {
        const [f, p, ev] = await Promise.all([
          request('get', '/api/admin/security/freezes', undefined, true),
          request('get', '/api/admin/security/policies', undefined, true),
          request('get', '/api/admin/security/events?limit=50&offset=0', undefined, true),
        ]);
        result.freezes = arr(f.data, 'freezes');
        result.policies = arr(p.data, 'policies');
        result.events = arr(ev.data, 'events');
      }
      setData(result);
    } catch (e) {
      setError(errText(e));
    } finally {
      setLoading(false);
    }
  }, [section, request]);

  useEffect(() => { load(); }, [load]);

  const run = async (operation, successText) => {
    setError('');
    setMessage('');
    setBusy(true);
    try {
      await operation();
      setMessage(successText);
      setReason('');
      await load();
    } catch (e) {
      setError(errText(e));
    } finally {
      setBusy(false);
    }
  };

  const confirm = (text) => window.confirm(text);

  const resolveWithdrawal = (item, action) => {
    const why = window.prompt('Escribe el motivo de la resolución (10–500 caracteres):');
    if (!why || why.trim().length < 10 || why.trim().length > 500) {
      setError('El motivo debe tener entre 10 y 500 caracteres.');
      return;
    }
    if (!confirm(`Confirmas la acción "${action}" para el retiro ${item.id}? Esta operación puede afectar un retiro real.`)) return;
    run(async () => {
      await request('post', `/api/admin/withdrawals/${encodeURIComponent(item.id)}/resolve`, {
        action, reason: why.trim(),
      }, true);
    }, 'Solicitud enviada al servidor.');
  };

  const changeWalletStatus = (wallet) => {
    const status = window.prompt('Nuevo estado: active, disabled o retired', 'disabled');
    if (!['active', 'disabled', 'retired'].includes(status)) {
      setError('Estado no válido.');
      return;
    }
    const why = window.prompt('Motivo (5–500 caracteres):');
    if (!why || why.trim().length < 5 || why.trim().length > 500) {
      setError('El motivo debe tener entre 5 y 500 caracteres.');
      return;
    }
    if (!confirm(`Cambiar wallet ${wallet.id} a "${status}"?`)) return;
    run(async () => {
      await request('post', `/api/admin/custody/wallets/${encodeURIComponent(wallet.id)}/status`, {
        status, reason: why.trim(),
      }, true);
    }, 'Estado de custodia actualizado.');
  };

  const createWallet = (e) => {
    e.preventDefault();
    if (!walletForm.reason.trim() || walletForm.reason.trim().length < 5) {
      setError('Escribe un motivo de al menos 5 caracteres.');
      return;
    }
    if (!confirm('Se solicitará al servidor crear una wallet de custodia. ¿Continuar?')) return;
    run(async () => {
      await request('post', '/api/admin/custody/wallets', walletForm, true);
      setWalletForm(f => ({ ...f, reason: '' }));
    }, 'Solicitud de creación enviada.');
  };

  const createTreasuryPolicy = (e) => {
    e.preventDefault();
    if (!treasuryForm.reason.trim() || treasuryForm.reason.trim().length < 5) {
      setError('Escribe un motivo de al menos 5 caracteres.');
      return;
    }
    if (!confirm('La política se creará DESACTIVADA. ¿Continuar?')) return;
    run(async () => {
      await request('post', '/api/admin/treasury/policies', treasuryForm, true);
      setTreasuryForm(f => ({ ...f, reason: '' }));
    }, 'Política creada desactivada.');
  };

  const toggleTreasury = (item, enabled) => {
    const why = window.prompt('Motivo del cambio (5–500 caracteres):');
    if (!why || why.trim().length < 5 || why.trim().length > 500) {
      setError('El motivo debe tener entre 5 y 500 caracteres.');
      return;
    }
    if (!confirm(`${enabled ? 'Activar' : 'Desactivar'} la política ${item.id}?`)) return;
    run(async () => {
      await request('post', `/api/admin/treasury/policies/${encodeURIComponent(item.id)}/enabled`, {
        enabled, reason: why.trim(),
      }, true);
    }, 'Estado de la política actualizado.');
  };

  const saveGas = (e) => {
    e.preventDefault();
    if (!gasForm.wallet_id.trim()) {
      setError('Introduce el ID de la wallet GAS.');
      return;
    }
    if (!gasForm.reason.trim() || gasForm.reason.trim().length < 5) {
      setError('Escribe un motivo de al menos 5 caracteres.');
      return;
    }
    if (!confirm('Guardar la política de recarga de gas para esta wallet?')) return;
    run(async () => {
      const { wallet_id, ...body } = gasForm;
      await request('put', `/api/admin/gas-station/policies/${encodeURIComponent(wallet_id)}`, body, true);
    }, 'Política de gas actualizada.');
  };

  const saveSecurityPolicy = (e) => {
    e.preventDefault();
    if (!confirm('Guardar esta política de seguridad? Comprueba cuidadosamente los límites.')) return;
    const body = { ...policyForm };
    for (const key of ['max_amount', 'daily_limit', 'daily_count_limit', 'monthly_limit', 'monthly_count_limit', 'step_up_threshold', 'manual_approval_threshold']) {
      if (body[key] === '') delete body[key];
    }
    for (const key of ['daily_count_limit', 'monthly_count_limit']) {
      if (body[key] !== undefined) body[key] = Number(body[key]);
    }
    run(async () => {
      await request('post', '/api/admin/security/policies', body, true);
    }, 'Política de seguridad guardada.');
  };

  const setFreeze = (e) => {
    e.preventDefault();
    if (!freezeForm.reason.trim()) {
      setError('El motivo de congelación es obligatorio.');
      return;
    }
    if (!confirm(`${freezeForm.frozen ? 'CONGELAR' : 'DESCONGELAR'} la operación "${freezeForm.operation}"?`)) return;
    run(async () => {
      await request('post', '/api/admin/security/freezes', freezeForm, true);
    }, 'Solicitud de congelación enviada.');
  };

  const logout = () => {
    clearTokens();
    window.location.href = '/login';
  };

  const items = data.items || [];

  return (
    <div className="admin-shell" data-theme={theme}>
      {mobileOpen && <button className="admin-mobile-overlay" aria-label="Cerrar menú" onClick={() => setMobileOpen(false)} />}
      <aside className={`admin-sidebar ${mobileOpen ? 'mobile-open' : ''}`}>
        <div className="admin-brand">
          <span className="admin-brand-mark">K</span>
          <span>Korzadi <b>Admin</b></span>
        </div>
        <div className="admin-nav-label">ADMINISTRACIÓN</div>
        <nav className="admin-nav">
          <Link className="admin-nav-link" to="/admin"><ArrowLeft size={17} /> Panel principal</Link>
          {sections.map(([id, label]) => (
            <Link
              key={id}
              to={`/admin/${id}`}
              onClick={() => setMobileOpen(false)}
              className={`admin-nav-link ${section === id ? 'active' : ''}`}
            >
              {id === 'security' ? <ShieldCheck size={17} /> : id === 'custody' ? <Wallet size={17} /> : <Activity size={17} />}
              {label}
            </Link>
          ))}
        </nav>
        <div className="admin-sidebar-bottom">
          <div className="admin-secure-note">
            <ShieldCheck size={20} />
            <div><strong>Área protegida</strong><small>Acciones sensibles auditables</small></div>
          </div>
          <button className="admin-nav-link admin-logout" onClick={logout}><LogOut size={17} /> Cerrar sesión</button>
        </div>
      </aside>

      <main className="admin-main">
        <header className="admin-topbar">
          <button className="admin-icon-button admin-mobile-toggle" onClick={() => setMobileOpen(v => !v)} aria-label="Menú">
            {mobileOpen ? <X size={18} /> : <Menu size={18} />}
          </button>
          <div className="admin-heading">
            <span>CONTROL OPERATIVO / KORZADI WALLET</span>
            <h1>{titles[section]}</h1>
          </div>
          <div className="admin-top-actions">
            <span className="admin-live"><span /> API</span>
            <button className="admin-icon-button" onClick={() => setTheme(t => t === 'dark' ? 'light' : 'dark')} title="Cambiar tema">
              {theme === 'dark' ? <Sun size={17} /> : <Moon size={17} />}
            </button>
            <button className="admin-icon-button" onClick={load} disabled={loading} title="Actualizar">
              <RefreshCw size={17} />
            </button>
          </div>
        </header>

        <div className="admin-content">
          <div className="aa-stepup">
            <LockKeyhole size={18} />
            <div className="aa-stepup-copy">
              <strong>Verificación de operaciones sensibles</strong>
              <small>Introduce un código 2FA vigente cuando la operación lo solicite.</small>
            </div>
            <input
              aria-label="Código 2FA"
              inputMode="numeric"
              autoComplete="one-time-code"
              placeholder="Código 2FA"
              value={twoFA}
              onChange={e => setTwoFA(e.target.value.replace(/\D/g, '').slice(0, 10))}
            />
          </div>

          {error && <div className="admin-alert error"><AlertTriangle size={18} /><div><strong>No se pudo completar</strong><p>{error}</p></div></div>}
          {message && <div className="admin-alert success"><CheckCircle2 size={18} /><div><strong>Operación enviada</strong><p>{message}</p></div></div>}
          {loading && <div className="admin-loading"><RefreshCw size={16} /> Cargando datos del servidor…</div>}

          {section === 'withdrawals' && <>
            <div className="admin-welcome"><div><span className="admin-eyebrow">OPERACIONES FINANCIERAS</span><h2>Retiros que requieren revisión</h2><p>Las resoluciones pueden afectar operaciones reales. Verifica la dirección, la red y el motivo antes de continuar.</p></div><div className="admin-welcome-icon"><Clock3 size={24} /></div></div>
            <section className="admin-panel">
              <div className="admin-panel-heading"><div><h2>Cola de revisión</h2><p>Datos obtenidos del backend; no se simulan registros.</p></div><span className="admin-count">{items.length} registros</span></div>
              <Table columns={[
                {label:'ID',render:r=><code title={r.id}>{short(r.id, 12)}</code>},
                {label:'Usuario',render:r=><code title={r.user_id}>{short(r.user_id, 12)}</code>},
                {label:'Activo / red',render:r=><>{r.asset_code || '—'} / {r.network || '—'}</>},
                {label:'Cantidad',key:'amount'},
                {label:'Estado',render:r=><span className={`admin-status ${r.needs_review ? 'danger' : ''}`}>{r.status || (r.needs_review ? 'needs_review' : '—')}</span>},
                {label:'Intentos',key:'attempts'},
                {label:'Tx hash',render:r=><code title={r.tx_hash}>{short(r.tx_hash, 12)}</code>},
                {label:'Acciones',render:r=><div className="aa-actions"><button disabled={busy} onClick={() => resolveWithdrawal(r,'release')}>Liberar</button><button disabled={busy} onClick={() => resolveWithdrawal(r,'retry_settlement')}>Reintentar</button></div>},
              ]} rows={items} />
            </section>
          </>}

          {section === 'hot-wallet' && <>
            <div className="admin-welcome"><div><span className="admin-eyebrow">CUSTODIA ON-CHAIN</span><h2>Hot Wallet</h2><p>Balances consultados desde los nodos blockchain. Esta pantalla no envía transacciones.</p></div><div className="admin-welcome-icon"><Wallet size={24} /></div></div>
            <section className="admin-panel">
              <div className="admin-panel-heading"><div><h2>Balances de wallets HOT</h2><p>Si no hay wallets configuradas, créalas en Custodia y selecciona el tipo HOT.</p></div><button className="aa-secondary" disabled={loading || busy} onClick={load}><RefreshCw size={15}/> Actualizar</button></div>
              <Table columns={[
                {label:'Red',key:'network'},
                {label:'Dirección',render:r=><code title={r.address}>{short(r.address,18)}</code>},
                {label:'USDT',key:'usdt_balance'},
                {label:'Moneda gas',render:r=><>{r.native_asset || '—'}: {r.native_balance ?? '—'}</>},
                {label:'Estado',render:r=><span className={`admin-status ${r.error ? 'danger' : 'success'}`}>{r.error || r.status || 'Consultado'}</span>},
              ]} rows={items} empty="No hay wallets HOT activas. Entra en Custodia y crea una wallet de tipo HOT." />
            </section>
          </>}

          {section === 'custody' && <>
            <div className="admin-welcome"><div><span className="admin-eyebrow">GESTIÓN DE CUSTODIA</span><h2>Wallets operativas</h2><p>Las claves privadas no se muestran ni se envían al navegador.</p></div><div className="admin-welcome-icon"><Wallet size={24} /></div></div>
            <section className="admin-panel">
              <div className="admin-panel-heading"><div><h2>Crear wallet de custodia</h2><p>El servidor administra la generación de la dirección.</p></div></div>
              <form className="aa-form" onSubmit={createWallet}>
                <SelectField
                  label="Red"
                  value={walletForm.network}
                  onChange={v => setWalletForm(f => ({
                    ...f,
                    network: v,
                    asset_code: 'USDT',
                    address_type: v === 'TRON' ? 'tron' : 'evm',
                  }))}
                  options={ [['TRON','TRON'],['BSC','BSC'],['ETHEREUM','ETHEREUM']] }
                />
                <SelectField
                  label="Activo"
                  value={walletForm.asset_code}
                  onChange={v => setWalletForm(f => ({ ...f, asset_code: v }))}
                  options={walletForm.network === 'TRON'
                    ? [['USDT','USDT'],['TRX','TRX']]
                    : walletForm.network === 'BSC'
                      ? [['USDT','USDT'],['BNB','BNB']]
                      : [['USDT','USDT'],['ETH','ETH']]}
                />
                <SelectField
                  label="Tipo de wallet"
                  value={walletForm.wallet_type}
                  onChange={v => setWalletForm(f => ({ ...f, wallet_type: v }))}
                  options={ [['HOT','HOT'],['GAS','GAS'],['TREASURY','TREASURY']] }
                />
                <SelectField
                  label="Formato de dirección"
                  value={walletForm.address_type}
                  onChange={v => setWalletForm(f => ({ ...f, address_type: v }))}
                  options={walletForm.network === 'TRON'
                    ? [['tron','TRON']]
                    : [['evm','EVM']]}
                />
                <Field
                  label="Motivo"
                  value={walletForm.reason}
                  onChange={v => setWalletForm(f => ({ ...f, reason: v }))}
                  placeholder="Motivo operativo"
                  required
                />
                <div className="aa-policy-summary">
                  <strong>Resumen de la solicitud</strong>
                  <p>Red: {walletForm.network} · Activo: {walletForm.asset_code} · Tipo: {walletForm.wallet_type}</p>
                  <p>Formato: {walletForm.address_type.toUpperCase()}</p>
                  <p>La clave privada se administra en el servidor y no se muestra en el navegador.</p>
                </div>
                <button className="aa-primary" disabled={busy}>Solicitar creación</button>
              </form>
            </section>
            <section className="admin-panel">
              <div className="admin-panel-heading"><div><h2>Wallets registradas</h2><p>Consulta y cambio de estado con motivo.</p></div><span className="admin-count">{items.length}</span></div>
              <Table columns={[
                {label:'ID',render:r=><code title={r.id}>{short(r.id,12)}</code>},
                {label:'Red / activo',render:r=><>{r.network} / {r.asset_code}</>},
                {label:'Tipo',key:'wallet_type'},
                {label:'Dirección',render:r=><code title={r.address}>{short(r.address,18)}</code>},
                {label:'Formato',key:'address_type'},
                {label:'Estado',render:r=><span className="admin-status">{r.status}</span>},
                {label:'Acción',render:r=><button className="aa-secondary" disabled={busy} onClick={()=>changeWalletStatus(r)}>Cambiar estado</button>},
              ]} rows={items} />
            </section>
          </>}

          {section === 'treasury' && <>
            <div className="admin-welcome"><div><span className="admin-eyebrow">CONTROL DE SALDOS</span><h2>Políticas de tesorería</h2><p>Las políticas nuevas se crean desactivadas y requieren una acción separada para activarse.</p></div><div className="admin-welcome-icon"><Wallet size={24} /></div></div>
            <section className="admin-panel">
              <div className="admin-panel-heading"><div><h2>Nueva política</h2><p>Usa importes decimales válidos para el activo elegido.</p></div></div>
              <form className="aa-form" onSubmit={createTreasuryPolicy}>
                {[
                  ['network','Red'],['asset_code','Activo'],['hot_custody_wallet_id','ID wallet HOT'],['treasury_custody_wallet_id','ID wallet TREASURY'],
                  ['min_balance','Saldo mínimo'],['target_balance','Saldo objetivo'],['max_balance','Saldo máximo'],
                  ['rebalance_threshold','Umbral de rebalanceo'],['max_rebalance_amount','Máximo por rebalanceo'],['reason','Motivo'],
                ].map(([k,label])=><Field key={k} label={label} value={treasuryForm[k]} onChange={v=>setTreasuryForm(f=>({...f,[k]:v}))} required />)}
                <button className="aa-primary" disabled={busy}>Crear desactivada</button>
              </form>
            </section>
            <section className="admin-panel">
              <div className="admin-panel-heading"><div><h2>Políticas existentes</h2></div><span className="admin-count">{items.length}</span></div>
              <Table columns={[
                {label:'ID',render:r=><code>{short(r.id,12)}</code>},
                {label:'Red / activo',render:r=><>{r.network} / {r.asset_code}</>},
                {label:'Mínimo',key:'min_balance'},
                {label:'Objetivo',key:'target_balance'},
                {label:'Máximo',key:'max_balance'},
                {label:'Estado',render:r=><span className={`admin-status ${r.enabled?'success':'danger'}`}>{r.enabled?'Activa':'Desactivada'}</span>},
                {label:'Acción',render:r=><button className="aa-secondary" disabled={busy} onClick={()=>toggleTreasury(r,!r.enabled)}>{r.enabled?'Desactivar':'Activar'}</button>},
              ]} rows={items} />
            </section>
          </>}

          {section === 'gas' && <div className="aa-gas-page">
            <div className="admin-welcome"><div><span className="admin-eyebrow">OPERACIÓN DE RED</span><h2>Política de Gas Station</h2><p>Selecciona una wallet de tipo GAS activa. Revisa los límites antes de guardar.</p></div><div className="admin-welcome-icon"><Activity size={24} /></div></div>
            <section className="admin-panel">
              <div className="admin-panel-heading"><div><h2>Consultar política</h2><p>Introduce el ID de una wallet GAS para consultar su política.</p></div></div>
              <form className="aa-form" onSubmit={e=>{e.preventDefault();run(async()=>{const r=await request('get',`/api/admin/gas-station/policies/${encodeURIComponent(gasForm.wallet_id)}`, undefined, true);const p=r.data?.policy||{};setData({policy:p});setGasForm(f=>({...f,minimum_balance:String(p.minimum_balance??'0'),target_balance:String(p.target_balance??'0'),max_refill:String(p.max_refill??'0'),enabled:!!p.enabled}));},'Política consultada.');}}>
                <Field label="ID wallet GAS" value={gasForm.wallet_id} onChange={v=>setGasForm(f=>({...f,wallet_id:v}))} required />
                <button className="aa-primary" disabled={busy}>Consultar</button>
              </form>
              {data.policy && <div className="aa-policy-summary"><strong>Política cargada</strong><p>Mínimo: {data.policy.minimum_balance ?? '—'} · Objetivo: {data.policy.target_balance ?? '—'} · Máximo de recarga: {data.policy.max_refill ?? '—'} · Estado: {data.policy.enabled ? 'Activa' : 'Desactivada'}</p></div>}
            </section>
            <section className="admin-panel">
              <div className="admin-panel-heading"><div><h2>Guardar política</h2><p>El ID debe corresponder a una wallet GAS activa.</p></div></div>
              <form className="aa-form" onSubmit={saveGas}>
                <Field label="ID wallet GAS" value={gasForm.wallet_id} onChange={v=>setGasForm(f=>({...f,wallet_id:v}))} required />
                <Field label="Saldo mínimo" value={gasForm.minimum_balance} onChange={v=>setGasForm(f=>({...f,minimum_balance:v}))} required />
                <Field label="Saldo objetivo" value={gasForm.target_balance} onChange={v=>setGasForm(f=>({...f,target_balance:v}))} required />
                <Field label="Máximo por recarga" value={gasForm.max_refill} onChange={v=>setGasForm(f=>({...f,max_refill:v}))} required />
                <Field label="Motivo" value={gasForm.reason} onChange={v=>setGasForm(f=>({...f,reason:v}))} required />
                <label className="aa-check"><input type="checkbox" checked={gasForm.enabled} onChange={e=>setGasForm(f=>({...f,enabled:e.target.checked}))}/> Política habilitada</label>
                <button className="aa-primary" disabled={busy}>Guardar política</button>
              </form>
            </section>
          </div>}

          {section === 'security' && <>
            <div className="admin-welcome"><div><span className="admin-eyebrow">PROTECCIÓN OPERATIVA</span><h2>Centro de seguridad</h2><p>Las congelaciones afectan operaciones reales. Usa un motivo claro y verifica el alcance.</p></div><div className="admin-welcome-icon"><ShieldCheck size={24} /></div></div>
            <section className="admin-panel">
              <div className="admin-panel-heading"><div><h2>Congelación de operaciones</h2><p>El alcance exacto depende de las operaciones admitidas por el backend.</p></div></div>
              <form className="aa-form" onSubmit={setFreeze}>
                <Field label="Operación" value={freezeForm.operation} onChange={v=>setFreezeForm(f=>({...f,operation:v}))} required />
                <SelectField label="Estado solicitado" value={String(freezeForm.frozen)} onChange={v=>setFreezeForm(f=>({...f,frozen:v==='true'}))} options={[['true','Congelar'],['false','Descongelar']]} />
                <Field label="Motivo" value={freezeForm.reason} onChange={v=>setFreezeForm(f=>({...f,reason:v}))} required />
                <button className="aa-primary" disabled={busy}>Aplicar cambio</button>
              </form>
            </section>
            <section className="admin-panel">
              <div className="admin-panel-heading"><div><h2>Estado de congelaciones</h2></div><span className="admin-count">{(data.freezes||[]).length}</span></div>
              <Table columns={[
                {label:'Operación',key:'operation'},
                {label:'Estado',render:r=><span className={`admin-status ${r.frozen?'danger':'success'}`}>{r.frozen?'Congelada':'Activa'}</span>},
                {label:'Motivo',key:'reason'},
                {label:'Actualización',render:r=>r.updated_at ? new Date(r.updated_at).toLocaleString() : '—'},
              ]} rows={data.freezes||[]} />
            </section>
            <section className="admin-panel">
              <div className="admin-panel-heading"><div><h2>Crear política de seguridad</h2><p>Los límites monetarios se envían como cadenas decimales.</p></div></div>
              <form className="aa-form" onSubmit={saveSecurityPolicy}>
                <Field label="Operación" value={policyForm.operation} onChange={v=>setPolicyForm(f=>({...f,operation:v}))} required />
                <Field label="Activo" value={policyForm.asset_code} onChange={v=>setPolicyForm(f=>({...f,asset_code:v.toUpperCase()}))} required />
                <Field label="Red" value={policyForm.network} onChange={v=>setPolicyForm(f=>({...f,network:v}))} required />
                {[
                  ['min_amount','Importe mínimo'],['max_amount','Importe máximo'],['daily_limit','Límite diario'],
                  ['daily_count_limit','Número máximo diario'],['monthly_limit','Límite mensual'],
                  ['monthly_count_limit','Número máximo mensual'],['step_up_threshold','Umbral de 2FA'],
                  ['manual_approval_threshold','Umbral de aprobación manual'],
                ].map(([k,label])=><Field key={k} label={label} value={policyForm[k]} onChange={v=>setPolicyForm(f=>({...f,[k]:v}))} />)}
                <label className="aa-check"><input type="checkbox" checked={policyForm.enabled} onChange={e=>setPolicyForm(f=>({...f,enabled:e.target.checked}))}/> Política habilitada</label>
                <button className="aa-primary" disabled={busy}>Guardar política</button>
              </form>
            </section>
            <section className="admin-panel">
              <div className="admin-panel-heading"><div><h2>Políticas registradas</h2></div><span className="admin-count">{(data.policies||[]).length}</span></div>
              <Table columns={[
                {label:'Operación',key:'operation'},
                {label:'Activo / red',render:r=><>{r.asset_code} / {r.network}</>},
                {label:'Mínimo',key:'min_amount'},
                {label:'Máximo',key:'max_amount'},
                {label:'Estado',render:r=><span className={`admin-status ${r.enabled?'success':'danger'}`}>{r.enabled?'Activa':'Desactivada'}</span>},
              ]} rows={data.policies||[]} />
            </section>
            <section className="admin-panel">
              <div className="admin-panel-heading"><div><h2>Eventos de seguridad</h2><p>Últimos eventos devueltos por el backend.</p></div><span className="admin-count">{(data.events||[]).length}</span></div>
              <Table columns={[
                {label:'Fecha',render:r=>r.created_at ? new Date(r.created_at).toLocaleString() : '—'},
                {label:'Tipo',render:r=>r.event_type || r.type || r.operation || '—'},
                {label:'Usuario',render:r=><code>{short(r.user_id,12)}</code>},
                {label:'Detalles',render:r=>short(r.details || r.reason || r.description,40)},
              ]} rows={data.events||[]} />
            </section>
          </>}
          <p className="admin-footnote">Korzadi Wallet · Panel operativo. Las respuestas y autorizaciones definitivas proceden del backend.</p>
        </div>
      </main>
    </div>
  );
}
