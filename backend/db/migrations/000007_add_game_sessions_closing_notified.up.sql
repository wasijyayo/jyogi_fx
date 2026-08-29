-- セッション終了通知＋日次まとめ（#44 NOTIFY-2, design.md §6.7・§6.9）の冪等性フラグ。
--
-- 寄り付き通知（§2.8）はOpenSessionが1日1回しか呼ばれない経路そのものが
-- 冪等性を保証する（tick.goのコメント参照）が、終了通知は毎分呼ばれる
-- 通常tickの経路内で「最後のtickかどうか」を判定して発火するため、
-- tickの重複実行（Cloud Schedulerの再試行）で同じ通知が2回投稿されないよう
-- 明示的なフラグが必要（events.teased/resolvedと同じ考え方。CLAUDE.md §5.5）。
ALTER TABLE game_sessions
    ADD COLUMN closing_notified BOOLEAN NOT NULL DEFAULT FALSE;
