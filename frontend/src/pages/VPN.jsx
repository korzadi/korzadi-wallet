import WalletLayout from '../components/WalletLayout';

export default function VPN() {
  return (
    <WalletLayout title="Integración VPN">
      <div className="kz-card" style={{ padding: '0 14px' }}>
        <strong>Korzadi VPN</strong>
        <p style={{ color: '#F2F6FF', fontSize: 14, marginTop: 8 }}>
          La integración de pagos VPN es un canal servidor-a-servidor firmado con HMAC.
          No se puede activar ni consultar desde el navegador.
        </p>
        <p style={{ color: '#9FB0D6', fontSize: 13, marginTop: 8 }}>
          Para contratar el servicio, usa la aplicación oficial de Korzadi VPN. El cobro se
          descuenta de tu saldo únicamente cuando el servicio envía una orden firmada y válida.
        </p>
      </div>
    </WalletLayout>
  );
}
