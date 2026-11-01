import type { ReplayCandle, ReplayGameStarted } from '../api/generated/model';

// 過去チャートモード（design.md §13）の表示用の純粋関数群。
// 金額・損益の計算はサーバー（internal/game）で行い、ここでは表示の整形と
// 「残り時間」のような時間の計算だけを行う（CLAUDE.md §3・§4・§5.2）。

// 1 営業日を公開する標準の速さ（×1）の間隔（秒）。design.md §13「1 ゲームの流れ」。
export const REPLAY_STANDARD_DAY_SECONDS = 3;

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

// replayErrorMessage は API のエラー応答から表示用のメッセージを取り出す。
// サーバーが返したメッセージ（例: 反対方向の建玉がある）をそのまま画面に出す。
export function replayErrorMessage(
  res: { status: number; data: unknown } | undefined,
  fallback: string,
): string {
  const message = (res?.data as { message?: string } | null | undefined)?.message;
  return message ?? fallback;
}

function trimZero(value: string): string {
  return value.replace(/^0+(?=\d)/, '');
}
