import type { ReplayCandle, ReplayGameStarted } from '../api/generated/model';

// 過去チャートモード（design.md §13）の表示用の純粋関数群。
// 金額・損益の計算はサーバー（internal/game）で行い、ここでは表示の整形と
// 「残り時間」のような時間の計算だけを行う（CLAUDE.md §3・§4・§5.2）。

// 1 営業日を公開する標準の速さ（×1）の間隔（秒）。design.md §13「1 ゲームの流れ」。
export const REPLAY_STANDARD_DAY_SECONDS = 3;

// 「取引開始」から時間が動き始めるまでのカウントダウン（秒）。サーバーが begin の時刻 +
// この秒数を started_at にする（design.md §13。#126 REPLAY-9・#138 REPLAY-13）。
// 2 回目以降や reduced-motion でもこの秒数は変えない。
export const REPLAY_BEGIN_COUNTDOWN_SECONDS = 5;

// 1 ゲームの区間の営業日数（design.md §13「対象と期間」）。
export const REPLAY_INTERVAL_DAYS = 60;

// 区間の前に最初から見せる営業日数（design.md §13「対象と期間」）。
export const REPLAY_PRE_DAYS = 20;

// 速さの選択肢（×1・×2・×3。一時停止なし）。
export const REPLAY_SPEEDS = [1, 2, 3] as const;

// ReplaySession は開始したゲームのトークンと開始時の応答。トークンは React の
// state だけで保持し、再読み込みでゲームは失われる（#105 で決めたこと）。
export interface ReplaySession {
  gameId: number;
  token: string;
  started: ReplayGameStarted;
}

// 通信に失敗したときに画面へ出す共通メッセージ。
export const REPLAY_NETWORK_ERROR_MESSAGE = '通信に失敗しました。もう一度お試しください';

// isReplayFinishedStatus は結果画面へ進む status かを返す。
// 早期終了の判定はサーバーが行い、フロントでは status を見るだけにする。
export function isReplayFinishedStatus(status: string): boolean {
  return status === 'finished' || status === 'early_finished';
}

// replayRemainingSeconds は残り時間の目安（秒）を返す。公開済み日数と速さから
// (60 - publishedDays) × 3 秒 ÷ speed で計算する（厳密でなくてよい）。
export function replayRemainingSeconds(publishedDays: number, speed: number): number {
  const remainingDays = Math.max(0, REPLAY_INTERVAL_DAYS - publishedDays);
  const safeSpeed = speed > 0 ? speed : 1;
  return Math.ceil((remainingDays * REPLAY_STANDARD_DAY_SECONDS) / safeSpeed);
}

// formatRemainingTime は秒を「m:ss」にする。
export function formatRemainingTime(seconds: number): string {
  const safe = Math.max(0, Math.ceil(seconds));
  const minutes = Math.floor(safe / 60);
  const rest = safe % 60;
  return `${minutes}:${String(rest).padStart(2, '0')}`;
}

// replayPollIntervalMs は状態取得のポーリング間隔（ミリ秒）を返す。
// 「3 秒 ÷ 速さ 以下」を目安にし、短くなりすぎないよう 1 秒で止める。
export function replayPollIntervalMs(speed: number): number {
  const safeSpeed = speed > 0 ? speed : 1;
  return Math.max(1000, Math.floor((REPLAY_STANDARD_DAY_SECONDS * 1000) / safeSpeed));
}

// 状態取得を次の 1 日が公開された直後に合わせるための余白（ミリ秒）。
// サーバーが計算した時刻は応答が届くまでに少し古くなるため、少し遅らせる（#129 REPLAY-12）。
export const REPLAY_POLL_MARGIN_MS = 120;
// 状態取得の最短間隔（ミリ秒）。境界をまたいだ直後に連打しないようにする。
export const REPLAY_MIN_POLL_MS = 200;

// replayPollDelayMs は次の状態取得までの待ち時間（ミリ秒）を返す。
// サーバーが返した msUntilNextDay から、取得してから経過した時間を引いて
// 「次の 1 日が公開される直後」に合わせる（#129 REPLAY-12）。msUntilNextDay が 0
// （ready・終了済み）や極端に大きい場合は、速さから決まる通常の間隔に丸める。
export function replayPollDelayMs(
  msUntilNextDay: number,
  elapsedMs: number,
  speed: number,
): number {
  const fallback = replayPollIntervalMs(speed);
  if (!Number.isFinite(msUntilNextDay) || msUntilNextDay <= 0) return fallback;
  const remaining = msUntilNextDay - Math.max(0, elapsedMs);
  const aligned = remaining + REPLAY_POLL_MARGIN_MS;
  return Math.min(Math.max(REPLAY_MIN_POLL_MS, aligned), fallback + REPLAY_POLL_MARGIN_MS);
}

// isPositiveAmount は decimal 文字列が正の値かを返す（損益がプラスかの判定だけに使う）。
// 数値に変換しない（表示用の符号判定。CLAUDE.md §5.2）。
export function isPositiveAmount(value: string | null | undefined): boolean {
  if (value == null) return false;
  const v = value.trim();
  if (v === '' || v.startsWith('-')) return false;
  return !/^0*(?:\.0*)?$/.test(v);
}

// validateGuestName はゲスト名の入力チェック。前後の空白を除いて 1〜16 文字。
// 問題なければ null、問題があれば画面に出すメッセージを返す。
export function validateGuestName(name: string): string | null {
  const trimmed = name.trim();
  if (trimmed === '') return 'ゲスト名を入力してください';
  if ([...trimmed].length > 16) return 'ゲスト名は 16 文字以内で入力してください';
  return null;
}

// isPositiveIntegerString は「1 以上の整数」の文字列かを返す（証拠金・レバレッジ・追加額）。
// 数値への変換はせず、入力の検証だけを行う。
export function isPositiveIntegerString(value: string): boolean {
  return /^[1-9][0-9]*$/.test(value.trim());
}

// 注文の入力で使う刻み（#138 REPLAY-13）。押すたびに現在値へ足し、1〜上限に丸める。
export const REPLAY_MARGIN_STEPS = [-1000, -500, -100, 100, 500, 1000] as const;
export const REPLAY_LEVERAGE_STEPS = [-10, -5, -1, 1, 5, 10] as const;

// replayIntegerPart は decimal 文字列の整数部だけを取り出す（表示用の上限計算）。
// 金額そのものの計算には使わない（CLAUDE.md §5.2）。負値・小数部は切り捨てる。
function replayIntegerPart(value: string): number {
  const match = /^([0-9]+)/.exec(value.trim());
  if (!match) return 0;
  const parsed = Number(match[1]);
  return Number.isFinite(parsed) ? parsed : 0;
}

// replayMaxMargin は注文に使える証拠金の上限（現在の資金の整数部）を返す。
// 証拠金は整数で扱うため、資金が小数でもその整数部までにする。
export function replayMaxMargin(funds: string): number {
  return replayIntegerPart(funds);
}

// canReplayOrder は注文の操作を有効にしてよいかを返す。資金が 1 未満なら注文できない。
export function canReplayOrder(funds: string): boolean {
  return replayMaxMargin(funds) >= 1;
}

// clampReplayMargin は証拠金を 1〜現在の資金（整数部）に丸める（#138 REPLAY-13）。
export function clampReplayMargin(value: string, funds: string): number {
  const max = Math.max(1, replayMaxMargin(funds));
  const current = replayIntegerPart(value);
  return Math.min(Math.max(current, 1), max);
}

// clampReplayLeverage はレバレッジを 1〜現在の上限に丸める（#138 REPLAY-13）。
export function clampReplayLeverage(value: string, maxLeverage: string): number {
  const max = Math.max(1, replayIntegerPart(maxLeverage));
  const current = replayIntegerPart(value);
  return Math.min(Math.max(current, 1), max);
}

// stepReplayMargin / stepReplayLeverage は刻みボタンの次の値を返す（文字列のまま）。
export function stepReplayMargin(value: string, delta: number, funds: string): string {
  const current = clampReplayMargin(value, funds);
  return String(clampReplayMargin(String(current + delta), funds));
}

export function stepReplayLeverage(value: string, delta: number, maxLeverage: string): string {
  const current = clampReplayLeverage(value, maxLeverage);
  return String(clampReplayLeverage(String(current + delta), maxLeverage));
}

// replayCountdownValue はカウントダウン開始から elapsedMs 経過したときに表示する数字を返す。
// 5 秒なら 5・4・3・2・1、5 秒を過ぎたら 0（終了）。reduced-motion でも秒数は変えない
// （#138 REPLAY-13）。負の値や不正な値は開始直後として扱う。
export function replayCountdownValue(
  elapsedMs: number,
  seconds: number = REPLAY_BEGIN_COUNTDOWN_SECONDS,
): number {
  if (!Number.isFinite(elapsedMs) || elapsedMs < 0) return seconds;
  const remaining = seconds - Math.floor(elapsedMs / 1000);
  return remaining > 0 ? remaining : 0;
}

// replayDayLabel は日足の通し番号を表示用のラベルにする。
// 0〜19 が開始前、20〜79 が区間の 1〜60 日目（design.md §13）。
export function replayDayLabel(day: number): string {
  if (day < REPLAY_PRE_DAYS) return '開始前';
  return `${day - REPLAY_PRE_DAYS + 1}日目`;
}

// latestReplayClose は公開済みの最後の終値を返す（無ければ null）。表示にだけ使う。
export function latestReplayClose(candles: ReplayCandle[]): string | null {
  if (candles.length === 0) return null;
  return candles[candles.length - 1].close;
}

// replayPricePrecision はチャートの価格表示に使う小数桁数を、サーバーが返した
// decimal 文字列から決める（描画のための桁数の推定であり、金額計算ではない）。
export function replayPricePrecision(candles: ReplayCandle[]): number {
  let max = 0;
  for (const candle of candles) {
    const value = candle.close;
    if (!value.includes('.')) continue;
    const decimals = value.split('.')[1].replace(/0+$/, '').length;
    if (decimals > max) max = decimals;
  }
  return Math.min(8, max);
}

// formatReplayDate は "2020-03-02" を「2020年3月2日」にする。
// Date に変換しない（タイムゾーンで日付がずれないようにする）。
export function formatReplayDate(value: string | null | undefined): string {
  if (!value) return '—';
  const [year, month, day] = value.slice(0, 10).split('-');
  if (!year || !month || !day) return value;
  return `${trimZero(year)}年${trimZero(month)}月${trimZero(day)}日`;
}

// formatReplayInterval は区間を「2020年3月2日〜5月22日」にする。
// 年がまたがる場合は終了日にも年を付ける。
export function formatReplayInterval(
  start: string | null | undefined,
  end: string | null | undefined,
): string {
  if (!start || !end) return '—';
  const [startYear, startMonth, startDay] = start.slice(0, 10).split('-');
  const [endYear, endMonth, endDay] = end.slice(0, 10).split('-');
  if (!startYear || !startMonth || !startDay || !endYear || !endMonth || !endDay) {
    return `${start}〜${end}`;
  }
  if (startYear === endYear) {
    return `${trimZero(startYear)}年${trimZero(startMonth)}月${trimZero(startDay)}日〜${trimZero(endMonth)}月${trimZero(endDay)}日`;
  }
  return `${formatReplayDate(start)}〜${formatReplayDate(end)}`;
}

// サーバーの英語メッセージのうち、画面では日本語で見せたいもの（#140 REPLAY-14）。
// ゲームルール自体はサーバー側のままで、表示だけを置き換える。
const REPLAY_ERROR_MESSAGES: Record<string, string> = {
  'close existing positions of this pair first':
    'このペアには反対の向きの建玉があります。先に決済してください',
};

// replayErrorMessage は API のエラー応答から表示用のメッセージを取り出す。
// サーバーが返したメッセージを画面に出す。既知の英語メッセージだけ日本語にする。
export function replayErrorMessage(
  res: { status: number; data: unknown } | undefined,
  fallback: string,
): string {
  const message = (res?.data as { message?: string } | null | undefined)?.message;
  if (message == null) return fallback;
  return REPLAY_ERROR_MESSAGES[message] ?? message;
}

function trimZero(value: string): string {
  return value.replace(/^0+(?=\d)/, '');
}
