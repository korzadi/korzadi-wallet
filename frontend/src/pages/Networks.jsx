import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Info } from 'lucide-react';
import { walletApi } from '../api/wallet';
import WalletLayout from '../components/WalletLayout';
import { NETWORKS, SOON_NETWORKS, getNetwork, setNetwork } from '../lib/user';

export default function Networks() {
  const nav = useNavigate();
  const [active, setActive] = useState(Object.keys(NETWORKS));
  const [sel, setSel] = useState(getNetwork());

  useEffect(() => {
    walletApi.networks().then(({ data }) => {
      const l = (data?.networks || []).map((n) => {
        const code = String(typeof n === 'string' ? n : n.network || n.code).toUpperCase();
        return code === 'ETHEREUM' ? 'ETH' : code;
      });
      if (l.length) setActive(l);
    }).catch(() => {});
  }, []);

  const pick = (k) => { setSel(k); setNetwork(k); nav(-1); };

  return (
    <WalletLayout title="Selecciona una red" tabs={false}>
      <div className="kz-card" style={{ padding: '0 14px' }}>
        {Object.entries(NETWORKS).filter(([k]) => active.includes(k)).map(([k, n]) => (
          <button key={k} className="kz-row" onClick={() => pick(k)}>
            <span className="kz-coin" style={{ background: n.color, width: 40, height: 40 }}>{n.label[0]}</span>
            <div className="main"><strong>{n.label}</strong><small>{n.assets}</small></div>
            <span className={`kz-radio ${sel === k ? 'on' : ''}`} />
          </button>
        ))}
        {SOON_NETWORKS.map((n) => (
          <div key={n.label} className="kz-row" style={{ opacity: 0.5, cursor: 'default' }}>
            <span className="kz-coin" style={{ background: n.color, width: 40, height: 40 }}>{n.label[0]}</span>
            <div className="main"><strong>{n.label}</strong><small>{n.assets}</small></div>
            <span className="kz-badge soon">Pronto</span>
          </div>
        ))}
      </div>
      <div className="kz-note"><Info size={18} style={{ flex: "none" }} />Asegúrate de seleccionar la misma red en el envío y en la recepción.</div>
    </WalletLayout>
  );
}
