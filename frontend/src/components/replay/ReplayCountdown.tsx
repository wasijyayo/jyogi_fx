import { useEffect, useRef, useState } from 'react';

import { replayCountdownValue } from '../../lib/replay';
import { playReplayCountdown } from '../../lib/replayAudio';
import styles from '../../pages/ReplayPage.module.css';

interface ReplayCountdownProps {
  // カウントダウンを始めた時刻（Date.now() のミリ秒）。
  startedAt: number;
  // 裏で読み込んでいるゲーム状態が使えるようになったか。false の間は画面を消さない。
  ready: boolean;
  // カウントダウンが終わって画面を消したときに 1 回だけ呼ぶ（BGM の開始に使う）。
  onFinished?: () => void;
}

// ReplayCountdown は「取引開始」を押した直後から出る不透明な最前面の画面
// （#138 REPLAY-13）。5・4・3・2・1 を毎回 5 秒出し、その後ろでゲーム状態を
// 読み込んでチャートを描画しておく。読み込みが終わるまでは数字を 1 のまま止め、
// 画面を消さない（「ゲームを読み込んでいます...」を見せない）。
// reduced-motion では CSS のアニメーションだけを止め、秒数は変えない。
// 数字が変わるたびに SE を 1 回ずつ鳴らす（#140 REPLAY-14）。スキップした後は鳴らさない。
export default function ReplayCountdown({ startedAt, ready, onFinished }: ReplayCountdownProps) {
  const [now, setNow] = useState(() => Date.now());
  const [skipped, setSkipped] = useState(false);
  // 最後に SE を鳴らした数字。同じ数字で何度も鳴らさない。
  const lastPlayedRef = useRef<number | null>(null);
  // onFinished を 1 回だけ呼ぶための印。
  const finishedNotifiedRef = useRef(false);

  // 100ms ごとに表示を更新する。カウントダウンの境目（1 秒）に十分間に合う粒度。
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 100);
    return () => window.clearInterval(timer);
  }, []);

  const remaining = replayCountdownValue(now - startedAt);
  const countdownOver = skipped || remaining <= 0;

  // 5・4・3・2・1 が表示されたときに 1 回ずつ鳴らす。スキップ後（skipped）は、
  // 表示が 1 に変わっても残りの数字では鳴らさない。
  useEffect(() => {
    if (skipped || remaining <= 0) return;
    if (lastPlayedRef.current === remaining) return;
    lastPlayedRef.current = remaining;
    playReplayCountdown();
  }, [remaining, skipped]);

  // カウントダウンが終わり、画面を消せるようになったら親に知らせる（BGM の開始）。
  useEffect(() => {
    if (!countdownOver || !ready || finishedNotifiedRef.current) return;
    finishedNotifiedRef.current = true;
    onFinished?.();
  }, [countdownOver, ready, onFinished]);

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
