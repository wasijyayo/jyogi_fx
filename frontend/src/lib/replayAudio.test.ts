import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import {
  isReplayMuted,
  playReplayCoin,
  setReplayMuted,
  unlockReplayAudio,
} from './replayAudio';

// 過去チャートモードの SE（#129 REPLAY-12）の単体テスト。
// jsdom には AudioContext が無いため、音を鳴らす関数は「落ちないこと」だけを確認する。

describe('replayAudio のミュート設定', () => {
  beforeEach(() => {
    window.localStorage.clear();
    setReplayMuted(false);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('設定を localStorage に保存し、読み出せる', () => {
    setReplayMuted(true);
    expect(isReplayMuted()).toBe(true);
    expect(window.localStorage.getItem('fxgame.replayMuted')).toBe('1');

    setReplayMuted(false);
    expect(isReplayMuted()).toBe(false);
    expect(window.localStorage.getItem('fxgame.replayMuted')).toBe('0');
  });

  it('localStorage が使えなくても落ちない', () => {
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('storage disabled');
    });

    expect(() => setReplayMuted(true)).not.toThrow();
    expect(isReplayMuted()).toBe(true);
  });

  it('AudioContext が無い環境でも音を鳴らそうとして落ちない', () => {
    expect(() => unlockReplayAudio()).not.toThrow();
    expect(() => playReplayCoin()).not.toThrow();
  });
});
