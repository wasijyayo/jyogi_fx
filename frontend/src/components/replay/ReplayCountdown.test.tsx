import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';

import ReplayCountdown from './ReplayCountdown';

// Date.now() を固定してから startedAt を渡す（fake timers と組み合わせる）。
const START = new Date('2026-01-01T00:00:00Z').getTime();

describe('ReplayCountdown', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(START);
  });

  afterEach(() => {
    cleanup();
    vi.useRealTimers();
    // @ts-expect-error jsdom の初期状態には無い
    delete window.matchMedia;
  });

  it('5 秒間を 5・4・3・2・1 と出し、読み込みが終わっていれば消える', () => {
    const { rerender } = render(<ReplayCountdown startedAt={START} ready />);

    expect(screen.getByRole('status').textContent).toContain('5');
    act(() => {
      vi.advanceTimersByTime(1000);
    });
    expect(screen.getByRole('status').textContent).toContain('4');
    act(() => {
      vi.advanceTimersByTime(3000);
    });
    expect(screen.getByRole('status').textContent).toContain('1');
    expect(screen.getByRole('status')).toBeTruthy();

    act(() => {
      vi.advanceTimersByTime(1000);
    });
    rerender(<ReplayCountdown startedAt={START} ready />);
    expect(screen.queryByRole('status')).toBeNull();
  });

  it('読み込みが間に合わないときは、完了まで最前面の画面を消さない', () => {
    const { rerender } = render(<ReplayCountdown startedAt={START} ready={false} />);

    act(() => {
      vi.advanceTimersByTime(5000);
    });
    // 5 秒たっても読み込み中なら画面は残り、数字は 1 のまま。
    expect(screen.getByRole('status')).toBeTruthy();
    expect(screen.getByRole('status').textContent).toContain('1');

    rerender(<ReplayCountdown startedAt={START} ready />);
    expect(screen.queryByRole('status')).toBeNull();
  });

  it('スキップしても読み込みが終わるまでは画面を消さない', () => {
    const { rerender } = render(<ReplayCountdown startedAt={START} ready={false} />);

    fireEvent.click(screen.getByRole('button', { name: 'スキップ' }));
    expect(screen.getByRole('status')).toBeTruthy();

    rerender(<ReplayCountdown startedAt={START} ready />);
    expect(screen.queryByRole('status')).toBeNull();
  });

  it('prefers-reduced-motion でも 5 秒のカウントダウンは短くしない', () => {
    window.matchMedia = vi.fn().mockReturnValue({ matches: true }) as never;
    render(<ReplayCountdown startedAt={START} ready />);

    expect(screen.getByRole('status').textContent).toContain('5');
    act(() => {
      vi.advanceTimersByTime(1000);
    });
    expect(screen.getByRole('status').textContent).toContain('4');
    act(() => {
      vi.advanceTimersByTime(3000);
    });
    expect(screen.getByRole('status').textContent).toContain('1');
  });
});
