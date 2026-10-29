import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

import Header from './Header';

// 生成された API hooks を差し替える（#99 の単体テスト。ネットワークは叩かない）。
vi.mock('../api/generated/api', () => ({
  useGetMe: vi.fn(),
  useGetMarketStatus: vi.fn(),
  getGetMeQueryKey: () => ['/api/me'] as const,
}));

import { useGetMe, useGetMarketStatus } from '../api/generated/api';

function renderHeader() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <Header />
    </QueryClientProvider>,
  );
}

describe('Header', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it('ログイン中は表示名・残高・ログアウトボタン・取引時間を表示する', () => {
    vi.mocked(useGetMe).mockReturnValue({
      data: {
        data: { discordId: 'u1', displayName: 'テスト太郎', balance: '1000.00000000' },
        status: 200,
        headers: new Headers(),
      },
      isLoading: false,
    } as never);
    vi.mocked(useGetMarketStatus).mockReturnValue({
      data: {
        data: { open: true, alwaysOpen: false, remainingMinutes: 30, nextStartAt: null },
        status: 200,
        headers: new Headers(),
      },
      isLoading: false,
    } as never);

    renderHeader();

    expect(screen.getByText('テスト太郎')).toBeTruthy();
    expect(screen.getByText('1000')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'ログアウト' })).toBeTruthy();
    expect(screen.getByText('開催中（残り30分）')).toBeTruthy();
  });

  it('未ログインなら「Discordでログイン」を表示し、ログアウトボタンは出さない', () => {
    vi.mocked(useGetMe).mockReturnValue({
      data: {
        data: { message: 'not logged in' },
        status: 401,
        headers: new Headers(),
      },
      isLoading: false,
    } as never);
    vi.mocked(useGetMarketStatus).mockReturnValue({
      data: {
        data: {
          open: false,
          alwaysOpen: false,
          remainingMinutes: null,
          nextStartAt: '2026-01-01T12:00:00+09:00',
        },
        status: 200,
        headers: new Headers(),
      },
      isLoading: false,
    } as never);

    renderHeader();

    expect(screen.getByText('Discordでログイン')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'ログアウト' })).toBeNull();
    expect(screen.getByText('次回 12:00 開始')).toBeTruthy();
  });

  it('ログアウトに失敗したらエラー表示を出す', async () => {
    vi.mocked(useGetMe).mockReturnValue({
      data: {
        data: { discordId: 'u1', displayName: 'テスト太郎', balance: '1000.00000000' },
        status: 200,
        headers: new Headers(),
      },
      isLoading: false,
    } as never);
    vi.mocked(useGetMarketStatus).mockReturnValue({
      data: {
        data: { open: true, alwaysOpen: false, remainingMinutes: 30, nextStartAt: null },
        status: 200,
        headers: new Headers(),
      },
      isLoading: false,
    } as never);
    // POST /auth/logout が失敗する（fetch を拒否）ことで mutation がエラーになる。
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('network error')));

    renderHeader();
    fireEvent.click(screen.getByRole('button', { name: 'ログアウト' }));

    expect(await screen.findByText('ログアウトに失敗しました')).toBeTruthy();
  });
});
