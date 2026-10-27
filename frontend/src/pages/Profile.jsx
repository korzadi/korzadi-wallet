import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { ArrowDownToLine, ArrowUpRight, ChevronRight, HelpCircle, Info, Settings, ShieldCheck, UserCheck } from 'lucide-react';
import { clearTokens } from '../api/client';
import { walletApi } from '../api/wallet';
import { getUser } from '../lib/user';
import WalletLayout from '../components/WalletLayout';

export default function Profile() {
  const user = getUser();
  const [kyc, setKyc] = useState('');

  useEffect(() => {
    walletApi.kycStatus().then(({ data }) => setKyc(String(data?.status || '').toLowerCase())).catch((e) => { console.error("Frontend error:", e); });
  }, []);

  const logout = () => { clearTokens(); window.location.href = '/'; };
  const verified = ['approved', 'verified', 'completed'].includes(kyc);

  const Row = ({ to, icon: Icon, label, badge }) => {
    const inner = (<><Icon size={19} color="#8da4c1" /><div className="main"><strong>{label}</strong></div>{badge}<ChevronRight size={18} color="#8da4c1" /></>);
    return to ? <Link to={to} className="kz-row">{inner}</Link> : <div className="kz-row" style={{ opacity: 0.6 }}>{inner}</div>;
  };

  return (
    <WalletLayout title="Perfil" back={false}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 14, margin: '8px 0 20px' }}>
        <div className="kz-avatar">{user.initials}</div>
        <div><strong style={{ fontSize: 18 }}>{user.name}</strong><div className="kz-hint">{user.email}</div></div>
      </div>
      <div className="kz-card" style={{ padding: '0 14px' }}>
        <Row to="/security" icon={ShieldCheck} label="Seguridad" />
        <Row to="/kyc" icon={UserCheck} label="Verificación KYC" badge={verified && <span className="kz-badge">Verificado</span>} />
        <Row to="/deposits" icon={ArrowDownToLine} label="Depósitos" />
        <Row to="/withdrawals" icon={ArrowUpRight} label="Retiros" />
        <Row icon={Settings} label="Configuración" />
        <Row icon={HelpCircle} label="Soporte" />
        <Row icon={Info} label="Acerca de" />
      </div>
      <button className="kz-btn danger" onClick={logout}>Cerrar sesión</button>
    </WalletLayout>
  );
}
