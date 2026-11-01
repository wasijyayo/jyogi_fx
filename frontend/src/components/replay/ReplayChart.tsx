import { useEffect, useMemo, useRef } from 'react';
import {
  CandlestickSeries,
  ColorType,
  LineStyle,
  createChart,
  type CandlestickData,
  type IChartApi,
  type IPriceLine,
  type ISeriesApi,
  type Time,
  type UTCTimestamp,
} from 'lightweight-charts';

import type { ReplayCandle, ReplayPosition } from '../../api/generated/model';
import { REPLAY_PRE_DAYS, replayDayLabel, replayPricePrecision } from '../../lib/replay';
import styles from '../../pages/ReplayPage.module.css';

interface ReplayChartProps {
  // 選択中の通貨ペア。切り替えたらチャートを作り直す。
  pairId: number;
  // サーバーが公開した分の日足（開始前 20 日を含む）。
  candles: ReplayCandle[];
  // 選択中のペアの未決済建玉。強制決済ラインを引く。
  positions: ReplayPosition[];
}

// 開始前 20 日は灰色系にして、区間の日足と描き分ける（デモの DemoChart と同じ考え方）。
const PRE_OPEN_BODY_COLOR = '#b0bec5';
const PRE_OPEN_WICK_COLOR = '#90a4ae';

// 新しい日足を伸ばす演出の長さ（#129 REPLAY-12）。標準の速さでは 1 日 3 秒、
// ×3 でも 1 秒なので、0.6 秒なら次の足と重ならない。
const CANDLE_ANIMATION_MS = 600;

// 日付は終了まで伏せるため、通し番号を秒に見立てて時間軸に使う（デモと同じ考え方）。
const DAY_SECONDS = 86_400;

function timeOf(day: number): UTCTimestamp {
  return (day * DAY_SECONDS) as UTCTimestamp;
}

function labelOf(time: Time): string {
  return replayDayLabel(Math.round(Number(time) / DAY_SECONDS));
}

// toCandleData は API の日足を lightweight-charts の描画データにする。
// 価格は描画座標に変換する必要があるため数値にする。ここでの金額計算はしない
// （lib/chart.ts と同じ扱い。CLAUDE.md §5.2）。
function toCandleData(candle: ReplayCandle): CandlestickData<Time> {
  return {
    time: timeOf(candle.day),
    open: Number(candle.open),
    high: Number(candle.high),
    low: Number(candle.low),
    close: Number(candle.close),
    ...(candle.day < REPLAY_PRE_DAYS
      ? {
          color: PRE_OPEN_BODY_COLOR,
          borderColor: PRE_OPEN_WICK_COLOR,
          wickColor: PRE_OPEN_WICK_COLOR,
        }
      : {}),
  };
}

// prefersReducedMotion は OS の「視差効果を減らす」設定が有効かを返す（#126・#129）。
function prefersReducedMotion(): boolean {
  return (
    typeof window !== 'undefined' &&
    typeof window.matchMedia === 'function' &&
    window.matchMedia('(prefers-reduced-motion: reduce)').matches
  );
}

// animateCandle は新しい日足を約 0.6 秒かけて伸ばす（#129 REPLAY-12）。
// 1 日の中の値動きの順序はデータに無いため、これは見た目だけの演出。
// 始値から終値へ実体を伸ばし、後半でヒゲを出して確定する。完了時に最終値を書き、
// onDone を呼ぶ。rafRef には現在の requestAnimationFrame の ID を入れる。
function animateCandle(
  series: ISeriesApi<'Candlestick'>,
  rafRef: { current: number | null },
  candle: ReplayCandle,
  onDone: () => void,
): void {
  const open = Number(candle.open);
  const close = Number(candle.close);
  const high = Number(candle.high);
  const low = Number(candle.low);
  const bodyHigh = Math.max(open, close);
  const bodyLow = Math.min(open, close);
  const time = timeOf(candle.day);
  const start = performance.now();

  function step(now: number) {
    const progress = Math.min(1, (now - start) / CANDLE_ANIMATION_MS);
    const eased = 1 - (1 - progress) ** 3;
    const currentClose = open + (close - open) * eased;
    // ヒゲは後半（45% 以降）で伸ばすと「出て確定する」ように見える。
    const wickProgress = Math.max(0, (progress - 0.45) / 0.55);
    const wickEased = 1 - (1 - wickProgress) ** 3;
    const currentHigh = Math.max(open, currentClose) + (high - bodyHigh) * wickEased;
    const currentLow = Math.min(open, currentClose) - (bodyLow - low) * wickEased;

    series.update({
      time,
      open,
      high: currentHigh,
      low: currentLow,
      close: currentClose,
    });

    if (progress < 1) {
      rafRef.current = requestAnimationFrame(step);
      return;
    }
    series.update(toCandleData(candle));
    rafRef.current = null;
    onDone();
  }

  rafRef.current = requestAnimationFrame(step);
}

// ReplayChart は過去チャートモードの日足ローソク足チャート。
// 選択中のペアの未決済建玉ごとに、サーバーが返した強制決済ラインを線で表示する。
export default function ReplayChart({ pairId, candles, positions }: ReplayChartProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const chartRef = useRef<IChartApi | null>(null);
  const seriesRef = useRef<ISeriesApi<'Candlestick'> | null>(null);
  const priceLinesRef = useRef<IPriceLine[]>([]);
  // displayedRef は「最終値で描き終えた日足の本数」。演出中の 1 本は含めない。
  const displayedRef = useRef(0);
  const finalRef = useRef<ReplayCandle[]>([]);
  const pairRef = useRef(pairId);
  const rafRef = useRef<number | null>(null);

  const precision = useMemo(() => replayPricePrecision(candles), [candles]);

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;
    const chart = createChart(container, {
      autoSize: true,
      layout: {
        background: { type: ColorType.Solid, color: '#ffffff' },
        textColor: '#333333',
      },
      grid: {
        vertLines: { color: '#f0f0f0' },
        horzLines: { color: '#f0f0f0' },
      },
      rightPriceScale: { borderColor: '#e0e0e0' },
      timeScale: {
        borderColor: '#e0e0e0',
        tickMarkFormatter: labelOf,
      },
      localization: { timeFormatter: labelOf },
      handleScroll: false,
      handleScale: false,
    });
    const series = chart.addSeries(CandlestickSeries, {});
    chartRef.current = chart;
    seriesRef.current = series;
    priceLinesRef.current = [];
    return () => {
      if (rafRef.current != null) {
        cancelAnimationFrame(rafRef.current);
        rafRef.current = null;
      }
      chart.remove();
      chartRef.current = null;
      seriesRef.current = null;
      priceLinesRef.current = [];
      displayedRef.current = 0;
      finalRef.current = [];
    };
  }, []);

  useEffect(() => {
    const chart = chartRef.current;
    const series = seriesRef.current;
    if (!chart || !series) return;

    series.applyOptions({
      priceFormat: { type: 'price', precision, minMove: 10 ** -precision },
    });

    // 前回の演出が残っていれば止めて、前回の日足を最終値で確定させる。
    // こうすると、速さを上げて取得が間に合わなくても演出同士が重ならない。
    if (rafRef.current != null) {
      cancelAnimationFrame(rafRef.current);
      rafRef.current = null;
    }
    const previous = finalRef.current;
    if (displayedRef.current < previous.length) {
      for (let i = displayedRef.current; i < previous.length; i += 1) {
        series.update(toCandleData(previous[i]));
      }
      displayedRef.current = previous.length;
    }

    const reset =
      pairRef.current !== pairId ||
      candles.length < displayedRef.current ||
      displayedRef.current === 0;
    pairRef.current = pairId;
    finalRef.current = candles;

    // ペアの切り替え・初回はまとめて描く（開始前の 20 日は演出しない）。
    if (reset || candles.length <= displayedRef.current) {
      if (reset) {
        series.setData(candles.map(toCandleData));
        displayedRef.current = candles.length;
        chart.timeScale().fitContent();
      }
      return;
    }

    // 動きを減らす設定なら演出せずに確定させる。
    if (prefersReducedMotion()) {
      for (let i = displayedRef.current; i < candles.length; i += 1) {
        series.update(toCandleData(candles[i]));
      }
      displayedRef.current = candles.length;
      chart.timeScale().fitContent();
      return;
    }

    // 一度に複数の日が増えたとき（タブが裏にあった等）は、最後の 1 本だけを演出し、
    // それ以前は最終値で確定させる。演出を並べないので次の取得にも間に合う。
    const lastIndex = candles.length - 1;
    for (let i = displayedRef.current; i < lastIndex; i += 1) {
      series.update(toCandleData(candles[i]));
    }
    displayedRef.current = lastIndex;

    const target = candles[lastIndex];
    // 演出の 1 フレーム目を待たずに足の位置を出し、fitContent で画面内に入れる。
    const open = Number(target.open);
    series.update({ time: timeOf(target.day), open, high: open, low: open, close: open });
    animateCandle(series, rafRef, target, () => {
      displayedRef.current = lastIndex + 1;
      chart.timeScale().fitContent();
    });
    // 演出中も新しい足の位置が見えるように、開始時点で全体を合わせる。
    chart.timeScale().fitContent();
  }, [candles, pairId, precision]);

  useEffect(() => {
    const series = seriesRef.current;
    if (!series) return;

    for (const line of priceLinesRef.current) {
      series.removePriceLine(line);
    }
    // 建値ラインと強制決済ラインを色・線種・ラベルで区別する（#138 REPLAY-13）。
    // 建値は買い・売りの両方を出し、強制決済ラインは従来どおり残す。
    const lines: IPriceLine[] = [];
    for (const pos of positions) {
      const side = pos.side === 'short' ? '売り' : '買い';
      lines.push(
        series.createPriceLine({
          // 描画用の数値にする（金額計算はサーバーの decimal。CLAUDE.md §5.2）。
          price: Number(pos.entryPrice),
          color: '#1e6fd9',
          lineWidth: 1,
          lineStyle: LineStyle.Dotted,
          axisLabelVisible: true,
          title: `${side} 建値`,
        }),
        series.createPriceLine({
          price: Number(pos.liquidationLine),
          color: '#ff9800',
          lineWidth: 1,
          lineStyle: LineStyle.Dashed,
          axisLabelVisible: true,
          title: `${side} 強制決済`,
        }),
      );
    }
    priceLinesRef.current = lines;
  }, [positions]);

  return <div ref={containerRef} className={styles.chart} />;
}
