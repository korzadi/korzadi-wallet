import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { ArrowLeft, Send as SendIcon, CheckCircle2, Clock, XCircle } from 'lucide-react';
import api, { idempotencyKey } from '../api/client';

const STATUS_CONFIG = {
  completed: { label: 'Completado', icon: CheckCircle2, cls: 'completed' },
  pending: { label: 'Pendiente', icon: Clock, cls: 'pending' },
  processing: { label: 'Procesando', icon: Clock, cls: 'processing' },
  failed: { label: 'Fallido', icon: XCircle, cls: 'failed' },
  cancelled: { label: 'Cancelado', icon: XCircle, cls: 'cancelled' },
};

export default function Transfer() {
  const [receiverId, setReceiverId] = useState('');
  const [amount, setAmount] = useState('');
  const [note, setNote] = useState('');
  const [result, setResult] = useState(null);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  const navigate = useNavigate();

  async function handleSubmit(e) {
    e.preventDefault();
    setError('');
    setResult(null);
    setLoading(true);
    try {
      const { data } = await api.post(
        '/api/wallet/transfers',
        { recipient: receiverId, asset: 'USD', amount, note },
        { headers: { 'Idempotency-Key': idempotencyKey() } }
      );
      setResult(data);
      setAmount('');
      setNote('');
    } catch (err) {
      setError(err.response?.data?.message || 'No se pudo completar la transferencia');
    } finally {
      setLoading(false);
    }
  }

  const status = result ? STATUS_CONFIG[result.status] : null;
  const StatusIcon = status?.icon;

  return (
    <div className="page">
      <div className="topbar">
        <button className="icon-btn" onClick={() => navigate('/dashboard')}>
          <ArrowLeft size={18} />
        </button>
        <span className="topbar-title">Enviar dinero</span>
        <span style={{ width: 38 }} />
      </div>

      <div className="form-card">
        <form onSubmit={handleSubmit}>
          <div className="field">
            <label>ID de usuario destinatario</label>
            <input value={receiverId} onChange={(e) => setReceiverId(e.target.value)} required
              placeholder="UUID del destinatario" className="mono" style={{ fontSize: 13 }} />
          </div>
          <div className="field">
            <label>Monto (USD)</label>
            <input type="number" step="0.01" min="0.01" value={amount}
              onChange={(e) => setAmount(e.target.value)} required placeholder="0.00" />
          </div>
          <div className="field">
            <label>Nota (opcional)</label>
            <input value={note} onChange={(e) => setNote(e.target.value)} placeholder="Ej: cena del viernes" />
          </div>

          {error && <div className="alert alert-error">{error}</div>}

          {result && status && (
            <div style={{ marginTop: 16 }}>
              <span className={`status-badge ${status.cls}`}>
                <StatusIcon size={13} /> {status.label}
              </span>
            </div>
          )}

          <button type="submit" disabled={loading} className="btn-primary">
            {loading ? <span className="spinner" /> : <SendIcon size={17} />}
            {loading ? 'Enviando...' : 'Enviar'}
          </button>
        </form>
      </div>
    </div>
  );
}
