import { useEffect, useState } from 'react';
import { RefreshCw, ArrowDownToLine } from 'lucide-react';
import { walletApi } from '../api/wallet';
import WalletLayout from '../components/WalletLayout';

const STATUS = { completed: 'Completado', confirmed: 'Confirmado', credited: 'Acreditado', pending: 'Pendiente', processing: 'Procesando', failed: 'Fallido', cancelled: 'Cancelado' };

export default function Deposits() {
  const [items, setItems] = useState([]);
  const [loading, setLoading] = useState(true);

  const load = async () => {
    setLoading(true);
    try {
      const { data } = await walletApi.cryptoDeposits({ limit: 50, offset: 0 });
      setItems(data?.deposits || []);
    } catch { setItems([]); }
    finally { setLoading(false); }
  };
  useEffect(() => { load(); }, []);

  return (
    <WalletLayout title="Depósitos">
      <button className="kz-btn ghost" style={{ marginBottom: 8, padding: 10 }} onClick={load}>
        <RefreshCw size={15} style={{ verticalAlign: -2 }} /> Actualizar
      </button>
      {loading ? <div className="kz-empty">Cargando…</div> : items.length === 0 ? (
        <div className="kz-empty">No hay depósitos todavía.</div>
      ) : items.map((x) => (
        <div className="kz-row" key={x.id}>
          <span className="kz-tx in"><ArrowDownToLine size={19} /></span>
          <div className="main">
            <strong>{x.asset_code} · {x.network}</strong>
            <small style={{ wordBreak: 'break-all' }}>{x.tx_hash ? `${x.tx_hash.slice(0, 10)}…${x.tx_hash.slice(-6)}` : 'Sin TXID'}</small>
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
