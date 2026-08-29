-- name: CreateDailyAssetSnapshot :one
-- /today用の基準値を1ユーザー分保存する（#41 CMD-1）。ON CONFLICT DO NOTHING で
-- 寄り付き処理の重複実行時に上書きしない（claims.sqlのCreateClaimと同じパターン）。
INSERT INTO daily_asset_snapshots (session_id, user_id, total_assets)
VALUES ($1, $2, $3)
ON CONFLICT (session_id, user_id) DO NOTHING
RETURNING *;

-- name: ListDailyAssetSnapshotsBySession :many
-- そのセッション分のスナップショットが既に記録済みか（1件以上あるか）の確認と、
-- /today実行時の基準値取得の両方に使う。
SELECT * FROM daily_asset_snapshots
WHERE session_id = $1;
