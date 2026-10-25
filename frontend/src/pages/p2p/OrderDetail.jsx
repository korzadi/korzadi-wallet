import { useEffect, useState } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { Copy, CheckCircle, AlertCircle, Clock, MessageSquare, AlertTriangle, ShieldCheck, XCircle } from 'lucide-react';
import { walletApi } from '../../api/wallet';
import WalletLayout from '../../components/WalletLayout';
import { getUser } from '../../lib/user';

const STATUS_LABELS = {
  created: { label: 'Creada', color: '#888' },
  escrow_locked: { label: 'Escrow bloqueado', color: '#3b8bff' },
  payment_marked: { label: 'Pago marcado', color: '#f59e0b' },
  released: { label: 'Liberada', color: '#18d98b' },
  cancelled: { label: 'Cancelada', color: '#ef4444' },
  disputed: { label: 'En disputa', color: '#ef4444' },
  expired: { label: 'Expirada', color: '#888' },
};

const DISPUTE_REASONS = [
  { id: 'payment_not_received', label: 'No recibí el pago' },
  { id: 'payment_sent_not_confirmed', label: 'Envié el pago pero no lo confirman' },
  { id: 'wrong_amount', label: 'Monto incorrecto' },
  { id: 'terms_violation', label: 'Violación de términos' },
  { id: 'other', label: 'Otro motivo' },
];

export default function OrderDetail() {
  const { id } = useParams();
  const navigate = useNavigate();
  const [order, setOrder] = useState(null);
  const [messages, setMessages] = useState([]);
  const [loading, setLoading] = useState(true);
  const [actionLoading, setActionLoading] = useState(false);
  const [error, setError] = useState('');
  const [showDispute, setShowDispute] = useState(false);
  const [disputeReason, setDisputeReason] = useState('');
  const [disputeDesc, setDisputeDesc] = useState('');
  const [newMessage, setNewMessage] = useState('');

  const load = async () => {
    setLoading(true);
    try {
      const [o, m] = await Promise.all([
        walletApi.p2pGetOrder(id),
        walletApi.p2pMessages(id),
      ]);
      setOrder(o.data);
      setMessages(m.data || []);
    } catch (e) {
      setError(e.response?.data?.error || 'No se pudo cargar la orden');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => { load(); }, [id]);

  const fmt = (n) => Number(n).toLocaleString('es-AR', { minimumFractionDigits: 2, maximumFractionDigits: 8 });

  const copyToClipboard = (text, label) => {
    navigator.clipboard.writeText(text);
    alert(`${label} copiado`);
  };

  const handleAction = async (action) => {
    setActionLoading(true);
    setError('');
    try {
      if (action === 'pay') await walletApi.p2pMarkPaid(id);
      else if (action === 'release') await walletApi.p2pRelease(id);
      else if (action === 'cancel') await walletApi.p2pCancel(id);
      await load();
    } catch (e) {
      setError(e.response?.data?.error || `No se pudo ${action === 'pay' ? 'marcar el pago' : action === 'release' ? 'liberar' : 'cancelar'}`);
    } finally {
      setActionLoading(false);
    }
  };

  const handleDispute = async () => {
    if (!disputeReason) { setError('Selecciona un motivo'); return; }
    setActionLoading(true);
    try {
      await walletApi.p2pOpenDispute(id, { reason: disputeReason, description: disputeDesc });
      setShowDispute(false);
      await load();
    } catch (e) {
      setError(e.response?.data?.error || 'No se pudo abrir la disputa');
    } finally {
      setActionLoading(false);
    }
  };

  const sendMessage = async () => {
    if (!newMessage.trim()) return;
    setActionLoading(true);
    try {
      await walletApi.p2pSendMessage(id, newMessage);
      setNewMessage('');
      const { data } = await walletApi.p2pMessages(id);
      setMessages(data || []);
    } catch (e) {
      setError(e.response?.data?.error || 'No se pudo enviar');
    } finally {
      setActionLoading(false);
    }
  };

  if (loading) {
    return <WalletLayout title="Detalle de Orden" tabs={false}><div className="kz-card" style={{ textAlign: 'center', padding: 40 }}>Cargando…</div></WalletLayout>;
  }
  if (!order) return <WalletLayout title="Detalle de Orden" tabs={false}><div className="kz-err">Orden no encontrada</div></WalletLayout>;

  const st = STATUS_LABELS[order.status] || { label: order.status, color: '#888' };
  const currentUserId = getUser().id;
  const isBuyer = !!currentUserId && String(order.buyer_id) === String(currentUserId);
  const isSeller = !!currentUserId && String(order.seller_id) === String(currentUserId);

  const canPay = isBuyer && order.status === 'escrow_locked';
  const canRelease = isSeller && order.status === 'payment_marked';
  const canCancel = (isBuyer && order.status === 'escrow_locked') || (isSeller && ['escrow_locked', 'payment_marked'].includes(order.status));
  const canDispute = (isBuyer || isSeller) && ['escrow_locked', 'payment_marked'].includes(order.status);

  return (
    <WalletLayout title={`Orden ${id.slice(0, 8)}…`} tabs={false}>
      {error && <div className="kz-err" style={{ marginBottom: 16 }}>{error}</div>}

      <div className="kz-card" style={{ marginBottom: 16 }}>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 16 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
            <span className="kz-badge" style={{ background: st.color }}>{st.label}</span>
            <div>
              <div style={{ fontSize: 18, fontWeight: 600 }}>{order.asset_code} / {order.fiat_currency}</div>
              <div className="kz-hint">Orden #{order.id.slice(0, 12)}…</div>
            </div>
          </div>
        </div>

        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(160px, 1fr))', gap: 12 }}>
          <div><span className="kz-hint">Cantidad</span><br/><strong>{fmt(order.amount)} {order.asset_code}</strong></div>
          <div><span className="kz-hint">Precio unitario</span><br/>{fmt(order.price_per_unit)} {order.fiat_currency}</div>
          <div><span className="kz-hint">Total fiduciario</span><br/><strong>{fmt(order.fiat_amount)} {order.fiat_currency}</strong></div>
          <div><span className="kz-hint">Método</span><br/>{order.payment_method || '—'}</div>
        </div>

        {order.expires_at && (
          <div style={{ marginTop: 16, padding: 12, background: '#fffbeb', borderRadius: 8, display: 'flex', alignItems: 'center', gap: 8 }}>
            <Clock size={20} color="#f59e0b" />
            <div>
              <strong>Expira:</strong> {new Date(order.expires_at).toLocaleString('es-AR')}
              <div className="kz-hint">Si no hay acción, la orden expirará y el escrow se devolverá al vendedor</div>
            </div>
          </div>
        )}

        <div style={{ marginTop: 16 }}>
          <div className="kz-label">Instrucciones de pago</div>
          <div style={{ padding: 12, background: '#f8fafc', borderRadius: 8, whiteSpace: 'pre-wrap', fontFamily: 'monospace', fontSize: 13 }}>
            {order.payment_instructions || 'Sin instrucciones'}
          </div>
          <button className="kz-btn ghost" style={{ marginTop: 8, fontSize: 13 }} onClick={() => copyToClipboard(order.payment_instructions || '', 'Instrucciones')}>
            <Copy size={14} /> Copiar instrucciones
          </button>
        </div>

        {canDispute && (
          <div style={{ marginTop: 16, padding: 12, background: '#fef2f2', borderRadius: 8 }}>
            <button className="kz-btn" style={{ background: '#ef4444' }} onClick={() => setShowDispute(true)}>
              <AlertTriangle size={16} /> Abrir disputa
            </button>
          </div>
        )}

        <div style={{ marginTop: 16, display: 'flex', gap: 8, flexWrap: 'wrap' }}>
          {canPay && <button className="kz-btn" onClick={() => handleAction('pay')} disabled={actionLoading}>
            <CheckCircle size={16} /> Marcar pago como realizado
          </button>}
          {canRelease && <button className="kz-btn" style={{ background: '#18d98b' }} onClick={() => handleAction('release')} disabled={actionLoading}>
            <ShieldCheck size={16} /> Confirmar y liberar fondos
          </button>}
          {canCancel && <button className="kz-btn ghost" style={{ color: '#ef4444' }} onClick={() => handleAction('cancel')} disabled={actionLoading}>
            <XCircle size={16} /> Cancelar orden
          </button>}
        </div>
      </div>

      <div className="kz-card" style={{ marginBottom: 16 }}>
        <h3 style={{ margin: '0 0 16px', fontSize: 16 }}>Chat y comprobantes</h3>
        <div style={{ maxHeight: 300, overflowY: 'auto', marginBottom: 16 }}>
          {messages.length === 0 ? (
            <div className="kz-hint" style={{ textAlign: 'center', padding: 20 }}>Sin mensajes aún</div>
          ) : (
            messages.map(m => (
              <div key={m.id} style={{ marginBottom: 12, padding: 12, background: String(m.sender_id) === String(currentUserId) ? '#eef2ff' : '#f8fafc', borderRadius: 8 }}>
                <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 4, fontSize: 12, color: '#888' }}>
                  <span>{m.sender_id.slice(0, 8)}…</span>
                  <span>{new Date(m.created_at).toLocaleString('es-AR')}</span>
                </div>
                {m.message && <div style={{ whiteSpace: 'pre-wrap' }}>{m.message}</div>}
                {m.attachment_path && (
                  <a href={m.attachment_path} target="_blank" rel="noopener noreferrer" style={{ display: 'inline-flex', alignItems: 'center', gap: 6, marginTop: 8, color: '#3b8bff' }}>
                    📎 {m.attachment_name}
                  </a>
                )}
              </div>
            ))
          )}
        </div>
        <div style={{ display: 'flex', gap: 8 }}>
          <input className="kz-input" value={newMessage} onChange={e => setNewMessage(e.target.value)} placeholder="Escribe un mensaje..." style={{ flex: 1 }} onKeyDown={e => e.key === 'Enter' && sendMessage()} />
          <button className="kz-btn" onClick={sendMessage} disabled={actionLoading || !newMessage.trim()}>
            <MessageSquare size={16} /> Enviar
          </button>
        </div>
      </div>

      {showDispute && (
        <div className="kz-card" style={{ marginBottom: 16, border: '1px solid #ef4444' }}>
          <h3 style={{ margin: '0 0 16px', color: '#ef4444' }}>Abrir Disputa</h3>
          <select className="kz-input" value={disputeReason} onChange={e => setDisputeReason(e.target.value)} style={{ marginBottom: 12 }}>
            <option value="">Selecciona un motivo</option>
            {DISPUTE_REASONS.map(r => <option key={r.id} value={r.id}>{r.label}</option>)}
          </select>
          <textarea className="kz-input" value={disputeDesc} onChange={e => setDisputeDesc(e.target.value)} placeholder="Describe lo sucedido y adjunta evidencias si las tienes" rows={4} style={{ marginBottom: 12 }}></textarea>
          <div style={{ display: 'flex', gap: 8 }}>
            <button className="kz-btn ghost" onClick={() => setShowDispute(false)}>Cancelar</button>
            <button className="kz-btn" style={{ background: '#ef4444' }} onClick={handleDispute} disabled={actionLoading}>
              <AlertCircle size={16} /> Enviar disputa
            </button>
          </div>
        </div>
      )}
    </WalletLayout>
  );
}