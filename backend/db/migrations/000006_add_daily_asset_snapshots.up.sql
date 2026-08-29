-- /today（当日の増減ランキング）用のスナップショット（#41 CMD-1）。
--
-- design.md には算出式が書かれていなかったため、ユーザーに確認のうえ
-- 「セッション開始時点からの総資産変化率(%)」に決定した（§7.7「新規参入者にも
-- 勝ち目がある」という資産下位バフ（#39）と同じ狙いを、絶対額ではなく率にすることで
-- 実現する）。率を計算するには寄り付き時点の基準値が必要なため、このテーブルに
-- 全登録者分を保存しておく。
--
-- claims（#39）と同じPRIMARY KEYパターンで、寄り付き処理の再実行時に
-- 重複保存されないようにする（CLAUDE.md §5.5）。
CREATE TABLE daily_asset_snapshots (
    session_id   BIGINT        NOT NULL REFERENCES game_sessions(id),
    user_id      TEXT          NOT NULL REFERENCES users(discord_id),
    total_assets NUMERIC(20,8) NOT NULL,
    PRIMARY KEY (session_id, user_id)
);
