-- 過去チャートモード（design.md §13）の出来事（#116 REPLAY-6）のクエリ。
-- 出来事はテーブルの行として扱い、コードに見出しや日付を書かない。
-- ゲームが使うのは採用済み（adopted = true）の行だけ。

-- name: UpsertHistoricalEvent :one
-- 取り込み（import-events）で 1 件入れる。key を冪等キーにして、既にある行は
-- 全項目を上書きする（見出し・説明・日付の修正を反映するため）。取り込んだ行は
-- 採用済み（adopted = true）にする。
INSERT INTO historical_events (
    key, year, event_date, date_precision, headline, description, category, source, source_url, adopted
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, true)
ON CONFLICT (key) DO UPDATE SET
    year = EXCLUDED.year,
    event_date = EXCLUDED.event_date,
    date_precision = EXCLUDED.date_precision,
    headline = EXCLUDED.headline,
    description = EXCLUDED.description,
    category = EXCLUDED.category,
    source = EXCLUDED.source,
    source_url = EXCLUDED.source_url,
    adopted = true
RETURNING *;

-- name: UnadoptHistoricalEventsNotInKeys :execrows
-- 今回取り込んだファイル全体に無い key の行は採用を外す（削除はしない。
-- replay_game_events が参照しているため）。0 件のときは呼び出さないこと
-- （空配列を渡すと全件の採用が外れてしまう）。
UPDATE historical_events
SET adopted = false
WHERE adopted AND NOT (key = ANY($1::text[]));

-- name: ListAdoptedHistoricalEventsForYear :many
-- 新聞に載せる、開始年の採用済みの出来事を全件返す（日付の精度は問わない）。
-- 日付は API では返さない（開始年だけを公開する。design.md §13「先読み対策」）。
-- 並びは md5(id::text) による再現可能な固定順にする。event_date 順や取り込み順にすると、
-- 日付を出さなくても新聞の並びから出来事の前後関係が分かってしまうため。
SELECT * FROM historical_events
WHERE year = $1 AND adopted
ORDER BY md5(id::text);

-- name: ListAdoptedDayEventsForYear :many
-- カットインの候補になる、開始年・日まで確定した採用済みの出来事を返す。
-- 区間への割り当て（取引のない日は次の共通営業日へ）は internal/game で行う。
SELECT * FROM historical_events
WHERE year = $1 AND adopted AND date_precision = 'day' AND event_date IS NOT NULL
ORDER BY event_date, id;
