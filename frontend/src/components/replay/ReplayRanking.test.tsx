import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, render, screen } from '@testing-library/react';

import ReplayRanking from './ReplayRanking';

describe('ReplayRanking', () => {
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it('ログインユーザーはアイコンと表示名、ゲストは「ゲスト」の印を表示する', () => {
    render(
      <ReplayRanking
        entries={[
          {
            rank: 1,
            name: 'テスト太郎',
            isGuest: false,
            avatarUrl: 'https://example.com/avatar.png',
            finalFunds: '1500.00000000',
          },
          {
            rank: 2,
            name: 'ゲストA',
            isGuest: true,
            avatarUrl: null,
            finalFunds: '1200.50000000',
          },
        ]}
      />,
    );

    expect(screen.getByText('テスト太郎')).toBeTruthy();
    expect(screen.getByText('ゲストA')).toBeTruthy();
    expect(screen.getByText('ゲスト')).toBeTruthy();
    expect(screen.getByText('1500')).toBeTruthy();
    expect(screen.getByText('1200.5')).toBeTruthy();
    const avatar = screen.getByAltText('テスト太郎 のアイコン');
    expect(avatar.getAttribute('src')).toBe('https://example.com/avatar.png');
  });

  it('記録が無ければその旨を表示する', () => {
    render(<ReplayRanking entries={[]} />);
    expect(screen.getByText('まだ記録がありません。最初の 1 本を目指しましょう。')).toBeTruthy();
  });
});
