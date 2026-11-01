import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';

import ReplayStart from './ReplayStart';

// 生成された API hooks を差し替える（#99 の単体テスト。ネットワークは叩かない）。
vi.mock('../../api/generated/api', () => ({
  useGetMe: vi.fn(),
  useStartReplayGame: vi.fn(),
}));

import { useGetMe, useStartReplayGame } from '../../api/generated/api';

describe('ReplayStart', () => {
  const mutate = vi.fn();

  beforeEach(() => {
    mutate.mockReset();
    vi.mocked(useStartReplayGame).mockReturnValue({
      mutate,
      isPending: false,
    } as never);
  });

  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it('ログイン済みならゲスト名の入力を出さず、そのまま開始する', () => {
    vi.mocked(useGetMe).mockReturnValue({
      data: {
        status: 200,
        headers: new Headers(),
        data: { discordId: 'u1', displayName: 'テスト太郎', balance: '1000' },
      },
      isLoading: false,
    } as never);

    render(<ReplayStart onStarted={vi.fn()} />);

    expect(screen.queryByLabelText('ゲスト名（1〜16 文字）')).toBeNull();
    expect(screen.getByText(/テスト太郎 さんとして参加します/)).toBeTruthy();

    fireEvent.click(screen.getByRole('button', { name: 'ゲームを始める' }));
    expect(mutate).toHaveBeenCalledWith({ data: {} });
  });

  it('未ログインでゲスト名が空なら開始せず、エラーを表示する', () => {
    vi.mocked(useGetMe).mockReturnValue({
      data: { status: 401, headers: new Headers(), data: { message: 'not logged in' } },
      isLoading: false,
    } as never);

    render(<ReplayStart onStarted={vi.fn()} />);

    fireEvent.click(screen.getByRole('button', { name: 'ゲームを始める' }));

    expect(mutate).not.toHaveBeenCalled();
    expect(screen.getByRole('alert').textContent).toBe('ゲスト名を入力してください');
  });

  it('未ログインでもゲスト名を入れて開始できる', () => {
    vi.mocked(useGetMe).mockReturnValue({
      data: { status: 401, headers: new Headers(), data: { message: 'not logged in' } },
      isLoading: false,
    } as never);

    render(<ReplayStart onStarted={vi.fn()} />);

    fireEvent.change(screen.getByLabelText('ゲスト名（1〜16 文字）'), {
      target: { value: ' ゲストA ' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'ゲームを始める' }));

    expect(mutate).toHaveBeenCalledWith({ data: { guestName: 'ゲストA' } });
  });
});
