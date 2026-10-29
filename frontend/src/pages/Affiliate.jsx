import { useEffect, useState } from 'react';
import WalletLayout from '../components/WalletLayout';
import { walletApi } from '../api/wallet';

export default function Affiliate() {
  const [data, setData] = useState(null);
  const [loading, setLoading] = useState(true);
  const [joining, setJoining] = useState(false);
  const [error, setError] = useState('');

  const load = () => {
    setLoading(true);
    setError('');
    walletApi.affiliate()
      .then(({ data }) => { setData(data || { enrolled: false }); })
      .catch((e) => { setError('No se pudo cargar la información de afiliados.'); console.error('Affiliate error:', e); })
      .finally(() => setLoading(false));
  };

  useEffect(load, []);

  async function enroll() {
    setJoining(true);
    setError('');
    try {
      await walletApi.affiliateEnroll();
      load();
    } catch (e) {
      setError(e.response?.data?.message || 'No se pudo activar el programa de afiliados.');
    } finally {
      setJoining(false);
    }
  }

  return (
    <WalletLayout title="Programa de afiliados">
      <div className="kz-card" style={{ padding: '0 14px' }}>
        <strong>Programa de afiliados</strong>
        {loading ? (
          <p style={{ color: '#9FB0D6', fontSize: 13 }}>Cargando...</p>
        ) : error ? (
          <p style={{ color: '#F87171', fontSize: 13 }}>{error}</p>
        ) : data?.enrolled ? (
          <div style={{ marginTop: 12 }}>
            <p style={{ color: '#F2F6FF', fontSize: 14 }}>Estado: {data.status || '—'}</p>
            <p style={{ color: '#9FB0D6', fontSize: 13, marginTop: 6 }}>Código: <span className="mono">{data.affiliate_code || '—'}</span></p>
            <p style={{ color: '#9FB0D6', fontSize: 13, marginTop: 6 }}>Referidos: {data.total_referrals ?? 0}</p>
            <p style={{ color: '#9FB0D6', fontSize: 13, marginTop: 6 }}>Comisiones: {data.total_commissions ?? '0'} USDT</p>
          </div>
        ) : (
          <>
            <p style={{ color: '#9FB0D6', fontSize: 13, marginTop: 8 }}>
              Aún no estás inscrito. Genera tu código y gana comisiones por referidos.
            </p>
            <button className="kz-btn" style={{ marginTop: 14 }} onClick={enroll} disabled={joining}>
              {joining ? 'Activando...' : 'Unirme al programa'}
            </button>
          </>
        )}
      </div>
    </WalletLayout>
  );
}
