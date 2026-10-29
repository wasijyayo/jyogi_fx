import { useEffect, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';

import {
  getGetMeQueryKey,
  getListPositionsQueryKey,
  useGetMarketStatus,
  useListCurrencies,
  usePlaceOrder,
} from '../api/generated/api';
import type { OrderRequestSide } from '../api/generated/model';
import { formatPrice } from '../lib/format';
import { canPlaceOrder } from '../lib/market';
import styles from './OrderForm.module.css';

// orderErrorMessage は PlaceOrder のレスポンスから表示用のエラーメッセージを取り出す。
// 200 以外はサーバー（server/trade.go）が Error（message）を返す。
function orderErrorMessage(res: { status: number; data: unknown } | undefined): string | null {
  if (!res || res.status === 200) return null;
  const message = (res.data as { message?: string } | null)?.message;
  return message ?? '注文に失敗しました';
}

// OrderForm は新規注文フォーム（#46 WEB-2）。通貨・売買方向・数量・レバレッジを入力して
// POST /api/orders を呼ぶ。ロジックは持たず、生成された usePlaceOrder を呼ぶだけ。
// セッション時間外・終了1分前は canPlaceOrder が false になり UI 全体を無効化する。
export default function OrderForm() {
  const queryClient = useQueryClient();
  const { data: currenciesData } = useListCurrencies();
  const { data: statusData } = useGetMarketStatus();

  const [symbol, setSymbol] = useState('');
  const [side, setSide] = useState<OrderRequestSide>('long');
  const [size, setSize] = useState('');
  const [leverage, setLeverage] = useState('1');

  const placeOrder = usePlaceOrder({
    mutation: {
      onSuccess: (res) => {
        if (res.status !== 200) return;
        setSize('');
        // 建玉一覧と残高（ヘッダー）を最新にする。
        void queryClient.invalidateQueries({ queryKey: getListPositionsQueryKey() });
        void queryClient.invalidateQueries({ queryKey: getGetMeQueryKey() });
      },
    },
  });

  const currencies = currenciesData?.data ?? [];

  // 通貨一覧が読めたら先頭を初期選択しておく（ユーザーは変更できる）。
  useEffect(() => {
    if (symbol === '' && currencies.length > 0) {
      setSymbol(currencies[0].symbol);
    }
  }, [currencies, symbol]);

  const status = statusData?.data;
  const canOrder = status != null && canPlaceOrder(status);
  const selectedSymbol = symbol || currencies[0]?.symbol || '';
  const submitDisabled =
    !canOrder || placeOrder.isPending || selectedSymbol === '' || size.trim() === '' || leverage.trim() === '';

  const errorMessage = orderErrorMessage(placeOrder.data);
  const success = placeOrder.data?.status === 200;

  return (
    <section className={styles.section}>
      <h2 className={styles.heading}>新規注文</h2>

      {status != null && !canOrder && (
        <p className={styles.closedNotice} role="status">
          現在は新規注文を受け付けていません（取引時間外、または終了間際です）。
        </p>
      )}

      <form
        className={styles.form}
        onSubmit={(e) => {
          e.preventDefault();
          if (submitDisabled) return;
          placeOrder.mutate({
            data: { symbol: selectedSymbol, side, size: size.trim(), leverage: leverage.trim() },
          });
        }}
      >
        <div className={styles.field}>
          <label className={styles.label} htmlFor="order-symbol">
            通貨
          </label>
          <select
            id="order-symbol"
            className={styles.select}
            value={selectedSymbol}
            onChange={(e) => setSymbol(e.target.value)}
            disabled={!canOrder}
          >
            {currencies.map((c) => (
              <option key={c.symbol} value={c.symbol}>
                {c.symbol}（{formatPrice(c.currentPrice)}）
              </option>
            ))}
          </select>
        </div>

        <div className={styles.field}>
          <span className={styles.label}>売買方向</span>
          <div className={styles.sideToggle} role="group" aria-label="売買方向">
            <button
              type="button"
              className={side === 'long' ? styles.sideBuyActive : styles.side}
              onClick={() => setSide('long')}
              disabled={!canOrder}
            >
              買い
            </button>
            <button
              type="button"
              className={side === 'short' ? styles.sideSellActive : styles.side}
              onClick={() => setSide('short')}
              disabled={!canOrder}
            >
              売り
            </button>
          </div>
        </div>

        <div className={styles.field}>
          <label className={styles.label} htmlFor="order-size">
            数量
          </label>
          <input
            id="order-size"
            className={styles.input}
            type="text"
            inputMode="decimal"
            value={size}
            onChange={(e) => setSize(e.target.value)}
            disabled={!canOrder}
            placeholder="例: 10"
          />
        </div>

        <div className={styles.field}>
          <label className={styles.label} htmlFor="order-leverage">
            レバレッジ
          </label>
          <input
            id="order-leverage"
            className={styles.input}
            type="text"
            inputMode="decimal"
            value={leverage}
            onChange={(e) => setLeverage(e.target.value)}
            disabled={!canOrder}
            placeholder="1〜10"
          />
        </div>

        <button type="submit" className={styles.submit} disabled={submitDisabled}>
          {placeOrder.isPending ? '注文中...' : side === 'long' ? '買い注文' : '売り注文'}
        </button>

        {success && (
          <p className={styles.success} role="status">
            注文が成立しました
          </p>
        )}
        {errorMessage != null && (
          <p className={styles.error} role="alert">
            {errorMessage}
          </p>
        )}
      </form>
    </section>
  );
}
