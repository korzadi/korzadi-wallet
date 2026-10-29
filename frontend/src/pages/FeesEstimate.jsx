import { useState } from 'react';
import WalletLayout from '../components/WalletLayout';
import { walletApi } from '../api/wallet';

export default function FeesEstimate() {
  const [asset, setAsset] = useState('USDT');
  const [network, setNetwork] = useState('TRON');
  const [quote, setQuote] = useState(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  const handleEstimate = async () => {
    setLoading(true);
    setError('');
    try {
      const { data } = await walletApi.estimateFee({ operation: 'withdrawal', asset, network });
      setQuote(data || null);
    } catch (e) {
      setError('No se pudo estimar la tarifa.');
      console.error('Fee estimate error:', e);
    } finally {
      setLoading(false);
    }
  };

  return (
    <WalletLayout title="Estimación de tarifas">
      <div className="kz-card" style={{ padding: '0 14px' }}>
        <strong>Estimación de tarifas</strong>
        <div style={{ marginTop: 12 }}>
          <label style={{ color: '#9FB0D6', fontSize: 13 }}>Activo</label>
          <input className="kz-input" value={asset} onChange={(e) => setAsset(e.target.value)} />
        </div>
        <div style={{ marginTop: 8 }}>
          <label style={{ color: '#9FB0D6', fontSize: 13 }}>Red</label>
          <input className="kz-input" value={network} onChange={(e) => setNetwork(e.target.value)} />
        </div>
        <button className="kz-btn" style={{ marginTop: 14 }} onClick={handleEstimate} disabled={loading}>
          {loading ? 'Estimando...' : 'Estimar tarifa'}
        </button>
        {error && <p style={{ color: '#F87171', fontSize: 13, marginTop: 10 }}>{error}</p>}
        {quote && (
          <div style={{ marginTop: 14, padding: 14, borderRadius: 12, background: '#0B1B4A', borderColor: '#1B2B5E' }}>
            <strong style={{ color: '#F2F6FF', fontSize: 15 }}>{quote?.total_fee || '—'} USDT</strong>
            <p style={{ color: '#9FB0D6', fontSize: 13, marginTop: 4 }}>Comisión de servicio: {quote?.service_fee || '—'} USDT · Red: {quote?.network_fee || '—'} USDT</p>
          </div>
        )}
      </div>
    </WalletLayout>
  );
}
