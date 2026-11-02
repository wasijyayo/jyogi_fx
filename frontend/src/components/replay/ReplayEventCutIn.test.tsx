import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, cleanup, render, screen } from '@testing-library/react';

import ReplayEventCutIn from './ReplayEventCutIn';

const eventA = { id: 11, headline: '中央銀行が政策金利を発表' };
const eventB = { id: 12, headline: '大手銀行の経営統合が発表される' };

describe('ReplayEventCutIn', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    cleanup();
    vi.useRealTimers();
  });

  it('同じ出来事は 1 回だけ出し、取り直しで繰り返さない', () => {
    const { rerender } = render(<ReplayEventCutIn events={[eventA]} />);
    expect(screen.getByRole('status').textContent).toContain(eventA.headline);

    act(() => {
      vi.advanceTimersByTime(3500);
    });
    expect(screen.queryByRole('status')).toBeNull();

    // 状態取得のたびに同じ出来事が返っても、もう出さない。
    rerender(<ReplayEventCutIn events={[eventA]} />);
    expect(screen.queryByRole('status')).toBeNull();
  });

  it('重なった出来事は順番に出す', () => {
    const { rerender } = render(<ReplayEventCutIn events={[eventA]} />);
    expect(screen.getByRole('status').textContent).toContain(eventA.headline);

    // 次の状態取得で 2 件目が公開された。
    rerender(<ReplayEventCutIn events={[eventA, eventB]} />);
    expect(screen.getByRole('status').textContent).toContain(eventA.headline);

    act(() => {
      vi.advanceTimersByTime(3500);
    });
    expect(screen.getByRole('status').textContent).toContain(eventB.headline);

    act(() => {
      vi.advanceTimersByTime(3500);
    });
    expect(screen.queryByRole('status')).toBeNull();
  });

  it('日付は出さない', () => {
    render(<ReplayEventCutIn events={[eventA]} />);
    expect(screen.getByRole('status').textContent).not.toMatch(/\d{4}-\d{2}-\d{2}/);
  });
});
