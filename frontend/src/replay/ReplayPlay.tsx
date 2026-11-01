import { useEffect, useMemo, useState } from 'react';

import type { ReplayGameState, ReplayPosition } from '../api/generated/model';
import { formatPrice, formatSide } from '../lib/format';
import ReplayChart from './ReplayChart';
import {
  REPLAY_INTERVAL_DAYS,
  REPLAY_PRE_DAYS,
  REPLAY_SPEEDS,
  formatAmount,
  formatPercent,
  formatRemaining,
  formatSignedAmount,
  formatYear,
  isPositiveInteger,
  latestClose,
  nextLeverageUnlock,
  numberFromDecimal,
  positionProfit,
  remainingSeconds,
  summarize,
  validateReplayOrder,
  type ReplaySide,
} from './replay';
import styles from './ReplayPage.module.css';

interface ReplayPlayProps {
  state: ReplayGameState;
  actionError: string | null;
  pending: boolean;
  onOrder: (pairId: number, side: ReplaySide, margin: string, leverage: string) => void;
  onClose: (positionId: number) => void;
  onAddMargin: (positionId: number, amount: string) => void;
  onUnlock: (pairId: number) => void;
  onSpeed: (speed: number) => void;
}

// ReplayPlay は過去チャートモードのプレイ画面（design.md §13）。
// 一時停止は無く、速さ（×1・×2・×3）だけを切り替えられる。価格はサーバーが
// 公開した分だけを表示し、注文・決済の結果もサーバーが返したものを使う。
export default function ReplayPlay({
  state,
  actionError,
  pending,
  onOrder,
  onClose,
  onAddMargin,
  onUnlock,
  onSpeed,
}: ReplayPlayProps) {
  const [selectedPairId, setSelectedPairId] = useState(state.pairs[0]?.pairId ?? 0);
  const [margin, setMargin] = useState('100');
  const [leverage, setLeverage] = useState('1');
  const [localError, setLocalError] = useState<string | null>(null);
  const [unlockOpen, setUnlockOpen] = useState(false);

  const pairById = useMemo(
    () => new Map(state.pairs.map((pair) => [pair.pairId, pair])),
    [state.pairs],
  );
  const candlesByPair = useMemo(
    () => new Map(state.candlesByPair.map((entry) => [entry.pairId, entry.candles])),
    [state.candlesByPair],
  );

  const selectedPair = pairById.get(selectedPairId) ?? state.pairs[0];
  const selectedPairIdSafe = selectedPair?.pairId ?? 0;
  const selectedCandles = candlesByPair.get(selectedPairIdSafe) ?? [];
  const selectedPositions = state.positions.filter(
    (position) => position.pairId === selectedPairIdSafe && position.status === 'open',
  );
  const openPositions = state.positions.filter((position) => position.status === 'open');
  const summary = summarize(state);

  // 選択中のペアを変えたら、解放の確認は閉じる。
  useEffect(() => {
    setUnlockOpen(false);
    setLocalError(null);
  }, [selectedPairIdSafe]);

  if (selectedPair === undefined) {
    return (
      <main className={styles.main}>
        <p className={styles.error} role="alert">
          通貨ペアの情報がありません。
        </p>
      </main>
    );
  }

  const maxLeverage = Math.round(numberFromDecimal(selectedPair.maxLeverage));
  const baseLeverage = Math.round(numberFromDecimal(selectedPair.baseLeverage));
  const feePercent = (numberFromDecimal(selectedPair.feeRate) * 100).toFixed(2);
  const unlock = nextLeverageUnlock(selectedPair);
  const hasLong = selectedPositions.some((position) => position.side === 'long');
  const hasShort = selectedPositions.some((position) => position.side === 'short');
  const remaining = remainingSeconds(state.publishedDays, state.speed);
  const progress = Math.min(100, (state.publishedDays / REPLAY_INTERVAL_DAYS) * 100);

  function submitOrder(side: ReplaySide) {
    const error = validateReplayOrder(margin, leverage, maxLeverage);
    setLocalError(error);
    if (error === null) onOrder(selectedPairIdSafe, side, margin, leverage);
  }

  return (
    <main className={styles.main}>
      <section className={styles.topbar}>
        <div className={styles.year}>
          <span className={styles.eyebrow}>この相場の年</span>
          <strong className={styles.yearValue}>{formatYear(state.startYear)}</strong>
          <span className={styles.sub}>月日は終了時に公開します</span>
        </div>
        <div className={styles.progress}>
          <div className={styles.progressText}>
            <span>
              {state.publishedDays} / {REPLAY_INTERVAL_DAYS} 日目
            </span>
            <span>残り {formatRemaining(remaining)}</span>
          </div>
          <div className={styles.progressBar}>
            <div className={styles.progressFill} style={{ width: `${progress}%` }} />
          </div>
          <span className={styles.sub}>開始前の {REPLAY_PRE_DAYS} 営業日も表示しています</span>
        </div>
        <div className={styles.speeds}>
          <span className={styles.sub}>速さ</span>
          <div className={styles.speedButtons}>
            {REPLAY_SPEEDS.map((speed) => (
              <button
                key={speed}
                type="button"
                className={speed === state.speed ? styles.toggleActive : styles.toggle}
                onClick={() => onSpeed(speed)}
                disabled={pending}
                aria-pressed={speed === state.speed}
              >
                ×{speed}
              </button>
            ))}
          </div>
        </div>
      </section>

      {(actionError !== null || localError !== null) && (
        <p className={styles.error} role="alert">
          {localError ?? actionError}
        </p>
      )}

      <section className={styles.pairs} aria-label="通貨ペア">
        {state.pairs.map((pair) => {
          const candles = candlesByPair.get(pair.pairId) ?? [];
          const last = latestClose(candles);
          const first = candles.length > 0 ? numberFromDecimal(candles[0].close) : 0;
          const change =
            last !== null && first > 0 ? (numberFromDecimal(last) / first - 1) * 100 : 0;
          const holdings = state.positions.filter(
            (position) => position.pairId === pair.pairId && position.status === 'open',
          ).length;
          return (
            <button
              key={pair.pairId}
              type="button"
              className={pair.pairId === selectedPairIdSafe ? styles.pairActive : styles.pair}
              onClick={() => setSelectedPairId(pair.pairId)}
            >
              <span className={styles.pairSymbol}>{pair.symbol}</span>
              <span className={styles.pairPrice}>
                {last === null ? '—' : formatPrice(last)}
              </span>
              <span className={changeClass(change)}>{formatPercent(change)}</span>
              {holdings > 0 && <span className={styles.badge}>保有 {holdings}</span>}
            </button>
          );
        })}
      </section>

      <div className={styles.layout}>
        <section className={styles.card}>
          <h2 className={styles.heading}>{selectedPair.symbol}</h2>
          <ReplayChart candles={selectedCandles} positions={selectedPositions} />
          <div className={styles.lines}>
            {selectedPositions.length === 0 ? (
              <span className={styles.sub}>このペアの建玉はありません</span>
            ) : (
              selectedPositions.map((position) => (
                <span key={position.id} className={styles.lineBadge}>
                  強制決済ライン {formatPrice(position.liquidationLine)}（
                  {position.side === 'long' ? '買い' : '売り'}）
                </span>
              ))
            )}
          </div>
        </section>

        <section className={styles.card}>
          <h2 className={styles.heading}>注文</h2>
          <p className={styles.sub}>
            {selectedPair.symbol} を、証拠金とレバレッジで売買します。約定は公開中の最新の終値です。
          </p>
          <label className={styles.field}>
            <span>証拠金（1 以上の整数）</span>
            <input
              aria-label="証拠金"
              type="number"
              min={1}
              step={1}
              value={margin}
              onChange={(event) => setMargin(event.target.value)}
            />
          </label>
          <label className={styles.field}>
            <span>レバレッジ（1 倍刻み・上限 {maxLeverage} 倍）</span>
            <input
              aria-label="レバレッジ"
              type="number"
              min={1}
              step={1}
              max={maxLeverage}
              value={leverage}
              onChange={(event) => setLeverage(event.target.value)}
            />
          </label>
          <p className={styles.sub}>片道手数料 {feePercent}%（新規と決済の 2 回）</p>
          {(hasLong || hasShort) && (
            <p className={styles.note}>
              同じペアの反対方向は保有できません（現在:
              {hasLong ? ' 買い' : ' 売り'}）。決済すると反対方向を注文できます。
            </p>
          )}
          <div className={styles.orderButtons}>
            <button
              type="button"
              className={styles.buy}
              onClick={() => submitOrder('long')}
              disabled={pending || hasShort}
              aria-label="買い"
            >
              買い
              <span className={styles.hint}>上がると得</span>
            </button>
            <button
              type="button"
              className={styles.sell}
              onClick={() => submitOrder('short')}
              disabled={pending || hasLong}
              aria-label="売り"
            >
              売り
              <span className={styles.hint}>下がると得</span>
            </button>
          </div>
        </section>
      </div>

      <section className={styles.card}>
        <h2 className={styles.heading}>現在の状況</h2>
        <dl className={styles.summary}>
          <div>
            <dt>未使用資金</dt>
            <dd>{formatAmount(summary.cash)}</dd>
          </div>
          <div>
            <dt>証拠金の合計</dt>
            <dd>{formatAmount(summary.lockedMargin)}</dd>
          </div>
          <div>
            <dt>含み損益</dt>
            <dd className={profitClass(summary.unrealized)}>
              {formatSignedAmount(summary.unrealized)}
            </dd>
          </div>
          <div className={styles.total}>
            <dt>合計（資金 + 証拠金 + 含み損益）</dt>
            <dd>{formatAmount(summary.equity)}</dd>
          </div>
        </dl>
      </section>

      <section className={styles.card}>
        <h2 className={styles.heading}>保有一覧（{openPositions.length} 件）</h2>
        {openPositions.length === 0 ? (
          <p className={styles.sub}>
            保有はありません。通貨ペアを選んで「買い」「売り」を押してください。
          </p>
        ) : (
          <ul className={styles.positions}>
            {openPositions.map((position) => (
              <PositionRow
                key={position.id}
                position={position}
                currentPrice={latestClose(candlesByPair.get(position.pairId) ?? [])}
                pending={pending}
                onClose={onClose}
                onAddMargin={onAddMargin}
              />
            ))}
          </ul>
        )}
        {openPositions.length > 0 && (
          <p className={styles.note}>
            証拠金を追加しても、取引規模と価格変化に対する損益は変わりません。強制決済ラインが遠ざかります。
          </p>
        )}
      </section>

      <section className={styles.card}>
        <h2 className={styles.heading}>レバレッジ上限</h2>
        <p className={styles.sub}>
          {selectedPair.symbol}: 現在 {maxLeverage} 倍（初期 {baseLeverage} 倍・1 段で +10 倍）
        </p>
        {unlockOpen ? (
          <div className={styles.confirm}>
            <p>
              上限を <strong>{unlock.current} 倍 → {unlock.next} 倍</strong> に上げます。費用は{' '}
              <strong>{formatAmount(unlock.cost)}</strong> です。支払いますか？
            </p>
            <div className={styles.actions}>
              <button
                type="button"
                className={styles.primary}
                onClick={() => {
                  setUnlockOpen(false);
                  onUnlock(selectedPairIdSafe);
                }}
                disabled={pending}
              >
                支払って解放する
              </button>
              <button
                type="button"
                className={styles.secondary}
                onClick={() => setUnlockOpen(false)}
              >
                キャンセル
              </button>
            </div>
          </div>
        ) : (
          <button
            type="button"
            className={styles.secondary}
            onClick={() => setUnlockOpen(true)}
            disabled={pending}
          >
            上限を解放する（{unlock.current} 倍 → {unlock.next} 倍 / 費用{' '}
            {formatAmount(unlock.cost)}）
          </button>
        )}
        <p className={styles.sub}>
          費用は未使用資金から払われ、スコア（最終資金）から減ります。解放はこのゲームの中だけ有効です。
        </p>
      </section>

      <p className={styles.source}>出典: EV Trading Labs</p>
    </main>
  );
}

interface PositionRowProps {
  position: ReplayPosition;
  currentPrice: string | null;
  pending: boolean;
  onClose: (positionId: number) => void;
  onAddMargin: (positionId: number, amount: string) => void;
}

function PositionRow({ position, currentPrice, pending, onClose, onAddMargin }: PositionRowProps) {
  const [amount, setAmount] = useState('10');
  const [error, setError] = useState<string | null>(null);
  const profit = currentPrice === null ? 0 : positionProfit(position, currentPrice);

  function submitMargin() {
    if (!isPositiveInteger(amount)) {
      setError('追加する証拠金は 1 以上の整数で入力してください');
      return;
    }
    setError(null);
    onAddMargin(position.id, amount);
  }

  return (
    <li className={styles.position}>
      <span className={styles.positionPair}>
        {position.symbol}
        <span className={position.side === 'long' ? styles.sideBuy : styles.sideSell}>
          {formatSide(position.side)}
        </span>
      </span>
      <span className={styles.positionDetail}>
        証拠金 {formatPrice(position.margin)} ／ {position.leverage} 倍
        <br />
        取得 {formatPrice(position.entryPrice)} →{' '}
        {currentPrice === null ? '—' : formatPrice(currentPrice)}
        <br />
        強制決済ライン {formatPrice(position.liquidationLine)}
      </span>
      <span className={`${styles.positionProfit} ${profitClass(profit)}`}>
        {currentPrice === null ? '—' : formatSignedAmount(profit)}
      </span>
      <span className={styles.positionActions}>
        <button
          type="button"
          className={styles.secondary}
          onClick={() => onClose(position.id)}
          disabled={pending}
        >
          決済
        </button>
        <span className={styles.marginForm}>
          <input
            aria-label={`証拠金の追加額（${position.symbol} #${position.id}）`}
            type="number"
            min={1}
            step={1}
            value={amount}
            onChange={(event) => setAmount(event.target.value)}
          />
          <button
            type="button"
            className={styles.secondary}
            onClick={submitMargin}
            disabled={pending}
          >
            証拠金を追加
          </button>
        </span>
        {error !== null && (
          <span className={styles.error} role="alert">
            {error}
          </span>
        )}
      </span>
    </li>
  );
}

function profitClass(value: number): string {
  if (value > 0.005) return styles.profit;
  if (value < -0.005) return styles.loss;
  return '';
}

function changeClass(value: number): string {
  return profitClass(value);
}
