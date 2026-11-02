-- 過去チャートモード（design.md §13）の出来事（#116 REPLAY-6）のクエリ。
-- 出来事はテーブルの行として扱い、コードに見出しや日付を書かない。
-- ゲームが使うのは採用済み（adopted = true）の行だけ。

-- name: CreateHistoricalEvent :one
-- 取り込み（import-events）で候補を 1 件入れる。再実行しても行を増やさないよう、
-- （source, year, headline）の一意キーで ON CONFLICT DO NOTHING にする。
-- 既存の行（採用状態・説明など）は上書きしない。既にある場合は pgx.ErrNoRows になる。
INSERT INTO historical_events (
    year, event_date, date_precision, headline, description, category, source, source_url, adopted
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (source, year, headline) DO NOTHING
RETURNING *;

-- name: ListAdoptedHistoricalEventsForYear :many
-- 新聞に載せる、開始年の採用済みの出来事を全件返す（日付の精度は問わない）。
-- 日付は API では返さない（開始年だけを公開する。design.md §13「先読み対策」）。
-- 並びは取り込み順（id）にする。event_date 順にすると、日付を出さなくても新聞の並びから
-- 出来事の前後関係が分かってしまうため。
SELECT * FROM historical_events
WHERE year = $1 AND adopted
ORDER BY id;

-- name: ListAdoptedDayEventsForYear :many
-- カットインの候補になる、開始年・日まで確定した採用済みの出来事を返す。
-- 区間への割り当て（取引のない日は次の共通営業日へ）は internal/game で行う。
SELECT * FROM historical_events
WHERE year = $1 AND adopted AND date_precision = 'day' AND event_date IS NOT NULL
ORDER BY event_date, id;
