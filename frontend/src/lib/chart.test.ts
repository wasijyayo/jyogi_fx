import { describe, expect, it } from 'vitest';

import type { Candle } from '../api/generated/model';
import {
  formatEventLabel,
  interpolateMissingCandles,
  isoToUnixSeconds,
  toChartPoints,
  type ChartPoint,
} from './chart';

function point(overrides: Partial<ChartPoint>): ChartPoint {
  return {
    time: 0,
    open: 0,
    high: 0,
    low: 0,
    close: 0,
    basePrice: 0,
    isOpening: false,
    ...overrides,
  };
}

describe('toChartPoints', () => {
  it('decimal文字列を数値へ、RFC3339をUnix秒へ変換する', () => {
    const candles: Candle[] = [
      {
        time: '1970-01-01T00:00:00Z',
        open: '100.00000000',
        high: '101',
        low: '99.5',
        close: '100.42',
        basePrice: '100',
        isOpening: true,
      },
    ];
    const [p] = toChartPoints(candles);
    expect(p.time).toBe(0);
    expect(p.open).toBe(100);
    expect(p.high).toBe(101);
    expect(p.low).toBe(99.5);
    expect(p.close).toBe(100.42);
    expect(p.basePrice).toBe(100);
    expect(p.isOpening).toBe(true);
  });
});

describe('isoToUnixSeconds', () => {
  it('RFC3339（UTC）をUnix秒に変換する', () => {
    expect(isoToUnixSeconds('1970-01-01T00:00:00Z')).toBe(0);
    expect(isoToUnixSeconds('1970-01-01T00:01:00Z')).toBe(60);
  });
});

describe('interpolateMissingCandles', () => {
  it('空・1本はそのまま返す', () => {
    expect(interpolateMissingCandles([])).toEqual([]);
    const single = [point({ time: 0, close: 100 })];
    expect(interpolateMissingCandles(single)).toEqual(single);
  });

  it('欠損が無ければそのまま返す（60秒間隔）', () => {
    const pts = [point({ time: 0 }), point({ time: 60 }), point({ time: 120 })];
    expect(interpolateMissingCandles(pts)).toEqual(pts);
  });

  it('許容差内のズレ（90秒以下）は補間しない', () => {
    // 60 + 30（許容差）= 90 までは欠損とみなさない。
    const pts = [point({ time: 0, close: 100 }), point({ time: 90, close: 110 })];
    expect(interpolateMissingCandles(pts)).toHaveLength(2);
  });

  it('2分の穴を1本の平坦な足で線形補間する', () => {
    const pts = [
      point({ time: 0, close: 100, basePrice: 90, isOpening: true }),
      point({ time: 120, close: 110, basePrice: 94 }),
    ];
    const got = interpolateMissingCandles(pts, 60);

    expect(got).toHaveLength(3);
    expect(got[0].time).toBe(0);
    expect(got[1].time).toBe(60); // 補間された足
    expect(got[2].time).toBe(120);

    const mid = got[1];
    expect(mid.close).toBeCloseTo(105, 6);
    expect(mid.basePrice).toBeCloseTo(92, 6);
    // 補間された足は平坦（o=h=l=c）で、寄り付き扱いにしない。
    expect(mid.open).toBeCloseTo(105, 6);
    expect(mid.high).toBeCloseTo(105, 6);
    expect(mid.low).toBeCloseTo(105, 6);
    expect(mid.isOpening).toBe(false);
    // 元の足の isOpening は保持される。
    expect(got[0].isOpening).toBe(true);
  });

  it('3分の穴を2本で線形補間する', () => {
    const pts = [
      point({ time: 0, close: 100, basePrice: 90 }),
      point({ time: 180, close: 130, basePrice: 120 }),
    ];
    const got = interpolateMissingCandles(pts, 60);

    expect(got.map((p) => p.time)).toEqual([0, 60, 120, 180]);
    expect(got[1].close).toBeCloseTo(110, 6);
    expect(got[1].basePrice).toBeCloseTo(100, 6);
    expect(got[2].close).toBeCloseTo(120, 6);
    expect(got[2].basePrice).toBeCloseTo(110, 6);
  });
});

describe('formatEventLabel', () => {
  it('shock は符号で方向を決める', () => {
    expect(formatEventLabel('shock', '0.04')).toBe('急騰');
    expect(formatEventLabel('shock', '-0.14')).toBe('暴落');
  });

  it('vol_up / liquidity_drain は固定文言', () => {
    expect(formatEventLabel('vol_up', '3')).toBe('ボラ上昇');
    expect(formatEventLabel('liquidity_drain', '0.30')).toBe('流動性枯渇');
  });

  it('未知の種別はそのまま返す', () => {
    expect(formatEventLabel('unknown_type', '1')).toBe('unknown_type');
  });
});
