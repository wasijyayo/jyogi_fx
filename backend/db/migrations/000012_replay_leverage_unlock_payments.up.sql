-- 過去チャートモード（design.md §13）の上限解放の支払いログ（#127 REPLAY-10）。
--
-- replay_leverage_unlocks は現在の段数しか持たないため、いつ・いくら払ったかが残らない。
-- 結果画面の資産推移（上限解放の費用を差し引く）と、解放の履歴表示のために 1 支払い
-- 1 行を記録する。金額は NUMERIC(20,8)（float禁止。CLAUDE.md §5.2）。
--
-- 公開日は通し番号（day_index 0..79）で保存する。ゲーム終了前は正確な日付を
-- どの API にも返さないため（design.md §13「先読み対策」）、日付ではなく通し番号を持つ。
CREATE TABLE replay_leverage_unlock_payments (
    id         BIGSERIAL     PRIMARY KEY,
    game_id    BIGINT        NOT NULL REFERENCES replay_games(id),
    pair_id    BIGINT        NOT NULL REFERENCES currency_pairs(id),
    tier       INT           NOT NULL,          -- 解放した段（1 始まり）
    day_index  INT           NOT NULL,          -- 支払った時点の公開日（通し番号 0..79）
    cost       NUMERIC(20,8) NOT NULL,          -- 支払った費用（250 から段ごとに 2 倍）
    created_at TIMESTAMPTZ   NOT NULL DEFAULT now()
);

CREATE INDEX idx_replay_leverage_unlock_payments_game
    ON replay_leverage_unlock_payments (game_id, day_index);
