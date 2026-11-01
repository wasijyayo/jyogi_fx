import { useEffect, useMemo, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';

import {
  getGetReplayGameStateQueryKey,
  getGetReplayRankingQueryKey,
  useAddReplayMargin,
  useChangeReplaySpeed,
  useCloseReplayPosition,
  useGetReplayGameState,
  usePlaceReplayOrder,
  useUnlockReplayLeverage,
} from '../api/generated/api';
import type { ReplayPosition } from '../api/generated/model';
import ReplayChart from '../components/replay/ReplayChart';
import ReplayResult from '../components/replay/ReplayResult';
import ReplayStart from '../components/replay/ReplayStart';
import { formatPrice, formatSide, formatSignedPrice } from '../lib/format';
import {
  REPLAY_INTERVAL_DAYS,
  REPLAY_NETWORK_ERROR_MESSAGE,
  REPLAY_SPEEDS,
  formatRemainingTime,
  isPositiveIntegerString,
  isReplayFinishedStatus,
  latestReplayClose,
  replayDayLabel,
  replayErrorMessage,
  replayPollIntervalMs,
  replayRemainingSeconds,
  type ReplaySession,
} from '../lib/replay';
import styles from './ReplayPage.module.css';

// ReplayPage は過去チャートモード（design.md §13）の入口。ゲームトークンは React の
// state だけで保持する（再読み込みでゲームは失われる。#105 で決めたこと）。
export default function ReplayPage() {
  const [session, setSession] = useState<ReplaySession | null>(null);

  if (session == null) {
    return <ReplayStart onStarted={setSession} />;
  }
  return (
    <ReplayGame
      key={session.gameId}
      session={session}
      onPlayAgain={() => setSession(null)}
    />
  );
}

interface ReplayGameProps {
  session: ReplaySession;
  onPlayAgain: () => void;
}

function ReplayGame({ session, onPlayAgain }: ReplayGameProps) {
  const { gameId, token } = session;
  const queryClient = useQueryClient();
  const authFetch = useMemo<RequestInit>(
    () => ({ headers: { Authorization: `Bearer ${token}` } }),
    [token],
  );

  const [actionError, setActionError] = useState<string | null>(null);
  const [selectedPairId, setSelectedPairId] = useState<number | null>(null);
  const [unlockConfirming, setUnlockConfirming] = useState(false);
  const [marginInputs, setMarginInputs] = useState<Record<number, string>>({});
  const [orderMargin, setOrderMargin] = useState('100');
  const [orderLeverage, setOrderLeverage] = useState('1');

  const stateQuery = useGetReplayGameState(gameId, {
    query: {
      // 3 秒 ÷ 速さ 以下を目安にポーリングする（WebSocket は使わない）。終了後は止める。
      refetchInterval: (query) => {
        const res = query.state.data;
        if (!res || res.status !== 200) return 1000;
        if (isReplayFinishedStatus(res.data.status)) return false;
        return replayPollIntervalMs(res.data.speed);
      },
    },
    fetch: authFetch,
  });

  const state = stateQuery.data?.status === 200 ? stateQuery.data.data : undefined;
  const finished = state != null && isReplayFinishedStatus(state.status);

  // 結果画面に進んだら、ランキングのクエリを無効化して取り直す。
  useEffect(() => {
    if (!finished) return;
    void queryClient.invalidateQueries({ queryKey: getGetReplayRankingQueryKey() });
  }, [finished, queryClient]);

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
      onSuccess: (res) => onActionResult(res, '注文できませんでした'),
      onError: onActionError,
    },
  });
  const closeMutation = useCloseReplayPosition({
    fetch: authFetch,
    mutation: {
      onSuccess: (res) => onActionResult(res, '決済できませんでした'),
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
  const speedMutation = useChangeReplaySpeed({
    fetch: authFetch,
    mutation: {
      onSuccess: (res) => onActionResult(res, '速さを変更できませんでした'),
      onError: onActionError,
    },
  });

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

  const selectedPair =
    state.pairs.find((p) => p.pairId === selectedPairId) ?? state.pairs[0];
  const selectedCandles =
    state.candlesByPair.find((pc) => pc.pairId === selectedPair?.pairId)?.candles ?? [];
  const selectedPositions = state.positions.filter((p) => p.pairId === selectedPair?.pairId);
  const remaining = replayRemainingSeconds(state.publishedDays, state.speed);

  function submitOrder(side: 'long' | 'short') {
    if (!selectedPair) return;
    const margin = orderMargin.trim();
    const leverage = orderLeverage.trim();
    if (!isPositiveIntegerString(margin)) {
      setActionError('証拠金は 1 以上の整数で入力してください');
      return;
    }
    if (!isPositiveIntegerString(leverage)) {
      setActionError('レバレッジは 1 以上の整数で入力してください');
      return;
    }
    if (Number(leverage) > Number(selectedPair.maxLeverage)) {
      setActionError(
        `レバレッジは現在の上限 ${formatPrice(selectedPair.maxLeverage)} 倍以下で入力してください`,
      );
      return;
    }
    setActionError(null);
    orderMutation.mutate({
      gameId,
      data: { pairId: selectedPair.pairId, side, margin, leverage },
    });
  }

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

  function changeSpeed(speed: number) {
    if (speed === state?.speed) return;
    speedMutation.mutate({ gameId, data: { speed } });
  }

  return (
    <main className={styles.main}>
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
        <div className={styles.controls} aria-label="速さ">
          {REPLAY_SPEEDS.map((speed) => (
            <button
              key={speed}
              type="button"
              className={speed === state.speed ? styles.toggleActive : styles.toggle}
              onClick={() => changeSpeed(speed)}
              disabled={speedMutation.isPending}
            >
              ×{speed}
            </button>
          ))}
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
              <span className={styles.pairLimit}>上限 {formatPrice(pair.maxLeverage)} 倍</span>
              {holding > 0 && <span className={styles.badge}>保有 {holding}</span>}
            </button>
          );
        })}
      </section>

      <div className={styles.layout}>
        <section className={styles.card}>
          <h2 className={styles.heading}>
            {selectedPair?.symbol ?? 'チャート'}{' '}
            <span className={styles.sub}>日足（1 本 = 1 営業日）</span>
          </h2>
          <ReplayChart candles={selectedCandles} positions={selectedPositions} />
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
            <label className={styles.field}>
              <span className={styles.fieldLabel}>証拠金（1 以上）</span>
              <input
                className={styles.textInput}
                type="number"
                min={1}
                step={1}
                inputMode="numeric"
                value={orderMargin}
                onChange={(e) => setOrderMargin(e.target.value)}
              />
            </label>
            <label className={styles.field}>
              <span className={styles.fieldLabel}>レバレッジ（1 倍刻み）</span>
              <input
                className={styles.textInput}
                type="number"
                min={1}
                step={1}
                inputMode="numeric"
                value={orderLeverage}
                onChange={(e) => setOrderLeverage(e.target.value)}
              />
            </label>
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
              disabled={orderMutation.isPending || !selectedPair}
            >
              買う
              <span className={styles.hint}>上がると得</span>
            </button>
            <button
              type="button"
              className={styles.sell}
              onClick={() => submitOrder('short')}
              disabled={orderMutation.isPending || !selectedPair}
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

      <section className={styles.card}>
        <h2 className={styles.heading}>資金と含み損益</h2>
        <dl className={styles.summary}>
          <div>
            <dt>資金</dt>
            <dd>{formatPrice(state.funds)}</dd>
          </div>
          <div>
            <dt>含み損益（合計）</dt>
            <dd
              className={
                state.totalUnrealizedPnl.startsWith('-') ? styles.loss : styles.profit
              }
            >
              {formatSignedPrice(state.totalUnrealizedPnl)}
            </dd>
          </div>
          <div>
            <dt>含み損益（建玉ごと）</dt>
            <dd>
              {state.positions.length === 0 ? (
                '—'
              ) : (
                <ul className={styles.pnlList}>
                  {state.positions.map((p) => (
                    <li key={p.id}>
                      {p.symbol} {formatSide(p.side)}{' '}
                      <span
                        className={
                          p.unrealizedPnl?.startsWith('-') ? styles.loss : styles.profit
                        }
                      >
                        {p.unrealizedPnl == null ? '—' : formatSignedPrice(p.unrealizedPnl)}
                      </span>
                    </li>
                  ))}
                </ul>
              )}
            </dd>
          </div>
          <div>
            <dt>未決済の建玉</dt>
            <dd>{state.positions.length} 件</dd>
          </div>
        </dl>
      </section>

      <section className={styles.card}>
        <h2 className={styles.heading}>保有一覧（{state.positions.length} 件）</h2>
        <p className={styles.sub}>
          証拠金を追加すると、取引規模と価格変化に対する損益は変わらず、強制決済ラインが
          遠ざかります。同じペア・同じ向きの建玉は複数持てます。
        </p>

        {state.positions.length === 0 ? (
          <p className={styles.sub}>保有はありません。通貨ペアを選んで「買う」「売る」を押してください。</p>
        ) : (
          <ul className={styles.positions}>
            {state.positions.map((position) => (
              <li key={position.id} className={styles.position}>
                <div className={styles.positionMain}>
                  <span className={styles.positionPair}>
                    {position.symbol}
                    <span
                      className={position.side === 'short' ? styles.sideSell : styles.sideBuy}
                    >
                      {formatSide(position.side)}
                    </span>
                  </span>
                  <span className={styles.positionDetail}>
                    証拠金 {formatPrice(position.margin)} / レバレッジ {position.leverage} 倍 /
                    建値 {formatPrice(position.entryPrice)}（{replayDayLabel(position.entryDay)}
                    ）
                  </span>
                </div>
                <div className={styles.positionFoot}>
                  <span>
                    強制決済ライン {formatPrice(position.liquidationLine)} ・ 含み損益{' '}
                    <span
                      className={
                        position.unrealizedPnl?.startsWith('-') ? styles.loss : styles.profit
                      }
                    >
                      {position.unrealizedPnl == null
                        ? '—'
                        : formatSignedPrice(position.unrealizedPnl)}
                    </span>
                  </span>
                  <span className={styles.positionActions}>
                    <input
                      className={styles.smallInput}
                      type="number"
                      min={1}
                      step={1}
                      inputMode="numeric"
                      aria-label={`${position.symbol} に追加する証拠金`}
                      value={marginInputs[position.id] ?? '10'}
                      onChange={(e) =>
                        setMarginInputs((prev) => ({ ...prev, [position.id]: e.target.value }))
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
                    <button
                      type="button"
                      className={styles.secondary}
                      onClick={() => closeMutation.mutate({ gameId, positionId: position.id })}
                      disabled={closeMutation.isPending}
                    >
                      決済
                    </button>
                  </span>
                </div>
              </li>
            ))}
          </ul>
        )}
      </section>
    </main>
  );
}
