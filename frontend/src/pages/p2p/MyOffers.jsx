import { useEffect, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { PauseCircle, PlayCircle, Trash2, Eye } from 'lucide-react';
import { walletApi } from '../../api/wallet';
import WalletLayout from '../../components/WalletLayout';

const STATUS_LABELS = {
  active: { label: 'Activa', color: '#18d98b' },
  paused: { label: 'Pausada', color: '#f59e0b' },
  cancelled: { label: 'Cancelada', color: '#ef4444' },
  closed: { label: 'Cerrada', color: '#888' },
};

export default function MyOffers() {
  const navigate = useNavigate();
  const [offers, setOffers] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [actionLoading, setActionLoading] = useState(null);

  const load = async () => {
    setLoading(true);
    try {
      const { data } = await walletApi.p2pMyOffers();
      setOffers(data || []);
    } catch (e) {
      setError(e.response?.data?.error || 'No se pudieron cargar tus ofertas');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => { load(); }, []);

  const fmt = (n) => Number(n).toLocaleString('es-AR', { minimumFractionDigits: 2, maximumFractionDigits: 8 });

  const handleAction = async (id, action, payload = {}) => {
    setActionLoading(id);
    try {
      await walletApi.p2pUpdateOffer(id, { ...payload, status: action });
      await load();
    } catch (e) {
      setError(e.response?.data?.error || `No se pudo ${action} la oferta`);
    } finally {
      setActionLoading(null);
    }
  };

  const handleDelete = async (id) => {
    if (!window.confirm('¿Cancelar esta oferta? No se puede deshacer si tiene órdenes activas.')) return;
    setActionLoading(id);
    try {
      await walletApi.p2pUpdateOffer(id, { status: 'cancelled' });
      await load();
    } catch (e) {
      setError(e.response?.data?.error || 'No se pudo cancelar');
    } finally {
      setActionLoading(null);
    }
  };

  if (loading) {
    return (
      <WalletLayout title="Mis Ofertas" tabs={false}>
        <div className="kz-card" style={{ textAlign: 'center', padding: 40 }}>Cargando…</div>
      </WalletLayout>
    );
  }

  return (
    <WalletLayout title="Mis Ofertas" tabs={false}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <h2 style={{ margin: 0 }}>Tus ofertas ({offers.length})</h2>
        <Link to="/p2p/create-offer" className="kz-btn">
          <span style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
            <span>+</span> Nueva oferta
          </span>
        </Link>
      </div>

      {error && <div className="kz-err" style={{ marginBottom: 16 }}>{error}</div>}

      {offers.length === 0 ? (
        <div className="kz-card" style={{ textAlign: 'center', padding: 40 }}>
          <p style={{ color: '#888', marginBottom: 16 }}>No tienes ofertas publicadas</p>
          <Link to="/p2p/create-offer" className="kz-btn">Crear tu primera oferta</Link>
        </div>
      ) : (
        <div style={{ display: 'grid', gap: 12 }}>
          {offers.map(offer => {
            const st = STATUS_LABELS[offer.status] || { label: offer.status, color: '#888' };
            const isLoading = actionLoading === offer.id;
            return (
              <div key={offer.id} className="kz-card" style={{ padding: 16 }}>
                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 12 }}>
                  <div>
                    <strong>{offer.asset_code} / {offer.fiat_currency}</strong>
                    <span className="kz-badge" style={{ background: st.color, marginLeft: 8 }}>
                      {offer.side === 'buy' ? 'Compro' : 'Vendo'}
                    </span>
                    <span className="kz-badge" style={{ background: st.color, marginLeft: 8 }}>
                      {st.label}
                    </span>
                  </div>
                  <div style={{ display: 'flex', gap: 8 }}>
                    <button className="kz-btn ghost" onClick={() => navigate(`/p2p/offers/${offer.id}`)} disabled={isLoading} title="Ver detalles">
                      <Eye size={16} />
                    </button>
                    {offer.status === 'active' && (
                      <button className="kz-btn ghost" onClick={() => handleAction(offer.id, 'paused')} disabled={isLoading} title="Pausar">
                        <PauseCircle size={16} />
                      </button>
                    )}
                    {offer.status === 'paused' && (
                      <button className="kz-btn ghost" onClick={() => handleAction(offer.id, 'active')} disabled={isLoading} title="Reactivar">
                        <PlayCircle size={16} />
                      </button>
                    )}
                    {(offer.status === 'active' || offer.status === 'paused') && (
                      <button className="kz-btn ghost" style={{ color: '#ef4444' }} onClick={() => handleDelete(offer.id)} disabled={isLoading} title="Cancelar">
                        <Trash2 size={16} />
                      </button>
                    )}
                  </div>
                </div>
                <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(140px, 1fr))', gap: 12, fontSize: 13 }}>
                  <div><span className="kz-hint">Precio</span><br/><strong>{fmt(offer.price_per_unit)} {offer.fiat_currency}</strong></div>
                  <div><span className="kz-hint">Límites</span><br/>{fmt(offer.min_amount)} - {fmt(offer.max_amount)} {offer.asset_code}</div>
                  <div><span className="kz-hint">Disponible</span><br/><strong>{fmt(offer.available_amount)}</strong> / {fmt(offer.total_amount)} {offer.asset_code}</div>
                  <div><span className="kz-hint">Métodos</span><br/>{(offer.payment_methods || []).join(', ')}</div>
                  <div><span className="kz-hint">Países</span><br/>{(offer.country_restrictions || []).join(', ')}</div>
                  <div><span className="kz-hint">Límite pago</span><br/>{offer.payment_time_limit_minutes} min</div>
                </div>
              </div>
            );
          })}
        </div>
      )}
    </WalletLayout>
  );
}