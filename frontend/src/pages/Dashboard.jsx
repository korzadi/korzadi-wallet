import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { ArrowUpRight, ArrowDownToLine, CreditCard, MoreHorizontal, Eye, EyeOff } from 'lucide-react';
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
    ['/send', 'Enviar', ArrowUpRight], ['/receive', 'Recibir', ArrowDownToLine],
    ['/buy', 'Comprar', CreditCard], ['/assets', 'Más', MoreHorizontal],
  ];

  return (
    <WalletLayout>
      <section className="kz-card kz-balance">
        <div className="row">
          <small>Saldo total</small>
          <button className="kz-iconbtn" onClick={() => setHidden((h) => !h)} aria-label={hidden ? 'Mostrar saldo' : 'Ocultar saldo'}>
            {hidden ? <EyeOff size={18} /> : <Eye size={18} />}
          </button>
        </div>
        <strong>{loading ? '—' : hidden ? '••••••' : <>$ {money(total)}<span>USD</span></>}</strong>
        {partial && <small>Algunas cotizaciones no están disponibles; el total puede ser parcial.</small>}
      </section>

      <div className="kz-actions">
        {actions.map(([to, label, Icon]) => (
          <Link key={to} to={to}><span><Icon size={22} /></span>{label}</Link>
        ))}
      </div>

      <div className="kz-sec"><h2>Tus activos</h2><Link to="/assets">Ver todos ›</Link></div>
      <div>
        {(balances.length ? balances.slice(0, 5) : [{ asset: 'USDT', balance: '0' }]).map((a) => (
          <div className="kz-row" key={a.asset}>
            <Coin code={showAsset(a.asset)} />
            <div className="main"><strong>{showAsset(a.asset)}</strong><small>{hidden ? '••••' : `$${money(a.value_usdt)} USD`}</small></div>
            <div className="end"><strong>{hidden ? '••••' : qty(a.balance)}</strong></div>
          </div>
        ))}
      </div>
    </WalletLayout>
  );
}
