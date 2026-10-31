-- 過去チャートモード（design.md §13）のゲーム進行（#103 REPLAY-3）のクエリ。
-- 通常モードの positions / trades とは別テーブル（replay_*）を扱う。

-- name: ListCurrencyPairs :many
-- 過去チャートモードで扱う全通貨ペア（design.md §13「対象と期間」）。コードに
-- ペア名を埋め込まず、常に全件を取ってループする（CLAUDE.md §5.3）。
SELECT * FROM currency_pairs
ORDER BY id;

-- name: GetCurrencyPairByID :one
-- 建玉・上限解放の処理で通貨ペアをIDで引く。
SELECT * FROM currency_pairs
WHERE id = $1;

-- name: ListEffectiveDailyRatesForPair :many
-- 各日付について、そのゲームの開始時点で一番新しく取り込まれた（imported_at 最新）データセットの
-- 日足を取り出す（区間選び・ゲームが使う値の決定の起点。#103 マネージャー決定）。
-- 同じ日付に複数データセットがある場合は imported_at が最新、同時刻は id の大きい方を優先して決定的にする。
SELECT DISTINCT ON (dr.date)
    dr.id, dr.dataset_id, dr.pair_id, dr.date, dr.open, dr.high, dr.low, dr.close
FROM daily_rates dr
JOIN rate_datasets rd ON rd.id = dr.dataset_id
WHERE dr.pair_id = $1
ORDER BY dr.date, rd.imported_at DESC, rd.id DESC;

-- name: CreateReplayGame :one
-- ゲームを 1 件作る（#103 REPLAY-3）。開始時の status は playing、funds は初期資金。
INSERT INTO replay_games (token_hash, user_id, guest_name, started_at, start_year, status, funds)
VALUES ($1, $2, $3, $4, $5, 'playing', $6)
RETURNING *;

-- name: GetReplayGameByIDForUpdate :one
-- 遅延評価（強制決済・確定・早期終了）の前にゲームの行をロックして読み込む
-- （同時リクエストで二重処理しないため。#103）。呼び出し側は必ずトランザクション内で使うこと。
SELECT * FROM replay_games
WHERE id = $1
FOR UPDATE;

-- name: UpdateReplayGameFunds :exec
-- 注文・決済・証拠金追加・上限解放で未使用資金を更新する。
UPDATE replay_games
SET funds = $2
WHERE id = $1;

-- name: FinishReplayGame :exec
-- ゲームを確定させる（60 営業日終了 or 早期終了）。スコア = 最終資金。
UPDATE replay_games
SET status = $2, final_funds = $3, finished_at = $4
WHERE id = $1;

-- name: CreateReplayGameDay :exec
-- ゲームが使う 1 営業日分の日付を通し番号（day_index）で記録する。
INSERT INTO replay_game_days (game_id, day_index, date)
VALUES ($1, $2, $3);

-- name: ListReplayGameDays :many
-- ゲームが使う 80 営業日（開始前 20 + 区間 60）を通し番号順で返す。
SELECT * FROM replay_game_days
WHERE game_id = $1
ORDER BY day_index;

-- name: CreateReplayGameRate :exec
-- ゲームが使う日足の行（daily_rates への参照）を 1 日分記録する（#103）。
INSERT INTO replay_game_rates (game_id, pair_id, day_index, daily_rate_id)
VALUES ($1, $2, $3, $4);

-- name: ListReplayGameRates :many
-- ゲームが使う日足を全ペア・全日分、daily_rates と結合して返す（約定・強制決済・状態取得で読む）。
SELECT rgr.pair_id, rgr.day_index,
       dr.id, dr.dataset_id, dr.date, dr.open, dr.high, dr.low, dr.close
FROM replay_game_rates rgr
JOIN daily_rates dr ON dr.id = rgr.daily_rate_id
WHERE rgr.game_id = $1
ORDER BY rgr.pair_id, rgr.day_index;

-- name: CreateReplaySpeedChange :exec
-- 速さの変更を履歴に記録する（design.md §13 scale-to-zero）。
INSERT INTO replay_speed_changes (game_id, changed_at, speed)
VALUES ($1, $2, $3);

-- name: ListReplaySpeedChanges :many
-- 速さの変更履歴を時刻順で返す（公開済み日数の計算に使う）。
SELECT * FROM replay_speed_changes
WHERE game_id = $1
ORDER BY changed_at, id;

-- name: CreateReplayPosition :one
-- 建玉を作る（design.md §13「注文・損益・強制決済」）。status は open。
INSERT INTO replay_positions (
    game_id, pair_id, side, initial_margin, margin, leverage, entry_price, entry_day, status
) VALUES ($1, $2, $3, $4, $4, $5, $6, $7, 'open')
RETURNING *;

-- name: ListOpenReplayPositions :many
-- 強制決済の判定対象の未決済建玉を返す（1 ゲーム分）。
SELECT * FROM replay_positions
WHERE game_id = $1 AND status = 'open'
ORDER BY id;

-- name: GetReplayPositionForUpdate :one
-- 決済・証拠金追加の前に建玉の行をロックして読み込む（#103）。
-- game_id も条件に含め、他人のゲームの建玉IDの存在有無を漏らさない。
SELECT * FROM replay_positions
WHERE id = $1 AND game_id = $2
FOR UPDATE;

-- name: CloseReplayPosition :one
-- 建玉を決済（通常決済 or 強制決済）として確定する。
UPDATE replay_positions
SET status = $2, closed_day = $3, close_price = $4, pnl = $5
WHERE id = $1
RETURNING *;

-- name: AddReplayPositionMargin :one
-- 建玉に証拠金を追加する（取引規模・取得価格は変えない。追加分だけ margin を増やす）。
UPDATE replay_positions
SET margin = $2
WHERE id = $1
RETURNING *;

-- name: GetReplayLeverageUnlock :one
-- 上限解放の現在の段数を返す（未解放なら 0 行 = pgx.ErrNoRows）。
SELECT * FROM replay_leverage_unlocks
WHERE game_id = $1 AND pair_id = $2;

-- name: ListReplayLeverageUnlocks :many
-- ゲームの上限解放の段数を全ペア分まとめて返す（状態取得で現在の上限を出す）。
SELECT * FROM replay_leverage_unlocks
WHERE game_id = $1
ORDER BY pair_id;

-- name: UpsertReplayLeverageUnlock :one
-- 上限解放の段数を進める（なければ作成、あれば上書き）。
INSERT INTO replay_leverage_unlocks (game_id, pair_id, tier)
VALUES ($1, $2, $3)
ON CONFLICT (game_id, pair_id) DO UPDATE SET tier = EXCLUDED.tier
RETURNING *;
