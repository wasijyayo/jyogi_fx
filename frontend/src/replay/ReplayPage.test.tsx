import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

import ReplayPage from './ReplayPage';

// 生成された API hooks を差し替える（ネットワーク・チャートは使わない）。
vi.mock('../api/generated/api', () => ({
  useGetMe: vi.fn(),
  useGetReplayGameState: vi.fn(),
  useGetReplayRanking: vi.fn(),
  useStartReplayGame: vi.fn(),
  usePlaceReplayOrder: vi.fn(),
  useCloseReplayPosition: vi.fn(),
  useAddReplayMargin: vi.fn(),
  useUnlockReplayLeverage: vi.fn(),
  useChangeReplaySpeed: vi.fn(),
  getGetReplayGameStateQueryKey: (gameId: number) => ['/api/replay/games', gameId] as const,
  getGetReplayRankingQueryKey: () => ['/api/replay/ranking'] as const,
}));

vi.mock('./ReplayChart', () => ({ default: () => null }));

import {
  useAddReplayMargin,
  useChangeReplaySpeed,
  useCloseReplayPosition,
  useGetMe,
  useGetReplayGameState,
  useGetReplayRanking,
  usePlaceReplayOrder,
  useStartReplayGame,
  useUnlockReplayLeverage,
} from '../api/generated/api';

const startMutateAsync = vi.fn();

function mutation() {
  return { mutateAsync: vi.fn(), isPending: false, data: undefined } as never;
}

function response<T>(data: T, status: number) {
  return { data, status, headers: new Headers() } as never;
}

function renderPage() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <ReplayPage />
    </QueryClientProvider>,
  );
}

describe('ReplayPage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    startMutateAsync.mockReset();
    window.sessionStorage.clear();

    vi.mocked(useGetMe).mockReturnValue({ data: undefined, isLoading: false } as never);
    vi.mocked(useGetReplayGameState).mockReturnValue({
      data: undefined,
      isLoading: false,
    } as never);
    vi.mocked(useGetReplayRanking).mockReturnValue({
      data: response([], 200),
      isLoading: false,
    } as never);
    vi.mocked(useStartReplayGame).mockReturnValue({
      mutateAsync: startMutateAsync,
      isPending: false,
    } as never);
    vi.mocked(usePlaceReplayOrder).mockReturnValue(mutation());
    vi.mocked(useCloseReplayPosition).mockReturnValue(mutation());
    vi.mocked(useAddReplayMargin).mockReturnValue(mutation());
    vi.mocked(useUnlockReplayLeverage).mockReturnValue(mutation());
    vi.mocked(useChangeReplaySpeed).mockReturnValue(mutation());
  });

  afterEach(() => {
    cleanup();
    window.sessionStorage.clear();
  });

  it('未ログインならモード選択とゲスト名の入力を表示する', () => {
    vi.mocked(useGetMe).mockReturnValue({
      data: response({ message: 'not logged in' }, 401),
      isLoading: false,
    } as never);

    renderPage();

    expect(screen.getByText('モードを選ぶ')).toBeTruthy();
    expect(screen.getByText('通常モード')).toBeTruthy();
    expect(screen.getAllByText('過去チャートモード').length).toBeGreaterThan(0);
    expect(screen.getByLabelText('ゲスト名')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'ゲームを開始' })).toBeTruthy();
  });

  it('ゲスト名が 16 文字を超えたら開始しない', () => {
    vi.mocked(useGetMe).mockReturnValue({
      data: response({ message: 'not logged in' }, 401),
      isLoading: false,
    } as never);

    renderPage();
    fireEvent.change(screen.getByLabelText('ゲスト名'), {
      target: { value: 'あ'.repeat(17) },
    });
    fireEvent.click(screen.getByRole('button', { name: 'ゲームを開始' }));

    expect(screen.getByText('ゲスト名は 1〜16 文字で入力してください')).toBeTruthy();
    expect(startMutateAsync).not.toHaveBeenCalled();
  });

  it('ログイン済みならゲスト名の入力を出さない', () => {
    vi.mocked(useGetMe).mockReturnValue({
      data: response(
        { discordId: 'u1', displayName: 'テスト太郎', balance: '1000.00000000' },
        200,
      ),
      isLoading: false,
    } as never);

    renderPage();

    expect(screen.queryByLabelText('ゲスト名')).toBeNull();
    expect(screen.getByText('テスト太郎')).toBeTruthy();
  });

  it('ログインユーザーとゲストが同じランキングに並ぶ', () => {
    vi.mocked(useGetMe).mockReturnValue({
      data: response({ message: 'not logged in' }, 401),
      isLoading: false,
    } as never);
    vi.mocked(useGetReplayRanking).mockReturnValue({
      data: response(
        [
          {
            rank: 1,
            name: 'ログイン太郎',
            isGuest: false,
            avatarUrl: 'https://example.com/a.png',
            finalFunds: '1500.00000000',
          },
          {
            rank: 2,
            name: 'ゲスト花子',
            isGuest: true,
            avatarUrl: null,
            finalFunds: '1200.00000000',
          },
        ],
        200,
      ),
      isLoading: false,
    } as never);

    renderPage();

    expect(screen.getByText('ログイン太郎')).toBeTruthy();
    expect(screen.getByText('ゲスト花子')).toBeTruthy();
    expect(screen.getByText('ゲスト')).toBeTruthy();
    expect(screen.getByText('1500')).toBeTruthy();
    expect(screen.getByText('1200')).toBeTruthy();
  });

  it('終了後はスコア・区間の日付・ランキング・出典を表示する', () => {
    window.sessionStorage.setItem(
      'fxgame.replay.session.v1',
      JSON.stringify({ gameId: 12, token: 'token-12' }),
    );
    vi.mocked(useGetMe).mockReturnValue({
      data: response({ message: 'not logged in' }, 401),
      isLoading: false,
    } as never);
    vi.mocked(useGetReplayGameState).mockReturnValue({
      data: response(
        {
          gameId: 12,
          status: 'finished',
          startYear: 2020,
          funds: '1234.50000000',
          speed: 1,
          publishedDays: 60,
          pairs: [],
          positions: [],
          candlesByPair: [],
          intervalStart: '2020-03-02',
          intervalEnd: '2020-05-22',
          finalFunds: '1234.50000000',
        },
        200,
      ),
      isLoading: false,
    } as never);
    vi.mocked(useGetReplayRanking).mockReturnValue({
      data: response(
        [
          {
            rank: 1,
            name: 'ゲスト花子',
            isGuest: true,
            avatarUrl: null,
            finalFunds: '1234.50000000',
          },
        ],
        200,
      ),
      isLoading: false,
    } as never);

    renderPage();

    expect(screen.getByRole('heading', { name: /スコア/ })).toBeTruthy();
    expect(screen.getAllByText('1234.5').length).toBeGreaterThan(0);
    expect(
      screen.getByText('2020 年 3 月 2 日〜5 月 22 日の相場でした'),
    ).toBeTruthy();
    expect(screen.getByRole('button', { name: 'もう一度遊ぶ' })).toBeTruthy();
    expect(screen.getByText('ゲスト')).toBeTruthy();
    expect(screen.getByText('出典: EV Trading Labs')).toBeTruthy();
  });
});
