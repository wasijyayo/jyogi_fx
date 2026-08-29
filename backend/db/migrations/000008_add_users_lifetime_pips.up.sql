-- 「人生の勝者」ロールの永続付与（#84。design.md §6.10「ロール自動付与」の
-- 最初の実装。ユーザーの追加要望で決定した具体的な発動条件）。
--
-- lifetime_pips は決済のたびに TradeService.ClosePosition が積み上げるネットの
-- 累計pips（design.md §2.8のpips定義。ユーザーの言う「現在の保持pips」）。
-- 通常決済・強制ロスカットのどちらも ClosePosition を通るため（CLAUDE.md §4）、
-- 両方が自然に反映される。プラスにもマイナスにもなりうる。
--
-- life_winner_granted は§6.8の「今日の称号」（翌セッション開始時に剥がす）とは
-- 別枠の永続ロール付与の冪等性フラグ。events.teased/resolved・
-- game_sessions.closing_notified と同じ考え方（CLAUDE.md §5.5）。
ALTER TABLE users
    ADD COLUMN lifetime_pips       NUMERIC(20,8) NOT NULL DEFAULT 0,
    ADD COLUMN life_winner_granted BOOLEAN       NOT NULL DEFAULT FALSE;
