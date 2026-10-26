import React, { useState } from 'react';
import { Bell, ChevronLeft, Home, WalletCards, ArrowLeftRight, UserRound, Handshake, Sun, Moon, Monitor } from 'lucide-react';
import { Link, useLocation, useNavigate } from 'react-router-dom';
import { getUser } from '../lib/user';

const nav = [
  { to: '/dashboard', label: 'Inicio', icon: Home },
  { to: '/assets', label: 'Activos', icon: WalletCards },
  { to: '/p2p', label: 'P2P', icon: Handshake },
  { to: '/transactions', label: 'Transacciones', icon: ArrowLeftRight },
  { to: '/profile', label: 'Perfil', icon: UserRound },
];

export default function WalletLayout({ title, back = true, tabs = true, children }) {
  const { pathname } = useLocation();
  const navigate = useNavigate();
  const user = getUser();
  const [themeOpen, setThemeOpen] = useState(false);

  return (
    <div className="wallet-shell">
      {tabs && (
        <aside className="wallet-sidebar" aria-label="Navegación principal">
          <div className="sidebar-brand">
            <span className="kz-logo" style={{ width: 28, height: 28, borderRadius: 8, background: 'linear-gradient(135deg,#1468ff,#19c3ff)', fontWeight: 800, fontSize: 16, display: 'grid', placeItems: 'center' }}>K</span>
            <strong style={{ fontSize: 15, color: '#F2F6FF' }}>Korzadi</strong>
          </div>
          <nav className="sidebar-nav" aria-label="Navegación">
            {nav.map(({ to, label, icon: Icon }) => (
              <Link key={to} to={to} className={`sidebar-item ${pathname.startsWith(to) ? 'active' : ''}`} aria-current={pathname.startsWith(to) ? 'page' : undefined}>
                <Icon size={20} />
                <span>{label}</span>
              </Link>
            ))}
          </nav>
          <div className="sidebar-bottom" style={{ marginTop: 'auto', display: 'flex', flexDirection: 'column', gap: 7 }}>
            <Link to="/security" className="sidebar-action" style={{ textDecoration: 'none', color: '#fff', background: '#1468ff', minHeight: 43, borderRadius: 12, fontSize: 12, fontWeight: 600, padding: '0 13px' }}>
              <Sun size={16} /> Seguridad
            </Link>
          </div>
        </aside>
      )}
      <div className="wallet-main">
        <div className="kz">
          {title ? (
            <header className="wa-top">
              {back && (
                <button className="kz-iconbtn" onClick={() => navigate(-1)} aria-label="Volver">
                  <ChevronLeft size={22} />
                </button>
              )}
              <h1>{title}</h1>
            </header>
          ) : (
            <header className="wa-top">
              <div className="wa-brand"><span className="kz-logo">K</span>Korzadi</div>
              <button className="kz-iconbtn" aria-label="Notificaciones"><Bell size={20} /></button>
              <button className="kz-iconbtn" aria-label="Tema" onClick={() => setThemeOpen(!themeOpen)}><Sun size={20} /></button>
              <Link to="/profile" className="kz-avatar sm">{user.initials}</Link>
            </header>
          )}

          <main>{children}</main>

          {themeOpen && (
            <div className="kz-card" style={{ position: 'fixed', top: 70, right: 16, zIndex: 60, width: 200, padding: 12, background: '#0B1633', borderColor: '#1B2B5E', boxShadow: '0 20px 60px rgba(0,0,0,.5)' }}>
              <h4 style={{ margin: '0 0 10px', fontSize: 13, color: '#9FB0D6' }}>Apariencia</h4>
              <button className="kz-btn ghost" style={{ width: '100%', justifyContent: 'flex-start', gap: 10, padding: 10, fontSize: 13, marginBottom: 6, background: 'transparent', borderColor: '#1B2B5E', color: '#F2F6FF' }} onClick={() => { document.documentElement.setAttribute('data-theme', 'light'); localStorage.setItem('kz-th', 'light'); setThemeOpen(false); }}>
                <Sun size={16} style={{ color: '#FBBF24' }} /> Claro
              </button>
              <button className="kz-btn ghost" style={{ width: '100%', justifyContent: 'flex-start', gap: 10, padding: 10, fontSize: 13, marginBottom: 6, background: 'transparent', borderColor: '#1B2B5E', color: '#F2F6FF' }} onClick={() => { document.documentElement.setAttribute('data-theme', 'dark'); localStorage.setItem('kz-th', 'dark'); setThemeOpen(false); }}>
                <Moon size={16} style={{ color: '#33B5FF' }} /> Oscuro
              </button>
              <button className="kz-btn ghost" style={{ width: '100%', justifyContent: 'flex-start', gap: 10, padding: 10, fontSize: 13, background: 'transparent', borderColor: '#1B2B5E', color: '#F2F6FF' }} onClick={() => { document.documentElement.removeAttribute('data-theme'); localStorage.setItem('kz-th', 'auto'); setThemeOpen(false); }}>
                <Monitor size={16} style={{ color: '#34D399' }} /> Automático
              </button>
            </div>
          )}

          {tabs && (
            <nav className="kz-tabs" aria-label="Navegación móvil">
              {nav.map(({ to, label, icon: Icon }) => (
                <Link key={to} to={to} className={pathname.startsWith(to) ? 'active' : ''} aria-current={pathname.startsWith(to) ? 'page' : undefined}>
                  <Icon size={21} /><span>{label}</span>
                </Link>
              ))}
            </nav>
          )}
        </div>
      </div>
    </div>
  );
}
