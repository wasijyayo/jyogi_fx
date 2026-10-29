import { useGetMarketStatus } from '../api/generated/api';
import type { MarketStatus } from '../api/generated/model';

// PRICE_POLL_INTERVAL_MS は価格ポーリングの間隔（#99 指示: 取引時間中は15秒）。
export const PRICE_POLL_INTERVAL_MS = 15_000;

// useIsMarketOpen は取引時間中かどうかを返す。取引時間の状態はサーバー側で計算して
// 返す（#99 指示。ブラウザの時計で判定しない）。ヘッダーと同一のクエリキャッシュを
// 共有するため、追加のリクエストにはならない。
export function useIsMarketOpen(): boolean {
  const { data } = useGetMarketStatus();
  return data?.data.open === true;
}

// canPlaceOrder は「新規注文ができる状態か」を返す（#46 指示: セッション時間外・
// 終了1分前は注文UIを無効化する）。判定はサーバーの SessionConfig.IsNewOrderAllowed と
// 同じ内容を MarketStatus から導く（ブラウザの時計では判定しない）。
// 決済（クローズ）は終了1分前も許可される仕様（design.md §7.10）のため、この判定を
// 決済に使い回さないこと。
export function canPlaceOrder(status: MarketStatus): boolean {
  if (status.alwaysOpen) return true;
  if (!status.open) return false;
  // open かつ remainingMinutes <= 1 は「終了1分前」（12:59台）。
  // remainingMinutes が null になるのは仕様上 alwaysOpen のみなので、安全側に true を返す。
  return status.remainingMinutes == null || status.remainingMinutes > 1;
}
