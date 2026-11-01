import { useEffect, useState } from 'react';

import { replayCountdownValue } from '../../lib/replay';
import styles from '../../pages/ReplayPage.module.css';

interface ReplayCountdownProps {
  // カウントダウンを始めた時刻（Date.now() のミリ秒）。
  startedAt: number;
  // 裏で読み込んでいるゲーム状態が使えるようになったか。false の間は画面を消さない。
  ready: boolean;
}

// ReplayCountdown は「取引開始」を押した直後から出る不透明な最前面の画面
// （#138 REPLAY-13）。5・4・3・2・1 を毎回 5 秒出し、その後ろでゲーム状態を
// 読み込んでチャートを描画しておく。読み込みが終わるまでは数字を 1 のまま止め、
// 画面を消さない（「ゲームを読み込んでいます...」を見せない）。
// reduced-motion では CSS のアニメーションだけを止め、秒数は変えない。
export default function ReplayCountdown({ startedAt, ready }: ReplayCountdownProps) {
  const [now, setNow] = useState(() => Date.now());
  const [skipped, setSkipped] = useState(false);

  // 100ms ごとに表示を更新する。カウントダウンの境目（1 秒）に十分間に合う粒度。
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 100);
    return () => window.clearInterval(timer);
  }, []);

  const remaining = replayCountdownValue(now - startedAt);
  const countdownOver = skipped || remaining <= 0;

  // カウントダウンが終わっても読み込みが終わっていなければ最前面の画面を残す。
  if (countdownOver && ready) return null;

  return (
    <div className={styles.countdownOverlay} role="status" aria-live="assertive">
      <p className={styles.countdownLabel}>まもなく取引開始</p>
      <p className={styles.countdownNumber}>{countdownOver ? 1 : remaining}</p>
      {countdownOver && !ready && (
        <p className={styles.countdownPreparing}>チャートを準備しています...</p>
      )}
      <button type="button" className={styles.introSkip} onClick={() => setSkipped(true)}>
        スキップ
      </button>
    </div>
  );
}
