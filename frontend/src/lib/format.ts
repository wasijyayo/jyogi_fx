import type { MarketStatus } from '../api/generated/model';

// 価格・前日比・取引時間の表示用フォーマッタ。
// 金額・価格は decimal の文字列で受け取り、表示の整形だけをここで行う
// （計算はサーバー側。CLAUDE.md §5.2）。

// formatPrice は decimal 文字列の末尾の余分な 0（と小数点）を落とす。
// 例: "100.00000000" → "100"、 "100.42000000" → "100.42"、 "1000" → "1000"。
export function formatPrice(value: string): string {
  if (!value.includes('.')) return value;
  return value.replace(/\.?0+$/, '');
}

// formatChangePercent は前日比（%）の文字列に符号と % を付けるだけにする。
// 値はサーバー側で小数2桁に丸めた文字列（例: "1.23" / "-0.50"）なので、
// ここで数値に変換しない（CLAUDE.md §5.2 の float 禁止の趣旨）。
// null（基準無し）は emダッシュにする。
export function formatChangePercent(value: string | null): string {
  if (value == null) return '—';
  return value.startsWith('-') ? `${value}%` : `+${value}%`;
}

// formatMarketStatus は取引時間の状態を表示用の文言にする。
// 開催中なら残り時間、時間外なら次の開始時刻、常時開催（GAME_ALWAYS_OPEN）なら
// その旨を返す。
export function formatMarketStatus(status: MarketStatus): string {
  if (status.alwaysOpen) return '開催中（常時開催）';
  if (status.open && status.remainingMinutes != null) {
    return `開催中（残り${status.remainingMinutes}分）`;
  }
  if (status.nextStartAt != null) {
    return `次回 ${formatJstTime(status.nextStartAt)} 開始`;
  }
  return '—';
}

// formatJstTime は RFC3339 の時刻文字列を JST の「HH:mm」にする。
// ブラウザのタイムゾーンに依存させず、ゲームの基準タイムゾーン（JST）で表示する。
export function formatJstTime(iso: string): string {
  return new Intl.DateTimeFormat('ja-JP', {
    timeZone: 'Asia/Tokyo',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  }).format(new Date(iso));
}

// formatSignedPrice は損益（decimal文字列）に符号を付けて返す。formatPrice と同じく
// 末尾の余分な0を落とし、正の値には「+」を付ける（含み損益・確定損益の表示用）。
// ゼロは符号を付けず "0" にする（+0 / -0 を出さない）。
export function formatSignedPrice(value: string): string {
  const trimmed = formatPrice(value);
  if (trimmed === '0' || trimmed === '-0') return '0';
  return value.startsWith('-') ? trimmed : `+${trimmed}`;
}

// formatSide は売買方向（positions.side / trades.side の "long"/"short"）を
// 表示用の日本語にする（design.md §8: side は long / short）。
export function formatSide(side: string): string {
  return side === 'short' ? '売り' : '買い';
}
