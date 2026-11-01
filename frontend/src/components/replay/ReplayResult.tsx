import { useGetReplayRanking } from '../../api/generated/api';
import type { ReplayGameState } from '../../api/generated/model';
import { formatPrice } from '../../lib/format';
import { formatReplayInterval } from '../../lib/replay';
import styles from '../../pages/ReplayPage.module.css';
import ReplayRanking from './ReplayRanking';

interface ReplayResultProps {
  state: ReplayGameState;
  onPlayAgain: () => void;
}

// ReplayResult は過去チャートモードの結果画面。スコア（最終資金）・区間の正確な日付・
// 出典・ランキングを表示し、もう一度遊べるようにする（design.md §13）。
export default function ReplayResult({ state, onPlayAgain }: ReplayResultProps) {
  const { data, isLoading, isError } = useGetReplayRanking();
  const entries = data?.status === 200 ? data.data : [];
  const rankingError =
    isError || (data != null && data.status !== 200) ? 'ランキングを取得できませんでした' : null;

  const score = state.finalFunds ?? state.funds;
  const interval = formatReplayInterval(state.intervalStart, state.intervalEnd);

  return (
    <main className={styles.main}>
      <section className={styles.result} role="status">
        <h1 className={styles.title}>結果</h1>
        <p className={styles.scoreLabel}>スコア（最終資金）</p>
        <p className={styles.score}>{formatPrice(score)}</p>

        <p className={styles.resultText}>
          {interval !== '—' ? (
            <>
              <strong>{interval}</strong> の相場でした。
            </>
          ) : (
            <>
              <strong>{state.startYear}年</strong>の相場でした。
            </>
          )}
        </p>
        {state.status === 'early_finished' && (
          <p className={styles.sub}>資金が少なくなったため、途中で終了しました。</p>
        )}

        <div className={styles.actions}>
          <button type="button" className={styles.primary} onClick={onPlayAgain}>
            もう一度遊ぶ
          </button>
          <a className={styles.secondaryLink} href="/replay/ranking">
            ランキングを見る
          </a>
          <a className={styles.secondaryLink} href="/">
            通常モードへ戻る
          </a>
        </div>

        <p className={styles.source}>出典: EV Trading Labs</p>
      </section>

      <ReplayRanking
        entries={entries}
        isLoading={isLoading}
        errorMessage={rankingError}
        compact
      />
    </main>
  );
}
