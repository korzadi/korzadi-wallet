import { useEffect, useState } from 'react';
import WalletLayout from '../components/WalletLayout';
import { walletApi } from '../api/wallet';

export default function Notifications() {
  const [notifications, setNotifications] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  useEffect(() => {
    walletApi.notifications()
      .then(({ data }) => { setNotifications(Array.isArray(data?.notifications) ? data.notifications : []); })
      .catch((e) => { setError('No se pudo cargar las notificaciones.'); console.error('Notifications error:', e); })
      .finally(() => setLoading(false));
  }, []);

  return (
    <WalletLayout title="Notificaciones">
      <div className="kz-card" style={{ padding: '0 14px' }}>
        <strong>Notificaciones</strong>
        {loading ? (
          <p style={{ color: '#9FB0D6', fontSize: 13 }}>Cargando...</p>
        ) : error ? (
          <p style={{ color: '#F87171', fontSize: 13 }}>{error}</p>
        ) : notifications.length === 0 ? (
          <p style={{ color: '#9FB0D6', fontSize: 13 }}>No hay notificaciones.</p>
        ) : (
          notifications.map((n, i) => (
            <div key={n.id || i} className="kz-row" style={{ marginTop: 10, alignItems: 'flex-start' }}>
              <div className="main">
                <strong>{n.title || 'Notificación'}</strong>
                {n.body && <div style={{ color: '#9FB0D6', fontSize: 13, marginTop: 3 }}>{n.body}</div>}
              </div>
              <span className="kz-badge">{n.read_at ? 'Leída' : 'Nueva'}</span>
            </div>
          ))
        )}
      </div>
    </WalletLayout>
  );
}
