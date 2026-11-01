import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';

import type { ReplayGameState } from '../api/generated/model';
import ReplayPlay from './ReplayPlay';

// チャートは canvas を使うため、単体テストでは差し替える（表示の検証は別途）。
vi.mock('./ReplayChart', () => ({ default: () => null }));

function state(overrides: Partial<ReplayGameState> = {}): ReplayGameState {
  return {
    gameId: 1,
    status: 'playing',
    startYear: 2020,
    funds: '800',
    speed: 1,
    publishedDays: 1,
    pairs: [
      {
        pairId: 1,
        symbol: 'USD/JPY',
        baseLeverage: '25',
        maxLeverage: '25',
        feeRate: '0.0005',
      },
    ],
    positions: [
      {
        id: 7,
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
      },
    ],
    candlesByPair: [
      {
        pairId: 1,
        candles: [
          { day: 19, open: '100', high: '101', low: '99', close: '100', date: null },
          { day: 20, open: '100', high: '112', low: '99', close: '110', date: null },
        ],
      },
    ],
    ...overrides,
  };
}

function renderPlay(overrides: Partial<ReplayGameState> = {}) {
  const handlers = {
    onOrder: vi.fn(),
    onClose: vi.fn(),
    onAddMargin: vi.fn(),
    onUnlock: vi.fn(),
    onSpeed: vi.fn(),
  };
  render(
    <ReplayPlay
      state={state(overrides)}
      actionError={null}
      pending={false}
      {...handlers}
    />,
  );
  return handlers;
}

describe('ReplayPlay', () => {
  afterEach(() => {
    cleanup();
  });

  it('開始年・残り時間・通貨ペア・注文・強制決済ライン・出典を表示する', () => {
    renderPlay();

    expect(screen.getByText('2020年')).toBeTruthy();
    expect(screen.getByText('残り 2:57')).toBeTruthy();
    expect(screen.getAllByText('USD/JPY').length).toBeGreaterThan(0);
    expect(screen.getByLabelText('証拠金')).toBeTruthy();
    expect(screen.getByLabelText('レバレッジ')).toBeTruthy();
    expect(screen.getAllByText(/強制決済ライン 90/).length).toBeGreaterThan(0);
    expect(screen.getByText('出典: EV Trading Labs')).toBeTruthy();
  });

  it('証拠金が 1 以上の整数でなければ注文しない', () => {
    const handlers = renderPlay();

    fireEvent.change(screen.getByLabelText('証拠金'), { target: { value: '0' } });
    fireEvent.click(screen.getByRole('button', { name: '買い' }));

    expect(screen.getByText('証拠金は 1 以上の整数で入力してください')).toBeTruthy();
    expect(handlers.onOrder).not.toHaveBeenCalled();
  });

  it('正しい入力なら買い注文を送る', () => {
    const handlers = renderPlay();

    fireEvent.change(screen.getByLabelText('証拠金'), { target: { value: '200' } });
    fireEvent.change(screen.getByLabelText('レバレッジ'), { target: { value: '10' } });
    fireEvent.click(screen.getByRole('button', { name: '買い' }));

    expect(handlers.onOrder).toHaveBeenCalledWith(1, 'long', '200', '10');
  });

  it('レバレッジ上限の解放は、次の倍率と費用を確認してから実行する', () => {
    const handlers = renderPlay();

    fireEvent.click(screen.getByRole('button', { name: /上限を解放する/ }));
    expect(screen.getByText('25 倍 → 35 倍')).toBeTruthy();
    expect(screen.getByText('250')).toBeTruthy();
    expect(handlers.onUnlock).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole('button', { name: '支払って解放する' }));
    expect(handlers.onUnlock).toHaveBeenCalledWith(1);
  });

  it('速さは ×1・×2・×3 を切り替えられる（一時停止は無い）', () => {
    const handlers = renderPlay();

    expect(screen.queryByRole('button', { name: '一時停止' })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: '×2' }));
    expect(handlers.onSpeed).toHaveBeenCalledWith(2);
  });

  it('建玉の決済と証拠金の追加ができる', () => {
    const handlers = renderPlay();

    fireEvent.click(screen.getByRole('button', { name: '決済' }));
    expect(handlers.onClose).toHaveBeenCalledWith(7);

    fireEvent.change(screen.getByLabelText('証拠金の追加額（USD/JPY #7）'), {
      target: { value: '50' },
    });
    fireEvent.click(screen.getByRole('button', { name: '証拠金を追加' }));
    expect(handlers.onAddMargin).toHaveBeenCalledWith(7, '50');
  });
});
