const COINS = {
  USDT: ['#26a17b', '₮'], BTC: ['#f7931a', '₿'], ETH: ['#627eea', 'Ξ'],
  BNB: ['#f3ba2f', 'B'], TRX: ['#ef0027', 'T'], USDC: ['#2775ca', '$'], USD: ['#1468ff', '$'],
};
export default function Coin({ code = 'USDT', size = 42 }) {
  const [bg, glyph] = COINS[code] || ['#1468ff', (code || '?')[0]];
  return (
    <span className="kz-coin" style={{ background: bg, width: size, height: size, fontSize: size * 0.46 }}>
      {glyph}
    </span>
  );
}
