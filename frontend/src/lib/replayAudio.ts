// 過去チャートモードの SE（#129 REPLAY-12）。
//
// 音声ファイルは使わず Web Audio API で合成する（利用条件の確認とファイル同梱が不要）。
// ブラウザの自動再生制限があるため、AudioContext は「取引開始」ボタンのクリック
// （ユーザー操作）の中で unlockReplayAudio() を呼んで開始する。

const MUTE_STORAGE_KEY = 'fxgame.replayMuted';

// AudioContext は 1 つだけ使い回す。作成はユーザー操作の中だけで行う。
let context: AudioContext | null = null;
// ミュート設定は毎回 localStorage を読まず、モジュール内に持つ。
let muted = readStoredMute();

interface AudioContextConstructor {
  new (): AudioContext;
}

// audioContextConstructor はブラウザの AudioContext を返す（Safari の webkit 接頭辞にも対応）。
function audioContextConstructor(): AudioContextConstructor | null {
  if (typeof window === 'undefined') return null;
  const w = window as typeof window & {
    AudioContext?: AudioContextConstructor;
    webkitAudioContext?: AudioContextConstructor;
  };
  return w.AudioContext ?? w.webkitAudioContext ?? null;
}

// unlockReplayAudio は AudioContext を作成・再開する。ユーザーのクリックの中で呼ぶこと
// （クリックの外で呼ぶと自動再生制限で suspended のままになる）。
export function unlockReplayAudio(): void {
  const Ctor = audioContextConstructor();
  if (Ctor == null) return;
  try {
    context ??= new Ctor();
    if (context.state === 'suspended') {
      void context.resume().catch(() => {
        // 再開できなくてもゲームは続けられる（音が鳴らないだけ）。
      });
    }
  } catch {
    // AudioContext が作れない環境でもゲームは続けられる。
    context = null;
  }
}

// playReplayCoin は利益が出た決済の「お金系」の SE を鳴らす。短い 2 音を合成する。
// AudioContext が無い・ミュート中なら何もしない。
export function playReplayCoin(): void {
  if (muted) return;
  const ctx = context;
  if (ctx == null) return;
  if (ctx.state === 'suspended') {
    // クリックの外から呼ばれた場合に備えて再開を試みる（鳴らなくても問題ない）。
    void ctx.resume().catch(() => undefined);
  }

  try {
    const now = ctx.currentTime;
    const notes = [
      { frequency: 988, delay: 0 },
      { frequency: 1319, delay: 0.09 },
    ];
    for (const note of notes) {
      const start = now + note.delay;
      const oscillator = ctx.createOscillator();
      const gain = ctx.createGain();
      oscillator.type = 'triangle';
      oscillator.frequency.setValueAtTime(note.frequency, start);
      gain.gain.setValueAtTime(0.0001, start);
      gain.gain.exponentialRampToValueAtTime(0.16, start + 0.01);
      gain.gain.exponentialRampToValueAtTime(0.0001, start + 0.14);
      oscillator.connect(gain);
      gain.connect(ctx.destination);
      oscillator.start(start);
      oscillator.stop(start + 0.16);
    }
  } catch {
    // 音の合成に失敗してもゲームは続けられる。
  }
}

// isReplayMuted は現在ミュート中かを返す。
export function isReplayMuted(): boolean {
  return muted;
}

// setReplayMuted はミュート設定を切り替えて localStorage に保存する。
// localStorage が使えない環境（プライベートモード等）でも落ちないよう try/catch で囲む。
export function setReplayMuted(value: boolean): void {
  muted = value;
  try {
    window.localStorage.setItem(MUTE_STORAGE_KEY, value ? '1' : '0');
  } catch {
    // 保存できなくてもその場の設定は反映される。
  }
}

// readStoredMute は保存されたミュート設定を読む（無い・読めないなら false）。
function readStoredMute(): boolean {
  try {
    return window.localStorage.getItem(MUTE_STORAGE_KEY) === '1';
  } catch {
    return false;
  }
}
