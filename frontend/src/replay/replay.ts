import type {
  ReplayCandle,
  ReplayGameState,
  ReplayPair,
  ReplayPosition,
} from '../api/generated/model';
import { formatPrice } from '../lib/format';

// 過去チャートモード（#105）の表示用ヘルパー。
// 金額・価格・損益は API が decimal 文字列で返す。計算が必要な箇所（含み損益・
// 評価額合計・強制決済ラインの表示位置）だけ Number に変換する。計算そのものは
// サーバーの結果を優先し、ここでは画面に出す合計値を求める。

export const REPLAY_INTERVAL_DAYS = 60;
export const REPLAY_PRE_DAYS = 20;
export const MS_PER_DAY = 3000;
export const REPLAY_SPEEDS = [1, 2, 3] as const;

export type ReplaySide = 'long' | 'short';

// numberFromDecimal は decimal 文字列を表示計算用の数値にする。
export function numberFromDecimal(value: string): number {
  const n = Number(value);
  return Number.isFinite(n) ? n : 0;
}

// formatAmount は計算した数値（含み損益・合計）を読みやすくする。
// API から来た decimal 文字列は formatPrice で余分な 0 を落として表示する。
export function formatAmount(value: number): string {
  if (!Number.isFinite(value)) return '—';
  return value.toLocaleString('ja-JP', { maximumFractionDigits: 2 });
}

export function formatSignedAmount(value: number): string {
  if (Math.abs(value) < 0.005) return '0';
  const text = formatAmount(Math.abs(value));
  return value > 0 ? `+${text}` : `-${text}`;
}

export function formatPercent(value: number): string {
  if (!Number.isFinite(value)) return '—';
  if (Math.abs(value) < 0.005) return '0.00%';
  const text = `${Math.abs(value).toFixed(2)}%`;
  return value > 0 ? `+${text}` : `-${text}`;
}

export function formatYear(year: number): string {
  return `${year}年`;
}

// formatRemaining は残り時間（秒）を m:ss にする。
export function formatRemaining(seconds: number): string {
  const safe = Math.max(0, Math.ceil(seconds));
  const m = Math.floor(safe / 60);
  const s = safe % 60;
  return `${m}:${String(s).padStart(2, '0')}`;
}

// remainingSeconds は公開済みの日数と速さから残り時間を概算する。
// 標準の速さは 3 秒で 1 営業日（60 日で 3 分）。サーバーも同じ規則で公開日数を
// 決めるため、画面の残り時間もこの式で揃える。
export function remainingSeconds(publishedDays: number, speed: number): number {
  const remainingDays = Math.max(0, REPLAY_INTERVAL_DAYS - publishedDays);
  const safeSpeed = speed > 0 ? speed : 1;
  return (remainingDays * MS_PER_DAY) / 1000 / safeSpeed;
}

// latestClose は公開済みの最後の終値を返す（約定・評価に使う値）。
export function latestClose(candles: ReplayCandle[]): string | null {
  if (candles.length === 0) return null;
  return candles[candles.length - 1].close;
}

// priceDigits は表示に使う小数桁を価格の文字列から決める。
// 4 ペアをコードに書かず、API の値（例: USD/JPY は 3 桁、EUR/USD は 5 桁）から求める。
export function priceDigits(candles: ReplayCandle[]): number {
  let digits = 0;
  for (const candle of candles) {
    for (const value of [candle.open, candle.high, candle.low, candle.close]) {
      const trimmed = formatPrice(value);
      const dot = trimmed.indexOf('.');
      if (dot >= 0) digits = Math.max(digits, Math.min(trimmed.length - dot - 1, 8));
    }
  }
  return digits;
}

export function positionNotional(position: ReplayPosition): number {
  return numberFromDecimal(position.initialMargin) * position.leverage;
}

// positionProfit は公開中の値段での含み損益。design.md §13 の
// 「取引規模 × 価格の変化率」で、買いは上がると得、売りは下がると得。
export function positionProfit(position: ReplayPosition, currentPrice: string): number {
  const entry = numberFromDecimal(position.entryPrice);
  const price = numberFromDecimal(currentPrice);
  if (entry <= 0) return 0;
  const notional = positionNotional(position);
  const rate = position.side === 'short' ? (entry - price) / entry : (price - entry) / entry;
  return notional * rate;
}

export interface ReplaySummary {
  // 未使用資金（API の funds）。
  cash: number;
  // 建玉の証拠金の合計。
  lockedMargin: number;
  // 含み損益の合計。
  unrealized: number;
  // 未使用資金 + 証拠金 + 含み損益。
  equity: number;
}

// summarize は画面に出す「現在の資金・含み損益・合計」を求める。
export function summarize(state: ReplayGameState): ReplaySummary {
  const closes = new Map<number, string>();
  for (const pairCandles of state.candlesByPair) {
    const close = latestClose(pairCandles.candles);
    if (close !== null) closes.set(pairCandles.pairId, close);
  }

  let lockedMargin = 0;
  let unrealized = 0;
  for (const position of state.positions) {
    if (position.status !== 'open') continue;
    lockedMargin += numberFromDecimal(position.margin);
    const close = closes.get(position.pairId);
    if (close !== undefined) unrealized += positionProfit(position, close);
  }

  const cash = numberFromDecimal(state.funds);
  return { cash, lockedMargin, unrealized, equity: cash + lockedMargin + unrealized };
}

// isPositiveInteger は API の「1 以上の整数」を文字列のまま判定する。
// 入力欄が "01" のように先頭に 0 を付けても、値が 1 以上なら受け付ける。
export function isPositiveInteger(text: string): boolean {
  if (!/^\d+$/.test(text)) return false;
  const value = Number(text);
  return Number.isSafeInteger(value) && value >= 1;
}

export function validateReplayOrder(
  margin: string,
  leverage: string,
  maxLeverage: number,
): string | null {
  if (!isPositiveInteger(margin)) {
    return '証拠金は 1 以上の整数で入力してください';
  }
  if (!isPositiveInteger(leverage)) {
    return 'レバレッジは 1 以上の整数で入力してください';
  }
  if (Number(leverage) > maxLeverage) {
    return `レバレッジは ${maxLeverage} 倍以下で入力してください`;
  }
  return null;
}

export interface LeverageUnlock {
  tier: number;
  current: number;
  next: number;
  cost: number;
}

// nextLeverageUnlock は次の段の倍率と費用を返す。
// 1 段で +10 倍、費用は 250 から段ごとに 2 倍（design.md §13）。
export function nextLeverageUnlock(pair: ReplayPair): LeverageUnlock {
  const base = Math.round(numberFromDecimal(pair.baseLeverage));
  const current = Math.round(numberFromDecimal(pair.maxLeverage));
  const tier = Math.max(0, Math.round((current - base) / 10));
  return {
    tier: tier + 1,
    current,
    next: current + 10,
    cost: 250 * 2 ** tier,
  };
}

// ゲスト名の文字数（サーバーは Unicode の文字数で 1〜16 を見る）。
export function guestNameLength(text: string): number {
  return Array.from(text.trim()).length;
}

// formatIntervalRange は区間の正確な日付を「2020 年 3 月 2 日〜5 月 22 日」にする。
// 年をまたぐ場合は終了年も出す。終了前は API が null を返す。
export function formatIntervalRange(start: string | null, end: string | null): string | null {
  const s = parseDate(start);
  const e = parseDate(end);
  if (s === null && e === null) return null;
  if (s !== null && e !== null) {
    if (s.year === e.year) {
      return `${s.year} 年 ${s.month} 月 ${s.day} 日〜${e.month} 月 ${e.day} 日`;
    }
    return `${s.year} 年 ${s.month} 月 ${s.day} 日〜${e.year} 年 ${e.month} 月 ${e.day} 日`;
  }
  const one = (s ?? e)!;
  return `${one.year} 年 ${one.month} 月 ${one.day} 日`;
}

export function formatIntervalSentence(start: string | null, end: string | null): string | null {
  const range = formatIntervalRange(start, end);
  return range === null ? null : `${range}の相場でした`;
}

interface ParsedDate {
  year: number;
  month: number;
  day: number;
}

// parseDate は "YYYY-MM-DD" をタイムゾーン変換なしで分解する。
// new Date("YYYY-MM-DD") は UTC 扱いになり、日本時間で日付がずれることがあるため使わない。
function parseDate(value: string | null): ParsedDate | null {
  if (value === null) return null;
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value);
  if (match === null) return null;
  return { year: Number(match[1]), month: Number(match[2]), day: Number(match[3]) };
}
