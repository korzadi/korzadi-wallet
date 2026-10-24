import { useEffect, useState } from 'react';
import { ShieldCheck } from 'lucide-react';
import { walletApi } from '../api/wallet';
import WalletLayout from '../components/WalletLayout';

const LABELS = {
  not_started: ['Sin verificar', 'Verifica tu identidad para aumentar tus límites y retirar fondos.'],
  pending: ['En revisión', 'Estamos revisando tus datos. Te avisaremos cuando termine.'],
  in_review: ['En revisión', 'Estamos revisando tus datos. Te avisaremos cuando termine.'],
  approved: ['Verificado', 'Tu identidad ya está verificada.'],
  verified: ['Verificado', 'Tu identidad ya está verificada.'],
  declined: ['Rechazado', 'No pudimos verificar tu identidad. Puedes intentarlo de nuevo.'],
  rejected: ['Rechazado', 'No pudimos verificar tu identidad. Puedes intentarlo de nuevo.'],
};

const COUNTRIES = [
  { code: 'AR', name: 'Argentina' },
  { code: 'BR', name: 'Brasil' },
  { code: 'CL', name: 'Chile' },
  { code: 'CO', name: 'Colombia' },
  { code: 'MX', name: 'México' },
  { code: 'PE', name: 'Perú' },
  { code: 'ES', name: 'España' },
  { code: 'US', name: 'Estados Unidos' },
  { code: 'GB', name: 'Reino Unido' },
  { code: 'FR', name: 'Francia' },
  { code: 'DE', name: 'Alemania' },
  { code: 'IT', name: 'Italia' },
  { code: 'PT', name: 'Portugal' },
  { code: 'EC', name: 'Ecuador' },
  { code: 'VE', name: 'Venezuela' },
  { code: 'UY', name: 'Uruguay' },
  { code: 'PY', name: 'Paraguay' },
  { code: 'BO', name: 'Bolivia' },
  { code: 'PA', name: 'Panamá' },
  { code: 'CR', name: 'Costa Rica' },
];

export default function KYC() {
  const [status, setStatus] = useState('');
  const [country, setCountry] = useState('');
  const [residence, setResidence] = useState('');
  const [loading, setLoading] = useState(true);
  const [starting, setStarting] = useState(false);
  const [error, setError] = useState('');

  const load = async () => {
    try {
      const { data } = await walletApi.kycStatus();
      setStatus(String(data?.status || data?.kyc_status || 'not_started').toLowerCase());
    } catch (e) {
      setError(e.response?.data?.error || 'No se pudo cargar el estado de la verificación');
    } finally { setLoading(false); }
  };
  useEffect(() => { load(); }, []);

  const start = async () => {
    if (!country && !residence) {
      setError('Selecciona tu país para continuar con la verificación.');
      return;
    }
    setStarting(true); setError('');
    try {
      const { data } = await walletApi.startKYC({
        ...(country ? { country } : {}),
        ...(residence ? { residence_country: residence } : {}),
      });
      if (data?.redirect_url) { window.location.href = data.redirect_url; return; }
      await load();
    } catch (e) {
      setError(e.response?.data?.error || 'No se pudo iniciar la verificación');
    } finally { setStarting(false); }
  };

  const [label, help] = LABELS[status] || [status || '—', ''];
  const verified = ['approved', 'verified'].includes(status);
  const inReview = ['pending', 'in_review'].includes(status);

  return (
    <WalletLayout title="Verificación KYC" tabs={false}>
      <div className="kz-card" style={{ textAlign: 'center' }}>
        <ShieldCheck size={44} color={verified ? '#18d98b' : '#3b8bff'} />
        <h2 style={{ margin: '10px 0 6px', fontSize: 18 }}>{loading ? 'Cargando…' : label}</h2>
        <div className="kz-hint" style={{ margin: 0 }}>{help}</div>
      </div>

      {!loading && !verified && !inReview && (
        <>
          <label className="kz-label">País de emisión de tu documento</label>
          <select className="kz-input" value={country} onChange={(e) => setCountry(e.target.value)}>
            <option value="">Selecciona un país</option>
            {COUNTRIES.map((c) => (
              <option key={c.code} value={c.code}>{c.name}</option>
            ))}
          </select>
          <label className="kz-label">País de residencia</label>
          <select className="kz-input" value={residence} onChange={(e) => setResidence(e.target.value)}>
            <option value="">Selecciona un país (opcional)</option>
            {COUNTRIES.map((c) => (
              <option key={c.code} value={c.code}>{c.name}</option>
            ))}
          </select>
          <button className="kz-btn" style={{ marginTop: 22 }} onClick={start} disabled={starting}>
            {starting ? 'Iniciando verificación…' : 'Iniciar verificación desde este dispositivo'}
          </button>
        </>
      )}
      {error && <div className="kz-err">{error}</div>}
    </WalletLayout>
  );
}
