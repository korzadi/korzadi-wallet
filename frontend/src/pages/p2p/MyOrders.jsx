import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { ChevronRight } from 'lucide-react';
import { walletApi } from '../../api/wallet';
import WalletLayout from '../../components/WalletLayout';
import { getUser } from '../../lib/user';

const STATUS_LABELS = {
  created: { label: 'Creada', color: '#888' },
  escrow_locked: { label: 'Escrow bloqueado', color: '#3b8bff' },
  payment_marked: { label: 'Pago marcado', color: '#f59e0b' },
  released: { label: 'Liberada', color: '#18d98b' },
  cancelled: { label: 'Cancelada', color: '#ef4444' },
  disputed: { label: 'En disputa', color: '#ef4444' },
  expired: { label: 'Expirada', color: '#888' },
};

export default function MyOrders() {
  const [orders, setOrders] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [filter, setFilter] = useState('all');

  const load = async () => {
    setLoading(true);
    try {
      const { data } = await walletApi.p2pMyOrders();
      setOrders(data || []);
    } catch (e) {
      setError(e.response?.data?.error || 'No se pudieron cargar tus órdenes');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => { load(); }, []);

  const fmt = (n) => Number(n).toLocaleString('es-AR', { minimumFractionDigits: 2, maximumFractionDigits: 8 });

  const currentUserId = getUser().id;
  const filtered = orders.filter(o => filter === 'all' || o.status === filter);

  return (
    <WalletLayout title="Mis Órdenes P2P" tabs={false}>
      <div style={{ display: 'flex', gap: 8, marginBottom: 16, flexWrap: 'wrap' }}>
        {['all', 'created', 'escrow_locked', 'payment_marked', 'released', 'cancelled', 'disputed', 'expired'].map(f => (
          <button key={f} className={`kz-btn ${filter === f ? '' : 'ghost'}`} onClick={() => setFilter(f)} style={{ fontSize: 12, textTransform: 'capitalize' }}>
            {f === 'all' ? 'Todas' : f.replace('_', ' ')}
          </button>
        ))}
      </div>

      {error && <div className="kz-err" style={{ marginBottom: 16 }}>{error}</div>}

      {loading ? (
        <div className="kz-card" style={{ textAlign: 'center', padding: 40 }}>Cargando…</div>
      ) : filtered.length === 0 ? (
        <div className="kz-card" style={{ textAlign: 'center', padding: 40 }}>
          <p style={{ color: '#888', marginBottom: 16 }}>No tienes órdenes {filter !== 'all' ? `con estado "${filter}"` : ''}</p>
        </div>
      ) : (
        <div style={{ display: 'grid', gap: 12 }}>
          {filtered.map(order => {
            const st = STATUS_LABELS[order.status] || { label: order.status, color: '#888' };
            const role = currentUserId && String(order.buyer_id) === String(currentUserId) ? 'Comprador' : 'Vendedor';
            return (
              <Link key={order.id} to={`/p2p/orders/${order.id}`} className="kz-card" style={{ textDecoration: 'none', color: 'inherit', display: 'block', padding: 16 }}>
                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 8 }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                    <span className="kz-badge" style={{ background: st.color }}>{st.label}</span>
                    <span className="kz-badge">{role}</span>
                  </div>
                  <ChevronRight size={20} color="#888" />
                </div>
                <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(140px, 1fr))', gap: 12, fontSize: 13 }}>
                  <div><span className="kz-hint">Par</span><br/><strong>{order.asset_code} / {order.fiat_currency}</strong></div>
                  <div><span className="kz-hint">Cantidad</span><br/>{fmt(order.amount)} {order.asset_code}</div>
                  <div><span className="kz-hint">Total</span><br/>{fmt(order.fiat_amount)} {order.fiat_currency}</div>
                  <div><span className="kz-hint">Método</span><br/>{order.payment_method || '—'}</div>
                  <div><span className="kz-hint">Creada</span><br/>{new Date(order.created_at).toLocaleDateString('es-AR')}</div>
                  {order.expires_at && <div><span className="kz-hint">Expira</span><br/>{new Date(order.expires_at).toLocaleString('es-AR')}</div>}
                </div>
              </Link>
            );
          })}
        </div>
      )}
    </WalletLayout>
  );
}