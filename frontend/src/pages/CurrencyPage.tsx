import { useParams } from '@tanstack/react-router';

import { useGetCurrency } from '../api/generated/api';
import { formatChangePercent, formatPrice } from '../lib/format';
import { PRICE_POLL_INTERVAL_MS, useIsMarketOpen } from '../lib/market';
import styles from './CurrencyPage.module.css';

// CurrencyPage は `/currencies/:symbol`。中身のチャートは #45、注文フォームは #46 で
// 作る。ここでは通貨の表示情報（シンボル・名前・現在価格・前日比）だけを出す枠。
export default function CurrencyPage() {
  const { symbol } = useParams({ from: '/currencies/$symbol' });
  const isOpen = useIsMarketOpen();
  const { data, isLoading } = useGetCurrency(symbol, {
    query: { refetchInterval: isOpen ? PRICE_POLL_INTERVAL_MS : false },
  });

  if (isLoading) {
    return <p className={styles.loading}>読み込み中...</p>;
  }

  if (data?.status !== 200) {
    return <p className={styles.loading}>通貨が見つかりません</p>;
  }

  const c = data.data;
  return (
    <main className={styles.main}>
      <h1 className={styles.symbol}>{c.symbol}</h1>
      <p className={styles.name}>{c.name}</p>
      <p className={styles.price}>
        現在価格: {formatPrice(c.currentPrice)}{' '}
        <span className={styles.change}>({formatChangePercent(c.dayChangePercent)})</span>
      </p>
      {/* チャートは #45、注文フォームは #46 で追加する。 */}
    </main>
  );
}
