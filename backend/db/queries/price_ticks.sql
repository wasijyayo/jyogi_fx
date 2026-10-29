-- name: UpsertPriceTick :exec
-- docs/design.md §8「UPSERT（冪等性の確保）」。tickが二重に走っても壊れず、
-- high/lowは正しく広がる方に更新される（CLAUDE.md §5.5）。
INSERT INTO price_ticks (
    currency_id, session_id, tick_index, ticked_at,
    base_price, pressure, net_volume,
    open, high, low, close, is_opening
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
ON CONFLICT (currency_id, tick_index) DO UPDATE SET
    high  = GREATEST(price_ticks.high, EXCLUDED.high),
    low   = LEAST(price_ticks.low, EXCLUDED.low),
    close = EXCLUDED.close,
    net_volume = EXCLUDED.net_volume,
    base_price = EXCLUDED.base_price,
    pressure   = EXCLUDED.pressure;

-- name: GetLastPriceTick :one
-- 寄り付きキャンドルの open に使う「前セッション最終tickのclose」を取得する
-- （確定 #19 → design.md §2.8）。行が無ければ呼び出し側で initial_price
-- （currencies.base_price）にフォールバックする。
SELECT * FROM price_ticks
WHERE currency_id = $1
ORDER BY tick_index DESC
LIMIT 1;

-- name: ListRecentPriceTicks :many
-- /price（#42 CMD-2）のスパークライン用に直近Ntick分のcloseを取得する
-- （design.md §6.4「直近8本のキャンドルのcloseを8段階に写像する」）。
-- tick_index降順で返すため、呼び出し側で時系列順に並べ替えること。
SELECT * FROM price_ticks
WHERE currency_id = $1
ORDER BY tick_index DESC
LIMIT $2;

-- name: GetOpeningPriceTick :one
-- 日次まとめ（design.md §6.9、#44 NOTIFY-2）の「本日の最大変動通貨」算出用に、
-- そのセッションの寄り付きキャンドル（本日の始値）を取得する。
SELECT * FROM price_ticks
WHERE session_id = $1 AND currency_id = $2 AND is_opening = TRUE;

-- name: ListPriceTicksBySession :many
-- チャート（#45 WEB-1）の1分足用に、そのセッションの price_ticks を取得する
-- （design.md §8「過去チャートの取得」ListPriceTicks そのもの。再現ロジック不要で
-- SELECT するだけ）。tick_index 昇順（時系列順）で返す。
SELECT * FROM price_ticks
WHERE currency_id = $1 AND session_id = $2
ORDER BY tick_index;

-- name: ListFiveMinCandlesBySession :many
-- チャート（#45 WEB-1）の5分足への集約（design.md §2.8 の集約クエリ）。
-- bucket = tick_index / 5 で5tickずつまとめる。OHLC は §2.8 の定義どおり
--   open  = バケット内最古 tick の open
--   high  = MAX(high) / low = MIN(low)
--   close = バケット内最新 tick の close
-- 5分足ではヒゲが生じうる（§2.8/§3.5）。
-- base_price（§3.4 の比較線）は close に合わせて最新 tick の値、
-- time はバケット先頭 tick の ticked_at、is_opening は寄り付きを含むかで決める。
SELECT
    (tick_index / 5)::bigint                                      AS bucket,
    MIN(ticked_at)::timestamptz                                   AS time,
    (array_agg(open  ORDER BY tick_index))[1]::numeric            AS open,
    MAX(high)::numeric                                            AS high,
    MIN(low)::numeric                                             AS low,
    (array_agg(close ORDER BY tick_index DESC))[1]::numeric       AS close,
    (array_agg(base_price ORDER BY tick_index DESC))[1]::numeric  AS base_price,
    bool_or(is_opening)                                           AS is_opening
FROM price_ticks
WHERE currency_id = $1 AND session_id = $2
GROUP BY tick_index / 5
ORDER BY bucket;

-- name: GetLastCloseBeforeDate :one
-- 通貨一覧の「前日比」（#99 WEB-4）の基準値として、「今日より前の直近セッションの
-- 最後の price_tick の close」を取得する。game_sessions.date が基準日より前である
-- セッションに絞り、tick_index 降順の先頭（＝最後に保存されたtick）を取る。
-- tick_index は通貨生成時点からの通算番号で単調増加のため、これが
-- 「直近セッションの最終close」になる。行が無ければ呼び出し側で null（未定義）にする。
SELECT pt.close
FROM price_ticks pt
JOIN game_sessions gs ON gs.id = pt.session_id
WHERE pt.currency_id = $1 AND gs.date < $2
ORDER BY pt.tick_index DESC
LIMIT 1;
