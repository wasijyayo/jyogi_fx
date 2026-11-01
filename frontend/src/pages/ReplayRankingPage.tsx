import { useGetReplayRanking } from '../api/generated/api';
import ReplayRanking from '../components/replay/ReplayRanking';
import styles from './ReplayPage.module.css';

// ReplayRankingPage は過去チャートモードのランキング画面。
// GET /api/replay/ranking の上位 20 件をそのまま表示する（期間の絞り込みなし）。
export default function ReplayRankingPage() {
  const { data, isLoading, isError } = useGetReplayRanking();
  const entries = data?.status === 200 ? data.data : [];
  const errorMessage =
    isError || (data != null && data.status !== 200) ? 'ランキングを取得できませんでした' : null;

  return (
    <main className={styles.main}>
      <h1 className={styles.title}>過去チャートモード ランキング</h1>
      <ReplayRanking entries={entries} isLoading={isLoading} errorMessage={errorMessage} />
      <div className={styles.actions}>
        <a className={styles.primaryLink} href="/replay">
          過去チャートモードを遊ぶ
        </a>
        <a className={styles.secondaryLink} href="/">
          通常モードへ戻る
        </a>
      </div>
    </main>
  );
}
