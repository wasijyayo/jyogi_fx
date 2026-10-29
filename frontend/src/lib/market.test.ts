import { describe, expect, it } from 'vitest';

import type { MarketStatus } from '../api/generated/model';
import { canPlaceOrder } from './market';

describe('canPlaceOrder', () => {
  it('常時開催（GAME_ALWAYS_OPEN）なら注文できる', () => {
    const status: MarketStatus = {
      open: true,
      alwaysOpen: true,
      remainingMinutes: null,
      nextStartAt: null,
    };
    expect(canPlaceOrder(status)).toBe(true);
  });

  it('開催中で残り時間が1分より多いなら注文できる', () => {
    const status: MarketStatus = {
      open: true,
      alwaysOpen: false,
      remainingMinutes: 30,
      nextStartAt: null,
    };
    expect(canPlaceOrder(status)).toBe(true);
  });

  it('開催中でも残り1分（終了1分前・12:59台）は注文できない', () => {
    const status: MarketStatus = {
      open: true,
      alwaysOpen: false,
      remainingMinutes: 1,
      nextStartAt: null,
    };
    expect(canPlaceOrder(status)).toBe(false);
  });

  it('時間外は注文できない', () => {
    const status: MarketStatus = {
      open: false,
      alwaysOpen: false,
      remainingMinutes: null,
      nextStartAt: '2026-01-01T12:00:00+09:00',
    };
    expect(canPlaceOrder(status)).toBe(false);
  });
});
