import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { useQueryClient } from '@tanstack/react-query';

import {
  getGetReplayGameStateQueryKey,
  getGetReplayRankingQueryKey,
  useAddReplayMargin,
  useBeginReplayGame,
  useChangeReplaySpeed,
  useCloseReplayPosition,
  useGetReplayGameState,
  usePlaceReplayOrder,
  useUnlockReplayLeverage,
} from '../api/generated/api';
import type { ReplayPosition } from '../api/generated/model';
import ReplayChart from '../components/replay/ReplayChart';
import ReplayCountdown from '../components/replay/ReplayCountdown';
import ReplayIntro, { markIntroSeen } from '../components/replay/ReplayIntro';
import ReplayResult from '../components/replay/ReplayResult';
import ReplayStart from '../components/replay/ReplayStart';
import { formatPrice, formatSide, formatSignedPrice } from '../lib/format';
import {
  REPLAY_INTERVAL_DAYS,
  REPLAY_LEVERAGE_STEPS,
  REPLAY_MARGIN_STEPS,
  REPLAY_NETWORK_ERROR_MESSAGE,
  REPLAY_SPEEDS,
  canReplayOrder,
  clampReplayLeverage,
  clampReplayMargin,
  formatRemainingTime,
  isPositiveAmount,
  isPositiveIntegerString,
  isReplayFinishedStatus,
  latestReplayClose,
  replayDayLabel,
  replayErrorMessage,
  replayMaxMargin,
  replayPollDelayMs,
  replayRemainingSeconds,
  stepReplayLeverage,
  stepReplayMargin,
  type ReplaySession,
} from '../lib/replay';
import {
  isReplayMuted,
  playReplayCoin,
  playReplayOrder,
  playReplaySettle,
  playReplayStart,
  setReplayMuted,
  startReplayBgm,
  stopReplayBgm,
  unlockReplayAudio,
} from '../lib/replayAudio';
import styles from './ReplayPage.module.css';

// ReplayPage は過去チャートモード（design.md §13）の入口。ゲームトークンは React の
// state だけで保持する（再読み込みでゲームは失われる。#105 で決めたこと）。
// 開始 API のあとに開始演出（#126 REPLAY-9）を挟み、「取引開始」で初めて時間が動く。
//
// #138 REPLAY-13: 「取引開始」を押した直後に不透明な最前面のカウントダウン（5 秒）を出し、
// その裏で ReplayGame をマウントして状態を読み込んでおく。読み込みが終わるまでは
// カウントダウン画面を消さない（「ゲームを読み込んでいます...」を見せない）。
export default function ReplayPage() {
  const [session, setSession] = useState<ReplaySession | null>(null);
  const [phase, setPhase] = useState<'intro' | 'playing'>('intro');
  // カウントダウンを始めた時刻（Date.now）。null ならカウントダウン中ではない。
  const [countdownStartedAt, setCountdownStartedAt] = useState<number | null>(null);
  // カウントダウンが終わってゲームが始まったか（BGM の開始に使う。#140 REPLAY-14）。
  const [countdownDone, setCountdownDone] = useState(false);
  // begin が成功したか。通信が終わるまではカウントダウン画面を消さない。
  const [beginSucceeded, setBeginSucceeded] = useState(false);
  // 裏の ReplayGame が状態を取得できたか。
  const [gameReady, setGameReady] = useState(false);
  const [beginError, setBeginError] = useState<string | null>(null);

  const beginMutation = useBeginReplayGame({
    fetch: { headers: { Authorization: `Bearer ${session?.token ?? ''}` } },
    mutation: {
      onSuccess: (res) => {
        if (res.status !== 200) {
          failBegin(replayErrorMessage(res, '取引を開始できませんでした'));
          return;
        }
        markIntroSeen();
        setBeginSucceeded(true);
      },
      onError: () => failBegin(REPLAY_NETWORK_ERROR_MESSAGE),
    },
  });

  function failBegin(message: string) {
    setPhase('intro');
    setCountdownStartedAt(null);
    setCountdownDone(false);
    setBeginSucceeded(false);
    setGameReady(false);
    setBeginError(message);
  }

  function start() {
    if (session == null) return;
    setBeginError(null);
    setGameReady(false);
    setBeginSucceeded(false);
    setCountdownDone(false);
    // 押した瞬間にカウントダウンを出す（サーバーは begin を受けた時刻 + 5 秒から動く）。
    setCountdownStartedAt(Date.now());
    setPhase('playing');
    // 自動再生制限があるため、AudioContext はこのクリックの中で開始する（#129 REPLAY-12）。
    unlockReplayAudio();
    // スタートの SE（#140 REPLAY-14）。
    playReplayStart();
    beginMutation.mutate({ gameId: session.gameId });
  }

  const handleLoaded = useCallback(() => setGameReady(true), []);
  const handleCountdownFinished = useCallback(() => setCountdownDone(true), []);

  const playAgain = useCallback(() => {
    setSession(null);
    setPhase('intro');
    setCountdownStartedAt(null);
    setCountdownDone(false);
    setBeginSucceeded(false);
    setGameReady(false);
    setBeginError(null);
  }, []);

  if (session == null) {
    return (
      <ReplayStart
        onStarted={(startedSession) => {
          setBeginError(null);
          setGameReady(false);
          setBeginSucceeded(false);
          setCountdownStartedAt(null);
          setCountdownDone(false);
          setPhase('intro');
          setSession(startedSession);
        }}
      />
    );
  }

  if (phase === 'intro') {
    return (
      <ReplayIntro
        session={session}
        onStart={start}
        starting={beginMutation.isPending}
        error={beginError}
      />
    );
  }

  return (
    <>
      <ReplayGame
        key={session.gameId}
        session={session}
        onPlayAgain={playAgain}
        onLoaded={handleLoaded}
        countdownDone={countdownDone}
      />
      {countdownStartedAt != null && (
        <ReplayCountdown
          startedAt={countdownStartedAt}
          ready={gameReady && beginSucceeded}
          onFinished={handleCountdownFinished}
        />
      )}
    </>
  );
}

interface ReplayGameProps {
  session: ReplaySession;
  onPlayAgain: () => void;
  // ゲーム状態の取得が終わった（成功・失敗のどちらでも）ときに呼ぶ。
  onLoaded: () => void;
  // カウントダウンが終わってゲームが始まったか（BGM の開始に使う）。
  countdownDone: boolean;
}

function ReplayGame({ session, onPlayAgain, onLoaded, countdownDone }: ReplayGameProps) {
  const { gameId, token } = session;
  const queryClient = useQueryClient();
  const authFetch = useMemo<RequestInit>(
    () => ({ headers: { Authorization: `Bearer ${token}` } }),
    [token],
  );

  const [actionError, setActionError] = useState<string | null>(null);
  const [selectedPairId, setSelectedPairId] = useState<number | null>(null);
  const [unlockConfirming, setUnlockConfirming] = useState(false);
  const [expandedPositions, setExpandedPositions] = useState<Set<number>>(() => new Set());
  const [marginInputs, setMarginInputs] = useState<Record<number, string>>({});
  const [orderMargin, setOrderMargin] = useState('100');
  const [orderLeverage, setOrderLeverage] = useState('1');
  // 速さの楽観的更新（#138 REPLAY-13）。押した瞬間に表示を切り替え、通信は裏で行う。
  // 連打に備えて、送信中に押された分は最新の 1 つだけを次に送る（最後に押した速さに揃える）。
  const [optimisticSpeed, setOptimisticSpeed] = useState<number | null>(null);
  const speedTargetRef = useRef<number | null>(null);
  const speedSendingRef = useRef(false);
  // 強制決済の演出（#129 REPLAY-12）。動きを止めず、注文の操作は妨げない。
  const [liquidating, setLiquidating] = useState(false);
  const [liquidationFlashKey, setLiquidationFlashKey] = useState(0);
  // 決済済みの建玉のうち、すでに演出を見たもの。新しく決済されたものだけに演出を出す。
  const seenClosedRef = useRef<Set<number> | null>(null);
  // 手動の決済で SE を鳴らした建玉。状態の取り直しで同じ決済の SE を二重に鳴らさない
  // （#140 REPLAY-14。利益の決済は playReplayCoin、それ以外は playReplaySettle）。
  const soundHandledRef = useRef<Set<number>>(new Set());
  // SE のミュート設定（localStorage に保存する）。
  const [muted, setMuted] = useState(isReplayMuted);

  const stateQuery = useGetReplayGameState(gameId, {
    query: {
      // 次の 1 日が公開される時刻に合わせて取り直す（#129 REPLAY-12）。終了後は止める。
      refetchInterval: (query) => {
        const res = query.state.data;
        if (!res || res.status !== 200) return 1000;
        if (isReplayFinishedStatus(res.data.status)) return false;
        const elapsed = Date.now() - query.state.dataUpdatedAt;
        return replayPollDelayMs(res.data.msUntilNextDay, elapsed, res.data.speed);
      },
    },
    fetch: authFetch,
  });

  const state = stateQuery.data?.status === 200 ? stateQuery.data.data : undefined;
  const finished = state != null && isReplayFinishedStatus(state.status);

  // カウントダウンが終わってゲームが始まったら BGM をループ再生する（#140 REPLAY-14）。
  // 結果画面に進んだとき・「もう一度」で離れたとき・画面を離れたとき（アンマウント）に止める。
  const gamePlaying = countdownDone && state != null && !finished;
  useEffect(() => {
    if (!gamePlaying) return;
    startReplayBgm();
    return () => stopReplayBgm();
  }, [gamePlaying]);

  // カウントダウン画面を消してよいかは親が決める。取得が終わったら知らせる。
  useEffect(() => {
    if (!stateQuery.isLoading) onLoaded();
  }, [stateQuery.isLoading, onLoaded]);

  // 結果画面に進んだら、ランキングのクエリを無効化して取り直す。
  useEffect(() => {
    if (!finished) return;
    void queryClient.invalidateQueries({ queryKey: getGetReplayRankingQueryKey() });
  }, [finished, queryClient]);

  // 新しく決済された建玉を見つけて、強制決済の演出と利益決済の SE を出す（#129 REPLAY-12）。
  // 初回の取得は「すでに決済済みだったもの」として扱い、演出しない。
  useEffect(() => {
    if (state == null) return;
    const seen = seenClosedRef.current;
    if (seen == null) {
      seenClosedRef.current = new Set(state.closedPositions.map((p) => p.id));
      return;
    }

    let liquidated = false;
    let profited = false;
    for (const position of state.closedPositions) {
      if (seen.has(position.id)) continue;
      seen.add(position.id);
      if (position.status === 'liquidated') liquidated = true;
      // 手動の決済は成功時に鳴らし済み。ここで鳴らすと二重になる（#140 REPLAY-14）。
      // ここで鳴らす場合も建玉 ID を記録し、応答順序が逆でも onSuccess 側で二重に鳴らさない。
      if (isPositiveAmount(position.pnl) && !soundHandledRef.current.has(position.id)) {
        soundHandledRef.current.add(position.id);
        profited = true;
      }
    }

    if (liquidated) {
      setLiquidating(true);
      setLiquidationFlashKey((key) => key + 1);
    }
    if (profited) playReplayCoin();
  }, [state]);

  // 強制決済の演出は一定時間で消す。演出中も注文・決済の操作は止めない。
  useEffect(() => {
    if (!liquidating) return;
    const timer = window.setTimeout(() => setLiquidating(false), 900);
    return () => window.clearTimeout(timer);
  }, [liquidating, liquidationFlashKey]);

  // サーバーが返した速さに追いついたら楽観的更新を終える（失敗時は呼び出し側で戻す）。
  // 送信中・送信待ちの間は、古い状態に追いついても表示を戻さない。
  useEffect(() => {
    if (speedSendingRef.current || speedTargetRef.current != null) return;
    if (optimisticSpeed != null && state?.speed === optimisticSpeed) {
      setOptimisticSpeed(null);
    }
  }, [optimisticSpeed, state?.speed]);

  function toggleMute() {
    const next = !muted;
    setMuted(next);
    setReplayMuted(next);
  }

  useEffect(() => {
    setUnlockConfirming(false);
  }, [selectedPairId]);

  function refreshState() {
    setActionError(null);
    void queryClient.invalidateQueries({ queryKey: getGetReplayGameStateQueryKey(gameId) });
  }

  function onActionResult(res: { status: number; data: unknown }, fallback: string) {
    if (res.status === 200) {
      refreshState();
      return;
    }
    setActionError(replayErrorMessage(res, fallback));
  }

  function onActionError() {
    setActionError(REPLAY_NETWORK_ERROR_MESSAGE);
  }

  const orderMutation = usePlaceReplayOrder({
    fetch: authFetch,
    mutation: {
      onSuccess: (res) => {
        // 注文が成功したときだけ購入の SE を鳴らす（失敗では鳴らさない。#140 REPLAY-14）。
        if (res.status === 200) playReplayOrder();
        onActionResult(res, '注文できませんでした');
      },
      onError: onActionError,
    },
  });
  const closeMutation = useCloseReplayPosition({
    fetch: authFetch,
    mutation: {
      onSuccess: (res) => {
        if (res.status === 200) {
          // 手動の決済が成功したときの SE。利益が出た決済は既存の利益の SE を鳴らし、
          // 決済の SE とは二重に鳴らさない（#140 REPLAY-14）。状態の取り直しが先に届いて
          // effect 側で鳴らし済みなら、ここでは何も鳴らさない（応答順序で二重にならない）。
          const positionId = res.data.position.id;
          if (!soundHandledRef.current.has(positionId)) {
            soundHandledRef.current.add(positionId);
            if (isPositiveAmount(res.data.pnl)) {
              playReplayCoin();
            } else {
              playReplaySettle();
            }
          }
        }
        onActionResult(res, '決済できませんでした');
      },
      onError: onActionError,
    },
  });
  const marginMutation = useAddReplayMargin({
    fetch: authFetch,
    mutation: {
      onSuccess: (res) => onActionResult(res, '証拠金を追加できませんでした'),
      onError: onActionError,
    },
  });
  const unlockMutation = useUnlockReplayLeverage({
    fetch: authFetch,
    mutation: {
      onSuccess: (res) => onActionResult(res, '上限を解放できませんでした'),
      onError: onActionError,
    },
  });
  // 速さは押した瞬間に表示を切り替え、通信中も次のクリックを受け付ける（#138 REPLAY-13）。
  const speedMutation = useChangeReplaySpeed({ fetch: authFetch });

  // 選択中のペア・公開済みの日足は、状態が無い間も安全に参照できるようにしておく。
  const selectedPair = state?.pairs.find((p) => p.pairId === selectedPairId) ?? state?.pairs[0];
  const displaySpeed = optimisticSpeed ?? state?.speed ?? 1;
  const maxMargin = state == null ? 1 : replayMaxMargin(state.funds);
  const canOrder = state != null && selectedPair != null && canReplayOrder(state.funds);

  // 上限が変わったら、入力中の値を 1〜上限に収める（#138 REPLAY-13）。
  const maxLeverage = selectedPair?.maxLeverage;
  useEffect(() => {
    if (maxLeverage == null) return;
    setOrderLeverage((prev) => String(clampReplayLeverage(prev, maxLeverage)));
  }, [maxLeverage]);
  useEffect(() => {
    if (state == null) return;
    setOrderMargin((prev) => String(clampReplayMargin(prev, state.funds)));
  }, [state?.funds]);

  if (state == null) {
    return (
      <main className={styles.main}>
        <section className={styles.card}>
          <h1 className={styles.title}>過去チャートモード</h1>
          {stateQuery.isLoading ? (
            <p className={styles.loading}>ゲームを読み込んでいます...</p>
          ) : (
            <>
              <p className={styles.error} role="alert">
                ゲームの状態を取得できませんでした。通信状況を確認してください。
              </p>
              <div className={styles.actions}>
                <button type="button" className={styles.primary} onClick={() => void stateQuery.refetch()}>
                  再読み込み
                </button>
                <button type="button" className={styles.secondary} onClick={onPlayAgain}>
                  最初からやり直す
                </button>
              </div>
            </>
          )}
        </section>
      </main>
    );
  }

  if (finished) {
    return <ReplayResult state={state} onPlayAgain={onPlayAgain} />;
  }

  const selectedCandles =
    state.candlesByPair.find((pc) => pc.pairId === selectedPair?.pairId)?.candles ?? [];
  const selectedPositions = state.positions.filter((p) => p.pairId === selectedPair?.pairId);
  const remaining = replayRemainingSeconds(state.publishedDays, displaySpeed);
  const clampedMargin = clampReplayMargin(orderMargin, state.funds);

  const submitOrder = (side: 'long' | 'short') => {
    if (!selectedPair) return;
    if (!canReplayOrder(state.funds)) {
      setActionError('資金がないため注文できません');
      return;
    }
    const margin = String(clampReplayMargin(orderMargin, state.funds));
    const leverage = String(clampReplayLeverage(orderLeverage, selectedPair.maxLeverage));
    setOrderMargin(margin);
    setOrderLeverage(leverage);
    setActionError(null);
    orderMutation.mutate({
      gameId,
      data: { pairId: selectedPair.pairId, side, margin, leverage },
    });
  };

  function addMargin(position: ReplayPosition) {
    const amount = (marginInputs[position.id] ?? '10').trim();
    if (!isPositiveIntegerString(amount)) {
      setActionError('追加する証拠金は 1 以上の整数で入力してください');
      return;
    }
    setActionError(null);
    marginMutation.mutate({
      gameId,
      positionId: position.id,
      data: { amount },
    });
  }

  function unlockLeverage() {
    if (!selectedPair) return;
    setUnlockConfirming(false);
    unlockMutation.mutate({ gameId, data: { pairId: selectedPair.pairId } });
  }

  async function changeSpeed(speed: number) {
    if (speed === displaySpeed) return;
    // 押した瞬間に表示を切り替える。通信中も次のクリックを受け付ける。
    setOptimisticSpeed(speed);
    setActionError(null);
    speedTargetRef.current = speed;
    if (speedSendingRef.current) return;

    speedSendingRef.current = true;
    try {
      // 送信中に押された分は最新の 1 つに畳んで、順番に送る（最後に押した速さが最後に届く）。
      while (speedTargetRef.current != null) {
        const target = speedTargetRef.current;
        speedTargetRef.current = null;
        setOptimisticSpeed(target);

        let res: Awaited<ReturnType<typeof speedMutation.mutateAsync>>;
        try {
          res = await speedMutation.mutateAsync({ gameId, data: { speed: target } });
        } catch {
          // 新しい指定が待っていれば、そちらを送る。最後の指定が失敗したときだけ戻す。
          if (speedTargetRef.current == null) {
            setOptimisticSpeed(null);
            setActionError(REPLAY_NETWORK_ERROR_MESSAGE);
          }
          continue;
        }

        if (res.status === 200) {
          void queryClient.invalidateQueries({
            queryKey: getGetReplayGameStateQueryKey(gameId),
          });
          continue;
        }
        if (speedTargetRef.current == null) {
          setOptimisticSpeed(null);
          setActionError(replayErrorMessage(res, '速さを変更できませんでした'));
        }
      }
    } finally {
      speedSendingRef.current = false;
    }
  }

  function togglePosition(positionID: number) {
    setExpandedPositions((prev) => {
      const next = new Set(prev);
      if (next.has(positionID)) {
        next.delete(positionID);
      } else {
        next.add(positionID);
      }
      return next;
    });
  }

  return (
    <main className={`${styles.main} ${styles.gameMain} ${liquidating ? styles.liquidating : ''}`}>
      {liquidating &&
        createPortal(
          <div key={liquidationFlashKey} className={styles.flashOverlay} aria-hidden="true" />,
          document.body,
        )}
      <section className={styles.status}>
        <div className={styles.era}>
          <span className={styles.eraLabel}>この相場の開始年</span>
          <span className={styles.eraValue}>{state.startYear}年</span>
        </div>
        <div className={styles.progress}>
          <div className={styles.progressText}>
            <span>
              {state.publishedDays} / {REPLAY_INTERVAL_DAYS} 営業日
            </span>
            <span>残り {formatRemainingTime(remaining)}</span>
          </div>
          <div className={styles.progressBar}>
            <div
              className={styles.progressFill}
              style={{ width: `${(state.publishedDays / REPLAY_INTERVAL_DAYS) * 100}%` }}
            />
          </div>
        </div>
        <div className={styles.money} aria-label="資金と含み損益">
          <div className={styles.moneyItem}>
            <span className={styles.moneyLabel}>資金</span>
            <span className={styles.moneyValue}>{formatPrice(state.funds)}</span>
          </div>
          <div className={styles.moneyItem}>
            <span className={styles.moneyLabel}>含み損益（合計）</span>
            <span
              className={`${styles.moneyValue} ${
                state.totalUnrealizedPnl.startsWith('-') ? styles.loss : styles.profit
              }`}
            >
              {formatSignedPrice(state.totalUnrealizedPnl)}
            </span>
          </div>
        </div>
        <div className={styles.controls} aria-label="速さと音">
          {REPLAY_SPEEDS.map((speed) => (
            <button
              key={speed}
              type="button"
              className={speed === displaySpeed ? styles.toggleActive : styles.toggle}
              onClick={() => changeSpeed(speed)}
              aria-pressed={speed === displaySpeed}
            >
              ×{speed}
            </button>
          ))}
          <button
            type="button"
            className={styles.toggle}
            onClick={toggleMute}
            aria-pressed={muted}
            title="効果音とBGMを鳴らすかどうか"
          >
            {muted ? '音を出す' : '音を消す'}
          </button>
        </div>
      </section>

      <div className={styles.sourceRow}>
        <p className={styles.source}>出典: EV Trading Labs</p>
        <p className={styles.sub}>一時停止はありません。速さだけ変えられます。</p>
      </div>

      {actionError != null && (
        <p className={styles.error} role="alert">
          {actionError}
        </p>
      )}

      <div className={styles.gameGrid}>
        <aside className={`${styles.column} ${styles.columnLeft}`}>
          <section className={`${styles.card} ${styles.positionsCard}`}>
            <h2 className={styles.heading}>保有（{state.positions.length} 件）</h2>
            <p className={styles.sub}>
              証拠金を追加すると、取引規模と価格変化に対する損益は変わらず、強制決済ラインが
              遠ざかります。同じペア・同じ向きの建玉は複数持てます。
            </p>

            {state.positions.length === 0 ? (
              <p className={styles.sub}>
                保有はありません。通貨ペアを選んで「買う」「売る」を押してください。
              </p>
            ) : (
              <ul className={styles.positions}>
                {state.positions.map((position) => {
                  const expanded = expandedPositions.has(position.id);
                  return (
                    <li key={position.id} className={styles.position}>
                      <div className={styles.positionHead}>
                        <span className={styles.positionPair}>
                          {position.symbol}
                          <span
                            className={
                              position.side === 'short' ? styles.sideSell : styles.sideBuy
                            }
                          >
                            {formatSide(position.side)}
                          </span>
                        </span>
                        <span
                          className={
                            position.unrealizedPnl?.startsWith('-')
                              ? styles.loss
                              : styles.profit
                          }
                        >
                          {position.unrealizedPnl == null
                            ? '—'
                            : formatSignedPrice(position.unrealizedPnl)}
                        </span>
                        <button
                          type="button"
                          className={styles.secondary}
                          onClick={() =>
                            closeMutation.mutate({ gameId, positionId: position.id })
                          }
                          disabled={closeMutation.isPending}
                        >
                          決済
                        </button>
                      </div>
                      <button
                        type="button"
                        className={styles.positionToggle}
                        onClick={() => togglePosition(position.id)}
                        aria-expanded={expanded}
                      >
                        {expanded ? '詳細を閉じる' : '建値・強制決済ラインを見る'}
                      </button>
                      {expanded && (
                        <div className={styles.positionDetails}>
                          <p className={styles.positionDetail}>
                            証拠金 {formatPrice(position.margin)} / レバレッジ{' '}
                            {position.leverage} 倍
                          </p>
                          <p className={styles.positionDetail}>
                            建値 {formatPrice(position.entryPrice)}（
                            {replayDayLabel(position.entryDay)}）
                          </p>
                          <p className={styles.positionDetail}>
                            強制決済ライン {formatPrice(position.liquidationLine)}
                          </p>
                          <div className={styles.positionActions}>
                            <input
                              className={styles.smallInput}
                              type="number"
                              min={1}
                              step={1}
                              inputMode="numeric"
                              aria-label={`${position.symbol} に追加する証拠金`}
                              value={marginInputs[position.id] ?? '10'}
                              onChange={(e) =>
                                setMarginInputs((prev) => ({
                                  ...prev,
                                  [position.id]: e.target.value,
                                }))
                              }
                            />
                            <button
                              type="button"
                              className={styles.secondary}
                              onClick={() => addMargin(position)}
                              disabled={marginMutation.isPending}
                            >
                              証拠金を追加
                            </button>
                          </div>
                        </div>
                      )}
                    </li>
                  );
                })}
              </ul>
            )}
          </section>
        </aside>

        <div className={`${styles.column} ${styles.columnCenter}`}>
          <section className={styles.pairs} aria-label="通貨ペア">
            {state.pairs.map((pair) => {
              const candles =
                state.candlesByPair.find((pc) => pc.pairId === pair.pairId)?.candles ?? [];
              const close = latestReplayClose(candles);
              const holding = state.positions.filter((p) => p.pairId === pair.pairId).length;
              return (
                <button
                  key={pair.pairId}
                  type="button"
                  className={pair.pairId === selectedPair?.pairId ? styles.pairActive : styles.pair}
                  onClick={() => setSelectedPairId(pair.pairId)}
                >
                  <span className={styles.pairSymbol}>{pair.symbol}</span>
                  <span className={styles.pairPrice}>{close ? formatPrice(close) : '—'}</span>
                  <span className={styles.pairLimit}>
                    上限 {formatPrice(pair.maxLeverage)} 倍
                  </span>
                  {holding > 0 && <span className={styles.badge}>保有 {holding}</span>}
                </button>
              );
            })}
          </section>

          <section className={styles.card}>
            <h2 className={styles.heading}>
              {selectedPair?.symbol ?? 'チャート'}{' '}
              <span className={styles.sub}>日足（1 本 = 1 営業日）</span>
            </h2>
            <ReplayChart
              pairId={selectedPair?.pairId ?? 0}
              candles={selectedCandles}
              positions={selectedPositions}
            />
            <p className={styles.sub}>
              薄い部分は開始前の 20 営業日です。価格はプレイヤーの売買では動きません。
            </p>
          </section>

          <section className={styles.card}>
            <h2 className={styles.heading}>注文</h2>
            {selectedPair && (
              <p className={styles.sub}>
                {selectedPair.symbol} を、証拠金とレバレッジで指定します。約定は公開済みの最新の
                終値です。片道手数料率 {formatPrice(selectedPair.feeRate)}。
              </p>
            )}

            <div className={styles.fieldRow}>
              <div className={styles.field}>
                <span className={styles.fieldLabel}>証拠金（1〜{maxMargin || 1}）</span>
                <input
                  className={styles.textInput}
                  type="number"
                  min={1}
                  max={maxMargin || 1}
                  step={1}
                  inputMode="numeric"
                  aria-label="証拠金"
                  value={orderMargin}
                  onChange={(e) => setOrderMargin(e.target.value)}
                  onBlur={() =>
                    setOrderMargin(String(clampReplayMargin(orderMargin, state.funds)))
                  }
                  disabled={!canOrder}
                />
                <div className={styles.stepRow}>
                  {REPLAY_MARGIN_STEPS.map((delta) => (
                    <button
                      key={delta}
                      type="button"
                      className={styles.stepButton}
                      onClick={() =>
                        setOrderMargin(stepReplayMargin(orderMargin, delta, state.funds))
                      }
                      disabled={!canOrder}
                    >
                      {delta > 0 ? `+${delta}` : delta}
                    </button>
                  ))}
                </div>
                <input
                  className={styles.slider}
                  type="range"
                  min={1}
                  max={maxMargin || 1}
                  step={1}
                  value={clampedMargin}
                  aria-label="証拠金のスライダー"
                  onChange={(e) => setOrderMargin(e.target.value)}
                  disabled={!canOrder}
                />
              </div>

              <div className={styles.field}>
                <span className={styles.fieldLabel}>
                  レバレッジ（1〜{selectedPair ? formatPrice(selectedPair.maxLeverage) : 1} 倍）
                </span>
                <input
                  className={styles.textInput}
                  type="number"
                  min={1}
                  max={selectedPair ? Number(selectedPair.maxLeverage) : 1}
                  step={1}
                  inputMode="numeric"
                  aria-label="レバレッジ"
                  value={orderLeverage}
                  onChange={(e) => setOrderLeverage(e.target.value)}
                  onBlur={() =>
                    setOrderLeverage(
                      String(
                        clampReplayLeverage(
                          orderLeverage,
                          selectedPair?.maxLeverage ?? '1',
                        ),
                      ),
                    )
                  }
                  disabled={!canOrder}
                />
                <div className={styles.stepRow}>
                  {REPLAY_LEVERAGE_STEPS.map((delta) => (
                    <button
                      key={delta}
                      type="button"
                      className={styles.stepButton}
                      onClick={() =>
                        setOrderLeverage(
                          stepReplayLeverage(
                            orderLeverage,
                            delta,
                            selectedPair?.maxLeverage ?? '1',
                          ),
                        )
                      }
                      disabled={!canOrder}
                    >
                      {delta > 0 ? `+${delta}` : delta}
                    </button>
                  ))}
                </div>
              </div>
            </div>
            {selectedPair && (
              <p className={styles.sub}>
                現在の上限 {formatPrice(selectedPair.maxLeverage)} 倍（初期上限{' '}
                {formatPrice(selectedPair.baseLeverage)} 倍）
              </p>
            )}

            <div className={styles.orderButtons}>
              <button
                type="button"
                className={styles.buy}
                onClick={() => submitOrder('long')}
                disabled={!canOrder || orderMutation.isPending}
              >
                買う
                <span className={styles.hint}>上がると得</span>
              </button>
              <button
                type="button"
                className={styles.sell}
                onClick={() => submitOrder('short')}
                disabled={!canOrder || orderMutation.isPending}
              >
                売る
                <span className={styles.hint}>下がると得</span>
              </button>
            </div>

            {selectedPair && (
              <div className={styles.unlock}>
                <p className={styles.unlockTitle}>レバレッジの上限解放</p>
                <p className={styles.sub}>
                  次の段：上限 {formatPrice(selectedPair.nextUnlockLeverage)} 倍 / 費用{' '}
                  {formatPrice(selectedPair.nextUnlockCost)}
                </p>
                {unlockConfirming ? (
                  <div className={styles.confirmBox}>
                    <p>
                      上限を {formatPrice(selectedPair.nextUnlockLeverage)} 倍に上げ、費用{' '}
                      {formatPrice(selectedPair.nextUnlockCost)} を資金から支払います。よろしいですか？
                    </p>
                    <div className={styles.actions}>
                      <button
                        type="button"
                        className={styles.primary}
                        onClick={unlockLeverage}
                        disabled={unlockMutation.isPending}
                      >
                        支払って解放する
                      </button>
                      <button
                        type="button"
                        className={styles.secondary}
                        onClick={() => setUnlockConfirming(false)}
                      >
                        キャンセル
                      </button>
                    </div>
                  </div>
                ) : (
                  <button
                    type="button"
                    className={styles.secondary}
                    onClick={() => setUnlockConfirming(true)}
                  >
                    上限を解放する
                  </button>
                )}
              </div>
            )}
          </section>
        </div>

        <aside className={`${styles.column} ${styles.columnRight}`}>
          <section className={`${styles.card} ${styles.eventsCard}`}>
            <h2 className={styles.heading}>出来事</h2>
            <p className={styles.sub}>出来事はここに表示されます</p>
          </section>
        </aside>
      </div>
    </main>
  );
}
