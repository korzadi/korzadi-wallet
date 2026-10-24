import { useEffect, useState } from 'react';
import { RefreshCw, ArrowUpRight } from 'lucide-react';
import { walletApi } from '../api/wallet';
import WalletLayout from '../components/WalletLayout';

const STATUS = { completed: 'Completado', confirmed: 'Confirmado', credited: 'Acreditado', pending: 'Pendiente', processing: 'Procesando', failed: 'Fallido', cancelled: 'Cancelado' };

export default function Withdrawals() {
  const [items, setItems] = useState([]);
  const [loading, setLoading] = useState(true);

  const load = async () => {
    setLoading(true);
    try {
      const { data } = await walletApi.withdrawals({ limit: 50, offset: 0 });
      setItems(data?.withdrawals || []);
    } catch { setItems([]); }
    finally { setLoading(false); }
  };
  useEffect(() => { load(); }, []);

  return (
    <WalletLayout title="Retiros">
      <button className="kz-btn ghost" style={{ marginBottom: 8, padding: 10 }} onClick={load}>
        <RefreshCw size={15} style={{ verticalAlign: -2 }} /> Actualizar
      </button>
      {loading ? <div className="kz-empty">Cargando…</div> : items.length === 0 ? (
        <div className="kz-empty">No hay retiros todavía.</div>
      ) : items.map((x) => (
        <div className="kz-row" key={x.id}>
          <span className="kz-tx out"><ArrowUpRight size={19} /></span>
          <div className="main">
            <strong>{x.asset} · {x.network}</strong>
            <small style={{ wordBreak: 'break-all' }}>{x.destination_address ? `${x.destination_address.slice(0, 8)}…${x.destination_address.slice(-6)}` : 'Dirección protegida'}</small>
          </div>
          <div className="end">
            <strong>{x.amount}</strong>
            <small>{STATUS[String(x.status).toLowerCase()] || x.status}</small>
          </div>
        </div>
      ))}
    </WalletLayout>
  );
}
