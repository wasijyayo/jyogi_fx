import { describe, expect, it } from 'vitest';

import type {
  ReplayCandle,
  ReplayGameState,
  ReplayPair,
  ReplayPosition,
} from '../api/generated/model';
import {
  formatIntervalRange,
  formatIntervalSentence,
  formatRemaining,
  formatYear,
  guestNameLength,
  nextLeverageUnlock,
  positionProfit,
  priceDigits,
  remainingSeconds,
  summarize,
  validateReplayOrder,
} from './replay';

function candle(day: number, close: string): ReplayCandle {
  return { day, open: close, high: close, low: close, close, date: null };
}

function position(overrides: Partial<ReplayPosition> = {}): ReplayPosition {
  return {
    id: 1,
    pairId: 1,
    symbol: 'USD/JPY',
    side: 'long',
    initialMargin: '100',
    margin: '100',
    leverage: 10,
    entryPrice: '100',
    entryDay: 20,
    liquidationLine: '90',
    status: 'open',
    closePrice: null,
    pnl: null,
    ...overrides,
  };
}

function pair(overrides: Partial<ReplayPair> = {}): ReplayPair {
  return {
    pairId: 1,
    symbol: 'USD/JPY',
    baseLeverage: '25',
    maxLeverage: '25',
    feeRate: '0.0005',
    ...overrides,
  };
}

function state(overrides: Partial<ReplayGameState> = {}): ReplayGameState {
  return {
    gameId: 1,
    status: 'playing',
    startYear: 2020,
    funds: '800',
    speed: 1,
    publishedDays: 0,
    pairs: [pair()],
    positions: [position()],
    candlesByPair: [
      {
        pairId: 1,
        candles: [candle(19, '100'), candle(20, '110')],
      },
    ],
    ...overrides,
  };
}

describe('過去チャートモードの表示ヘルパー', () => {
  it('開始年だけを「2020年」と表示する', () => {
    expect(formatYear(2020)).toBe('2020年');
  });

  it('区間の正確な日付を日本語にする（同じ年は終了年を省略）', () => {
    expect(formatIntervalRange('2020-03-02', '2020-05-22')).toBe(
      '2020 年 3 月 2 日〜5 月 22 日',
    );
    expect(formatIntervalSentence('2020-03-02', '2020-05-22')).toBe(
      '2020 年 3 月 2 日〜5 月 22 日の相場でした',
    );
  });

  it('年をまたぐ区間は終了年も表示する', () => {
    expect(formatIntervalRange('2020-12-01', '2021-02-26')).toBe(
      '2020 年 12 月 1 日〜2021 年 2 月 26 日',
    );
  });

  it('日付が無ければ null を返す（終了前）', () => {
    expect(formatIntervalRange(null, null)).toBeNull();
    expect(formatIntervalSentence(null, null)).toBeNull();
  });

  it('価格の文字列から表示用の小数桁を求める（末尾の 0 は数えない）', () => {
    expect(priceDigits([candle(0, '108.12300000')])).toBe(3);
    expect(priceDigits([candle(0, '1.23456000')])).toBe(5);
  });

  it('残り時間は 3 秒 × 残り営業日 ÷ 速さで求め、m:ss で表示する', () => {
    expect(remainingSeconds(0, 1)).toBe(180);
    expect(remainingSeconds(0, 3)).toBe(60);
    expect(remainingSeconds(59, 1)).toBe(3);
    expect(formatRemaining(180)).toBe('3:00');
    expect(formatRemaining(60)).toBe('1:00');
  });

  it('買いの含み損益は上がると得、売りは下がると得', () => {
    expect(positionProfit(position(), '110')).toBeCloseTo(100);
    expect(positionProfit(position({ side: 'short' }), '110')).toBeCloseTo(-100);
  });

  it('現在の資金・証拠金・含み損益・合計を集計する', () => {
    expect(summarize(state())).toEqual({
      cash: 800,
      lockedMargin: 100,
      unrealized: 100,
      equity: 1000,
    });
  });

  it('次の上限解放は +10 倍・費用 250 から段ごとに 2 倍', () => {
    expect(nextLeverageUnlock(pair())).toMatchObject({ current: 25, next: 35, cost: 250 });
    expect(nextLeverageUnlock(pair({ maxLeverage: '35' }))).toMatchObject({
      current: 35,
      next: 45,
      cost: 500,
    });
    expect(nextLeverageUnlock(pair({ maxLeverage: '45' }))).toMatchObject({
      current: 45,
      next: 55,
      cost: 1000,
    });
  });

  it('証拠金・レバレッジは 1 以上の整数だけを受け付ける', () => {
    expect(validateReplayOrder('100', '10', 25)).toBeNull();
    expect(validateReplayOrder('0', '10', 25)).not.toBeNull();
    expect(validateReplayOrder('1.5', '10', 25)).not.toBeNull();
    expect(validateReplayOrder('100', '0', 25)).not.toBeNull();
    expect(validateReplayOrder('100', '26', 25)).not.toBeNull();
  });

  it('ゲスト名は前後の空白を除いた Unicode 文字数で数える', () => {
    expect(guestNameLength('  ゲスト  ')).toBe(3);
    expect(guestNameLength('')).toBe(0);
  });
});
