import { useEffect, useMemo, useRef } from 'react';
import {
  CandlestickSeries,
  ColorType,
  LineStyle,
  createChart,
  type IChartApi,
  type IPriceLine,
  type ISeriesApi,
  type Time,
  type UTCTimestamp,
} from 'lightweight-charts';

import type { ReplayCandle, ReplayPosition } from '../../api/generated/model';
import { replayDayLabel, replayPricePrecision } from '../../lib/replay';
import styles from '../../pages/ReplayPage.module.css';

interface ReplayChartProps {
  // サーバーが公開した分の日足（開始前 20 日を含む）。
  candles: ReplayCandle[];
  // 選択中のペアの未決済建玉。強制決済ラインを引く。
  positions: ReplayPosition[];
}

// 日付は終了まで伏せるため、通し番号を秒に見立てて時間軸に使う（デモと同じ考え方）。
const DAY_SECONDS = 86_400;

function timeOf(day: number): UTCTimestamp {
  return (day * DAY_SECONDS) as UTCTimestamp;
}

function labelOf(time: Time): string {
  return replayDayLabel(Math.round(Number(time) / DAY_SECONDS));
}

// ReplayChart は過去チャートモードの日足ローソク足チャート。
// 選択中のペアの未決済建玉ごとに、サーバーが返した強制決済ラインを線で表示する。
export default function ReplayChart({ candles, positions }: ReplayChartProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const chartRef = useRef<IChartApi | null>(null);
  const seriesRef = useRef<ISeriesApi<'Candlestick'> | null>(null);
  const priceLinesRef = useRef<IPriceLine[]>([]);

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
      chart.remove();
      chartRef.current = null;
      seriesRef.current = null;
      priceLinesRef.current = [];
    };
  }, []);

  useEffect(() => {
    const chart = chartRef.current;
    const series = seriesRef.current;
    if (!chart || !series) return;

    // 価格は描画座標に変換する必要があるため数値にする。ここでの金額計算はしない
    // （lib/chart.ts と同じ扱い。CLAUDE.md §5.2）。
    series.applyOptions({
      priceFormat: { type: 'price', precision, minMove: 10 ** -precision },
    });
    series.setData(
      candles.map((c) => ({
        time: timeOf(c.day),
        open: Number(c.open),
        high: Number(c.high),
        low: Number(c.low),
        close: Number(c.close),
      })),
    );

    for (const line of priceLinesRef.current) {
      series.removePriceLine(line);
    }
    priceLinesRef.current = positions.map((pos) =>
      series.createPriceLine({
        // 強制決済ラインも描画用の数値にする（金額計算はサーバーの decimal）。
        price: Number(pos.liquidationLine),
        color: '#ff9800',
        lineWidth: 1,
        lineStyle: LineStyle.Dashed,
        axisLabelVisible: true,
        title: `${pos.side === 'short' ? '売り' : '買い'} 強制決済`,
      }),
    );

    chart.timeScale().fitContent();
  }, [candles, positions, precision]);

  return <div ref={containerRef} className={styles.chart} />;
}
