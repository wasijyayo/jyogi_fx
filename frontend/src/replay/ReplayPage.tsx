import { useEffect, useMemo, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';

import {
  getGetReplayGameStateQueryKey,
  getGetReplayRankingQueryKey,
  useAddReplayMargin,
  useChangeReplaySpeed,
  useCloseReplayPosition,
  useGetMe,
  useGetReplayGameState,
  useGetReplayRanking,
  usePlaceReplayOrder,
  useStartReplayGame,
  useUnlockReplayLeverage,
} from '../api/generated/api';
import type { ReplayGameState } from '../api/generated/model';
import { formatPrice } from '../lib/format';
import ReplayPlay from './ReplayPlay';
import ReplayRanking from './ReplayRanking';
import {
  formatIntervalRange,
  formatIntervalSentence,
  formatYear,
  guestNameLength,
  type ReplaySide,
} from './replay';
import styles from './ReplayPage.module.css';

const SESSION_KEY = 'fxgame.replay.session.v1';

interface ReplaySession {
  gameId: number;
  token: string;
}

// sessionStorage にゲームトークンを置き、再読み込みでも同じゲームを続けられるようにする。
// トークンはゲームごとに使い捨て（design.md §13「先読み対策」）。
function readStoredSession(): ReplaySession | null {
  try {
    const raw = window.sessionStorage.getItem(SESSION_KEY);
    if (raw === null) return null;
    const parsed: unknown = JSON.parse(raw);
    if (
      typeof parsed === 'object' &&
      parsed !== null &&
      typeof (parsed as ReplaySession).gameId === 'number' &&
      typeof (parsed as ReplaySession).token === 'string'
    ) {
      return { gameId: (parsed as ReplaySession).gameId, token: (parsed as ReplaySession).token };
    }
  } catch {
    // 壊れた値は無視して開始画面に戻す。
  }
  return null;
}

function storeSession(session: ReplaySession | null): void {
  try {
    if (session === null) {
      window.sessionStorage.removeItem(SESSION_KEY);
    } else {
      window.sessionStorage.setItem(SESSION_KEY, JSON.stringify(session));
    }
  } catch {
    // sessionStorage が使えない環境でも、その場の state だけで遊べる。
  }
}

// ReplayPage は `/replay`（過去チャートモード・#105）。
// モード選択と開始、プレイ、結果、ランキングを 1 画面の状態遷移で見せる。
export default function ReplayPage() {
  const queryClient = useQueryClient();
  const [session, setSession] = useState<ReplaySession | null>(() => readStoredSession());
  const [guestName, setGuestName] = useState('');
  const [startError, setStartError] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);

  const { data: meData, isLoading: meLoading } = useGetMe();
  const me = meData?.status === 200 ? meData.data : null;

  const gameId = session?.gameId ?? 0;
  const authFetch = useMemo<RequestInit | undefined>(
    () =>
      session === null
        ? undefined
        : { headers: { Authorization: `Bearer ${session.token}` } },
    [session],
  );

  const stateQuery = useGetReplayGameState(gameId, {
    query: {
      enabled: session !== null,
      retry: false,
      refetchInterval: (query) => {
        const data = query.state.data;
        if (data === undefined) return 1000;
        if (data.status !== 200) return false;
        return data.data.status === 'playing' ? 1000 : false;
      },
    },
    fetch: authFetch,
  });

  const rankingQuery = useGetReplayRanking({
    query: { refetchOnWindowFocus: false },
  });
  const ranking = rankingQuery.data?.data ?? [];

  const startMutation = useStartReplayGame();
  const orderMutation = usePlaceReplayOrder({ fetch: authFetch });
  const closeMutation = useCloseReplayPosition({ fetch: authFetch });
  const addMarginMutation = useAddReplayMargin({ fetch: authFetch });
  const unlockMutation = useUnlockReplayLeverage({ fetch: authFetch });
  const speedMutation = useChangeReplaySpeed({ fetch: authFetch });

  const stateData = stateQuery.data;
  const state: ReplayGameState | null =
    stateData?.status === 200 ? stateData.data : null;
  const finished = state !== null && state.status !== 'playing';

  // トークンが無効・ゲームが見つからない場合は開始画面に戻す。
  useEffect(() => {
    if (stateData === undefined) return;
    if (stateData.status === 401 || stateData.status === 404) {
      storeSession(null);
      setSession(null);
    }
  }, [stateData]);

  // ゲームが終わったらランキングを取り直す（確定したスコアを反映する）。
  useEffect(() => {
    if (finished) {
      void queryClient.invalidateQueries({ queryKey: getGetReplayRankingQueryKey() });
    }
  }, [finished, queryClient]);

  const pending =
    orderMutation.isPending ||
    closeMutation.isPending ||
    addMarginMutation.isPending ||
    unlockMutation.isPending ||
    speedMutation.isPending;

  async function handleStart() {
    setStartError(null);
    const isGuest = me === null;
    const trimmed = guestName.trim();
    if (isGuest) {
      const length = guestNameLength(trimmed);
      if (length < 1 || length > 16) {
        setStartError('ゲスト名は 1〜16 文字で入力してください');
        return;
      }
    }

    try {
      const res = await startMutation.mutateAsync({ data: isGuest ? { guestName: trimmed } : {} });
      if (res.status !== 200) {
        setStartError(res.data.message);
        return;
      }
      const next = { gameId: res.data.gameId, token: res.data.token };
      storeSession(next);
      setSession(next);
      setActionError(null);
    } catch {
      setStartError('ゲームを開始できませんでした。通信状態を確認してください');
    }
  }

  function handleRestart() {
    storeSession(null);
    setSession(null);
    setGuestName('');
    setStartError(null);
    setActionError(null);
  }

  async function refetchGame() {
    await queryClient.invalidateQueries({ queryKey: getGetReplayGameStateQueryKey(gameId) });
  }

  async function handleOrder(
    pairId: number,
    side: ReplaySide,
    margin: string,
    leverage: string,
  ) {
    if (session === null) return;
    setActionError(null);
    try {
      const res = await orderMutation.mutateAsync({
        gameId: session.gameId,
        data: { pairId, side, margin, leverage },
      });
      if (res.status !== 200) {
        setActionError(res.data.message);
        return;
      }
      await refetchGame();
    } catch {
      setActionError('注文できませんでした。通信状態を確認してください');
    }
  }

  async function handleClose(positionId: number) {
    if (session === null) return;
    setActionError(null);
    try {
      const res = await closeMutation.mutateAsync({ gameId: session.gameId, positionId });
      if (res.status !== 200) {
        setActionError(res.data.message);
        return;
      }
      await refetchGame();
    } catch {
      setActionError('決済できませんでした。通信状態を確認してください');
    }
  }

  async function handleAddMargin(positionId: number, amount: string) {
    if (session === null) return;
    setActionError(null);
    try {
      const res = await addMarginMutation.mutateAsync({
        gameId: session.gameId,
        positionId,
        data: { amount },
      });
      if (res.status !== 200) {
        setActionError(res.data.message);
        return;
      }
      await refetchGame();
    } catch {
      setActionError('証拠金を追加できませんでした。通信状態を確認してください');
    }
  }

  async function handleUnlock(pairId: number) {
    if (session === null) return;
    setActionError(null);
    try {
      const res = await unlockMutation.mutateAsync({
        gameId: session.gameId,
        data: { pairId },
      });
      if (res.status !== 200) {
        setActionError(res.data.message);
        return;
      }
      await refetchGame();
    } catch {
      setActionError('レバレッジの上限を解放できませんでした。通信状態を確認してください');
    }
  }

  async function handleSpeed(speed: number) {
    if (session === null) return;
    setActionError(null);
    try {
      const res = await speedMutation.mutateAsync({
        gameId: session.gameId,
        data: { speed },
      });
      if (res.status !== 200) {
        setActionError(res.data.message);
        return;
      }
      await refetchGame();
    } catch {
      setActionError('速さを変更できませんでした。通信状態を確認してください');
    }
  }

  if (session === null) {
    return (
      <StartView
        meLoading={meLoading}
        displayName={me?.displayName ?? null}
        guestName={guestName}
        onGuestNameChange={setGuestName}
        onStart={() => void handleStart()}
        starting={startMutation.isPending}
        error={startError}
        ranking={ranking}
        rankingLoading={rankingQuery.isLoading}
      />
    );
  }

  if (stateQuery.isLoading && state === null) {
    return (
      <main className={styles.main}>
        <p className={styles.loading}>ゲームを読み込み中...</p>
      </main>
    );
  }

  if (state === null) {
    return (
      <main className={styles.main}>
        <p className={styles.error} role="alert">
          ゲームの状態を取得できませんでした。
        </p>
        <button type="button" className={styles.primary} onClick={handleRestart}>
          開始画面に戻る
        </button>
      </main>
    );
  }

  if (finished) {
    return (
      <ResultView
        state={state}
        ranking={ranking}
        rankingLoading={rankingQuery.isLoading}
        onRestart={handleRestart}
      />
    );
  }

  return (
    <ReplayPlay
      state={state}
      actionError={actionError}
      pending={pending}
      onOrder={(pairId, side, margin, leverage) =>
        void handleOrder(pairId, side, margin, leverage)
      }
      onClose={(positionId) => void handleClose(positionId)}
      onAddMargin={(positionId, amount) => void handleAddMargin(positionId, amount)}
      onUnlock={(pairId) => void handleUnlock(pairId)}
      onSpeed={(speed) => void handleSpeed(speed)}
    />
  );
}

interface StartViewProps {
  meLoading: boolean;
  displayName: string | null;
  guestName: string;
  onGuestNameChange: (value: string) => void;
  onStart: () => void;
  starting: boolean;
  error: string | null;
  ranking: Parameters<typeof ReplayRanking>[0]['entries'];
  rankingLoading: boolean;
}

function StartView({
  meLoading,
  displayName,
  guestName,
  onGuestNameChange,
  onStart,
  starting,
  error,
  ranking,
  rankingLoading,
}: StartViewProps) {
  return (
    <main className={styles.main}>
      <h1 className={styles.title}>過去チャートモード</h1>

      <section className={styles.card}>
        <h2 className={styles.heading}>モードを選ぶ</h2>
        <div className={styles.modeGrid}>
          <a href="/" className={styles.modeCard}>
            <strong>通常モード</strong>
            <span>みんなで同じ 1 時間に遊ぶモード。通貨一覧と取引はこちら。</span>
          </a>
          <div className={`${styles.modeCard} ${styles.modeCardActive}`}>
            <strong>過去チャートモード</strong>
            <span>過去の実チャートで 1 人 3 分。終了後にランキングに載ります。</span>
          </div>
        </div>
      </section>

      <section className={styles.card}>
        <h2 className={styles.heading}>ゲームを開始</h2>
        <p className={styles.sub}>
          仮想資金 1000 から始まり、4 つの通貨ペアを同時に売買できます。標準の速さで 3 分です。
        </p>
        {meLoading ? (
          <p className={styles.sub}>ログイン状態を確認しています...</p>
        ) : displayName === null ? (
          <label className={styles.field}>
            <span>ゲスト名（1〜16 文字）</span>
            <input
              aria-label="ゲスト名"
              type="text"
              maxLength={16}
              value={guestName}
              onChange={(event) => onGuestNameChange(event.target.value)}
              placeholder="例: ゲストA"
            />
          </label>
        ) : (
          <p className={styles.loginName}>
            <strong>{displayName}</strong> としてプレイします
          </p>
        )}
        {error !== null && (
          <p className={styles.error} role="alert">
            {error}
          </p>
        )}
        <button
          type="button"
          className={styles.primary}
          onClick={onStart}
          disabled={starting || meLoading}
        >
          {starting ? '開始しています...' : 'ゲームを開始'}
        </button>
        <p className={styles.sub}>
          ログインなしでも遊べます。ゲストは入力した名前でランキングに載ります。
        </p>
      </section>

      <ReplayRanking entries={ranking} isLoading={rankingLoading} />
      <p className={styles.source}>出典: EV Trading Labs</p>
    </main>
  );
}

interface ResultViewProps {
  state: ReplayGameState;
  ranking: Parameters<typeof ReplayRanking>[0]['entries'];
  rankingLoading: boolean;
  onRestart: () => void;
}

function ResultView({ state, ranking, rankingLoading, onRestart }: ResultViewProps) {
  const score = state.finalFunds ?? state.funds;
  const sentence = formatIntervalSentence(state.intervalStart ?? null, state.intervalEnd ?? null);
  const range = formatIntervalRange(state.intervalStart ?? null, state.intervalEnd ?? null);

  return (
    <main className={styles.main}>
      <section className={styles.result} role="status">
        <p className={styles.eyebrow}>結果</p>
        <h1 className={styles.resultScore}>
          スコア <strong>{formatPrice(score)}</strong>
        </h1>
        <p>
          {sentence ?? `${formatYear(state.startYear)}の相場でした`}
          {range === null && '（区間の日付は取得できませんでした）'}
        </p>
        {state.status === 'early_finished' && (
          <p className={styles.sub}>注文できる資金がなくなったため、途中で終了しました。</p>
        )}
        <button type="button" className={styles.primary} onClick={onRestart}>
          もう一度遊ぶ
        </button>
      </section>

      <ReplayRanking entries={ranking} isLoading={rankingLoading} />
      <p className={styles.source}>出典: EV Trading Labs</p>
    </main>
  );
}
