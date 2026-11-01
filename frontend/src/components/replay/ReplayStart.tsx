import { useState, type FormEvent } from 'react';

import { useGetMe, useStartReplayGame } from '../../api/generated/api';
import { replayErrorMessage, validateGuestName, type ReplaySession } from '../../lib/replay';
import styles from '../../pages/ReplayPage.module.css';

interface ReplayStartProps {
  onStarted: (session: ReplaySession) => void;
}

// ReplayStart は過去チャートモードの開始画面。ログインしていればゲスト名は不要で、
// していなければゲスト名（1〜16 文字）を入力して始める（design.md §13）。
export default function ReplayStart({ onStarted }: ReplayStartProps) {
  const { data: meData, isLoading: meLoading } = useGetMe();
  const [guestName, setGuestName] = useState('');
  const [error, setError] = useState<string | null>(null);

  const isLoggedIn = meData?.status === 200;

  const startMutation = useStartReplayGame({
    mutation: {
      onSuccess: (res) => {
        if (res.status !== 200) {
          setError(replayErrorMessage(res, 'ゲームを開始できませんでした'));
          return;
        }
        setError(null);
        onStarted({ gameId: res.data.gameId, token: res.data.token, started: res.data });
      },
    },
  });

  function start(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (startMutation.isPending || meLoading) return;

    if (isLoggedIn) {
      setError(null);
      startMutation.mutate({ data: {} });
      return;
    }

    const nameError = validateGuestName(guestName);
    if (nameError) {
      setError(nameError);
      return;
    }
    setError(null);
    startMutation.mutate({ data: { guestName: guestName.trim() } });
  }

  return (
    <main className={styles.main}>
      <h1 className={styles.title}>過去チャートモード</h1>

      <section className={styles.card}>
        <h2 className={styles.heading}>過去の実際の相場で 1 人プレイ</h2>
        <p className={styles.lead}>
          2015 年以降の実際の為替の日足から、60 営業日の区間をランダムに選んで遊びます。
          標準の速さでは 3 秒ごとに 1 営業日が公開され、1 ゲームは 3 分です。
        </p>
        <ul className={styles.rules}>
          <li>仮想資金 1000 から開始し、最終資金がスコアになります。</li>
          <li>複数の通貨ペアを同時に保有でき、買い・売りの両方を扱えます。</li>
          <li>強制決済ラインに価格が届くと、その建玉は強制決済されます。</li>
          <li>一時停止はありません。速さは ×1・×2・×3 から選べます。</li>
        </ul>
      </section>

      <form className={styles.card} onSubmit={start}>
        <h2 className={styles.heading}>ゲームを始める</h2>

        {meLoading ? (
          <p className={styles.sub}>ログイン状態を確認しています...</p>
        ) : isLoggedIn ? (
          <p className={styles.sub}>
            {meData.data.displayName} さんとして参加します（ランキングには Discord の表示名と
            アイコンが載ります）。
          </p>
        ) : (
          <>
            <p className={styles.sub}>
              ログインしていなくても遊べます。ランキングには入力したゲスト名と「ゲスト」の印が
              載ります。
            </p>
            <label className={styles.field}>
              <span className={styles.fieldLabel}>ゲスト名（1〜16 文字）</span>
              <input
                className={styles.textInput}
                type="text"
                value={guestName}
                maxLength={16}
                onChange={(e) => setGuestName(e.target.value)}
                placeholder="ゲスト名"
                autoComplete="nickname"
              />
            </label>
          </>
        )}

        {error != null && (
          <p className={styles.error} role="alert">
            {error}
          </p>
        )}

        <div className={styles.actions}>
          <button type="submit" className={styles.primary} disabled={startMutation.isPending || meLoading}>
            {startMutation.isPending ? '開始しています...' : 'ゲームを始める'}
          </button>
          <a className={styles.secondaryLink} href="/replay/ranking">
            ランキングを見る
          </a>
          <a className={styles.secondaryLink} href="/">
            通常モードへ戻る
          </a>
        </div>
      </form>
    </main>
  );
}
