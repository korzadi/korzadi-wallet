import React, { useEffect, useState } from 'react';
import { ArrowLeft } from 'lucide-react';
import { Link } from 'react-router-dom';
import { walletApi } from '../api/wallet';

export default function Withdraw() {
  const [networks, setNetworks] = useState([]);
  const [asset, setAsset] = useState('USDT');
  const [network, setNetwork] = useState('');
  const [amount, setAmount] = useState('');
  const [destination, setDestination] = useState('');
  const [twoFA, setTwoFA] = useState('');
  const [quote, setQuote] = useState(null);
  const [loading, setLoading] = useState(false);
  const [quoting, setQuoting] = useState(false);
  const [message, setMessage] = useState('');
  const [error, setError] = useState('');

  useEffect(() => {
    walletApi.networks()
      .then(r => {
        const list = r.data?.networks || [];
        setNetworks(list);
        if (list.length) {
          const first = list[0];
          setNetwork(typeof first === 'string' ? first : (first.network || first.code || first.name));
        }
      })
      .catch((e) => { console.error("Frontend error:", e); });
  }, []);

  const estimate = async () => {
    if (!amount || !network) return;
    setQuoting(true);
    setError('');
    try {
      const r = await walletApi.estimateFee({
        operation: 'withdrawal',
        network,
        asset,
        amount,
      });
      setQuote(r.data);
    } catch (e) {
      setError(e.response?.data?.error || 'No se pudo calcular la comisión');
    } finally {
      setQuoting(false);
    }
  };

  const submit = async (e) => {
    e.preventDefault();
    setLoading(true);
    setError('');
    setMessage('');

    try {
      const r = await walletApi.createWithdrawal({
        asset,
        network,
        amount,
        destination_address: destination,
        ...(quote?.quote_id ? { fee_quote_id: quote.quote_id } : {}),
      }, twoFA);

      setMessage(
        r.data?.notice ||
        `Retiro creado correctamente: ${r.data?.withdrawal?.id || ''}`
      );

      setAmount('');
      setDestination('');
      setTwoFA('');
    } catch (e) {
      setError(e.response?.data?.error || 'No se pudo crear el retiro');
    } finally {
      setLoading(false);
    }
  };

  return (
    <main className="wallet-page">
      <div className="wallet-header">
        <Link to="/dashboard"><ArrowLeft size={20} /> Volver</Link>
        <h1>Retirar</h1>
      </div>

      <section className="wallet-card">
        <form onSubmit={submit} className="wallet-form-column">
          <label>Activo</label>
          <input value={asset} onChange={e => setAsset(e.target.value.toUpperCase())} />

          <label>Red</label>
          <select value={network} onChange={e => setNetwork(e.target.value)}>
            <option value="">Selecciona una red</option>
            {networks.map((n, i) => {
              const value = typeof n === 'string' ? n : (n.network || n.code || n.name);
              const label = typeof n === 'string' ? n : (n.name || n.network || n.code);
              return <option key={i} value={value}>{label}</option>;
            })}
          </select>

          <label>Monto</label>
          <input
            type="number"
            min="0"
            step="any"
            value={amount}
            onChange={e => setAmount(e.target.value)}
            placeholder="0.00"
          />

          <label>Dirección de destino</label>
          <input
            value={destination}
            onChange={e => setDestination(e.target.value.trim())}
            placeholder="Dirección blockchain"
          />

          <button type="button" onClick={estimate} disabled={quoting || !amount || !network}>
            {quoting ? 'Calculando...' : 'Calcular comisión'}
          </button>

          {quote && (
            <div className="quote-box">
              <div>Comisión de servicio: <strong>{quote.service_fee}</strong></div>
              <div>Comisión de red: <strong>{quote.network_fee}</strong></div>
              <div>Total comisión: <strong>{quote.total_fee}</strong></div>
              <div>Válida hasta: <strong>{quote.expires_at}</strong></div>
            </div>
          )}

          <label>Código 2FA</label>
          <input
            inputMode="numeric"
            maxLength="8"
            value={twoFA}
            onChange={e => setTwoFA(e.target.value.replace(/\D/g, ''))}
            placeholder="Código de autenticación"
          />

          <button type="submit" disabled={loading || !amount || !destination || !network || !twoFA}>
            {loading ? 'Procesando...' : 'Confirmar retiro'}
          </button>
        </form>

        {message && <div className="success-box">{message}</div>}
        {error && <div className="error-box">{error}</div>}
      </section>
    </main>
  );
}
