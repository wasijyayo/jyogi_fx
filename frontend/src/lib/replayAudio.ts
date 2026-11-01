// 過去チャートモードの SE・BGM（#129 REPLAY-12・#140 REPLAY-14）。
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

interface ReplayTone {
  frequency: number;
  delay: number;
  duration: number;
  volume: number;
  type?: OscillatorType;
}

// playTones は短い音を並べて鳴らす。AudioContext が無い・ミュート中なら何もしない。
// 音の合成に失敗してもゲームは続けられるよう、例外は握りつぶす。
function playTones(tones: readonly ReplayTone[]): void {
  if (muted) return;
  const ctx = context;
  if (ctx == null) return;
  if (ctx.state === 'suspended') {
    // クリックの外から呼ばれた場合に備えて再開を試みる（鳴らなくても問題ない）。
    void ctx.resume().catch(() => undefined);
  }

  try {
    const now = ctx.currentTime;
    for (const tone of tones) {
      const start = now + tone.delay;
      const attackEnd = start + Math.min(0.01, tone.duration / 2);
      const oscillator = ctx.createOscillator();
      const gain = ctx.createGain();
      oscillator.type = tone.type ?? 'triangle';
      oscillator.frequency.setValueAtTime(tone.frequency, start);
      gain.gain.setValueAtTime(0.0001, start);
      gain.gain.exponentialRampToValueAtTime(tone.volume, attackEnd);
      gain.gain.exponentialRampToValueAtTime(0.0001, start + tone.duration);
      oscillator.connect(gain);
      gain.connect(ctx.destination);
      oscillator.start(start);
      oscillator.stop(start + tone.duration + 0.02);
    }
  } catch {
    // 音の合成に失敗してもゲームは続けられる。
  }
}

// playReplayStart は「取引開始」を押した瞬間の SE。
export function playReplayStart(): void {
  playTones([
    { frequency: 523.25, delay: 0, duration: 0.12, volume: 0.12 },
    { frequency: 659.25, delay: 0.1, duration: 0.12, volume: 0.12 },
    { frequency: 783.99, delay: 0.2, duration: 0.18, volume: 0.12 },
  ]);
}

// playReplayCountdown はカウントダウンの数字が変わるたびの SE（1 回ずつ）。
export function playReplayCountdown(): void {
  playTones([{ frequency: 880, delay: 0, duration: 0.09, volume: 0.1 }]);
}

// playReplayOrder は注文（買う・売る）が成功したときの SE。
export function playReplayOrder(): void {
  playTones([
    { frequency: 523.25, delay: 0, duration: 0.07, volume: 0.11 },
    { frequency: 783.99, delay: 0.06, duration: 0.09, volume: 0.11 },
  ]);
}

// playReplaySettle は手動の決済が成功したときの SE。利益が出た決済では代わりに
// playReplayCoin を鳴らし、この SE と二重に鳴らさない。
export function playReplaySettle(): void {
  playTones([
    { frequency: 659.25, delay: 0, duration: 0.1, volume: 0.12 },
    { frequency: 523.25, delay: 0.09, duration: 0.14, volume: 0.12 },
  ]);
}

// playReplayCoin は利益が出た決済の「お金系」の SE を鳴らす。短い 2 音を合成する。
// AudioContext が無い・ミュート中なら何もしない。
export function playReplayCoin(): void {
  playTones([
    { frequency: 988, delay: 0, duration: 0.14, volume: 0.16 },
    { frequency: 1319, delay: 0.09, duration: 0.14, volume: 0.16 },
  ]);
}

// ---- BGM ----

// 短いフレーズを繰り返す（音声ファイルは使わない）。SE を邪魔しないよう控えめな音量にする。
const BGM_MASTER_VOLUME = 0.5;
const BGM_NOTE_VOLUME = 0.08;
const BGM_NOTE_SECONDS = 0.5;
const BGM_PHRASE_MS = 4000;
const BGM_FREQUENCIES = [261.63, 329.63, 392.0, 329.63, 293.66, 349.23, 440.0, 349.23];

interface ReplayBgm {
  master: GainNode;
  timer: number;
}

// 実際に鳴っている BGM。二重起動しないよう、ここが null のときだけ開始する。
let bgm: ReplayBgm | null = null;
// 鳴らしたい状態（ミュートで止めても保持する）。解除したらプレイ中なら再開する。
let bgmWanted = false;

// scheduleBgmPhrase は 1 フレーズ分の音をまとめて予約する。
function scheduleBgmPhrase(ctx: AudioContext, master: GainNode): void {
  const startAt = ctx.currentTime;
  BGM_FREQUENCIES.forEach((frequency, index) => {
    const start = startAt + index * BGM_NOTE_SECONDS;
    const end = start + BGM_NOTE_SECONDS * 0.9;
    const oscillator = ctx.createOscillator();
    const gain = ctx.createGain();
    oscillator.type = 'sine';
    oscillator.frequency.setValueAtTime(frequency, start);
    gain.gain.setValueAtTime(0.0001, start);
    gain.gain.exponentialRampToValueAtTime(BGM_NOTE_VOLUME, start + 0.08);
    gain.gain.exponentialRampToValueAtTime(0.0001, end);
    oscillator.connect(gain);
    gain.connect(master);
    oscillator.start(start);
    oscillator.stop(end + 0.02);
  });
}

// beginBgm は実際に BGM を鳴らし始める（ミュート中・AudioContext が無いときは何もしない）。
function beginBgm(): void {
  if (muted || bgm != null) return;
  const ctx = context;
  if (ctx == null) return;
  if (ctx.state === 'suspended') {
    void ctx.resume().catch(() => undefined);
  }

  try {
    const now = ctx.currentTime;
    const master = ctx.createGain();
    master.gain.setValueAtTime(0.0001, now);
    master.gain.exponentialRampToValueAtTime(BGM_MASTER_VOLUME, now + 0.5);
    master.connect(ctx.destination);

    const schedule = () => {
      try {
        scheduleBgmPhrase(ctx, master);
      } catch {
        // 途中で音の合成に失敗してもゲームは続けられる。
      }
    };
    schedule();
    const timer = window.setInterval(schedule, BGM_PHRASE_MS);
    bgm = { master, timer };
  } catch {
    // 音の合成に失敗してもゲームは続けられる。
    bgm = null;
  }
}

// endBgm は実際に鳴っている BGM を止める（鳴らしたい状態は変えない）。
function endBgm(): void {
  const current = bgm;
  if (current == null) return;
  bgm = null;
  try {
    window.clearInterval(current.timer);
    const now = context?.currentTime ?? 0;
    current.master.gain.cancelScheduledValues(now);
    current.master.gain.setValueAtTime(0.0001, now);
    current.master.disconnect();
  } catch {
    // 止められなくてもゲームは続けられる。
  }
}

// startReplayBgm はゲーム開始時に BGM をループ再生する。二重に呼んでも増えない。
export function startReplayBgm(): void {
  bgmWanted = true;
  beginBgm();
}

// stopReplayBgm は結果画面・画面離脱時に BGM を止める。
// ミュート解除で再開しないよう、鳴らしたい状態も false にする。
export function stopReplayBgm(): void {
  bgmWanted = false;
  endBgm();
}

// isReplayBgmPlaying は BGM が実際に鳴っているかを返す（テスト用）。
export function isReplayBgmPlaying(): boolean {
  return bgm != null;
}

// isReplayMuted は現在ミュート中かを返す。
export function isReplayMuted(): boolean {
  return muted;
}

// setReplayMuted はミュート設定を切り替えて localStorage に保存する。
// SE は各関数が muted を見て鳴らさない。BGM は即座に止め、解除したら
// プレイ中（startReplayBgm 済み）なら再開する。
// localStorage が使えない環境（プライベートモード等）でも落ちないよう try/catch で囲む。
export function setReplayMuted(value: boolean): void {
  muted = value;
  try {
    window.localStorage.setItem(MUTE_STORAGE_KEY, value ? '1' : '0');
  } catch {
    // 保存できなくてもその場の設定は反映される。
  }

  if (value) {
    endBgm();
  } else if (bgmWanted) {
    beginBgm();
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
