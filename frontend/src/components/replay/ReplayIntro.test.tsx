import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';

import ReplayIntro from './ReplayIntro';
import type { ReplaySession } from '../../lib/replay';

const session = {
  gameId: 42,
  token: 'token-42',
  started: {
    gameId: 42,
    token: 'token-42',
    startYear: 2020,
    funds: '1000',
    speed: 1,
    publishedDays: 0,
    pairs: [],
    candlesByPair: [],
  },
} as ReplaySession;

describe('ReplayIntro', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    window.localStorage.clear();
  });

  afterEach(() => {
    cleanup();
    vi.useRealTimers();
    vi.clearAllMocks();
    // 他のテストに影響しないよう matchMedia の差し替えを戻す。
    // @ts-expect-error jsdom の初期状態には無い
    delete window.matchMedia;
  });

  it('初回は年 → 新聞の順に出し、取引開始で親に知らせる', () => {
    const onStart = vi.fn();
    render(
      <ReplayIntro session={session} onStart={onStart} starting={false} error={null} />,
    );

    // 暗転して年が出る。
    expect(screen.getByText('2020年')).toBeTruthy();
    expect(screen.queryByRole('button', { name: '取引開始' })).toBeNull();

    act(() => {
      vi.advanceTimersByTime(1600);
    });
    const beginButton = screen.getByRole('button', { name: '取引開始' });
    expect(beginButton).toBeTruthy();

    fireEvent.click(beginButton);
    expect(onStart).toHaveBeenCalledTimes(1);
  });

  it('2 回目以降は年の演出を飛ばして新聞から始める', () => {
    window.localStorage.setItem('fxgame.replayIntroSeen', '1');

    render(
      <ReplayIntro session={session} onStart={vi.fn()} starting={false} error={null} />,
    );

    expect(screen.getByRole('button', { name: '取引開始' })).toBeTruthy();
  });

  it('スキップで年の演出を飛ばせる', () => {
    render(
      <ReplayIntro session={session} onStart={vi.fn()} starting={false} error={null} />,
    );

    fireEvent.click(screen.getByRole('button', { name: 'スキップ' }));

    expect(screen.getByRole('button', { name: '取引開始' })).toBeTruthy();
  });

  it('prefers-reduced-motion では年を出さず、すぐ新聞にする', () => {
    window.matchMedia = vi.fn().mockReturnValue({ matches: true }) as never;

    render(
      <ReplayIntro session={session} onStart={vi.fn()} starting={false} error={null} />,
    );

    expect(screen.getByRole('button', { name: '取引開始' })).toBeTruthy();
  });

  it('begin に失敗したメッセージを出す', () => {
    window.localStorage.setItem('fxgame.replayIntroSeen', '1');

    render(
      <ReplayIntro
        session={session}
        onStart={vi.fn()}
        starting={false}
        error="取引を開始できませんでした"
      />,
    );

    expect(screen.getByRole('alert').textContent).toBe('取引を開始できませんでした');
  });
});
