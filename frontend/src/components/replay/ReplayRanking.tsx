import type { ReplayRankingEntry } from '../../api/generated/model';
import { formatPrice } from '../../lib/format';
import styles from '../../pages/ReplayPage.module.css';

interface ReplayRankingProps {
  entries: ReplayRankingEntry[];
  isLoading?: boolean;
  errorMessage?: string | null;
  // 結果画面に埋め込むときは見出しを小さくする。
  compact?: boolean;
}

// ReplayRanking は過去チャートモードのランキング（API の上位 20 件をそのまま表示）。
// ログインユーザーは Discord アイコン + 表示名、ゲストは「ゲスト」の印を付ける。
export default function ReplayRanking({
  entries,
  isLoading = false,
  errorMessage = null,
  compact = false,
}: ReplayRankingProps) {
  return (
    <section className={compact ? styles.cardCompact : styles.card} aria-label="ランキング">
      <h2 className={styles.heading}>ランキング</h2>
      <p className={styles.sub}>ログインユーザーとゲストの上位 20 件です。</p>

      {isLoading ? (
        <p className={styles.loading}>読み込み中...</p>
      ) : errorMessage ? (
        <p className={styles.error} role="alert">
          {errorMessage}
        </p>
      ) : entries.length === 0 ? (
        <p className={styles.sub}>まだ記録がありません。最初の 1 本を目指しましょう。</p>
      ) : (
        <ol className={styles.ranking}>
          {entries.map((entry) => (
            <li key={`${entry.rank}-${entry.name}`} className={styles.rankingRow}>
              <span className={styles.rank}>{entry.rank}</span>
              {entry.avatarUrl ? (
                <img
                  className={styles.avatar}
                  src={entry.avatarUrl}
                  alt={`${entry.name} のアイコン`}
                  width={24}
                  height={24}
                  loading="lazy"
                />
              ) : (
                <span className={styles.avatarGuest} aria-hidden="true">
                  ?
                </span>
              )}
              <span className={styles.rankingName}>
                {entry.name}
                {entry.isGuest && <span className={styles.guestBadge}>ゲスト</span>}
              </span>
              <span className={styles.rankingScore}>{formatPrice(entry.finalFunds)}</span>
            </li>
          ))}
        </ol>
      )}
    </section>
  );
}
