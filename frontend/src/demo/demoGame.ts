// デモ用モック（#111）の売買ロジック。ブラウザ内の状態だけで完結する。
//
// **ここでの資金・損益の計算は仮仕様。** 注文額・損益の共通単位・レバレッジなどは
// #111 の第 2 段階で決める。本番の売買はサーバー側（internal/game・decimal）で行うので、
// このファイルを本番のロジックとして流用しないこと。
//
// 仮仕様の中身:
// - 資金 1000 から始める（design.md §13 の初期資金に合わせる）
// - 注文は「資金からいくら出すか」で指定する。出した分は保有中は資金から引かれる
// - 損益 = 出した額 × 価格の変化率（買いは上がると得、売りは下がると得）
//   → 通貨ペアごとの単位を気にせず、全ペアを同じ「資金」で合算できる
// - レバレッジ・手数料はなし。最終日に残っている保有はすべて決済する

import { DEMO_DAYS, type DemoScenario } from './sampleData';

export const INITIAL_CASH = 1000;

export type Side = 'buy' | 'sell';

export interface Position {
  id: number;
  pair: number;
  side: Side;
  amount: number;
  entryPrice: number;
  entryDay: number;
}

export interface ClosedPosition extends Position {
  exitPrice: number;
  exitDay: number;
  profit: number;
}

export interface DemoState {
  scenario: DemoScenario;
  day: number;
  cash: number;
  positions: Position[];
  closed: ClosedPosition[];
  nextId: number;
  finished: boolean;
}

export function initialState(scenario: DemoScenario): DemoState {
  return {
    scenario,
    day: 0,
    cash: INITIAL_CASH,
    positions: [],
    closed: [],
    nextId: 1,
    finished: false,
  };
}

export function priceOf(state: DemoState, pair: number, day = state.day): number {
  return state.scenario.prices[pair][day];
}

// profitOf は保有 1 件の、指定した価格での損益（仮仕様）。
export function profitOf(position: Position, price: number): number {
  const rate = price / position.entryPrice - 1;
  return position.amount * rate * (position.side === 'buy' ? 1 : -1);
}

export interface Summary {
  cash: number;
  // 保有中の評価額（出した額 + 含み損益）の合計。
  positionsValue: number;
  unrealizedProfit: number;
  realizedProfit: number;
  total: number;
  returnRate: number;
}

export function summarize(state: DemoState): Summary {
  let positionsValue = 0;
  let unrealizedProfit = 0;
  for (const p of state.positions) {
    const profit = profitOf(p, priceOf(state, p.pair));
    unrealizedProfit += profit;
    positionsValue += p.amount + profit;
  }
  const realizedProfit = state.closed.reduce((sum, c) => sum + c.profit, 0);
  const total = state.cash + positionsValue;
  return {
    cash: state.cash,
    positionsValue,
    unrealizedProfit,
    realizedProfit,
    total,
    returnRate: total / INITIAL_CASH - 1,
  };
}

export type OrderError = 'finished' | 'invalid_amount' | 'insufficient_cash';

export function validateOrder(state: DemoState, amount: number): OrderError | null {
  if (state.finished) return 'finished';
  if (!Number.isFinite(amount) || amount <= 0) return 'invalid_amount';
  if (amount > state.cash) return 'insufficient_cash';
  return null;
}

export function openPosition(state: DemoState, pair: number, side: Side, amount: number): DemoState {
  if (validateOrder(state, amount) !== null) return state;
  const position: Position = {
    id: state.nextId,
    pair,
    side,
    amount,
    entryPrice: priceOf(state, pair),
    entryDay: state.day,
  };
  return {
    ...state,
    cash: state.cash - amount,
    positions: [...state.positions, position],
    nextId: state.nextId + 1,
  };
}

function closeAt(state: DemoState, position: Position): ClosedPosition {
  const exitPrice = priceOf(state, position.pair);
  return { ...position, exitPrice, exitDay: state.day, profit: profitOf(position, exitPrice) };
}

// closePosition は 1 件だけ決済する。ほかの保有はそのまま残る。
export function closePosition(state: DemoState, id: number): DemoState {
  const position = state.positions.find((p) => p.id === id);
  if (!position || state.finished) return state;
  const closed = closeAt(state, position);
  return {
    ...state,
    cash: state.cash + closed.amount + closed.profit,
    positions: state.positions.filter((p) => p.id !== id),
    closed: [...state.closed, closed],
  };
}

// advanceDay は 1 日進める。最終日に着いたら残りの保有をすべて決済して終了する。
export function advanceDay(state: DemoState): DemoState {
  if (state.finished) return state;
  const next = { ...state, day: Math.min(state.day + 1, DEMO_DAYS - 1) };
  if (next.day < DEMO_DAYS - 1) return next;
  return settleAll(next);
}

function settleAll(state: DemoState): DemoState {
  const closed = state.positions.map((p) => closeAt(state, p));
  const returned = closed.reduce((sum, c) => sum + c.amount + c.profit, 0);
  return {
    ...state,
    cash: state.cash + returned,
    positions: [],
    closed: [...state.closed, ...closed],
    finished: true,
  };
}
