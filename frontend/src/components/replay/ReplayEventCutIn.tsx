import { useEffect, useRef, useState } from 'react';

import type { ReplayCutInEvent } from '../../api/generated/model';
import styles from '../../pages/ReplayPage.module.css';

// カットインを 1 件出しておく時間（ミリ秒）。ゲームは止めず、注文の操作も妨げない。
export const REPLAY_CUTIN_DURATION_MS = 3500;

interface ReplayEventCutInProps {
  // サーバーが返した公開済みの出来事（day_index 昇順・同じ日は id 順）。
  events: ReplayCutInEvent[];
  durationMs?: number;
}

// ReplayEventCutIn は出来事の当日に、出来事の名前を短く出す（#116 REPLAY-6）。
// 同じ出来事は 1 回だけ出し、同時に複数あれば順番に出す。日付・相場への影響・
// 値動き・売買の推奨は出さない。時間を止めず、注文ボタンも隠さない（ポインターは通す）。
export default function ReplayEventCutIn({
  events,
  durationMs = REPLAY_CUTIN_DURATION_MS,
}: ReplayEventCutInProps) {
  // すでにキューへ入れた出来事。状態の取り直しで同じ出来事が何度も返っても 1 回だけにする。
  const seenRef = useRef<Set<number>>(new Set());
  const [queue, setQueue] = useState<ReplayCutInEvent[]>([]);

  useEffect(() => {
    // 重複判定と記録は effect 本体で行う。setQueue の更新関数に副作用を持たせると、
    // StrictMode で更新関数が 2 回呼ばれたときにイベントが落ちる。
    const fresh: ReplayCutInEvent[] = [];
    for (const event of events) {
      if (seenRef.current.has(event.id)) continue;
      seenRef.current.add(event.id);
      fresh.push(event);
    }
    if (fresh.length > 0) {
      setQueue((prev) => [...prev, ...fresh]);
    }
  }, [events]);

  const current = queue[0];

  // 1 件ずつ一定時間だけ出し、次の出来事があれば続けて出す。
  useEffect(() => {
    if (current == null) return;
    const timer = window.setTimeout(() => {
      setQueue((prev) => prev.slice(1));
    }, durationMs);
    return () => window.clearTimeout(timer);
  }, [current, durationMs]);

  if (current == null) return null;

  return (
    <div key={current.id} className={styles.cutIn} role="status" aria-live="polite">
      <p className={styles.cutInKicker}>出来事</p>
      <p className={styles.cutInHeadline}>{current.headline}</p>
    </div>
  );
}
