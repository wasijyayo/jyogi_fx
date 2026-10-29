import { useEffect, useMemo, useReducer, useState } from 'react';

import DemoChart from './DemoChart';
import {
  advanceDay,
  closePosition,
  initialState,
  openPosition,
  priceOf,
  profitOf,
  summarize,
  validateOrder,
  type DemoState,
  type OrderError,
  type Side,
} from './demoGame';
import { DEMO_DAYS, DEMO_PAIRS, createScenario, randomSeed } from './sampleData';
import styles from './DemoPage.module.css';

// DemoPage は発表用のデモ画面（#111 第 1 段階）。サーバーには一切つながず、
// サンプル相場とブラウザ内の状態だけで「複数ペアの売買・同時保有・決済」を実演する。

// 1 日を何ミリ秒で進めるか。design.md §13 の「3 秒 = 1 営業日（60 日で 3 分）」に合わせる。
const MS_PER_DAY = 3000;
const SPEEDS = [1, 3] as const;
const AMOUNTS = [100, 200, 500] as const;

type Action =
  | { type: 'tick' }
  | { type: 'open'; pair: number; side: Side; amount: number }
  | { type: 'close'; id: number }
  | { type: 'reset'; seed: number };

function reducer(state: DemoState, action: Action): DemoState {
  switch (action.type) {
    case 'tick':
      return advanceDay(state);
    case 'open':
      return openPosition(state, action.pair, action.side, action.amount);
    case 'close':
      return closePosition(state, action.id);
    case 'reset':
      return initialState(createScenario(action.seed));
  }
}

const ORDER_ERROR_TEXT: Record<OrderError, string> = {
  finished: 'ゲームは終了しました。「最初からやり直す」で再開できます',
  invalid_amount: '金額を正しく入力してください',
  insufficient_cash: '資金が足りません',
};

function formatMoney(value: number): string {
  return value.toLocaleString('ja-JP', { minimumFractionDigits: 1, maximumFractionDigits: 1 });
}

function formatSigned(value: number): string {
  const text = formatMoney(Math.abs(value));
  if (Math.abs(value) < 0.05) return text;
  return value > 0 ? `+${text}` : `-${text}`;
}

function formatPercent(rate: number): string {
  const text = (Math.abs(rate) * 100).toFixed(2);
  if (Math.abs(rate) < 0.00005) return `${text}%`;
  return rate > 0 ? `+${text}%` : `-${text}%`;
}

function profitClass(value: number): string {
  if (value > 0.05) return styles.profit;
  if (value < -0.05) return styles.loss;
  return '';
}

function formatRemaining(days: number): string {
  const seconds = Math.ceil((days * MS_PER_DAY) / 1000);
  const m = Math.floor(seconds / 60);
  const s = seconds % 60;
  return `${m}:${String(s).padStart(2, '0')}`;
}

export default function DemoPage() {
  const [state, dispatch] = useReducer(reducer, undefined, () =>
    initialState(createScenario(randomSeed())),
  );
  const [running, setRunning] = useState(false);
  const [speed, setSpeed] = useState<(typeof SPEEDS)[number]>(1);
  const [selected, setSelected] = useState(0);
  const [amount, setAmount] = useState<number>(AMOUNTS[1]);
  const [orderError, setOrderError] = useState<OrderError | null>(null);

  const isRunning = running && !state.finished;

  useEffect(() => {
    if (!isRunning) return;
    const timer = setInterval(() => dispatch({ type: 'tick' }), MS_PER_DAY / speed);
    return () => clearInterval(timer);
  }, [isRunning, speed]);

  const summary = summarize(state);
  const pair = DEMO_PAIRS[selected];
  const visiblePrices = useMemo(
    () => state.scenario.prices[selected].slice(0, state.day + 1),
    [state.scenario, selected, state.day],
  );
  const selectedPositions = useMemo(
    () => state.positions.filter((p) => p.pair === selected),
    [state.positions, selected],
  );

  function order(side: Side) {
    const error = validateOrder(state, amount);
    setOrderError(error);
    if (error === null) dispatch({ type: 'open', pair: selected, side, amount });
  }

  function reset() {
    setRunning(false);
    setOrderError(null);
    dispatch({ type: 'reset', seed: randomSeed() });
  }

  const remainingDays = DEMO_DAYS - 1 - state.day;

  return (
    <main className={styles.main}>
      <p className={styles.notice} role="note">
        <strong>デモ用サンプル</strong>
        ：値動きは乱数で作った架空のデータで、実際の過去相場ではありません。
        年代の表示・売買と集計のルールも仮の仕様です。
      </p>

      <section className={styles.status}>
        <div className={styles.era}>
          <span className={styles.eraLabel}>この相場の年代</span>
          <span className={styles.eraValue}>{state.scenario.year}年</span>
          <span className={styles.tentative}>表示の粒度は仮置き</span>
        </div>
        <div className={styles.progress}>
          <div className={styles.progressText}>
            <span>
              {state.day + 1} / {DEMO_DAYS} 日目
            </span>
            <span>{state.finished ? '終了' : `残り ${formatRemaining(remainingDays / speed)}`}</span>
          </div>
          <div className={styles.progressBar}>
            <div
              className={styles.progressFill}
              style={{ width: `${((state.day + 1) / DEMO_DAYS) * 100}%` }}
            />
          </div>
        </div>
        <div className={styles.controls}>
          <button
            type="button"
            className={styles.primary}
            onClick={() => setRunning((r) => !r)}
            disabled={state.finished}
          >
            {isRunning ? '一時停止' : state.day === 0 ? 'スタート' : '再開'}
          </button>
          {SPEEDS.map((s) => (
            <button
              key={s}
              type="button"
              className={s === speed ? styles.toggleActive : styles.toggle}
              onClick={() => setSpeed(s)}
            >
              ×{s}
            </button>
          ))}
          <button type="button" className={styles.secondary} onClick={reset}>
            最初からやり直す
          </button>
        </div>
      </section>

      <section className={styles.pairs} aria-label="通貨ペア">
        {DEMO_PAIRS.map((p, i) => {
          const price = priceOf(state, i);
          const change = price / priceOf(state, i, 0) - 1;
          const holding = state.positions.filter((pos) => pos.pair === i).length;
          return (
            <button
              key={p.symbol}
              type="button"
              className={i === selected ? styles.pairActive : styles.pair}
              onClick={() => setSelected(i)}
            >
              <span className={styles.pairSymbol}>{p.symbol}</span>
              <span className={styles.pairPrice}>{price.toFixed(p.digits)}</span>
              <span className={profitClass(change * 1000)}>{formatPercent(change)}</span>
              {holding > 0 && <span className={styles.badge}>保有 {holding}</span>}
            </button>
          );
        })}
      </section>

      <div className={styles.layout}>
        <section className={styles.card}>
          <h2 className={styles.heading}>
            {pair.symbol} <span className={styles.sub}>{pair.name}</span>
          </h2>
          <DemoChart
            history={state.scenario.history[selected]}
            prices={visiblePrices}
            digits={pair.digits}
            positions={selectedPositions}
          />
        </section>

        <section className={styles.card}>
          <h2 className={styles.heading}>注文</h2>
          <p className={styles.sub}>
            {pair.symbol} を、資金のうち指定した額で売買します（仮仕様）
          </p>
          <div className={styles.amounts}>
            {AMOUNTS.map((a) => (
              <button
                key={a}
                type="button"
                className={a === amount ? styles.toggleActive : styles.toggle}
                onClick={() => setAmount(a)}
              >
                {a}
              </button>
            ))}
            <input
              className={styles.amountInput}
              type="number"
              min={1}
              value={amount}
              onChange={(e) => setAmount(Number(e.target.value))}
              aria-label="注文額"
            />
          </div>
          <div className={styles.orderButtons}>
            <button
              type="button"
              className={styles.buy}
              onClick={() => order('buy')}
              disabled={state.finished}
            >
              買う
              <span className={styles.hint}>上がると得</span>
            </button>
            <button
              type="button"
              className={styles.sell}
              onClick={() => order('sell')}
              disabled={state.finished}
            >
              売る
              <span className={styles.hint}>下がると得</span>
            </button>
          </div>
          {orderError && (
            <p className={styles.error} role="alert">
              {ORDER_ERROR_TEXT[orderError]}
            </p>
          )}
        </section>
      </div>

      <section className={styles.card}>
        <h2 className={styles.heading}>
          成績 <span className={styles.tentative}>集計方法は仮仕様</span>
        </h2>
        <dl className={styles.summary}>
          <div>
            <dt>残り資金</dt>
            <dd>{formatMoney(summary.cash)}</dd>
          </div>
          <div>
            <dt>保有の評価額</dt>
            <dd>{formatMoney(summary.positionsValue)}</dd>
          </div>
          <div>
            <dt>含み損益</dt>
            <dd className={profitClass(summary.unrealizedProfit)}>
              {formatSigned(summary.unrealizedProfit)}
            </dd>
          </div>
          <div>
            <dt>確定損益</dt>
            <dd className={profitClass(summary.realizedProfit)}>
              {formatSigned(summary.realizedProfit)}
            </dd>
          </div>
          <div className={styles.total}>
            <dt>合計</dt>
            <dd>
              {formatMoney(summary.total)}{' '}
              <span className={profitClass(summary.returnRate * 1000)}>
                （{formatPercent(summary.returnRate)}）
              </span>
            </dd>
          </div>
        </dl>
      </section>

      <section className={styles.card}>
        <h2 className={styles.heading}>保有一覧（{state.positions.length} 件）</h2>
        {state.positions.length === 0 ? (
          <p className={styles.sub}>保有はありません。通貨ペアを選んで「買う」「売る」を押してください</p>
        ) : (
          <ul className={styles.positions}>
            {state.positions.map((p) => {
              const info = DEMO_PAIRS[p.pair];
              const price = priceOf(state, p.pair);
              const profit = profitOf(p, price);
              return (
                <li key={p.id} className={styles.position}>
                  <span className={styles.positionPair}>
                    {info.symbol}
                    <span className={p.side === 'buy' ? styles.sideBuy : styles.sideSell}>
                      {p.side === 'buy' ? '買い' : '売り'}
                    </span>
                  </span>
                  <span className={styles.positionDetail}>
                    {formatMoney(p.amount)} ／ {p.entryPrice.toFixed(info.digits)} →{' '}
                    {price.toFixed(info.digits)}
                  </span>
                  <span className={`${styles.positionProfit} ${profitClass(profit)}`}>
                    {formatSigned(profit)}
                  </span>
                  <button
                    type="button"
                    className={styles.secondary}
                    onClick={() => dispatch({ type: 'close', id: p.id })}
                  >
                    決済
                  </button>
                </li>
              );
            })}
          </ul>
        )}
      </section>

      {state.closed.length > 0 && (
        <section className={styles.card}>
          <h2 className={styles.heading}>決済した取引（{state.closed.length} 件）</h2>
          <ul className={styles.history}>
            {[...state.closed].reverse().map((c) => (
              <li key={c.id}>
                {DEMO_PAIRS[c.pair].symbol} {c.side === 'buy' ? '買い' : '売り'}{' '}
                {formatMoney(c.amount)}（{c.entryDay + 1}→{c.exitDay + 1}日目）{' '}
                <span className={profitClass(c.profit)}>{formatSigned(c.profit)}</span>
              </li>
            ))}
          </ul>
        </section>
      )}

      {state.finished && (
        <section className={styles.result} role="status">
          <h2>終了！</h2>
          <p>
            {state.scenario.year}年の相場（デモ用サンプル）で、最終成績は{' '}
            <strong>{formatMoney(summary.total)}</strong>（
            <span className={profitClass(summary.returnRate * 1000)}>
              {formatPercent(summary.returnRate)}
            </span>
            ）でした
          </p>
          <p className={styles.sub}>最終日に残っていた保有は、すべて自動で決済しました</p>
          <button type="button" className={styles.primary} onClick={reset}>
            もう一度遊ぶ
          </button>
        </section>
      )}
    </main>
  );
}
