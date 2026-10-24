import { useState } from 'react';
import { Lock, ShieldCheck } from 'lucide-react';
import WalletLayout from '../components/WalletLayout';
import Coin from '../components/Coin';

export default function Buy() {
  const [tab, setTab] = useState('Tarjeta');
  const [amount, setAmount] = useState('50');
  return (
    <WalletLayout title="Comprar cripto">
      <div className="kz-seg">
        {['Tarjeta', 'P2P', 'Transferencia'].map((t) => (
          <button key={t} className={tab === t ? 'on' : ''} onClick={() => setTab(t)}>{t}</button>
        ))}
      </div>
      <span className="kz-label">Selecciona la criptomoneda</span>
      <div className="kz-select"><Coin code="USDT" /><div className="main"><strong>USDT (TRC20)</strong><small>Stablecoin</small></div></div>
      <span className="kz-label">Monto</span>
      <input className="kz-input" type="number" min="0" value={amount} onChange={(e) => setAmount(e.target.value)} />
      <div className="kz-hint">≈ {Number(amount || 0).toFixed(2)} USDT</div>
      <div className="kz-seg" style={{ marginTop: 14 }}>
        {[50, 100, 200, 500].map((v) => <button key={v} onClick={() => setAmount(String(v))}>${v}</button>)}
      </div>
      <div className="kz-note warn">La compra con {tab.toLowerCase()} estará disponible pronto.</div>
      <button className="kz-btn" disabled>Comprar</button>
      <div style={{ display: 'flex', justifyContent: 'space-around', marginTop: 18, color: '#8da4c1', fontSize: 12 }}>
        <span><Lock size={12} /> Pago seguro</span><span><ShieldCheck size={12} /> Procesado por terceros</span>
      </div>
    </WalletLayout>
  );
}
