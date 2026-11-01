import type { ReplayRankingEntry } from '../api/generated/model';
import { formatPrice } from '../lib/format';
import styles from './ReplayPage.module.css';

interface ReplayRankingProps {
  entries: ReplayRankingEntry[];
  isLoading: boolean;
}

// ReplayRanking は過去チャートモードのランキング（上位 20 件・#104）。
// ログインユーザーは Discord のアイコンと表示名、ゲストは「ゲスト」の印を付ける。
export default function ReplayRanking({ entries, isLoading }: ReplayRankingProps) {
  return (
    <section className={styles.card} aria-label="ランキング">
      <h2 className={styles.heading}>ランキング（上位20件）</h2>
      {isLoading ? (
        <p className={styles.sub}>読み込み中...</p>
      ) : entries.length === 0 ? (
        <p className={styles.sub}>まだ記録がありません。最初の記録を作りましょう。</p>
      ) : (
        <ol className={styles.ranking}>
          {entries.map((entry) => (
            <li key={`${entry.rank}-${entry.name}`} className={styles.rankingRow}>
              <span className={styles.rank}>{entry.rank}</span>
              {entry.avatarUrl !== null ? (
                <img
                  className={styles.avatar}
                  src={entry.avatarUrl}
                  alt=""
                  width={28}
                  height={28}
                  loading="lazy"
                />
              ) : (
                <span className={styles.avatarFallback} aria-hidden>
                  {Array.from(entry.name)[0] ?? '?'}
                </span>
              )}
              <span className={styles.rankName}>
                {entry.name}
                {entry.isGuest && <span className={styles.guestBadge}>ゲスト</span>}
              </span>
              <span className={styles.rankFunds}>{formatPrice(entry.finalFunds)}</span>
            </li>
          ))}
        </ol>
      )}
    </section>
  );
}
