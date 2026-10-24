import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { ChevronDown } from 'lucide-react';
import { walletApi } from '../api/wallet';
import WalletLayout from '../components/WalletLayout';
import Coin from '../components/Coin';
import { NETWORKS, getNetwork } from '../lib/user';

const errMsg = (e, d) => e.response?.data?.message || e.response?.data?.error || d;

export default function Send() {
  const network = getNetwork();
  const meta = NETWORKS[network] || { label: network, short: network, assets: '' };
  const asset = network === 'BTC' ? 'BTC' : 'USDT';
  const [balance, setBalance] = useState('0');
  const [address, setAddress] = useState('');
  const [amount, setAmount] = useState('');
  const [quote, setQuote] = useState(null);
  const [code, setCode] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [done, setDone] = useState(null);

  useEffect(() => {
    walletApi.balances().then(({ data }) => {
      const b = (data?.balances || []).find((x) => x.asset === asset || (asset === 'USDT' && x.asset === 'USD'));
      setBalance(b?.balance || '0');
    }).catch(() => {});
  }, [asset]);

  const continuar = async (e) => {
    e.preventDefault(); setBusy(true); setError('');
    try {
      const { data } = await walletApi.estimateFee({ network, asset, amount });
      setQuote(data);
    } catch (err) { setError(errMsg(err, 'No se pudo calcular la comisión')); }
    finally { setBusy(false); }
  };

  const confirmar = async () => {
    setBusy(true); setError('');
    try {
      const { data } = await walletApi.createWithdrawal({
        asset, network, amount, destination_address: address,
        ...(quote?.quote_id ? { fee_quote_id: quote.quote_id } : {}),
      }, code);
      setDone(data?.withdrawal || data);
    } catch (err) { setError(errMsg(err, 'No se pudo enviar. Revisa el código 2FA.')); }
    finally { setBusy(false); }
  };

  if (done) {
    return (
      <WalletLayout title="Enviar criptomonedas" back={false} tabs={false}>
        <div className="kz-ok">Envío registrado. Estado: {done.status || 'en proceso'}.</div>
        <Link to="/transactions" className="kz-btn">Ver transacciones</Link>
      </WalletLayout>
    );
  }

  return (
    <WalletLayout title="Enviar criptomonedas" tabs={false}>
      <Link to="/networks" className="kz-select">
        <Coin code={asset} />
        <div className="main"><strong>{asset} ({meta.short})</strong><small>Saldo: {balance} {asset}</small></div>
        <ChevronDown size={18} />
      </Link>

      {!quote ? (
        <form onSubmit={continuar}>
          <label className="kz-label">Dirección del destinatario</label>
          <input className="kz-input" value={address} onChange={(e) => setAddress(e.target.value.trim())} placeholder="Pega la dirección" required />
          <label className="kz-label">Monto</label>
          <input className="kz-input" type="number" step="any" min="0" value={amount} onChange={(e) => setAmount(e.target.value)} placeholder="0.00" required />
          <div className="kz-hint">Disponible: {balance} {asset}</div>
          {error && <div className="kz-err">{error}</div>}
          <button className="kz-btn" style={{ marginTop: 24 }} disabled={busy || !address || !amount}>{busy ? 'Calculando…' : 'Continuar'}</button>
        </form>
      ) : (
        <div style={{ marginTop: 16 }}>
          <div className="kz-card">
            <div className="kz-row"><div className="main">Enviarás</div><div className="end"><strong>{amount} {asset}</strong></div></div>
            <div className="kz-row"><div className="main">Comisión de red</div><div className="end"><strong>{quote.network_fee} {quote.fee_asset || ''}</strong></div></div>
            {quote.total_fee && <div className="kz-row"><div className="main">Comisión total</div><div className="end"><strong>{quote.total_fee}</strong></div></div>}
            <div className="kz-row"><div className="main">Destino</div><div className="end"><small style={{ wordBreak: 'break-all', maxWidth: 190 }}>{address}</small></div></div>
          </div>
          <label className="kz-label">Código 2FA</label>
          <input className="kz-input" inputMode="numeric" maxLength={6} value={code} onChange={(e) => setCode(e.target.value.replace(/\D/g, ''))} placeholder="000000" />
          {error && <div className="kz-err">{error}</div>}
          <button className="kz-btn" style={{ marginTop: 20 }} onClick={confirmar} disabled={busy || code.length < 6}>{busy ? 'Enviando…' : 'Confirmar envío'}</button>
          <button className="kz-btn ghost" style={{ marginTop: 10 }} onClick={() => setQuote(null)}>Editar</button>
        </div>
      )}
    </WalletLayout>
  );
}
