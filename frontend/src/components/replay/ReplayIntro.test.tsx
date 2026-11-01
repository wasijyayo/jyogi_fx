import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';

import ReplayIntro from './ReplayIntro';
import type { ReplaySession } from '../../lib/replay';

// 生成された API hooks を差し替える（ネットワークは叩かない）。
vi.mock('../../api/generated/api', () => ({
  useBeginReplayGame: vi.fn(),
}));

import { useBeginReplayGame } from '../../api/generated/api';

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

type BeginOptions = {
  mutation?: {
    onSuccess?: (res: { status: number; data: unknown }) => void;
    onError?: () => void;
  };
};

describe('ReplayIntro', () => {
  const mutate = vi.fn();
  let lastOptions: BeginOptions | undefined;

  beforeEach(() => {
    vi.useFakeTimers();
    window.localStorage.clear();
    mutate.mockReset();
    lastOptions = undefined;
    vi.mocked(useBeginReplayGame).mockImplementation(((options: BeginOptions) => {
      lastOptions = options;
      return { mutate, isPending: false } as never;
    }) as never);
  });

  afterEach(() => {
    cleanup();
    vi.useRealTimers();
    vi.clearAllMocks();
    // 他のテストに影響しないよう matchMedia の差し替えを戻す。
    // @ts-expect-error jsdom の初期状態には無い
    delete window.matchMedia;
  });

  it('初回は年 → 新聞の順に出し、取引開始でカウントダウンしてから開始する', () => {
    const onBegun = vi.fn();
    render(<ReplayIntro session={session} onBegun={onBegun} />);

    // 暗転して年が出る。
    expect(screen.getByText('2020年')).toBeTruthy();
    expect(screen.queryByRole('button', { name: '取引開始' })).toBeNull();

    act(() => {
      vi.advanceTimersByTime(1600);
    });
    const beginButton = screen.getByRole('button', { name: '取引開始' });
    expect(beginButton).toBeTruthy();

    fireEvent.click(beginButton);
    expect(mutate).toHaveBeenCalledWith({ gameId: 42 });

    act(() => {
      lastOptions?.mutation?.onSuccess?.({ status: 200, data: {} });
    });
    // 3 から始まり、押すまでサーバーの時間は動かない（begin 後にカウントダウン）。
    expect(screen.getByRole('status').textContent).toBe('3');
    expect(onBegun).not.toHaveBeenCalled();

    act(() => {
      vi.advanceTimersByTime(1000);
    });
    act(() => {
      vi.advanceTimersByTime(1000);
    });
    act(() => {
      vi.advanceTimersByTime(1000);
    });
    expect(onBegun).toHaveBeenCalledTimes(1);
  });

  it('2 回目以降は年の演出を飛ばして新聞から始める', () => {
    window.localStorage.setItem('fxgame.replayIntroSeen', '1');

    render(<ReplayIntro session={session} onBegun={vi.fn()} />);

    expect(screen.getByRole('button', { name: '取引開始' })).toBeTruthy();
    expect(screen.queryByRole('status')).toBeNull();
  });

  it('スキップで年の演出を飛ばせる', () => {
    const onBegun = vi.fn();
    render(<ReplayIntro session={session} onBegun={onBegun} />);

    fireEvent.click(screen.getByRole('button', { name: 'スキップ' }));

    expect(screen.getByRole('button', { name: '取引開始' })).toBeTruthy();
    expect(onBegun).not.toHaveBeenCalled();
  });

  it('prefers-reduced-motion では年を出さず、カウントダウンも短くする', () => {
    window.matchMedia = vi.fn().mockReturnValue({ matches: true }) as never;
    const onBegun = vi.fn();

    render(<ReplayIntro session={session} onBegun={onBegun} />);

    // 年の画面を挟まず、すぐ新聞。
    expect(screen.getByRole('button', { name: '取引開始' })).toBeTruthy();

    fireEvent.click(screen.getByRole('button', { name: '取引開始' }));
    act(() => {
      lastOptions?.mutation?.onSuccess?.({ status: 200, data: {} });
    });
    expect(screen.getByRole('status').textContent).toBe('1');

    act(() => {
      vi.advanceTimersByTime(1000);
    });
    expect(onBegun).toHaveBeenCalledTimes(1);
  });
});
