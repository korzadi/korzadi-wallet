import { useEffect, useState } from 'react';
import { ArrowDownToLine, ArrowUpRight, ShoppingCart } from 'lucide-react';
import api from '../api/client';
import WalletLayout from '../components/WalletLayout';

const isBuy = (t) => /buy|purchase|compra/i.test(`${t.type || ''}${t.kind || ''}`);
const fmtDate = (d) => (d ? new Date(d).toLocaleDateString('es', { day: 'numeric', month: 'short', year: 'numeric' }) : '');

export default function Transactions() {
  const [items, setItems] = useState([]);
  const [tab, setTab] = useState('all');

  useEffect(() => {
    api.get('/api/wallet/transactions').then(({ data }) => setItems(data?.transactions || [])).catch((e) => { console.error("Frontend error:", e); });
  }, []);

  const shown = items.filter((t) => {
    const n = Number(t.amount);
    if (tab === 'out') return n < 0 && !isBuy(t);
    if (tab === 'in') return n >= 0 && !isBuy(t);
    if (tab === 'buy') return isBuy(t);
    return true;
  });

  return (
    <WalletLayout title="Transacciones" back={false}>
      <div className="kz-pills">
        {[['all', 'Todas'], ['out', 'Enviadas'], ['in', 'Recibidas'], ['buy', 'Compras']].map(([k, l]) => (
          <button key={k} className={tab === k ? 'on' : ''} onClick={() => setTab(k)}>{l}</button>
        ))}
      </div>
      {shown.length ? shown.map((t) => {
        const n = Number(t.amount); const buy = isBuy(t); const pos = n >= 0;
        const Icon = buy ? ShoppingCart : pos ? ArrowDownToLine : ArrowUpRight;
        return (
          <div className="kz-row" key={t.id}>
            <span className={`kz-tx ${buy ? 'buy' : pos ? 'in' : 'out'}`}><Icon size={19} /></span>
            <div className="main"><strong>{buy ? 'Compra' : pos ? 'Recibido' : 'Enviado'}</strong><small>{fmtDate(t.created_at)}</small></div>
            <div className="end"><strong className={pos ? 'kz-pos' : 'kz-neg'}>{pos ? '+' : ''}{t.amount} {t.asset || 'USDT'}</strong></div>
          </div>
        );
      }) : <div className="kz-empty">Todavía no tienes movimientos.</div>}
    </WalletLayout>
  );
}
