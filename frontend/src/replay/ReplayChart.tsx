import { useEffect, useRef } from 'react';
import {
  CandlestickSeries,
  ColorType,
  LineSeries,
  LineStyle,
  createChart,
  createSeriesMarkers,
  type IChartApi,
  type ISeriesApi,
  type ISeriesMarkersPluginApi,
  type SeriesMarker,
  type Time,
  type UTCTimestamp,
} from 'lightweight-charts';

import type { ReplayCandle, ReplayPosition } from '../api/generated/model';
import { priceDigits } from './replay';
import styles from './ReplayPage.module.css';

interface ReplayChartProps {
  // 公開済みの日足（開始前 20 日 + 公開済みの区間）。
  candles: ReplayCandle[];
  // このペアの未決済建玉。強制決済ラインと約定日の印を描く。
  positions: ReplayPosition[];
}

// 正確な日付は終了まで伏せるため、通し番号を秒に見立てて時間軸に使う。
const DAY_SECONDS = 86_400;

function timeOf(day: number): UTCTimestamp {
  return (day * DAY_SECONDS) as UTCTimestamp;
}

function dayLabel(time: Time): string {
  const day = Math.round(Number(time) / DAY_SECONDS);
  return day < 20 ? '開始前' : `${day - 19}日目`;
}

// ReplayChart は過去チャートモードの日足ローソク足。
// 強制決済ラインは建玉ごとに水平の破線で描き、証拠金の追加で値が変われば動く。
export default function ReplayChart({ candles, positions }: ReplayChartProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const chartRef = useRef<IChartApi | null>(null);
  const seriesRef = useRef<ISeriesApi<'Candlestick'> | null>(null);
  const markersRef = useRef<ISeriesMarkersPluginApi<Time> | null>(null);
  const lineSeriesRef = useRef<ISeriesApi<'Line'>[]>([]);

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
      timeScale: { borderColor: '#e0e0e0', tickMarkFormatter: dayLabel },
      localization: { timeFormatter: dayLabel },
      handleScroll: false,
      handleScale: false,
    });
    const series = chart.addSeries(CandlestickSeries, {
      upColor: '#26a69a',
      downColor: '#ef5350',
      borderVisible: false,
      wickUpColor: '#26a69a',
      wickDownColor: '#ef5350',
    });
    chartRef.current = chart;
    seriesRef.current = series;
    markersRef.current = createSeriesMarkers(series);
    return () => {
      chart.remove();
      chartRef.current = null;
      seriesRef.current = null;
      markersRef.current = null;
      lineSeriesRef.current = [];
    };
  }, []);

  useEffect(() => {
    const chart = chartRef.current;
    const series = seriesRef.current;
    const markers = markersRef.current;
    if (!chart || !series || !markers) return;

    const digits = priceDigits(candles);
    series.applyOptions({
      priceFormat: { type: 'price', precision: digits, minMove: 10 ** -digits },
    });
    series.setData(
      candles.map((candle) => ({
        time: timeOf(candle.day),
        open: Number(candle.open),
        high: Number(candle.high),
        low: Number(candle.low),
        close: Number(candle.close),
      })),
    );

    const marks: SeriesMarker<Time>[] = positions
      .filter((position) => position.status === 'open')
      .map((position) => ({
        time: timeOf(position.entryDay),
        position: position.side === 'long' ? 'belowBar' : 'aboveBar',
        shape: position.side === 'long' ? 'arrowUp' : 'arrowDown',
        color: position.side === 'long' ? '#26a69a' : '#ef5350',
        text: position.side === 'long' ? '買い' : '売り',
      }));
    marks.sort((a, b) => Number(a.time) - Number(b.time));
    markers.setMarkers(marks);

    // 強制決済ラインは建玉の数だけ引き直す（証拠金の追加で値が動く）。
    for (const lineSeries of lineSeriesRef.current) {
      chart.removeSeries(lineSeries);
    }
    lineSeriesRef.current = [];
    for (const position of positions) {
      if (position.status !== 'open') continue;
      const lineSeries = chart.addSeries(LineSeries, {
        color: position.side === 'long' ? '#e08e0b' : '#8e44ad',
        lineWidth: 1,
        lineStyle: LineStyle.Dashed,
        priceLineVisible: false,
        lastValueVisible: false,
        crosshairMarkerVisible: false,
      });
      const value = Number(position.liquidationLine);
      lineSeries.setData(candles.map((candle) => ({ time: timeOf(candle.day), value })));
      lineSeriesRef.current.push(lineSeries);
    }

    chart.timeScale().fitContent();
  }, [candles, positions]);

  return <div ref={containerRef} className={styles.chart} />;
}
