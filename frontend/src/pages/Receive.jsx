import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { ChevronDown, Copy, Check, Info } from 'lucide-react';
import { QRCodeSVG } from 'qrcode.react';
import { walletApi } from '../api/wallet';
import WalletLayout from '../components/WalletLayout';
import Coin from '../components/Coin';
import { NETWORKS, getNetwork } from '../lib/user';

export default function Receive() {
  const network = getNetwork();
  const meta = NETWORKS[network] || { label: network, short: network, assets: '' };
  const asset = network === 'BTC' ? 'BTC' : 'USDT';
  const [address, setAddress] = useState('');
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [copied, setCopied] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    (async () => {
      try {
        if (network === 'BSC') {
          const { data } = await walletApi.bscDepositAddress();
          setAddress(data?.address || data?.deposit_address || data?.wallet_address || '');
        } else {
          const { data } = await walletApi.addresses();
          const a = (data?.addresses || []).find((x) => String(x.network).toUpperCase() === network);
          setAddress(a?.address || '');
        }
      } catch { /* sin dirección todavía */ }
      setLoading(false);
    })();
  }, [network]);

  const create = async () => {
    setBusy(true); setError('');
    try {
      await walletApi.createAddress(network);
      const { data } = await walletApi.addresses();
      const a = (data?.addresses || []).find((x) => String(x.network).toUpperCase() === network);
      setAddress(a?.address || '');
    } catch (e) {
      setError(e.response?.data?.message || e.response?.data?.error || 'No se pudo crear la dirección');
    } finally { setBusy(false); }
  };

  const copy = async () => {
    await navigator.clipboard.writeText(address);
    setCopied(true); setTimeout(() => setCopied(false), 1500);
  };
  const share = () => (navigator.share ? navigator.share({ text: address }) : copy());

  return (
    <WalletLayout title="Recibir criptomonedas" tabs={false}>
      <Link to="/networks" className="kz-select">
        <Coin code={asset} />
        <div className="main"><strong>{asset} ({meta.short})</strong><small>{meta.assets}</small></div>
        <ChevronDown size={18} />
      </Link>

      {loading ? <div className="kz-empty">Cargando…</div> : address ? (
        <>
          <div className="kz-qr"><QRCodeSVG value={address} size={180} /></div>
          <span className="kz-label">Dirección de recepción</span>
          <div className="kz-addr">
            <span>{address}</span>
            <button className="kz-iconbtn" onClick={copy} aria-label="Copiar dirección">{copied ? <Check size={18} /> : <Copy size={18} />}</button>
          </div>
          <div className="kz-note"><Info size={18} style={{ flex: 'none' }} />Solo envía {asset} ({meta.short}) a esta dirección. Otras redes pueden perder tus fondos.</div>
          <button className="kz-btn ghost" onClick={share}>Compartir</button>
        </>
      ) : (
        <>
          <div className="kz-empty">Aún no tienes una dirección en esta red.</div>
          <button className="kz-btn" onClick={create} disabled={busy}>{busy ? 'Creando…' : 'Crear dirección'}</button>
        </>
      )}
      {error && <div className="kz-err">{error}</div>}
    </WalletLayout>
  );
}
