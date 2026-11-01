import { useMemo, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';

import {
  getGetReplayGameStateQueryKey,
  useGetReplayGameState,
  usePlaceReplayOrder,
} from '../api/generated/api';
import ReplayChart from '../components/replay/ReplayChart';
import ReplayStart from '../components/replay/ReplayStart';
import { formatPrice, formatSide, formatSignedPrice } from '../lib/format';
import {
  REPLAY_INTERVAL_DAYS,
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
  const [orderMargin, setOrderMargin] = useState('100');
  const [orderLeverage, setOrderLeverage] = useState('1');

  const stateQuery = useGetReplayGameState(gameId, {
    query: {
      // 3 秒 ÷ 速さ 以下を目安にポーリングする（WebSocket は使わない）。
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

  const orderMutation = usePlaceReplayOrder({
    fetch: authFetch,
    mutation: {
      onSuccess: (res) => {
        if (res.status === 200) {
          setActionError(null);
          void queryClient.invalidateQueries({ queryKey: getGetReplayGameStateQueryKey(gameId) });
          return;
        }
        setActionError(replayErrorMessage(res, '注文できませんでした'));
      },
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

  if (isReplayFinishedStatus(state.status)) {
    return (
      <main className={styles.main}>
        <section className={styles.result} role="status">
          <h1 className={styles.title}>ゲーム終了</h1>
          <p className={styles.scoreLabel}>スコア（最終資金）</p>
          <p className={styles.score}>{formatPrice(state.finalFunds ?? state.funds)}</p>
          <div className={styles.actions}>
            <button type="button" className={styles.primary} onClick={onPlayAgain}>
              もう一度遊ぶ
            </button>
          </div>
          <p className={styles.source}>出典: EV Trading Labs</p>
        </section>
      </main>
    );
  }

  const selectedPair =
    state.pairs.find((p) => p.pairId === selectedPairId) ?? state.pairs[0];
  const selectedCandles =
    state.candlesByPair.find((pc) => pc.pairId === selectedPair?.pairId)?.candles ?? [];
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
      </section>

      <div className={styles.sourceRow}>
        <p className={styles.source}>出典: EV Trading Labs</p>
        <p className={styles.sub}>一時停止はありません。</p>
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
          <ReplayChart candles={selectedCandles} />
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
                </div>
              </li>
            ))}
          </ul>
        )}
      </section>
    </main>
  );
}
