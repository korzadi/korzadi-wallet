import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { ChevronDown, Filter, ShoppingBag, BadgeCheck } from 'lucide-react';
import { walletApi } from '../../api/wallet';
import WalletLayout from '../../components/WalletLayout';
import Coin from '../../components/Coin';

const ASSETS = [
  { code: 'USDT', name: 'Tether (USDT)', icon: '💲' },
  { code: 'BTC', name: 'Bitcoin (BTC)', icon: '₿' },
  { code: 'ETH', name: 'Ethereum (ETH)', icon: 'Ξ' },
];

const FIATS = [
  { code: 'ARS', name: 'Peso Argentino' },
  { code: 'USD', name: 'Dólar Estadounidense' },
  { code: 'EUR', name: 'Euro' },
  { code: 'BRL', name: 'Real Brasileño' },
  { code: 'MXN', name: 'Peso Mexicano' },
  { code: 'COP', name: 'Peso Colombiano' },
  { code: 'CLP', name: 'Peso Chileno' },
  { code: 'PEN', name: 'Sol Peruano' },
];

const PAYMENT_METHODS = [
  { id: 'bank_transfer', name: 'Transferencia bancaria' },
  { id: 'mercadopago', name: 'Mercado Pago' },
  { id: 'pix', name: 'PIX' },
  { id: 'sepa', name: 'SEPA' },
  { id: 'wise', name: 'Wise' },
  { id: 'cash', name: 'Efectivo' },
];

const COUNTRIES = [
  { code: 'AR', name: 'Argentina' },
  { code: 'BR', name: 'Brasil' },
  { code: 'CL', name: 'Chile' },
  { code: 'CO', name: 'Colombia' },
  { code: 'MX', name: 'México' },
  { code: 'PE', name: 'Perú' },
  { code: 'ES', name: 'España' },
  { code: 'US', name: 'Estados Unidos' },
  { code: 'VE', name: 'Venezuela' },
  { code: 'UY', name: 'Uruguay' },
];

export default function P2PMarket() {
  const [offers, setOffers] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [filters, setFilters] = useState({
    asset: '',
    fiat: '',
    side: '',
    paymentMethod: '',
    country: '',
    sort: 'price_asc',
  });

  const load = async () => {
    setLoading(true);
    setError('');
    try {
      const params = {};
      if (filters.asset) params.asset = filters.asset;
      if (filters.fiat) params.fiat = filters.fiat;
      if (filters.side) params.side = filters.side;
      const { data } = await walletApi.p2pOffers(params);
      setOffers(data || []);
    } catch (e) {
      setError(e.response?.data?.error || 'No se pudieron cargar las ofertas');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    load();
  }, [filters.asset, filters.fiat, filters.side]);

  const fmt = (n) => Number(n).toLocaleString('es-AR', { minimumFractionDigits: 2, maximumFractionDigits: 8 });

  return (
    <WalletLayout title="Mercado P2P" tabs={false}>
      <div style={{ display: 'flex', gap: 8, marginBottom: 16, flexWrap: 'wrap' }}>
        <Link to="/p2p/create-offer" className="kz-btn">+ Nueva oferta</Link>
        <Link to="/p2p/my-offers" className="kz-btn ghost">Mis ofertas</Link>
        <Link to="/p2p/my-orders" className="kz-btn ghost">Mis órdenes</Link>
      </div>
      <div className="kz-card" style={{ marginBottom: 20 }}>
        <h3 style={{ margin: '0 0 16px', fontSize: 16 }}>Filtros</h3>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(160px, 1fr))', gap: 12 }}>
          <select className="kz-input" value={filters.asset} onChange={e => setFilters({...filters, asset: e.target.value})}>
            <option value="">Todos los activos</option>
            {ASSETS.map(a => <option key={a.code} value={a.code}>{a.name}</option>)}
          </select>
          <select className="kz-input" value={filters.fiat} onChange={e => setFilters({...filters, fiat: e.target.value})}>
            <option value="">Todas las monedas</option>
            {FIATS.map(f => <option key={f.code} value={f.code}>{f.name}</option>)}
          </select>
          <select className="kz-input" value={filters.side} onChange={e => setFilters({...filters, side: e.target.value})}>
            <option value="">Comprar y Vender</option>
            <option value="buy">Comprar</option>
            <option value="sell">Vender</option>
          </select>
          <select className="kz-input" value={filters.paymentMethod} onChange={e => setFilters({...filters, paymentMethod: e.target.value})}>
            <option value="">Todos los métodos</option>
            {PAYMENT_METHODS.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}
          </select>
          <select className="kz-input" value={filters.country} onChange={e => setFilters({...filters, country: e.target.value})}>
            <option value="">Todos los países</option>
            {COUNTRIES.map(c => <option key={c.code} value={c.code}>{c.name}</option>)}
          </select>
        </div>
      </div>

      {error && <div className="kz-err" style={{ marginBottom: 16 }}>{error}</div>}

      {loading ? (
        <div className="kz-card" style={{ textAlign: 'center', padding: 40 }}>Cargando ofertas…</div>
      ) : offers.length === 0 ? (
        <div className="kz-card" style={{ textAlign: 'center', padding: 40 }}>
          <ShoppingBag size={48} color="#888" />
          <p style={{ marginTop: 12, color: '#888' }}>No hay ofertas disponibles con los filtros actuales</p>
          <Link to="/p2p/create-offer" className="kz-btn" style={{ marginTop: 16, display: 'inline-block' }}>Crear la primera oferta</Link>
        </div>
      ) : (
        <div style={{ display: 'grid', gap: 12 }}>
          {offers.map(offer => (
            <Link key={offer.id} to={`/p2p/offers/${offer.id}`} className="kz-card" style={{ textDecoration: 'none', color: 'inherit', display: 'block' }}>
              <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 8 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                  <span style={{ fontSize: 20 }}>{ASSETS.find(a => a.code === offer.asset_code)?.icon || '💲'}</span>
                  <strong>{offer.asset_code} / {offer.fiat_currency}</strong>
                  <span className={`kz-badge ${offer.side === 'buy' ? 'buy' : 'sell'}`}>
                    {offer.side === 'buy' ? 'Compro' : 'Vendo'}
                  </span>
                </div>
                <div style={{ textAlign: 'right' }}>
                  <div style={{ fontSize: 14, fontWeight: 600 }}>{fmt(offer.price_per_unit)} {offer.fiat_currency}</div>
                  <div className="kz-hint">por {offer.asset_code}</div>
                </div>
              </div>
              <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(120px, 1fr))', gap: 8, fontSize: 13, marginBottom: 8 }}>
                <div><span className="kz-hint">Límite</span><br/>{fmt(offer.min_amount)} - {fmt(offer.max_amount)} {offer.asset_code}</div>
                <div><span className="kz-hint">Disponible</span><br/><strong>{fmt(offer.available_amount)} {offer.asset_code}</strong></div>
                <div><span className="kz-hint">Método</span><br/>{(offer.payment_methods || []).slice(0,2).join(', ')}</div>
                <div><span className="kz-hint">País</span><br/>{(offer.country_restrictions || []).slice(0,2).join(', ')}</div>
              </div>
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', paddingTop: 8, borderTop: '1px solid #eee' }}>
                <span className="kz-hint">Por {offer.user_id?.slice(0,8)}…</span>
                <span className="kz-btn" style={{ padding: '6px 12px', fontSize: 13 }}>Ver oferta</span>
              </div>
            </Link>
          ))}
        </div>
      )}
    </WalletLayout>
  );
}