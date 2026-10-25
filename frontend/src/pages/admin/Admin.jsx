import { useCallback, useEffect, useState } from 'react';
import { Link, useLocation } from 'react-router-dom';
import {
  Activity, ArrowLeft, CheckCircle2, Clock3, Handshake, LayoutDashboard,
  LogOut, Menu, Moon, RefreshCw, ShieldCheck, Sun, Users,
  Wallet, Ban, FileText, AlertCircle
} from 'lucide-react';
import api, { clearTokens } from '../../api/client';
import { getUser } from '../../lib/user';
import './admin.css';

const routes = [
  { path: '/admin', label: 'Dashboard', icon: LayoutDashboard },
  { path: '/admin/users', label: 'Usuarios', icon: Users },
  { path: '/admin/transactions', label: 'Transacciones', icon: Wallet },
  { path: '/admin/withdrawals', label: 'Retiros', icon: Clock3 },
  { path: '/admin/custody', label: 'Custodia', icon: ShieldCheck },
  { path: '/admin/treasury', label: 'Tesorería', icon: Activity },
  { path: '/admin/gas', label: 'Gas Station', icon: CheckCircle2 },
  { path: '/admin/p2p', label: 'P2P', icon: Handshake },
  { path: '/admin/security', label: 'Seguridad', icon: ShieldCheck },
  { path: '/admin/profile', label: 'Perfil', icon: Users },
  { path: '/admin/audit', label: 'Auditoría', icon: FileText },
];

const titles = {
  '/admin': 'Panel de control',
  '/admin/users': 'Gestión de usuarios',
  '/admin/transactions': 'Transacciones',
  '/admin/withdrawals': 'Revisión de retiros',
  '/admin/custody': 'Custodia',
  '/admin/treasury': 'Tesorería',
  '/admin/gas': 'Gas Station',
  '/admin/p2p': 'Mercado P2P',
  '/admin/security': 'Seguridad',
  '/admin/profile': 'Perfil del administrador',
  '/admin/audit': 'Registro de auditoría',
};

function date(value) {
  if (!value) return '—';
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? String(value) : parsed.toLocaleString();
}

function shortId(value) {
  if (!value) return '—';
  const text = String(value);
  return text.length > 18 ? `${text.slice(0, 8)}…${text.slice(-6)}` : text;
}

function Status({ value }) {
  const status = String(value || 'desconocido');
  const kind = /block|fail|reject|error|revok/i.test(status)
    ? 'danger'
    : /success|complete|active|confirm|done/i.test(status)
      ? 'success' : 'neutral';

  return <span className={`admin-status ${kind}`}>{status}</span>;
}

function Table({ columns, rows, empty }) {
  if (!rows?.length) {
    return <div className="admin-empty">{empty || 'No hay registros disponibles.'}</div>;
  }

  return (
    <div className="admin-table-wrap">
      <table className="admin-table">
        <thead>
          <tr>{columns.map((column) => <th key={column.key}>{column.label}</th>)}</tr>
        </thead>
        <tbody>
          {rows.map((row, index) => (
            <tr key={row.id || `${index}`}>
              {columns.map((column) => (
                <td key={column.key}>
                  {column.render ? column.render(row) : (row[column.key] ?? '—')}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

export default function Admin() {
  const location = useLocation();
  const [theme, setTheme] = useState(() => {
    try {
      return localStorage.getItem('korzadi_admin_theme') || 'dark';
    } catch {
      return 'dark';
    }
  });
  const [mobileMenu, setMobileMenu] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [forbidden, setForbidden] = useState(false);
  const [users, setUsers] = useState([]);
  const [transactions, setTransactions] = useState([]);
  const [audit, setAudit] = useState([]);
  const [notice, setNotice] = useState('');
  const [workingId, setWorkingId] = useState('');

  const currentPath = location.pathname;
  const isDashboard = currentPath === '/admin';
  const isUsers = currentPath === '/admin/users';
  const isTransactions = currentPath === '/admin/transactions';
  const isAudit = currentPath === '/admin/audit';
  const isProfile = currentPath === '/admin/profile';
  const profileUser = getUser();

  const loadData = useCallback(async () => {
    setLoading(true);
    setError('');
    setForbidden(false);
    setNotice('');

    try {
      // El backend valida token y rol. No confiamos en roles del navegador.
      const first = await api.get('/api/admin/users', {
        params: { limit: 50, offset: 0 },
      });
      setUsers(Array.isArray(first.data?.users) ? first.data.users : []);

      if (isDashboard || isTransactions) {
        const tx = await api.get('/api/admin/transactions', {
          params: { limit: 20, offset: 0 },
        });
        setTransactions(Array.isArray(tx.data?.transactions) ? tx.data.transactions : []);
      }

      if (isDashboard || isAudit) {
        const logs = await api.get('/api/admin/audit', {
          params: { limit: 20, offset: 0 },
        });
        setAudit(Array.isArray(logs.data?.audit_logs) ? logs.data.audit_logs : []);
      }
    } catch (err) {
      const status = err.response?.status;
      const code = err.response?.data?.error;

      if (status === 403 && code === '2fa_required') {
        setForbidden(false);
        setError('Esta operación requiere verificación 2FA. Verifica qué ruta la está solicitando.');
      } else if (status === 403 && code === 'invalid_2fa_code') {
        setForbidden(false);
        setError('El código 2FA es incorrecto o ha expirado.');
      } else if (status === 403) {
        setForbidden(true);
        setError('Tu cuenta no tiene permisos administrativos.');
      } else if (status === 401) {
        setError('La sesión no es válida. Inicia sesión nuevamente.');
      } else {
        setError(code || 'No se pudieron cargar los datos del panel.');
      }
    } finally {
      setLoading(false);
    }
  }, [isDashboard, isTransactions, isAudit]);

  useEffect(() => {
    loadData();
  }, [loadData]);

  useEffect(() => {
    document.documentElement.dataset.adminTheme = theme;
    try {
      localStorage.setItem('korzadi_admin_theme', theme);
    } catch {
      // El tema sigue funcionando aunque el almacenamiento esté bloqueado.
    }
    return () => {
      delete document.documentElement.dataset.adminTheme;
    };
  }, [theme]);

  async function blockUser(user) {
    if (!user?.id || workingId) return;

    const label = user.email || user.id;
    if (!window.confirm(`¿Confirmas bloquear la cuenta ${label}?\n\nSe revocarán las sesiones de esa cuenta. Esta acción requiere autorización del servidor.`)) {
      return;
    }

    setWorkingId(user.id);
    setNotice('');
    setError('');

    try {
      await api.post(`/api/admin/users/${encodeURIComponent(user.id)}/block`);
      setNotice(`Solicitud completada: ${label} ha sido bloqueado.`);
      await loadData();
    } catch (err) {
      setError(err.response?.data?.error || 'No se pudo bloquear la cuenta.');
    } finally {
      setWorkingId('');
    }
  }

  function logout() {
    clearTokens();
    window.location.href = '/login';
  }

  const title = titles[currentPath] || 'Panel administrativo';

  return (
    <div className="admin-shell" data-theme={theme}>
      <aside className={`admin-sidebar ${mobileMenu ? 'mobile-open' : ''}`}>
        <div className="admin-brand">
          <span className="admin-brand-mark">K</span>
          <span>Korzadi <b>Admin</b></span>
        </div>

        <div className="admin-nav-label">ADMINISTRACIÓN</div>
        <nav className="admin-nav">
          {routes.map(({ path, label, icon: Icon }) => (
            <Link
              key={path}
              to={path}
              onClick={() => setMobileMenu(false)}
              className={`admin-nav-link ${currentPath === path ? 'active' : ''}`}
            >
              <Icon size={19} />
              <span>{label}</span>
            </Link>
          ))}
        </nav>

        <div className="admin-sidebar-bottom">
          <div className="admin-secure-note">
            <ShieldCheck size={19} />
            <div>
              <strong>Acceso protegido</strong>
              <small>Permisos verificados por API</small>
            </div>
          </div>
          <Link to="/dashboard" className="admin-nav-link">
            <ArrowLeft size={18} /><span>Volver a la wallet</span>
          </Link>
          <button className="admin-nav-link admin-logout" onClick={logout}>
            <LogOut size={18} /><span>Cerrar sesión</span>
          </button>
        </div>
      </aside>

      <main className="admin-main">
        <header className="admin-topbar">
          <button
            className="admin-icon-button admin-mobile-toggle"
            onClick={() => setMobileMenu(!mobileMenu)}
            aria-label="Abrir menú"
          >
            <Menu size={20} />
          </button>
          <div className="admin-heading">
            <span>Kor­zadi Wallet / Administración</span>
            <h1>{title}</h1>
          </div>
          <div className="admin-top-actions">
            <span className="admin-live"><span /> Panel API</span>
            <button
              className="admin-icon-button"
              onClick={() => setTheme(theme === 'dark' ? 'light' : 'dark')}
              aria-label="Cambiar tema"
              title={theme === 'dark' ? 'Cambiar a tema claro' : 'Cambiar a tema oscuro'}
            >
              {theme === 'dark' ? <Sun size={19} /> : <Moon size={19} />}
            </button>
            <button
              className="admin-icon-button"
              onClick={loadData}
              aria-label="Actualizar datos"
              title="Actualizar datos"
            >
              <RefreshCw size={18} />
            </button>
          </div>
        </header>

        <div className="admin-content">
          {error && (
            <div className={`admin-alert ${forbidden ? 'warning' : 'error'}`}>
              <AlertCircle size={19} />
              <div>
                <strong>{forbidden ? 'Acceso restringido' : 'Aviso'}</strong>
                <p>{error}</p>
                {forbidden && <Link to="/dashboard">Volver a la wallet</Link>}
                {error.includes('sesión') && <Link to="/login">Ir al inicio de sesión</Link>}
              </div>
            </div>
          )}

          {notice && <div className="admin-alert success"><CheckCircle2 size={19} /><p>{notice}</p></div>}

          {!forbidden && (
            <>
              {isProfile && (
  <section className="admin-panel">
    <div className="admin-panel-heading">
      <div><h2>Perfil del administrador</h2><p>Cuenta autenticada en Korzadi Wallet.</p></div>
    </div>
    <div className="admin-profile-card">
      <div className="admin-profile-avatar">{profileUser.initials}</div>
      <div className="admin-profile-details">
        <span className="admin-eyebrow">CUENTA ACTUAL</span>
        <h2>{profileUser.name || 'Mi cuenta'}</h2>
        <p>{profileUser.email || 'Correo no disponible'}</p>
      </div>
    </div>
    <div className="admin-profile-actions">
      <Link className="admin-nav-link" to="/admin/security"><ShieldCheck size={18} /> Seguridad y 2FA</Link>
      <Link className="admin-nav-link" to="/dashboard"><ArrowLeft size={18} /> Volver a la wallet</Link>
    </div>
  </section>
)}

{isDashboard && (
                <>
                  <section className="admin-welcome">
                    <div>
                      <div className="admin-eyebrow">CENTRO DE CONTROL</div>
                      <h2>Estado de la plataforma</h2>
                      <p>Información obtenida de los endpoints administrativos del backend.</p>
                    </div>
                    <div className="admin-welcome-icon"><Activity size={27} /></div>
                  </section>

                  <section className="admin-metrics">
                    <article className="admin-metric">
                      <div className="admin-metric-top"><span>Usuarios cargados</span><Users size={19} /></div>
                      <strong>{loading ? '…' : users.length}</strong>
                      <small>Máximo 50 registros por consulta</small>
                    </article>
                    <article className="admin-metric">
                      <div className="admin-metric-top"><span>Transacciones recientes</span><Wallet size={19} /></div>
                      <strong>{loading ? '…' : transactions.length}</strong>
                      <small>Máximo 20 registros por consulta</small>
                    </article>
                    <article className="admin-metric">
                      <div className="admin-metric-top"><span>Eventos de auditoría</span><ShieldCheck size={19} /></div>
                      <strong>{loading ? '…' : audit.length}</strong>
                      <small>Máximo 20 registros por consulta</small>
                    </article>
                  </section>

                  <section className="admin-panel">
                    <div className="admin-panel-heading">
                      <div><h2>Transacciones recientes</h2><p>Actividad registrada en el ledger</p></div>
                      <Link to="/admin/transactions">Ver todas <ArrowLeft size={15} /></Link>
                    </div>
                    {loading ? <div className="admin-empty">Cargando datos…</div> : (
                      <Table
                        rows={transactions.slice(0, 8)}
                        columns={[
                          { key: 'id', label: 'ID', render: (r) => <code>{shortId(r.id)}</code> },
                          { key: 'type', label: 'Tipo' },
                          { key: 'status', label: 'Estado', render: (r) => <Status value={r.status} /> },
                          { key: 'created_at', label: 'Fecha', render: (r) => date(r.created_at) },
                        ]}
                      />
                    )}
                  </section>

                  <section className="admin-panel">
                    <div className="admin-panel-heading">
                      <div><h2>Auditoría reciente</h2><p>Acciones registradas por el sistema</p></div>
                      <Link to="/admin/audit">Ver auditoría <ArrowLeft size={15} /></Link>
                    </div>
                    {loading ? <div className="admin-empty">Cargando datos…</div> : (
                      <Table
                        rows={audit.slice(0, 6)}
                        columns={[
                          { key: 'action', label: 'Acción' },
                          { key: 'actor_role', label: 'Rol' },
                          { key: 'entity_type', label: 'Entidad' },
                          { key: 'created_at', label: 'Fecha', render: (r) => date(r.created_at) },
                        ]}
                      />
                    )}
                  </section>
                </>
              )}

              {isUsers && (
                <section className="admin-panel">
                  <div className="admin-panel-heading">
                    <div><h2>Usuarios registrados</h2><p>Listado del backend; se muestran hasta 50 cuentas.</p></div>
                    <span className="admin-count">{users.length} registros</span>
                  </div>
                  {loading ? <div className="admin-empty">Cargando usuarios…</div> : (
                    <Table
                      rows={users}
                      columns={[
                        { key: 'email', label: 'Correo' },
                        { key: 'first_name', label: 'Nombre', render: (r) => [r.first_name, r.last_name].filter(Boolean).join(' ') || '—' },
                        { key: 'status', label: 'Estado', render: (r) => <Status value={r.status} /> },
                        { key: 'created_at', label: 'Registro', render: (r) => date(r.created_at) },
                        { key: 'actions', label: 'Acciones', render: (r) => (
                          <button
                            className="admin-action-danger"
                            disabled={workingId === r.id || /block/i.test(String(r.status || ''))}
                            onClick={() => blockUser(r)}
                          >
                            <Ban size={15} /> {workingId === r.id ? 'Procesando…' : 'Bloquear'}
                          </button>
                        )},
                      ]}
                    />
                  )}
                  <p className="admin-footnote">El servidor impide bloquear tu propia cuenta y aplica restricciones adicionales a superadministradores.</p>
                </section>
              )}

              {isTransactions && (
                <section className="admin-panel">
                  <div className="admin-panel-heading">
                    <div><h2>Transacciones del ledger</h2><p>Las últimas 20 operaciones devueltas por la API.</p></div>
                    <span className="admin-count">{transactions.length} registros</span>
                  </div>
                  {loading ? <div className="admin-empty">Cargando transacciones…</div> : (
                    <Table
                      rows={transactions}
                      columns={[
                        { key: 'id', label: 'ID', render: (r) => <code>{shortId(r.id)}</code> },
                        { key: 'type', label: 'Tipo' },
                        { key: 'status', label: 'Estado', render: (r) => <Status value={r.status} /> },
                        { key: 'reference_id', label: 'Referencia', render: (r) => r.reference_id || '—' },
                        { key: 'created_at', label: 'Creación', render: (r) => date(r.created_at) },
                        { key: 'completed_at', label: 'Finalización', render: (r) => date(r.completed_at) },
                      ]}
                    />
                  )}
                </section>
              )}

              {isAudit && (
                <section className="admin-panel">
                  <div className="admin-panel-heading">
                    <div><h2>Registro de auditoría</h2><p>Los últimos 20 eventos devueltos por el backend.</p></div>
                    <span className="admin-count">{audit.length} registros</span>
                  </div>
                  {loading ? <div className="admin-empty">Cargando auditoría…</div> : (
                    <Table
                      rows={audit}
                      columns={[
                        { key: 'created_at', label: 'Fecha', render: (r) => date(r.created_at) },
                        { key: 'actor_role', label: 'Rol' },
                        { key: 'action', label: 'Acción' },
                        { key: 'entity_type', label: 'Tipo de entidad', render: (r) => r.entity_type || '—' },
                        { key: 'entity_id', label: 'ID entidad', render: (r) => <code>{shortId(r.entity_id)}</code> },
                        { key: 'ip', label: 'IP', render: (r) => r.ip || '—' },
                      ]}
                    />
                  )}
                </section>
              )}

              {loading && !error && <div className="admin-loading"><Clock3 size={17} /> Consultando API segura…</div>}
            </>
          )}
        </div>
        <footer className="admin-footer">Korzadi Wallet · Administración · Datos sujetos a permisos del servidor</footer>
      </main>
      {mobileMenu && <button className="admin-mobile-overlay" aria-label="Cerrar menú" onClick={() => setMobileMenu(false)} />}
    </div>
  );
}
