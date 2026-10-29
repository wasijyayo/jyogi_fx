import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

import OrderForm from './OrderForm';

// 生成された API hooks を差し替える（#99 の単体テストと同じ。ネットワークは叩かない）。
vi.mock('../api/generated/api', () => ({
  useListCurrencies: vi.fn(),
  useGetMarketStatus: vi.fn(),
  usePlaceOrder: vi.fn(),
  getListPositionsQueryKey: () => ['/api/positions'] as const,
  getGetMeQueryKey: () => ['/api/me'] as const,
}));

import { useGetMarketStatus, useListCurrencies, usePlaceOrder } from '../api/generated/api';

const CURRENCIES = [
  { symbol: 'JOG', name: 'ジョグ', currentPrice: '100.00000000', dayChangePercent: '1.00' },
  { symbol: 'WASI', name: 'ワシ', currentPrice: '200.00000000', dayChangePercent: null },
];

function marketStatus(open: boolean, alwaysOpen: boolean, remainingMinutes: number | null) {
  return {
    data: {
      open,
      alwaysOpen,
      remainingMinutes,
      nextStartAt: open ? null : '2026-01-01T12:00:00+09:00',
    },
    status: 200,
    headers: new Headers(),
  } as never;
}

function renderOrderForm() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <OrderForm />
    </QueryClientProvider>,
  );
}

describe('OrderForm', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(useListCurrencies).mockReturnValue({
      data: { data: CURRENCIES, status: 200, headers: new Headers() },
      isLoading: false,
    } as never);
    vi.mocked(useGetMarketStatus).mockReturnValue({
      data: marketStatus(true, false, 30),
      isLoading: false,
    } as never);
    vi.mocked(usePlaceOrder).mockReturnValue({
      mutate: vi.fn(),
      isPending: false,
      data: undefined,
    } as never);
  });

  afterEach(() => {
    cleanup();
  });

  it('通貨・売買方向・数量・レバレッジ・送信ボタンを表示する', () => {
    renderOrderForm();

    expect(screen.getByLabelText('通貨')).toBeTruthy();
    expect(screen.getByRole('button', { name: '買い' })).toBeTruthy();
    expect(screen.getByRole('button', { name: '売り' })).toBeTruthy();
    expect(screen.getByLabelText('数量')).toBeTruthy();
    expect(screen.getByLabelText('レバレッジ')).toBeTruthy();
    expect(screen.getByRole('button', { name: '買い注文' })).toBeTruthy();
  });

  it('数量・レバレッジを入力して送信すると usePlaceOrder の mutate を正しい内容で呼ぶ', () => {
    const mutate = vi.fn();
    vi.mocked(usePlaceOrder).mockReturnValue({
      mutate,
      isPending: false,
      data: undefined,
    } as never);

    renderOrderForm();

    fireEvent.change(screen.getByLabelText('数量'), { target: { value: '10' } });
    fireEvent.change(screen.getByLabelText('レバレッジ'), { target: { value: '5' } });
    fireEvent.click(screen.getByRole('button', { name: '買い注文' }));

    expect(mutate).toHaveBeenCalledWith({
      data: { symbol: 'JOG', side: 'long', size: '10', leverage: '5' },
    });
  });

  it('時間外は新規注文を受け付けない旨を表示し、入力と送信を無効化する', () => {
    vi.mocked(useGetMarketStatus).mockReturnValue({
      data: marketStatus(false, false, null),
      isLoading: false,
    } as never);

    renderOrderForm();

    expect(screen.getByText(/新規注文を受け付けていません/)).toBeTruthy();
    expect((screen.getByLabelText('数量') as HTMLInputElement).disabled).toBe(true);
    expect((screen.getByRole('button', { name: '買い注文' }) as HTMLButtonElement).disabled).toBe(true);
  });

  it('サーバーが残高不足を返すとエラーメッセージを表示する', () => {
    vi.mocked(usePlaceOrder).mockReturnValue({
      mutate: vi.fn(),
      isPending: false,
      data: { data: { message: '残高が不足しています' }, status: 409, headers: new Headers() },
    } as never);

    renderOrderForm();

    expect(screen.getByRole('alert').textContent).toBe('残高が不足しています');
  });
});
