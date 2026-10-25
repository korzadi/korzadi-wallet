import { Bell, ChevronLeft, Home, WalletCards, ArrowLeftRight, UserRound, Handshake } from 'lucide-react';
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

  return (
    <div className="kz">
      {title ? (
        <header className="kz-top">
          {back && (
            <button className="kz-iconbtn" onClick={() => navigate(-1)} aria-label="Volver">
              <ChevronLeft size={22} />
            </button>
          )}
          <h1>{title}</h1>
        </header>
      ) : (
        <header className="kz-top">
          <div className="kz-brand"><span className="kz-logo">K</span>Korzadi <b>Wallet</b></div>
          <button className="kz-iconbtn" aria-label="Notificaciones"><Bell size={20} /></button>
          <Link to="/profile" className="kz-avatar sm">{user.initials}</Link>
        </header>
      )}

      <main>{children}</main>

      {tabs && (
        <nav className="kz-tabs">
          {nav.map(({ to, label, icon: Icon }) => (
            <Link key={to} to={to} className={pathname.startsWith(to) ? 'active' : ''}>
              <Icon size={21} /><span>{label}</span>
            </Link>
          ))}
        </nav>
      )}
    </div>
  );
}
