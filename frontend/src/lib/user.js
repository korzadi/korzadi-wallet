// Solo para mostrar nombre/iniciales: decodifica el JWT sin verificarlo.
export function getUser() {
  let u = {};
  try { u = JSON.parse(localStorage.getItem('korzadi_user') || '{}'); } catch { /* vacío */ }
  const email = u.email || '';
  const name = [u.first_name, u.last_name].filter(Boolean).join(' ') || (email ? email.split('@')[0] : 'Mi cuenta');
  const initials = name.split(/\s+/).filter(Boolean).slice(0, 2).map((p) => p[0].toUpperCase()).join('') || 'K';
  return { name, email, initials };
}

export const NETWORKS = {
  TRON: { label: 'Tron (TRC20)', short: 'TRC20', assets: 'USDT • TRX', color: '#ef0027' },
  ETH: { label: 'Ethereum (ERC20)', short: 'ERC20', assets: 'USDT • USDC • ETH', color: '#627eea' },
  BSC: { label: 'BSC (BEP20)', short: 'BEP20', assets: 'USDT • BNB', color: '#f3ba2f' },
  BTC: { label: 'Bitcoin', short: 'Bitcoin', assets: 'BTC', color: '#f7931a' },
};
export const SOON_NETWORKS = [
  { label: 'Solana (SPL)', assets: 'USDT • SOL', color: '#14f195' },
  { label: 'Polygon (MATIC)', assets: 'USDT • USDC', color: '#8247e5' },
  { label: 'Arbitrum (ARB)', assets: 'USDT • USDC', color: '#28a0f0' },
];
export const getNetwork = () => sessionStorage.getItem('kz_network') || 'BSC';
export const setNetwork = (n) => sessionStorage.setItem('kz_network', n);

// El ledger guarda el saldo como 'USD'; para el usuario es USDT (1:1).
export const showAsset = (code) => (code === 'USD' ? 'USDT' : code);
