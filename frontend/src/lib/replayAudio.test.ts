import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import {
  isReplayBgmPlaying,
  isReplayMuted,
  playReplayCoin,
  playReplayOrder,
  playReplaySettle,
  playReplayStart,
  setReplayMuted,
  startReplayBgm,
  stopReplayBgm,
  unlockReplayAudio,
} from './replayAudio';

// 過去チャートモードの SE・BGM（#129 REPLAY-12・#140 REPLAY-14）の単体テスト。
// jsdom には AudioContext が無いため、BGM の開始・停止を確かめるテストでは
// 生成した音の数を数えられる最小限の偽 AudioContext を使う。

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
    expect(() => playReplayStart()).not.toThrow();
    expect(() => playReplayOrder()).not.toThrow();
    expect(() => playReplaySettle()).not.toThrow();
    expect(() => startReplayBgm()).not.toThrow();
    expect(() => stopReplayBgm()).not.toThrow();
  });
});

// 生成したオシレーターの数（鳴らした音の数）を数える。AudioContext はモジュール内で
// 使い回されるため、テストをまたいでも数えられるようファイル全体で共有する。
let oscillatorCount = 0;

class FakeGainParam {
  value = 1;
  setValueAtTime = vi.fn();
  exponentialRampToValueAtTime = vi.fn();
  cancelScheduledValues = vi.fn();
}

class FakeGainNode {
  gain = new FakeGainParam();
  connect = vi.fn();
  disconnect = vi.fn();
}

class FakeOscillatorNode {
  type: OscillatorType = 'sine';
  frequency = { setValueAtTime: vi.fn() };
  connect = vi.fn();
  start = vi.fn();
  stop = vi.fn();

  constructor() {
    oscillatorCount += 1;
  }
}

class FakeAudioContext {
  state: AudioContextState = 'running';
  currentTime = 0;
  destination = {} as AudioDestinationNode;
  resume = vi.fn(async () => undefined);

  createGain(): FakeGainNode {
    return new FakeGainNode();
  }

  createOscillator(): FakeOscillatorNode {
    return new FakeOscillatorNode();
  }
}

function installFakeAudioContext() {
  Object.defineProperty(window, 'AudioContext', {
    configurable: true,
    writable: true,
    value: FakeAudioContext,
  });
}

describe('replayAudio の SE と BGM', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    window.localStorage.clear();
    stopReplayBgm();
    setReplayMuted(false);
    oscillatorCount = 0;
    installFakeAudioContext();
    unlockReplayAudio();
  });

  afterEach(() => {
    stopReplayBgm();
    setReplayMuted(false);
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it('ミュート中は SE も BGM も鳴らない', () => {
    setReplayMuted(true);

    playReplayStart();
    startReplayBgm();

    expect(isReplayBgmPlaying()).toBe(false);
    expect(oscillatorCount).toBe(0);
  });

  it('BGM を開始し、止められる', () => {
    startReplayBgm();
    expect(isReplayBgmPlaying()).toBe(true);
    expect(oscillatorCount).toBeGreaterThan(0);

    stopReplayBgm();
    expect(isReplayBgmPlaying()).toBe(false);
  });

  it('BGM を二重に開始しない', () => {
    startReplayBgm();
    const created = oscillatorCount;

    startReplayBgm();

    expect(oscillatorCount).toBe(created);
    expect(isReplayBgmPlaying()).toBe(true);
  });

  it('BGM はフレーズを繰り返し、止めた後は鳴らさない', () => {
    startReplayBgm();
    const phrase = oscillatorCount;

    vi.advanceTimersByTime(4000);
    expect(oscillatorCount).toBe(phrase * 2);

    stopReplayBgm();
    const stopped = oscillatorCount;
    vi.advanceTimersByTime(8000);
    expect(oscillatorCount).toBe(stopped);
  });

  it('ミュートで BGM を止め、解除するとプレイ中なら再開する', () => {
    startReplayBgm();
    expect(isReplayBgmPlaying()).toBe(true);

    setReplayMuted(true);
    expect(isReplayBgmPlaying()).toBe(false);

    setReplayMuted(false);
    expect(isReplayBgmPlaying()).toBe(true);
  });

  it('止めた後にミュートを解除しても BGM は再開しない', () => {
    startReplayBgm();
    stopReplayBgm();

    setReplayMuted(true);
    setReplayMuted(false);

    expect(isReplayBgmPlaying()).toBe(false);
  });
});
