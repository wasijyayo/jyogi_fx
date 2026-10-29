import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

import PositionsList from './PositionsList';

// 生成された API hooks を差し替える（ネットワークは叩かない）。
vi.mock('../api/generated/api', () => ({
  useListPositions: vi.fn(),
  useClosePosition: vi.fn(),
  useGetMarketStatus: vi.fn(),
  getListPositionsQueryKey: () => ['/api/positions'] as const,
  getGetMeQueryKey: () => ['/api/me'] as const,
}));

import {
  useClosePosition,
  useGetMarketStatus,
  useListPositions,
} from '../api/generated/api';

const POSITIONS = [
  {
    id: 1,
    symbol: 'JOG',
    side: 'long',
    size: '10.00000000',
    entryPrice: '100.00000000',
    leverage: '1.00000000',
    currentPrice: '105.00000000',
    unrealizedPnl: '50.00000000',
    openedAt: '2026-01-01T12:00:00+09:00',
  },
  {
    id: 2,
    symbol: 'WASI',
    side: 'short',
    size: '5.00000000',
    entryPrice: '200.00000000',
    leverage: '2.00000000',
    currentPrice: '210.00000000',
    unrealizedPnl: '-50.00000000',
    openedAt: '2026-01-01T12:05:00+09:00',
  },
];

function renderPositionsList() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <PositionsList />
    </QueryClientProvider>,
  );
}

describe('PositionsList', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(useListPositions).mockReturnValue({
      data: { data: POSITIONS, status: 200, headers: new Headers() },
      isLoading: false,
    } as never);
    vi.mocked(useClosePosition).mockReturnValue({
      mutate: vi.fn(),
      isPending: false,
      data: undefined,
    } as never);
    // useIsMarketOpen が参照するマーケット状態（開催中）。
    vi.mocked(useGetMarketStatus).mockReturnValue({
      data: {
        data: { open: true, alwaysOpen: false, remainingMinutes: 30, nextStartAt: null },
        status: 200,
        headers: new Headers(),
      },
      isLoading: false,
    } as never);
  });

  afterEach(() => {
    cleanup();
  });

  it('建玉が無いときはその旨を表示する', () => {
    vi.mocked(useListPositions).mockReturnValue({
      data: { data: [], status: 200, headers: new Headers() },
      isLoading: false,
    } as never);

    renderPositionsList();

    expect(screen.getByText('保有ポジションはありません。')).toBeTruthy();
  });

  it('建玉の通貨・売買方向・含み損益・決済ボタンを表示する', () => {
    renderPositionsList();

    expect(screen.getByText('JOG')).toBeTruthy();
    expect(screen.getByText('WASI')).toBeTruthy();
    expect(screen.getAllByText('決済').length).toBe(2);
    // 含み損益は符号付きで表示される。
    expect(screen.getByText('含み損益 +50')).toBeTruthy();
    expect(screen.getByText('含み損益 -50')).toBeTruthy();
  });

  it('決済ボタンを押すと確認が表示され、「はい」で useClosePosition を呼ぶ', () => {
    const mutate = vi.fn();
    vi.mocked(useClosePosition).mockReturnValue({
      mutate,
      isPending: false,
      data: undefined,
    } as never);

    renderPositionsList();

    fireEvent.click(screen.getAllByText('決済')[0]);
    expect(screen.getByText('本当に決済しますか？')).toBeTruthy();

    fireEvent.click(screen.getByRole('button', { name: 'はい' }));
    expect(mutate).toHaveBeenCalledWith({ positionId: 1 });
  });

  it('確認で「キャンセル」を押すと決済しない', () => {
    const mutate = vi.fn();
    vi.mocked(useClosePosition).mockReturnValue({
      mutate,
      isPending: false,
      data: undefined,
    } as never);

    renderPositionsList();

    fireEvent.click(screen.getAllByText('決済')[0]);
    fireEvent.click(screen.getByRole('button', { name: 'キャンセル' }));

    expect(mutate).not.toHaveBeenCalled();
  });
});
