import { describe, expect, it } from 'vitest';

import {
  INITIAL_CASH,
  advanceDay,
  closePosition,
  initialState,
  openPosition,
  profitOf,
  summarize,
  validateOrder,
  type DemoState,
} from './demoGame';
import {
  DEMO_DAYS,
  DEMO_PAIRS,
  HISTORY_DAYS,
  createScenario,
  type DemoScenario,
} from './sampleData';

// fixedScenario は全ペアの価格を日ごとに指定したシナリオ（テスト用）。
function fixedScenario(pricesByPair: number[][]): DemoScenario {
  const prices = pricesByPair.map((series) => {
    const filled = [...series];
    while (filled.length < DEMO_DAYS) filled.push(series[series.length - 1]);
    return filled;
  });
  return { seed: 0, year: 2008, prices, history: prices.map(() => []) };
}

function advance(state: DemoState, days: number): DemoState {
  let s = state;
  for (let i = 0; i < days; i++) s = advanceDay(s);
  return s;
}

describe('createScenario', () => {
  it('同じ seed なら同じ値動きになる（純粋関数）', () => {
    expect(createScenario(42)).toEqual(createScenario(42));
  });

  it('全ペア・全日の価格があり、すべて正の値', () => {
    const s = createScenario(7);
    expect(s.prices).toHaveLength(DEMO_PAIRS.length);
    for (const series of s.prices) {
      expect(series).toHaveLength(DEMO_DAYS);
      expect(series.every((p) => p > 0)).toBe(true);
    }
    for (const series of s.history) expect(series).toHaveLength(HISTORY_DAYS);
    expect(s.year).toBeGreaterThanOrEqual(2000);
    expect(s.year).toBeLessThanOrEqual(2024);
  });
});

describe('profitOf', () => {
  const base = { id: 1, pair: 0, amount: 100, entryPrice: 100, entryDay: 0 };

  it('買いは価格が上がると利益', () => {
    expect(profitOf({ ...base, side: 'buy' }, 110)).toBeCloseTo(10);
  });

  it('売りは価格が下がると利益', () => {
    expect(profitOf({ ...base, side: 'sell' }, 90)).toBeCloseTo(10);
    expect(profitOf({ ...base, side: 'sell' }, 110)).toBeCloseTo(-10);
  });
});

describe('売買の流れ', () => {
  it('2 種類を同時に保有し、一方を決済してももう一方は残る', () => {
    const scenario = fixedScenario([
      [100, 110],
      [1.0, 0.9],
    ]);
    let s = initialState(scenario);
    s = openPosition(s, 0, 'buy', 200);
    s = openPosition(s, 1, 'sell', 300);
    expect(s.positions).toHaveLength(2);
    expect(s.cash).toBe(INITIAL_CASH - 500);

    s = advanceDay(s);
    const firstId = s.positions[0].id;
    s = closePosition(s, firstId);

    expect(s.positions).toHaveLength(1);
    expect(s.positions[0].pair).toBe(1);
    expect(s.closed).toHaveLength(1);
    // 200 × +10% = +20 が資金に戻る
    expect(s.cash).toBeCloseTo(INITIAL_CASH - 500 + 220);
  });

  it('値動きで含み損益と合計が変わる', () => {
    const scenario = fixedScenario([[100, 120]]);
    let s = openPosition(initialState(scenario), 0, 'buy', 500);
    expect(summarize(s).total).toBeCloseTo(INITIAL_CASH);

    s = advanceDay(s);
    const sum = summarize(s);
    expect(sum.unrealizedProfit).toBeCloseTo(100);
    expect(sum.total).toBeCloseTo(INITIAL_CASH + 100);
    expect(sum.returnRate).toBeCloseTo(0.1);
  });

  it('資金を超える注文・0 以下の注文は受け付けない', () => {
    const s = initialState(fixedScenario([[100]]));
    expect(validateOrder(s, INITIAL_CASH + 1)).toBe('insufficient_cash');
    expect(validateOrder(s, 0)).toBe('invalid_amount');
    expect(openPosition(s, 0, 'buy', INITIAL_CASH + 1)).toBe(s);
  });

  it('最終日に残りの保有をすべて決済して終了する', () => {
    const scenario = fixedScenario([[100, 100, 150]]);
    let s = openPosition(initialState(scenario), 0, 'buy', 100);
    s = advance(s, DEMO_DAYS);

    expect(s.finished).toBe(true);
    expect(s.day).toBe(DEMO_DAYS - 1);
    expect(s.positions).toHaveLength(0);
    expect(s.cash).toBeCloseTo(INITIAL_CASH + 50);
    // 終了後は注文できない
    expect(validateOrder(s, 10)).toBe('finished');
  });
});
