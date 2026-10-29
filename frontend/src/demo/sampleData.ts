// デモ用モック（#111）のサンプル相場。
//
// **実際の過去相場ではない。** 乱数で作った架空の値動きで、発表で画面の雰囲気を
// 見せるためだけに使う。実データ（FRB H.10）の取り込みは #102 で別に行う。
// 画面にも必ず「デモ用サンプル」と表示すること（#111 の要件）。
//
// 価格の乱数は (seed, ペア, 日) の純粋関数にしている（CLAUDE.md §5.7 と同じ考え方）。
// 同じ seed なら何度リセットしても同じ値動きになり、テストもしやすい。

export interface DemoPair {
  // 表示名。H.10 の向きに合わせ、「1 単位目の通貨 = ○ 2 単位目の通貨」で読む。
  symbol: string;
  name: string;
  // サンプル相場の始値の目安と、1 日あたりの値動きの大きさ（比率）。
  startPrice: number;
  dailyVolatility: number;
  // 表示する小数の桁数。
  digits: number;
}

// モックで見せる通貨ペア。一覧は #111 の第 2 段階で決める（ここは仮置き）。
export const DEMO_PAIRS: readonly DemoPair[] = [
  { symbol: 'USD/JPY', name: '米ドル / 円', startPrice: 110, dailyVolatility: 0.006, digits: 3 },
  { symbol: 'EUR/USD', name: 'ユーロ / 米ドル', startPrice: 1.15, dailyVolatility: 0.006, digits: 5 },
  { symbol: 'GBP/USD', name: '英ポンド / 米ドル', startPrice: 1.35, dailyVolatility: 0.007, digits: 5 },
  { symbol: 'AUD/USD', name: '豪ドル / 米ドル', startPrice: 0.75, dailyVolatility: 0.008, digits: 5 },
];

// 1 ゲームの日数（営業日）。design.md §13 の「60 営業日を 3 分」に合わせる。
export const DEMO_DAYS = 60;

// 開始前に見せておく過去の日数。最初から値動きの流れが分かり、予想の材料になる。
export const HISTORY_DAYS = 20;

// 年代表示の候補。表示の粒度（年／年月／日付）は #111 の第 2 段階で決める（ここは「年」で仮置き）。
const FIRST_YEAR = 2000;
const LAST_YEAR = 2024;

export interface DemoScenario {
  seed: number;
  year: number;
  // prices[ペアの添字][日] = その日の終値（0 日目 〜 DEMO_DAYS - 1 日目）。
  prices: number[][];
  // history[ペアの添字][i] = 開始前の終値（古い順。HISTORY_DAYS 日分）。
  history: number[][];
}

// hash32 は整数の組から 32 ビットの擬似乱数を作る（逐次生成ではなく純粋関数）。
function hash32(...values: number[]): number {
  let h = 0x9e3779b9;
  for (const v of values) {
    h = Math.imul(h ^ (v | 0), 0x85ebca6b);
    h ^= h >>> 13;
    h = Math.imul(h, 0xc2b2ae35);
    h ^= h >>> 16;
  }
  return h >>> 0;
}

// unit は [0, 1) の一様乱数を返す。
function unit(...values: number[]): number {
  return hash32(...values) / 0x1_0000_0000;
}

// gaussian は平均 0・分散 1 に近い乱数を返す（一様乱数 4 個の和。モックには十分）。
function gaussian(seed: number, pair: number, day: number): number {
  let sum = 0;
  for (let k = 0; k < 4; k++) sum += unit(seed, pair, day, k);
  return (sum - 2) * Math.sqrt(3);
}

// createScenario は seed から 1 ゲーム分のサンプル相場を作る。
export function createScenario(seed: number): DemoScenario {
  const year = FIRST_YEAR + (hash32(seed, 0xfeed) % (LAST_YEAR - FIRST_YEAR + 1));
  const series = DEMO_PAIRS.map((pair, i) => {
    // 始値も少しずらし、シナリオごとに見た目を変える。
    let price = pair.startPrice * (0.9 + 0.2 * unit(seed, i, -1));
    const values: number[] = [];
    for (let k = 0; k < HISTORY_DAYS + DEMO_DAYS; k++) {
      if (k > 0) price *= Math.exp(pair.dailyVolatility * gaussian(seed, i, k));
      values.push(price);
    }
    return values;
  });
  return {
    seed,
    year,
    prices: series.map((values) => values.slice(HISTORY_DAYS)),
    history: series.map((values) => values.slice(0, HISTORY_DAYS)),
  };
}

// randomSeed は「やり直す」たびに別の値動きにするための seed を作る。
export function randomSeed(): number {
  return Math.floor(Math.random() * 0x7fff_ffff);
}
