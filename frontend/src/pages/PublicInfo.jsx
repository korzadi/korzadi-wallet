import { useEffect, useState } from 'react';
import WalletLayout from '../components/WalletLayout';
import api from '../api/client';

export default function PublicInfo() {
  const [status, setStatus] = useState(null);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    api.get('/api/public/status')
      .then(({ data }) => setStatus(data || null))
      .catch(() => setError('No se pudo obtener el estado del servicio.'))
      .finally(() => setLoading(false));
  }, []);

  const services = Array.isArray(status?.services) ? status.services : [];

  return (
    <WalletLayout title="Información pública">
      <div className="kz-card" style={{ padding: '0 14px' }}>
        <strong>Estado del servicio</strong>
        {loading && <p style={{ color: '#9FB0D6', fontSize: 13, marginTop: 8 }}>Consultando...</p>}
        {error && <p style={{ color: '#F87171', fontSize: 13, marginTop: 8 }}>{error}</p>}
        {!loading && !error && services.length === 0 && (
          <p style={{ color: '#9FB0D6', fontSize: 13, marginTop: 8 }}>No hay información disponible.</p>
        )}
        {services.map((s) => (
          <div key={s.name} className="kz-row" style={{ marginTop: 10 }}>
            <div className="main"><strong>{s.name}</strong></div>
            <span className="kz-badge">{s.state}</span>
          </div>
        ))}
      </div>
    </WalletLayout>
  );
}
