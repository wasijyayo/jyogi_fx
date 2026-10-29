import { useGetMarketStatus } from '../api/generated/api';

// PRICE_POLL_INTERVAL_MS は価格ポーリングの間隔（#99 指示: 取引時間中は15秒）。
export const PRICE_POLL_INTERVAL_MS = 15_000;

// useIsMarketOpen は取引時間中かどうかを返す。取引時間の状態はサーバー側で計算して
// 返す（#99 指示。ブラウザの時計で判定しない）。ヘッダーと同一のクエリキャッシュを
// 共有するため、追加のリクエストにはならない。
export function useIsMarketOpen(): boolean {
  const { data } = useGetMarketStatus();
  return data?.data.open === true;
}
