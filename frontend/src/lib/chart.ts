import type { Candle } from '../api/generated/model';

// チャート描画用の純粋関数群。ここでは価格の decimal 文字列を数値へ変換して
// lightweight-charts が描ける形（Unix秒 + 数値）に組み立てる。
//
// CLAUDE.md §5.2 の「金額・価格に float を使わない」は、残高・損益などの金額計算を
// サーバー側の decimal で行うという規約であり、チャートの「描画座標」への変換は
// その対象外（軽量チャートは数値でプロットする必要がある）。表示専用の変換であり、
// ここで金額の集計・合算は一切行わない。

// ONE_MINUTE_SECONDS は1分足のステップ幅（秒）。
export const ONE_MINUTE_SECONDS = 60;

// OPENING_CANDLE_COLOR は寄り付き足（is_opening = TRUE）の色。実価格の赤/緑（up/down）と
// 区別できる橙にする（design.md §2.8「is_opening = TRUE の行は色を変える」）。
export const OPENING_CANDLE_COLOR = '#ff9800';

// candleBodyColor は足の色を決める純粋関数。寄り付き足なら OPENING_CANDLE_COLOR、
// 通常・補間された足（isOpening = false）なら undefined を返す（undefined の場合は
// シリーズ既定の up/down 色を使う）。
export function candleBodyColor(isOpening: boolean): string | undefined {
  return isOpening ? OPENING_CANDLE_COLOR : undefined;
}

// ChartPoint は lightweight-charts に渡せる数値表現のローソク足1本。
export interface ChartPoint {
  /** Unix秒（UTCTimestamp）。 */
  time: number;
  open: number;
  high: number;
  low: number;
  close: number;
  basePrice: number;
  isOpening: boolean;
}

// toChartPoints は API の Candle[]（decimal文字列）を描画用の数値（ChartPoint[]）に
// 変換する。入力はサーバーが tick_index 昇順（時系列順）で返すため、その順を保つ。
export function toChartPoints(candles: Candle[]): ChartPoint[] {
  return candles.map((c) => ({
    time: isoToUnixSeconds(c.time),
    open: Number(c.open),
    high: Number(c.high),
    low: Number(c.low),
    close: Number(c.close),
    basePrice: Number(c.basePrice),
    isOpening: c.isOpening,
  }));
}

// isoToUnixSeconds は RFC3339 の文字列を Unix秒（UTC 基準）に変換する。
// 無効な文字列は NaN になる（呼び出し側ではサーバー由来の正しい RFC3339 を前提）。
export function isoToUnixSeconds(iso: string): number {
  return Math.floor(Date.parse(iso) / 1000);
}

// interpolateMissingCandles は tick の欠損（Cloud Scheduler の失敗など。design.md §3.6）を
// 線形補間で埋める純粋関数。
//
//   - 入力 points は time 昇順を前提とする。
//   - 隣接する足の間隔が stepSeconds + 許容差（stepSeconds/2）を超えている場合、
//     その間を stepSeconds 刻みの平坦な足（o=h=l=c=補間値）で埋める。
//   - 補間値は前後の close（および basePrice）の線形補間。寄り付き扱いにはしない
//     （isOpening = false）。
//
// 1 分の穴はチャート上ほぼ見えない（design.md §3.6）ため、複雑な穴埋めはせず
// 線形補間だけに留める。
export function interpolateMissingCandles(
  points: ChartPoint[],
  stepSeconds = ONE_MINUTE_SECONDS,
): ChartPoint[] {
  if (points.length < 2) return points;

  const tolerance = stepSeconds / 2;
  const out: ChartPoint[] = [points[0]];

  for (let i = 1; i < points.length; i++) {
    const prev = points[i - 1];
    const cur = points[i];
    const gap = cur.time - prev.time;

    if (gap <= stepSeconds + tolerance) {
      out.push(cur);
      continue;
    }

    // gap を stepSeconds で埋める。cur 自身はこの後の out.push(cur) で追加される。
    const missing = Math.round(gap / stepSeconds) - 1;
    for (let k = 1; k <= missing; k++) {
      const t = prev.time + stepSeconds * k;
      const ratio = (t - prev.time) / (cur.time - prev.time);
      const close = lerp(prev.close, cur.close, ratio);
      const basePrice = lerp(prev.basePrice, cur.basePrice, ratio);
      out.push({
        time: t,
        open: close,
        high: close,
        low: close,
        close,
        basePrice,
        isOpening: false,
      });
    }
    out.push(cur);
  }

  return out;
}

// lerp は a から b への線形補間（ratio は 0〜1）。
function lerp(a: number, b: number, ratio: number): number {
  return a + (b - a) * ratio;
}

// formatEventLabel はイベント種別と magnitude（decimal文字列）から注釈の文言を作る
// 純粋関数（design.md §3.1「ここで暴落」の注釈）。shock の方向は magnitude の符号で決める。
export function formatEventLabel(type: string, magnitude: string): string {
  switch (type) {
    case 'shock':
      return magnitude.startsWith('-') ? '暴落' : '急騰';
    case 'vol_up':
      return 'ボラ上昇';
    case 'liquidity_drain':
      return '流動性枯渇';
    default:
      return type;
  }
}
