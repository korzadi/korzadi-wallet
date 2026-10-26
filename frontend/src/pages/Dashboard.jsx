import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { ArrowUpRight, ArrowDownToLine, CreditCard, MoreHorizontal, Eye, EyeOff, Handshake, ChevronRight } from 'lucide-react';
import api from '../api/client';
import WalletLayout from '../components/WalletLayout';
import Coin from '../components/Coin';
import { showAsset } from '../lib/user';

const money = (v, d = 2) => Number(v || 0).toLocaleString('en-US', { minimumFractionDigits: d, maximumFractionDigits: d });
const qty = (v) => Number(v || 0).toLocaleString('en-US', { maximumFractionDigits: 8 });

export default function Dashboard() {
  const [balances, setBalances] = useState([]);
  const [total, setTotal] = useState('0');
  const [partial, setPartial] = useState(false);
  const [loading, setLoading] = useState(true);
  const [hidden, setHidden] = useState(false);

  useEffect(() => {
    api.get('/api/wallet/balances')
      .then(({ data }) => {
        setBalances(data?.balances || []);
        setTotal(data?.total_usdt || '0');
        setPartial(data?.valuation_complete === false);
      })
      .catch(() => {})
      .finally(() => setLoading(false));
  }, []);

  const actions = [
    ['/send', 'Enviar', ArrowUpRight],
    ['/receive', 'Recibir', ArrowDownToLine],
    ['/buy', 'Comprar', CreditCard],
    ['/assets', 'Más', MoreHorizontal],
  ];

  return (
    <WalletLayout>
      <div className="kz-card hero-balance">
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 8 }}>
          <span className="kz-badge" style={{ background: '#0d3326', borderColor: '#14604a', color: '#7ff0bf' }}>Saldo total</span>
          <button className="kz-iconbtn" aria-label={hidden ? 'Mostrar saldo' : 'Ocultar saldo'} onClick={() => setHidden((h) => !h)} style={{ width: 36, height: 36 }}>
            {hidden ? <EyeOff size={18} /> : <Eye size={18} />}
          </button>
        </div>
        <h2 style={{ margin: '8px 0 4px', fontSize: 36, fontWeight: 800, letterSpacing: -1.2, color: '#fff' }}>
          {loading ? '—' : hidden ? '••••••' : <>$ {money(total)} <span style={{ fontSize: 16, fontWeight: 500, color: '#8da4c1' }}>USD</span></>}
        </h2>
        {partial && <p style={{ fontSize: 12, color: '#8da4c1', marginTop: 4 }}>Algunas cotizaciones no están disponibles; el total puede ser parcial.</p>}
      </div>

      <section style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(120px, 1fr))', gap: 12, margin: '18px 0 22px' }}>
        {actions.map(([to, label, Icon]) => (
          <Link key={to} to={to} style={{ textDecoration: 'none', color: 'inherit' }}>
            <button className="kz-btn" style={{ width: '100%', justifyContent: 'flex-start', gap: 12, padding: 14, background: '#0b2a52', borderColor: '#1B2B5E', color: '#F2F6FF' }}>
              <span style={{ display: 'grid', placeItems: 'center', width: 44, height: 44, borderRadius: '50%', background: '#111F44', flexShrink: 0 }}><Icon size={20} style={{ color: '#33B5FF' }} /></span>
              <span>{label}</span>
            </button>
          </Link>
        ))}
      </section>

      <Link to="/p2p" style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginTop: 12, textDecoration: 'none', color: 'inherit' }}>
        <div className="kz-card" style={{ flex: 1, marginRight: 8 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
            <span style={{ display: 'grid', placeItems: 'center', width: 40, height: 40, borderRadius: 12, background: '#111F44', flexShrink: 0, color: '#33B5FF' }}><Handshake size={20} /></span>
            <div>
              <h3 style={{ margin: 0, fontSize: 16, fontWeight: 700, color: '#F2F6FF' }}>Mercado P2P</h3>
              <p style={{ margin: 0, fontSize: 12, color: '#9FB0D6' }}>Compra y vende cripto con otros usuarios</p>
            </div>
          </div>
        </div>
        <ChevronRight size={18} color="#9FB0D6" />
      </Link>

      <header style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', margin: '24px 0 14px' }}>
        <h2 style={{ margin: 0, fontSize: 16, fontWeight: 700, color: '#F2F6FF' }}>Tus activos</h2>
        <Link to="/assets" style={{ fontSize: 13, color: '#33B5FF' }}>Ver todos ›</Link>
      </header>

      <div>
        {(balances.length ? balances.slice(0, 5) : [{ asset: 'USDT', balance: '0' }]).map((a) => (
          <div key={a.asset} className="kz-card" style={{ borderRadius: 14, padding: 14, marginBottom: 10, background: '#0B1633', borderColor: '#1B2B5E' }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
              <Coin code={showAsset(a.asset)} />
              <div style={{ flex: 1, minWidth: 0 }}>
                <strong style={{ display: 'block', fontSize: 15, fontWeight: 600, color: '#F2F6FF' }}>{showAsset(a.asset)}</strong>
                <small style={{ display: 'block', marginTop: 2, color: '#9FB0D6', fontSize: 12 }}>{hidden ? '••••' : `$${money(a.value_usdt)} USD`}</small>
              </div>
              <div style={{ textAlign: 'right' }}>
                <strong style={{ display: 'block', fontSize: 15, fontWeight: 600, color: '#F2F6FF' }}>{hidden ? '••••' : qty(a.balance)}</strong>
              </div>
            </div>
          </div>
        ))}
      </div>
    </WalletLayout>
  );
}
