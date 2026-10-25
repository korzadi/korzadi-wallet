import { useEffect, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { ShieldCheck } from 'lucide-react';
import { walletApi } from '../../api/wallet';
import WalletLayout from '../../components/WalletLayout';
import { getUser } from '../../lib/user';

const PAYMENT_LABELS = {
  bank_transfer: 'Transferencia bancaria',
  mercadopago: 'Mercado Pago',
  pix: 'PIX',
  sepa: 'SEPA',
  wise: 'Wise',
  cash: 'Efectivo',
};

const fmt = (n) => Number(n || 0).toLocaleString('es-AR', { minimumFractionDigits: 2, maximumFractionDigits: 8 });

export default function P2POfferDetail() {
  const { id } = useParams();
  const navigate = useNavigate();
  const currentUser = getUser();
  const [offer, setOffer] = useState(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [amount, setAmount] = useState('');
  const [paymentMethod, setPaymentMethod] = useState('');
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    let active = true;
    setLoading(true);
    walletApi.p2pGetOffer(id)
      .then(({ data }) => {
        if (!active) return;
        setOffer(data);
        setPaymentMethod(data?.payment_methods?.[0] || '');
      })
      .catch((e) => {
        if (active) setError(e.response?.data?.error || 'No se pudo cargar la oferta');
      })
      .finally(() => active && setLoading(false));
    return () => { active = false; };
  }, [id]);

  const isOwnOffer = !!offer && !!currentUser.id && String(offer.user_id) === String(currentUser.id);
  const amountNum = parseFloat(amount);
  const priceNum = offer ? parseFloat(offer.price_per_unit) : 0;
  const fiatTotal = Number.isFinite(amountNum) && Number.isFinite(priceNum) ? amountNum * priceNum : 0;

  const submit = async (e) => {
    e.preventDefault();
    setError('');
    if (!Number.isFinite(amountNum) || amountNum <= 0) {
      setError('Introduce una cantidad válida');
      return;
    }
    if (amountNum < parseFloat(offer.min_amount) || amountNum > parseFloat(offer.max_amount)) {
      setError(`La cantidad debe estar entre ${fmt(offer.min_amount)} y ${fmt(offer.max_amount)} ${offer.asset_code}`);
      return;
    }
    if (amountNum > parseFloat(offer.available_amount)) {
      setError('La cantidad supera el disponible de la oferta');
      return;
    }
    if (!paymentMethod) {
      setError('Selecciona un método de pago');
      return;
    }
    setSubmitting(true);
    try {
      const { data } = await walletApi.p2pCreateOrder({
        offer_id: offer.id,
        amount: String(amountNum),
        payment_method: paymentMethod,
      });
      navigate(`/p2p/orders/${data.id}`);
    } catch (err) {
      setError(err.response?.data?.error || 'No se pudo crear la orden');
    } finally {
      setSubmitting(false);
    }
  };

  if (loading) {
    return <WalletLayout title="Oferta P2P" tabs={false}><div className="kz-card" style={{ textAlign: 'center', padding: 40 }}>Cargando…</div></WalletLayout>;
  }

  if (!offer) {
    return (
      <WalletLayout title="Oferta P2P" tabs={false}>
        <div className="kz-card" style={{ textAlign: 'center', padding: 40 }}>
          <p className="kz-hint">La oferta no está disponible.</p>
          <button className="kz-btn" onClick={() => navigate('/p2p/market')} style={{ marginTop: 16 }}>Volver al mercado</button>
        </div>
      </WalletLayout>
    );
  }

  return (
    <WalletLayout title="Oferta P2P" tabs={false}>
      <div className="kz-card" style={{ marginBottom: 16 }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 12 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <strong>{offer.asset_code} / {offer.fiat_currency}</strong>
            <span className={`kz-badge ${offer.side === 'buy' ? 'buy' : 'sell'}`}>
              {offer.side === 'buy' ? 'Compro' : 'Vendo'}
            </span>
          </div>
          <div style={{ textAlign: 'right' }}>
            <div style={{ fontWeight: 600 }}>{fmt(offer.price_per_unit)} {offer.fiat_currency}</div>
            <div className="kz-hint">por {offer.asset_code}</div>
          </div>
        </div>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(140px, 1fr))', gap: 12, fontSize: 13 }}>
          <div><span className="kz-hint">Límites</span><br/>{fmt(offer.min_amount)} - {fmt(offer.max_amount)} {offer.asset_code}</div>
          <div><span className="kz-hint">Disponible</span><br/><strong>{fmt(offer.available_amount)} {offer.asset_code}</strong></div>
          <div><span className="kz-hint">Métodos</span><br/>{(offer.payment_methods || []).join(', ') || '—'}</div>
          <div><span className="kz-hint">Países</span><br/>{(offer.country_restrictions || []).join(', ') || '—'}</div>
          <div><span className="kz-hint">Límite de pago</span><br/>{offer.payment_time_limit_minutes} min</div>
        </div>
        {offer.instructions && (
          <div style={{ marginTop: 12, fontSize: 13 }}>
            <span className="kz-hint">Instrucciones del vendedor</span>
            <p style={{ margin: '4px 0 0' }}>{offer.instructions}</p>
          </div>
        )}
      </div>

      {isOwnOffer ? (
        <div className="kz-card" style={{ textAlign: 'center', padding: 32 }}>
          <ShieldCheck size={32} color="#888" />
          <p className="kz-hint" style={{ marginTop: 8 }}>No puedes operar con tu propia oferta.</p>
          <button className="kz-btn" onClick={() => navigate('/p2p/my-offers')} style={{ marginTop: 16 }}>Ir a mis ofertas</button>
        </div>
      ) : (
        <div className="kz-card">
          <h3 style={{ margin: '0 0 16px', fontSize: 16 }}>Crear orden</h3>
          <form onSubmit={submit}>
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(180px, 1fr))', gap: 12, marginBottom: 12 }}>
              <label className="kz-label">
                Cantidad ({offer.asset_code})
                <input
                  className="kz-input"
                  type="number"
                  step="any"
                  value={amount}
                  onChange={(e) => setAmount(e.target.value)}
                  placeholder={`Entre ${fmt(offer.min_amount)} y ${fmt(offer.max_amount)}`}
                  min={offer.min_amount}
                  max={offer.max_amount}
                  required
                />
              </label>
              <label className="kz-label">
                Método de pago
                <select className="kz-input" value={paymentMethod} onChange={(e) => setPaymentMethod(e.target.value)} required>
                  <option value="">Selecciona un método</option>
                  {(offer.payment_methods || []).map((pm) => (
                    <option key={pm} value={pm}>{PAYMENT_LABELS[pm] || pm}</option>
                  ))}
                </select>
              </label>
            </div>
            <div className="kz-card" style={{ padding: 12, background: '#f8fafc', marginBottom: 12, fontSize: 14 }}>
              <span className="kz-hint">Total a pagar</span><br/>
              <strong>{fmt(fiatTotal)} {offer.fiat_currency}</strong>
            </div>
            {error && <div className="kz-err" style={{ marginBottom: 12 }}>{error}</div>}
            <div style={{ display: 'flex', gap: 12 }}>
              <button type="button" className="kz-btn ghost" onClick={() => navigate(-1)} style={{ flex: 1 }}>Cancelar</button>
              <button type="submit" className="kz-btn" disabled={submitting} style={{ flex: 1 }}>
                {submitting ? 'Creando orden…' : 'Comprar / Reservar escrow'}
              </button>
            </div>
          </form>
        </div>
      )}
    </WalletLayout>
  );
}
