-- name: CreateEvent :one
-- 抽選で決まったイベント1件をDBに書き込む（#40 EVENT-1。design.md §5.1/§5.4）。
-- UNIQUE (currency_id, fire_tick) への ON CONFLICT DO NOTHING で、寄り付き処理
-- （SessionService.OpenSession）が再実行された場合の冪等性を確保する。
-- 抽選そのもの（DrawSessionEvents）は game_sessions.seed の純粋関数なので、
-- 再実行しても同じ内容が算出され、同じ行への再INSERTになる（CLAUDE.md §5.5）。
-- 競合時は RETURNING が0行になるため、呼び出し側は「既に挿入済み」として無視してよい
-- （claims.sqlのCreateClaimと同じパターンだが、ここではエラー扱いにしない点が異なる）。
INSERT INTO events (session_id, currency_id, type, fire_tick, duration_ticks, magnitude)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (currency_id, fire_tick) DO NOTHING
RETURNING *;

-- name: ListEventsBySession :many
-- そのセッションで既にイベントが抽選済みか（1件以上あるか）を確認するために使う
-- （#40完了条件「tick処理が再抽選しないこと」）。
SELECT * FROM events
WHERE session_id = $1
ORDER BY fire_tick;

-- name: ListEventsByCurrency :many
-- 価格計算（BasePrice の shock・vol_up 反映、TradeService の liquidity_drain 反映）が
-- 参照する、その通貨に紐づく全イベント（全セッション分。design.md §5.4: shockは
-- 発火後恒久的にbase(n)へ効くため、当日分だけでなく過去分も必要）。
-- fire_tick 昇順にしておくと呼び出し側のΠ計算・区間判定がわずかに書きやすくなる。
SELECT * FROM events
WHERE currency_id = $1
ORDER BY fire_tick;

-- name: MarkEventTeased :exec
-- 予兆メッセージ（design.md §5.3）を投稿できたら呼ぶ（#44 NOTIFY-2）。
-- teased=FALSE の行にしかヒットしないため、Discord投稿に失敗して呼ばれなかった
-- 場合は次tickで再試行される（冪等性。CLAUDE.md §5.5）。
UPDATE events SET teased = TRUE WHERE id = $1 AND teased = FALSE;

-- name: MarkEventResolved :exec
-- 発火通知（design.md §5.4「resolvedは通知の冪等性のためだけに使う」）を投稿できたら呼ぶ。
-- MarkEventTeasedと同じくWHERE句で二重更新を防ぐ。
UPDATE events SET resolved = TRUE WHERE id = $1 AND resolved = FALSE;
