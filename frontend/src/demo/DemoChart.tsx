import { useEffect, useRef } from 'react';
import {
  ColorType,
  LineSeries,
  createChart,
  createSeriesMarkers,
  type IChartApi,
  type ISeriesApi,
  type ISeriesMarkersPluginApi,
  type SeriesMarker,
  type Time,
  type UTCTimestamp,
} from 'lightweight-charts';

import type { Position } from './demoGame';
import { HISTORY_DAYS } from './sampleData';
import styles from './DemoPage.module.css';

interface DemoChartProps {
  // 開始前の終値（古い順）。
  history: number[];
  // 0 日目から今日までの終値（未来の値は渡さない）。
  prices: number[];
  digits: number;
  // このペアの保有。取得した日に印を付ける。
  positions: Position[];
}

// 日付は伏せたまま「○日目」で見せるため、日の番号を秒に見立てて時間軸に使う。
// 開始前の日が 0 から並び、その後ろに 0 日目以降が続く。
const DAY_SECONDS = 86_400;

function dayLabel(time: Time): string {
  const day = Math.round(Number(time) / DAY_SECONDS) - HISTORY_DAYS;
  return day < 0 ? '開始前' : `${day + 1}日目`;
}

function timeOf(index: number): UTCTimestamp {
  return (index * DAY_SECONDS) as UTCTimestamp;
}

// DemoChart はデモ用の折れ線チャート。H.10 は 1 営業日 1 レートなので、
// ローソク足ではなく折れ線で描く（#111 第 2 段階の論点。ここは折れ線で仮置き）。
export default function DemoChart({ history, prices, digits, positions }: DemoChartProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const chartRef = useRef<IChartApi | null>(null);
  const seriesRef = useRef<ISeriesApi<'Line'> | null>(null);
  const markersRef = useRef<ISeriesMarkersPluginApi<Time> | null>(null);

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
    const series = chart.addSeries(LineSeries, { lineWidth: 2 });
    chartRef.current = chart;
    seriesRef.current = series;
    markersRef.current = createSeriesMarkers(series);
    return () => {
      chart.remove();
      chartRef.current = null;
      seriesRef.current = null;
      markersRef.current = null;
    };
  }, []);

  useEffect(() => {
    const chart = chartRef.current;
    const series = seriesRef.current;
    const markers = markersRef.current;
    if (!chart || !series || !markers) return;

    series.applyOptions({
      priceFormat: { type: 'price', precision: digits, minMove: 10 ** -digits },
    });
    // 開始前は灰色、ゲーム中は青で描き分ける。
    series.setData([
      ...history.map((value, i) => ({ time: timeOf(i), value, color: '#9e9e9e' })),
      ...prices.map((value, day) => ({ time: timeOf(HISTORY_DAYS + day), value, color: '#1e6fd9' })),
    ]);
    const marks: SeriesMarker<Time>[] = positions
      .filter((p) => p.entryDay < prices.length)
      .map((p) => ({
        time: timeOf(HISTORY_DAYS + p.entryDay),
        position: p.side === 'buy' ? 'belowBar' : 'aboveBar',
        shape: p.side === 'buy' ? 'arrowUp' : 'arrowDown',
        color: p.side === 'buy' ? '#26a69a' : '#ef5350',
        text: p.side === 'buy' ? '買い' : '売り',
      }));
    marks.sort((a, b) => Number(a.time) - Number(b.time));
    markers.setMarkers(marks);
    chart.timeScale().fitContent();
  }, [history, prices, digits, positions]);

  return <div ref={containerRef} className={styles.chart} />;
}
