import { useEffect, useMemo, useState } from 'react';
import { Search, ChevronRight } from 'lucide-react';
import api from '../api/client';
import WalletLayout from '../components/WalletLayout';
import Coin from '../components/Coin';
import { showAsset } from '../lib/user';

const STABLES = ['USD', 'USDT', 'USDC', 'BUSD', 'DAI'];
const money = (v) => Number(v || 0).toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 });
const qty = (v) => Number(v || 0).toLocaleString('en-US', { maximumFractionDigits: 8 });

export default function Assets() {
  const [balances, setBalances] = useState([]);
  const [q, setQ] = useState('');
  const [tab, setTab] = useState('all');

  useEffect(() => {
    api.get('/api/wallet/balances').then(({ data }) => setBalances(data?.balances || [])).catch(() => {});
  }, []);

  const list = useMemo(() => balances.filter((a) => {
    if (q && !showAsset(a.asset).toLowerCase().includes(q.toLowerCase())) return false;
    if (tab === 'stable') return STABLES.includes(a.asset);
    if (tab === 'crypto') return !STABLES.includes(a.asset);
    return true;
  }), [balances, q, tab]);

  return (
    <WalletLayout title="Mis activos" back={false}>
      <div className="kz-search">
        <Search size={17} />
        <input className="kz-input" placeholder="Buscar activo" value={q} onChange={(e) => setQ(e.target.value)} />
      </div>
      <div className="kz-pills">
        {[['all', 'Todos'], ['crypto', 'Criptomonedas'], ['stable', 'Stablecoins']].map(([k, l]) => (
          <button key={k} className={tab === k ? 'on' : ''} onClick={() => setTab(k)}>{l}</button>
        ))}
      </div>
      {list.length ? list.map((a) => (
        <div className="kz-row" key={a.asset}>
          <Coin code={showAsset(a.asset)} />
          <div className="main"><strong>{showAsset(a.asset)}</strong><small>${money(a.value_usdt)} USD</small></div>
          <div className="end"><strong>{qty(a.balance)}</strong></div>
          <ChevronRight size={18} color="#8da4c1" />
        </div>
      )) : <div className="kz-empty">No hay activos para mostrar.</div>}
    </WalletLayout>
  );
}
