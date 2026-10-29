import { Link } from '@tanstack/react-router';

import { useListCurrencies } from '../api/generated/api';
import { formatChangePercent, formatPrice } from '../lib/format';
import { PRICE_POLL_INTERVAL_MS, useIsMarketOpen } from '../lib/market';
import styles from './CurrencyListPage.module.css';

// CurrencyListPage は `/`（通貨一覧）。currencies テーブルの全行を表示する
// （CLAUDE.md §5.3 ハードコードしない）。価格は取引時間中のみ15秒ごとにポーリングする。
export default function CurrencyListPage() {
  const isOpen = useIsMarketOpen();
  const { data: currenciesData, isLoading } = useListCurrencies({
    query: { refetchInterval: isOpen ? PRICE_POLL_INTERVAL_MS : false },
  });

  if (isLoading) {
    return <p className={styles.loading}>読み込み中...</p>;
  }

  const currencies = currenciesData?.data ?? [];

  return (
    <main className={styles.main}>
      <h1 className={styles.title}>通貨一覧</h1>
      <ul className={styles.list}>
        {currencies.map((c) => (
          <li key={c.symbol}>
            <Link
              to="/currencies/$symbol"
              params={{ symbol: c.symbol }}
              className={styles.item}
            >
              <span className={styles.symbol}>{c.symbol}</span>
              <span className={styles.name}>{c.name}</span>
              <span className={styles.price}>{formatPrice(c.currentPrice)}</span>
              <span className={styles.change}>{formatChangePercent(c.dayChangePercent)}</span>
            </Link>
          </li>
        ))}
      </ul>
    </main>
  );
}
