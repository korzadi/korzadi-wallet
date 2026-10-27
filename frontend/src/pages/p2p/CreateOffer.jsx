import { useState, useEffect } from 'react';
import { useNavigate } from 'react-router-dom';
import { ArrowLeft, ShieldCheck } from 'lucide-react';
import { walletApi } from '../../api/wallet';
import WalletLayout from '../../components/WalletLayout';
import Coin from '../../components/Coin';

const ASSETS = [
  { code: 'USDT', name: 'Tether (USDT)' },
  { code: 'BTC', name: 'Bitcoin (BTC)' },
  { code: 'ETH', name: 'Ethereum (ETH)' },
];

const FIATS = [
  { code: 'ARS', name: 'Peso Argentino (ARS)' },
  { code: 'USD', name: 'Dólar Estadounidense (USD)' },
  { code: 'EUR', name: 'Euro (EUR)' },
  { code: 'BRL', name: 'Real Brasileño (BRL)' },
  { code: 'MXN', name: 'Peso Mexicano (MXN)' },
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

export default function CreateOffer() {
  const navigate = useNavigate();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [balance, setBalance] = useState('0');
  const [form, setForm] = useState({
    asset_code: 'USDT',
    fiat_currency: 'ARS',
    side: 'sell',
    price_per_unit: '',
    min_amount: '',
    max_amount: '',
    total_amount: '',
    payment_methods: ['bank_transfer'],
    country_restrictions: ['AR'],
    payment_time_limit_minutes: 30,
    instructions: '',
    terms: 'Al operar en este mercado P2P, aceptas los términos y condiciones de Korzadi Wallet.',
  });

  useEffect(() => {
    walletApi.balances().then(({ data }) => {
      const b = (data?.balances || []).find(x => x.asset === form.asset_code);
      setBalance(b?.balance || '0');
    }).catch((e) => { console.error("Frontend error:", e); });
  }, [form.asset_code]);

  const handleChange = (e) => {
    const { name, value, type, checked } = e.target;
    if (type === 'checkbox') {
      setForm(prev => {
        const arr = prev[name] || [];
        return { ...prev, [name]: checked ? [...arr, value] : arr.filter(v => v !== value) };
      });
    } else {
      setForm(prev => ({ ...prev, [name]: value }));
    }
  };

  const estimated = form.price_per_unit && form.total_amount ?
    (parseFloat(form.price_per_unit) * parseFloat(form.total_amount)).toLocaleString('es-AR', { minimumFractionDigits: 2 }) : '—';

  const submit = async (e) => {
    e.preventDefault();
    setError('');
    if (!form.price_per_unit || !form.min_amount || !form.max_amount || !form.total_amount) {
      setError('Completa todos los campos obligatorios');
      return;
    }
    if (parseFloat(form.min_amount) > parseFloat(form.max_amount)) {
      setError('El mínimo no puede ser mayor al máximo');
      return;
    }
    if (parseFloat(form.total_amount) < parseFloat(form.min_amount)) {
      setError('El total debe ser al menos el mínimo');
      return;
    }
    if (parseFloat(form.total_amount) > parseFloat(balance) && form.side === 'sell') {
      setError(`Saldo insuficiente. Disponible: ${parseFloat(balance).toLocaleString()} ${form.asset_code}`);
      return;
    }
    if (form.payment_methods.length === 0) {
      setError('Selecciona al menos un método de pago');
      return;
    }

    setLoading(true);
    try {
      await walletApi.p2pCreateOffer({
        asset_code: form.asset_code,
        fiat_currency: form.fiat_currency,
        side: form.side,
        price_per_unit: form.price_per_unit,
        min_amount: form.min_amount,
        max_amount: form.max_amount,
        total_amount: form.total_amount,
        payment_methods: form.payment_methods,
        country_restrictions: form.country_restrictions,
        payment_time_limit_minutes: form.payment_time_limit_minutes,
        instructions: form.instructions,
        terms: form.terms,
      });
      navigate('/p2p/my-offers');
    } catch (err) {
      setError(err.response?.data?.error || 'No se pudo crear la oferta');
    } finally {
      setLoading(false);
    }
  };

  return (
    <WalletLayout title="Crear Oferta P2P" tabs={false}>
      <div className="kz-card" style={{ marginBottom: 20 }}>
        <h3 style={{ margin: '0 0 16px', fontSize: 16 }}>Nueva Oferta</h3>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(180px, 1fr))', gap: 12, marginBottom: 16 }}>
          <select className="kz-input" name="asset_code" value={form.asset_code} onChange={handleChange}>
            {ASSETS.map(a => <option key={a.code} value={a.code}>{a.name}</option>)}
          </select>
          <select className="kz-input" name="fiat_currency" value={form.fiat_currency} onChange={handleChange}>
            {FIATS.map(f => <option key={f.code} value={f.code}>{f.name}</option>)}
          </select>
          <select className="kz-input" name="side" value={form.side} onChange={handleChange}>
            <option value="sell">Vender {form.asset_code}</option>
            <option value="buy">Comprar {form.asset_code}</option>
          </select>
        </div>

        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(140px, 1fr))', gap: 12, marginBottom: 16 }}>
          <input className="kz-input" type="number" step="any" name="price_per_unit" value={form.price_per_unit} onChange={handleChange} placeholder="Precio por unidad (ej: 1000)" required />
          <input className="kz-input" type="number" step="any" name="min_amount" value={form.min_amount} onChange={handleChange} placeholder="Mínimo por orden" required />
          <input className="kz-input" type="number" step="any" name="max_amount" value={form.max_amount} onChange={handleChange} placeholder="Máximo por orden" required />
          <input className="kz-input" type="number" step="any" name="total_amount" value={form.total_amount} onChange={handleChange} placeholder="Total a ofrecer" required />
        </div>

        <div className="kz-card" style={{ padding: 16, marginBottom: 16, background: '#f8fafc' }}>
          <div style={{ fontWeight: 600, marginBottom: 8 }}>Resumen estimado</div>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(140px, 1fr))', gap: 8, fontSize: 14 }}>
            <div><span className="kz-hint">Precio unitario</span><br/>{form.price_per_unit} {form.fiat_currency}</div>
            <div><span className="kz-hint">Total {form.asset_code}</span><br/><strong>{form.total_amount}</strong></div>
            <div><span className="kz-hint">Importe fiduciario</span><br/><strong>{estimated} {form.fiat_currency}</strong></div>
            <div><span className="kz-hint">Tu saldo {form.asset_code}</span><br/>{parseFloat(balance).toLocaleString()}</div>
          </div>
        </div>

        <div style={{ marginBottom: 16 }}>
          <label className="kz-label">Métodos de pago</label>
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8 }}>
            {PAYMENT_METHODS.map(pm => (
              <label key={pm.id} style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '8px 12px', border: '1px solid #ddd', borderRadius: 8, cursor: 'pointer', background: form.payment_methods.includes(pm.id) ? '#eef2ff' : 'white' }}>
                <input type="checkbox" name="payment_methods" value={pm.id} checked={form.payment_methods.includes(pm.id)} onChange={handleChange} />
                {pm.name}
              </label>
            ))}
          </div>
        </div>

        <div style={{ marginBottom: 16 }}>
          <label className="kz-label">Países permitidos</label>
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8 }}>
            {COUNTRIES.map(c => (
              <label key={c.code} style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '8px 12px', border: '1px solid #ddd', borderRadius: 8, cursor: 'pointer', background: form.country_restrictions.includes(c.code) ? '#eef2ff' : 'white' }}>
                <input type="checkbox" name="country_restrictions" value={c.code} checked={form.country_restrictions.includes(c.code)} onChange={handleChange} />
                {c.name}
              </label>
            ))}
          </div>
        </div>

        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(180px, 1fr))', gap: 12, marginBottom: 16 }}>
          <input className="kz-input" type="number" name="payment_time_limit_minutes" value={form.payment_time_limit_minutes} onChange={handleChange} placeholder="Límite de pago (min)" min="5" max="1440" />
          <textarea className="kz-input" name="instructions" value={form.instructions} onChange={handleChange} placeholder="Instrucciones de pago (CBU, alias, datos bancarios...)" rows={3} style={{ gridColumn: '1 / -1' }}></textarea>
        </div>

        <div style={{ marginBottom: 20 }}>
          <label style={{ display: 'flex', alignItems: 'flex-start', gap: 8, cursor: 'pointer', fontSize: 13 }}>
            <input type="checkbox" name="terms" checked={true} onChange={handleChange} required />
            <span className="kz-hint">Acepto los <strong>términos y condiciones</strong> del mercado P2P. Entiendo que los fondos serán bloqueados en escrow hasta que se complete la operación.</span>
          </label>
        </div>

        {error && <div className="kz-err" style={{ marginBottom: 16 }}>{error}</div>}

        <div style={{ display: 'flex', gap: 12 }}>
          <button className="kz-btn ghost" onClick={() => navigate(-1)} style={{ flex: 1 }}>Cancelar</button>
          <button className="kz-btn" onClick={submit} disabled={loading} style={{ flex: 1 }}>
            {loading ? 'Creando oferta…' : 'Publicar oferta'}
          </button>
        </div>
      </div>
    </WalletLayout>
  );
}