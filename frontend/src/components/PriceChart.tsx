import { useEffect, useMemo, useRef } from 'react';
import {
  CandlestickSeries,
  ColorType,
  CrosshairMode,
  LineSeries,
  LineStyle,
  createChart,
  createSeriesMarkers,
  type CandlestickData,
  type IChartApi,
  type ISeriesApi,
  type ISeriesMarkersPluginApi,
  type LineData,
  type SeriesMarker,
  type Time,
  type UTCTimestamp,
} from 'lightweight-charts';

import type { Candle, ChartEvent, GetCandlesResolution } from '../api/generated/model';
import {
  ONE_MINUTE_SECONDS,
  candleBodyColor,
  formatEventLabel,
  interpolateMissingCandles,
  isoToUnixSeconds,
  toChartPoints,
  type ChartPoint,
} from '../lib/chart';
import styles from './PriceChart.module.css';

interface PriceChartProps {
  candles: Candle[];
  events: ChartEvent[];
  resolution: GetCandlesResolution;
}

// PriceChart はローソク足チャートの「描画」だけを担うコンポーネント。
// データの取得（API 呼び出し）はしない。後で #105（過去チャートモード）が再利用する
// ため、足の配列などを props で受ける形に分離している（issue #45 指示）。
//
// 描くもの（design.md §3.4/§3.5）:
//   - 実価格のローソク足（太い実体。1分足はヒゲなし、5分足でヒゲが出る）
//   - base_price（人がいなかった場合の価格）を薄い線で重ねる → 乖離が「人間が動かした分」
//   - 寄り付き（is_opening）は足の色を変えて区別（＋「寄り付き」マーカー）、発火済みイベントを注釈
export default function PriceChart({ candles, events, resolution }: PriceChartProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const chartRef = useRef<IChartApi | null>(null);
  const candleSeriesRef = useRef<ISeriesApi<'Candlestick'> | null>(null);
  const baseLineRef = useRef<ISeriesApi<'Line'> | null>(null);
  const markersRef = useRef<ISeriesMarkersPluginApi<Time> | null>(null);

  // API の decimal 文字列を数値へ変換し、1分足の場合は欠損 tick を線形補間する
  // （design.md §3.6）。5分足は集約済みなので補間しない。
  const points = useMemo(() => {
    const p = toChartPoints(candles);
    return resolution === '1m' ? interpolateMissingCandles(p, ONE_MINUTE_SECONDS) : p;
  }, [candles, resolution]);

  // チャートとシリーズの生成はマウント時に1回だけ。アンマウント時に remove する。
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
      timeScale: { borderColor: '#e0e0e0', timeVisible: true, secondsVisible: false },
      crosshair: { mode: CrosshairMode.Normal },
    });

    const candleSeries = chart.addSeries(CandlestickSeries, {
      upColor: '#26a69a',
      downColor: '#ef5350',
      borderVisible: false,
      wickUpColor: '#26a69a',
      wickDownColor: '#ef5350',
    });

    // base_price は「人がいなかった場合の価格」の薄い線（design.md §3.4）。
    const baseLine = chart.addSeries(LineSeries, {
      color: '#9e9e9e',
      lineWidth: 1,
      lineStyle: LineStyle.Dashed,
      priceLineVisible: false,
      lastValueVisible: false,
      crosshairMarkerVisible: false,
    });

    // 寄り付き・イベントの注釈マーカー（v5 は createSeriesMarkers プラグイン）。
    const markersPlugin = createSeriesMarkers(candleSeries);

    chartRef.current = chart;
    candleSeriesRef.current = candleSeries;
    baseLineRef.current = baseLine;
    markersRef.current = markersPlugin;

    return () => {
      chart.remove();
      chartRef.current = null;
      candleSeriesRef.current = null;
      baseLineRef.current = null;
      markersRef.current = null;
    };
  }, []);

  // データ・マーカーの更新（ポーリングで candles/events が変わるたびに反映）。
  useEffect(() => {
    const chart = chartRef.current;
    const candleSeries = candleSeriesRef.current;
    const baseLine = baseLineRef.current;
    const markers = markersRef.current;
    if (!chart || !candleSeries || !baseLine || !markers) return;

    candleSeries.setData(points.map(toCandlestickData));
    baseLine.setData(points.map(toLineData));
    markers.setMarkers(buildMarkers(points, events));
    chart.timeScale().fitContent();
  }, [points, events]);

  return (
    <div className={styles.wrapper}>
      {candles.length === 0 && (
        <p className={styles.empty}>この日の価格データはまだありません</p>
      )}
      <div ref={containerRef} className={styles.chart} />
    </div>
  );
}

function toCandlestickData(p: ChartPoint): CandlestickData<Time> {
  // 寄り付き足（is_opening）は 1 本ごとの色で区別する（design.md §2.8）。補間された足は
  // isOpening = false なので candleBodyColor が undefined を返し、通常の up/down 色になる。
  const color = candleBodyColor(p.isOpening);
  return {
    time: p.time as UTCTimestamp,
    open: p.open,
    high: p.high,
    low: p.low,
    close: p.close,
    ...(color === undefined ? {} : { color, borderColor: color, wickColor: color }),
  };
}

function toLineData(p: ChartPoint): LineData<Time> {
  return { time: p.time as UTCTimestamp, value: p.basePrice };
}

// buildMarkers は寄り付きキャンドルと発火済みイベントの注釈マーカーを組み立てる。
function buildMarkers(points: ChartPoint[], events: ChartEvent[]): SeriesMarker<Time>[] {
  const markers: SeriesMarker<Time>[] = [];

  for (const p of points) {
    if (!p.isOpening) continue;
    markers.push({
      time: p.time as UTCTimestamp,
      position: 'belowBar',
      shape: 'arrowDown',
      color: '#607d8b',
      text: '寄り付き',
    });
  }

  for (const e of events) {
    markers.push({
      time: isoToUnixSeconds(e.time) as UTCTimestamp,
      position: 'aboveBar',
      shape: 'arrowUp',
      color: eventColor(e.type),
      text: formatEventLabel(e.type, e.magnitude),
    });
  }

  return markers.sort(byTime);
}

function byTime(a: SeriesMarker<Time>, b: SeriesMarker<Time>): number {
  const at = typeof a.time === 'number' ? a.time : 0;
  const bt = typeof b.time === 'number' ? b.time : 0;
  return at - bt;
}

function eventColor(type: string): string {
  switch (type) {
    case 'shock':
      return '#e53935';
    case 'vol_up':
      return '#fb8c00';
    case 'liquidity_drain':
      return '#8e24aa';
    default:
      return '#546e7a';
  }
}
