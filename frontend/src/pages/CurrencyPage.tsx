import { useState } from 'react';
import { useParams } from '@tanstack/react-router';

import { useGetCandles, useGetCurrency } from '../api/generated/api';
import type { GetCandlesResolution } from '../api/generated/model';
import PriceChart from '../components/PriceChart';
import { formatChangePercent, formatPrice } from '../lib/format';
import { PRICE_POLL_INTERVAL_MS, useIsMarketOpen } from '../lib/market';
import styles from './CurrencyPage.module.css';

// 足の種類（1分足/5分足）のラベル。切替ボタンと PriceChart の resolution に使う。
const RESOLUTIONS: { value: GetCandlesResolution; label: string }[] = [
  { value: '1m', label: '1分足' },
  { value: '5m', label: '5分足' },
];

// CurrencyPage は `/currencies/:symbol`。通貨の表示情報とローソク足チャート（#45）を
// 出す。チャートは未ログインでも見られる（issue #45 決定事項）。価格は取引時間中のみ
// 15秒ごとにポーリングし、時間外は止める（#99 と同じ方針）。
export default function CurrencyPage() {
  const { symbol } = useParams({ from: '/currencies/$symbol' });
  const isOpen = useIsMarketOpen();
  const [resolution, setResolution] = useState<GetCandlesResolution>('1m');

  const { data, isLoading } = useGetCurrency(symbol, {
    query: { refetchInterval: isOpen ? PRICE_POLL_INTERVAL_MS : false },
  });
  const { data: candlesData } = useGetCandles(
    symbol,
    { resolution },
    { query: { refetchInterval: isOpen ? PRICE_POLL_INTERVAL_MS : false } },
  );

  if (isLoading) {
    return <p className={styles.loading}>読み込み中...</p>;
  }

  if (data?.status !== 200) {
    return <p className={styles.loading}>通貨が見つかりません</p>;
  }

  const c = data.data;
  const chart = candlesData?.status === 200 ? candlesData.data : undefined;

  return (
    <main className={styles.main}>
      <h1 className={styles.symbol}>{c.symbol}</h1>
      <p className={styles.name}>{c.name}</p>
      <p className={styles.price}>
        現在価格: {formatPrice(c.currentPrice)}{' '}
        <span className={styles.change}>({formatChangePercent(c.dayChangePercent)})</span>
      </p>

      <div className={styles.resolutions}>
        {RESOLUTIONS.map((r) => (
          <button
            key={r.value}
            type="button"
            className={resolution === r.value ? styles.resolutionActive : styles.resolution}
            onClick={() => setResolution(r.value)}
          >
            {r.label}
          </button>
        ))}
      </div>

      <PriceChart
        candles={chart?.candles ?? []}
        events={chart?.events ?? []}
        resolution={resolution}
      />
    </main>
  );
}
