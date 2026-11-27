import { useEffect, useState } from 'react';

import type { ReplaySession } from '../../lib/replay';
import styles from '../../pages/ReplayPage.module.css';

// 開始演出を見たことを覚えておくキー（#126 REPLAY-9）。2 回目以降は年・新聞を短くする。
const REPLAY_INTRO_SEEN_KEY = 'fxgame.replayIntroSeen';
// 初回の「20XX年」を出しておく時間（ミリ秒）。押すまで待つのは新聞の画面から。
const REPLAY_YEAR_DISPLAY_MS = 1600;

interface ReplayIntroProps {
  session: ReplaySession;
  // 「取引開始」を押したときに呼ぶ。カウントダウンと begin は親（ReplayPage）が行う。
  onStart: () => void;
  // begin の通信中かどうか（押した直後は最前面のカウントダウンが出る）。
  starting: boolean;
  // begin に失敗したときのメッセージ。
  error: string | null;
}

// ReplayIntro は「暗転 → 年 → 新聞 →『取引開始』」の開始演出（#126 REPLAY-9）。
// 年は開始 API の応答（session.started.startYear）から出せるため、begin の前に流せる。
// 「取引開始」を押すまでサーバーの時間は動かない（押してから親が begin を呼ぶ）。
// カウントダウン（5 秒）は ReplayCountdown が担当する（#138 REPLAY-13）。
export default function ReplayIntro({ session, onStart, starting, error }: ReplayIntroProps) {
  // 初回かどうか・動きを減らす設定かどうかは初回レンダーで決める。
  const [isRepeat] = useState(readIntroSeen);
  const [reducedMotion] = useState(prefersReducedMotion);

  // 初回だけ「年」の画面を自動で出す。2 回目以降・reduced-motion では新聞から始める。
  const [showYear, setShowYear] = useState(() => !isRepeat && !reducedMotion);

  // 初回の「年」は一定時間で新聞に切り替える。スキップでも同じ新聞へ。
  useEffect(() => {
    if (!showYear) return;
    const timer = window.setTimeout(() => setShowYear(false), REPLAY_YEAR_DISPLAY_MS);
    return () => window.clearTimeout(timer);
  }, [showYear]);

  if (showYear) {
    return (
      <main className={`${styles.intro} ${styles.introDark}`}>
        <p className={styles.introKicker}>過去チャートモード</p>
        <p className={styles.introYear} aria-live="polite">
          {session.started.startYear}年
        </p>
        <button type="button" className={styles.introSkip} onClick={() => setShowYear(false)}>
          スキップ
        </button>
      </main>
    );
  }

  return (
    <main className={`${styles.intro} ${styles.introPaper}`}>
      <section className={`${styles.newspaper} ${isRepeat ? styles.newspaperShort : ''}`}>
        <p className={styles.newspaperMasthead}>MARKET REPLAY</p>
        <p className={styles.newspaperDate}>{session.started.startYear}年</p>
        <h1 className={styles.newspaperHeadline}>{session.started.startYear}年の相場</h1>
        <p className={styles.newspaperLead}>
          これから 60 営業日の値動きが、標準の速さで 3 秒ごとに 1 日ずつ公開されます。
          開始前の 20 営業日は、予想の材料として最初から見えます。
        </p>
        <section className={styles.newspaperEvents} aria-label={`${session.started.startYear}年の出来事`}>
          <h2 className={styles.newspaperEventsTitle}>{session.started.startYear}年の出来事</h2>
          {session.started.newspaper.length === 0 ? (
            <p className={styles.newspaperEmpty}>この年の出来事の記録はありません。</p>
          ) : (
            <ul className={styles.newspaperEventList}>
              {session.started.newspaper.map((event, index) => (
                <li key={`${index}-${event.headline}`} className={styles.newspaperEvent}>
                  <p className={styles.newspaperEventHeadline}>{event.headline}</p>
                  <p className={styles.newspaperEventDescription}>{event.description}</p>
                </li>
              ))}
            </ul>
          )}
        </section>
        <ul className={styles.newspaperList}>
          <li>仮想資金は 1000。最終資金がスコアになります。</li>
          <li>複数の通貨ペアを同時に保有でき、買い・売りの両方を扱えます。</li>
          <li>一時停止はありません。速さは ×1・×2・×3 から選べます。</li>
        </ul>
        <p className={styles.newspaperNote}>
          準備ができたら「取引開始」を押してください。押すまで時間は進みません。
        </p>

        {error != null && (
          <p className={styles.error} role="alert">
            {error}
          </p>
        )}

        <div className={styles.actions}>
          <button type="button" className={styles.primary} onClick={onStart} disabled={starting}>
            {starting ? '開始しています...' : '取引開始'}
          </button>
          <a className={styles.secondaryLink} href="/replay/ranking">
            ランキングを見る
          </a>
        </div>
      </section>
    </main>
  );
}

// readIntroSeen は開始演出を見たことがあるかを返す（localStorage が使えなければ false）。
function readIntroSeen(): boolean {
  try {
    return window.localStorage.getItem(REPLAY_INTRO_SEEN_KEY) === '1';
  } catch {
    return false;
  }
}

// markIntroSeen は開始演出を見たことを記録する（失敗しても遊べるように握りつぶす）。
// カウントダウンは毎回 5 秒だが、年・新聞の演出は 2 回目以降に短くする（#126）。
export function markIntroSeen() {
  try {
    window.localStorage.setItem(REPLAY_INTRO_SEEN_KEY, '1');
  } catch {
    // ストレージが使えない環境では毎回ゆっくりした演出になるだけなので無視する。
  }
}

// prefersReducedMotion は OS の「視差効果を減らす」設定が有効かを返す。
function prefersReducedMotion(): boolean {
  return (
    typeof window !== 'undefined' &&
    typeof window.matchMedia === 'function' &&
    window.matchMedia('(prefers-reduced-motion: reduce)').matches
  );
}
