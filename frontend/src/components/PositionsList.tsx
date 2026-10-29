import { useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';

import {
  useClosePosition,
  useListPositions,
} from '../api/generated/api';
import type { Position } from '../api/generated/model';
import { formatPrice, formatSide, formatSignedPrice } from '../lib/format';
import { PRICE_POLL_INTERVAL_MS, useIsMarketOpen } from '../lib/market';
import styles from './PositionsList.module.css';

// closeErrorMessage は決済のレスポンスから表示用のエラーメッセージを取り出す。
function closeErrorMessage(res: { status: number; data: unknown } | undefined): string | null {
  if (!res || res.status === 200) return null;
  const message = (res.data as { message?: string } | null)?.message;
  return message ?? '決済に失敗しました';
}

// isLoss は含み損益（decimal文字列）が負かどうかを返す。数値には変換しない
// （CLAUDE.md §5.2 float禁止）。文字列の符号だけを見る。
function isLoss(pnl: string): boolean {
  return pnl.startsWith('-');
}

// PositionsList は建玉一覧（#46 WEB-2）。未決済ポジションと含み損益を表示し、
// 各行に決済ボタンを置く。価格は取引時間中のみ15秒ごとにポーリングする（#46 決定事項）。
export default function PositionsList() {
  const queryClient = useQueryClient();
  const isOpen = useIsMarketOpen();

  const [confirmingId, setConfirmingId] = useState<number | null>(null);

  const { data: positionsData, isLoading } = useListPositions({
    query: { refetchInterval: isOpen ? PRICE_POLL_INTERVAL_MS : false },
  });

  const closePosition = useClosePosition({
    mutation: {
      onSuccess: (res) => {
        setConfirmingId(null);
        if (res.status !== 200) return;
        // 建玉一覧・残高（ヘッダー）に加え、決済は価格を動かすのでチャートと通貨の
        // 価格も最新にする（#46 完了条件「チャートに自分の取引の影響が現れること」）。
        void queryClient.invalidateQueries();
      },
    },
  });

  const positions: Position[] = positionsData?.status === 200 ? positionsData.data : [];
  const closeError = closeErrorMessage(closePosition.data);

  if (isLoading) {
    return <p className={styles.loading}>読み込み中...</p>;
  }

  return (
    <section className={styles.section}>
      <h2 className={styles.heading}>建玉一覧</h2>

      {positions.length === 0 ? (
        <p className={styles.empty}>保有ポジションはありません。</p>
      ) : (
        <ul className={styles.list}>
          {positions.map((p) => (
            <li key={p.id} className={styles.row}>
              <div className={styles.rowMain}>
                <span className={styles.symbol}>{p.symbol}</span>
                <span className={p.side === 'short' ? styles.sideShort : styles.sideLong}>
                  {formatSide(p.side)}
                </span>
                <span className={styles.size}>{formatPrice(p.size)}</span>
              </div>

              <div className={styles.rowDetail}>
                建値 {formatPrice(p.entryPrice)} → 現在 {formatPrice(p.currentPrice)}（レバレッジ{' '}
                {formatPrice(p.leverage)}）
              </div>

              <div className={styles.rowFoot}>
                <span className={isLoss(p.unrealizedPnl) ? styles.pnlLoss : styles.pnlProfit}>
                  含み損益 {formatSignedPrice(p.unrealizedPnl)}
                </span>

                {confirmingId === p.id ? (
                  <span className={styles.confirm}>
                    本当に決済しますか？
                    <button
                      type="button"
                      className={styles.confirmYes}
                      onClick={() => closePosition.mutate({ positionId: p.id })}
                      disabled={closePosition.isPending}
                    >
                      はい
                    </button>
                    <button
                      type="button"
                      className={styles.confirmNo}
                      onClick={() => setConfirmingId(null)}
                    >
                      キャンセル
                    </button>
                  </span>
                ) : (
                  <button
                    type="button"
                    className={styles.close}
                    onClick={() => setConfirmingId(p.id)}
                  >
                    決済
                  </button>
                )}
              </div>
            </li>
          ))}
        </ul>
      )}

      {closeError != null && (
        <p className={styles.error} role="alert">
          {closeError}
        </p>
      )}
    </section>
  );
}
