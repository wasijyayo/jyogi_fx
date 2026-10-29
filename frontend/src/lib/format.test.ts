import { describe, expect, it } from 'vitest';

import type { MarketStatus } from '../api/generated/model';
import {
  formatChangePercent,
  formatJstTime,
  formatMarketStatus,
  formatPrice,
  formatSide,
  formatSignedPrice,
} from './format';

describe('formatPrice', () => {
  it('末尾の余分な0と小数点を落とす', () => {
    expect(formatPrice('100.00000000')).toBe('100');
    expect(formatPrice('100.42000000')).toBe('100.42');
    expect(formatPrice('100.40')).toBe('100.4');
  });

  it('小数点を含まない値はそのまま返す', () => {
    expect(formatPrice('1000')).toBe('1000');
  });
});

describe('formatChangePercent', () => {
  it('プラス値は符号を付けて返す（小数2桁の文字列のまま）', () => {
    expect(formatChangePercent('1.23')).toBe('+1.23%');
  });

  it('マイナス値はそのまま返す（小数2桁の文字列のまま）', () => {
    expect(formatChangePercent('-0.50')).toBe('-0.50%');
  });

  it('ゼロは符号を付けて返す', () => {
    expect(formatChangePercent('0.00')).toBe('+0.00%');
  });

  it('null（基準無し）はダッシュを返す', () => {
    expect(formatChangePercent(null)).toBe('—');
  });
});

describe('formatMarketStatus', () => {
  it('開催中は残り時間を返す', () => {
    const status: MarketStatus = {
      open: true,
      alwaysOpen: false,
      remainingMinutes: 30,
      nextStartAt: null,
    };
    expect(formatMarketStatus(status)).toBe('開催中（残り30分）');
  });

  it('時間外は次の開始時刻を返す', () => {
    const status: MarketStatus = {
      open: false,
      alwaysOpen: false,
      remainingMinutes: null,
      nextStartAt: '2026-01-01T12:00:00+09:00',
    };
    expect(formatMarketStatus(status)).toBe('次回 12:00 開始');
  });

  it('常時開催（GAME_ALWAYS_OPEN）はその旨を返す', () => {
    const status: MarketStatus = {
      open: true,
      alwaysOpen: true,
      remainingMinutes: null,
      nextStartAt: null,
    };
    expect(formatMarketStatus(status)).toBe('開催中（常時開催）');
  });
});

describe('formatJstTime', () => {
  it('RFC3339 を JST の HH:mm にする', () => {
    expect(formatJstTime('2026-01-01T12:00:00+09:00')).toBe('12:00');
  });

  it('UTC の文字列も JST に変換する', () => {
    // UTC 03:00 = JST 12:00
    expect(formatJstTime('2026-01-01T03:00:00Z')).toBe('12:00');
  });
});

describe('formatSignedPrice', () => {
  it('正の値には + を付ける', () => {
    expect(formatSignedPrice('50.00000000')).toBe('+50');
  });

  it('負の値は符号をそのまま返す', () => {
    expect(formatSignedPrice('-12.34000000')).toBe('-12.34');
  });

  it('ゼロは符号を付けない', () => {
    expect(formatSignedPrice('0.00000000')).toBe('0');
  });
});

describe('formatSide', () => {
  it('long は「買い」', () => {
    expect(formatSide('long')).toBe('買い');
  });

  it('short は「売り」', () => {
    expect(formatSide('short')).toBe('売り');
  });
});
