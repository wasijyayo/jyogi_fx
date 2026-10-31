-- 過去チャートモード（design.md §13）の 1 ゲーム（1 回 3 分）を動かすスキーマ（#103 REPLAY-3）。
--
-- 過去レート（#102 REPLAY-2 の currency_pairs / rate_datasets / daily_rates）を使う
-- ゲーム本体。通常モードの price_ticks / positions / trades とは別テーブル。
-- 金額・価格はすべて NUMERIC(20,8)（float禁止。CLAUDE.md §5.2）。
-- 通貨ペアは行として扱い、コードにハードコードしない（CLAUDE.md §5.3）。

-- currency_pairs.fee_rate: 過去チャートモードの片道手数料率（design.md §13）。
-- 通常モードの currencies.fee_rate（§2.9）と同じ 0.05% = 0.000500。コードに定数を
-- 埋めず通貨ペアの行に持たせる（CLAUDE.md §5.3）。既存の4ペアは DEFAULT で 0.000500 になる。
ALTER TABLE currency_pairs
    ADD COLUMN fee_rate NUMERIC(8,6) NOT NULL DEFAULT 0.000500;

-- replay_games: 過去チャートモードの 1 ゲーム（design.md §13「データモデル（概要）」）。
-- プレイヤーはログインユーザー（users への参照）か、ゲスト名のどちらか（§13）。
-- token_hash はゲームトークンの SHA-256（16進数）。トークン本体は保存せず、開始時に
-- 一度だけ返す（先読み対策。§13）。
CREATE TABLE replay_games (
    id          BIGSERIAL      PRIMARY KEY,
    token_hash  TEXT           NOT NULL UNIQUE,   -- ゲームトークンの SHA-256（hex）
    user_id     TEXT           REFERENCES users(discord_id),  -- NULL = ゲスト
    guest_name  TEXT,           -- ゲスト名（前後の空白を除いて 1〜16 文字）
    started_at  TIMESTAMPTZ    NOT NULL,
    start_year  INT            NOT NULL,          -- 開始時に公開する年（§13。終了までは年だけ）
    status      TEXT           NOT NULL,          -- playing / finished / early_finished
    funds       NUMERIC(20,8)  NOT NULL,          -- 現在の未使用資金
    final_funds NUMERIC(20,8),                    -- スコア（確定時にセット。§13）
    finished_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ    NOT NULL DEFAULT now(),
    CONSTRAINT replay_games_player_check CHECK (
        (user_id IS NULL AND guest_name IS NOT NULL) OR
        (user_id IS NOT NULL AND guest_name IS NULL)
    )
);

-- replay_game_days: ゲームが使う 80 営業日（開始前 20 日 + 区間 60 日）の日付。
-- day_index は通し番号（0..79）。0〜19 が開始前 20 日、20〜79 が区間 60 日。
-- 終了前に日付を返さないため、日足は day_index（通し番号）で返し、この表で
-- 通し番号→日付を復元する（先読み対策。§13）。
CREATE TABLE replay_game_days (
    game_id     BIGINT NOT NULL REFERENCES replay_games(id),
    day_index   INT    NOT NULL,
    date        DATE   NOT NULL,
    PRIMARY KEY (game_id, day_index)
);

-- replay_game_datasets: ゲームが使ったデータセット（通貨ペアごとに 1 つ）。
-- ゲーム開始時点で一番新しく取り込まれた（imported_at 最新）データセットを記録し、
-- 以降はそれだけを読む（後からデータセットが増えてもゲームの値は変わらない。#103）。
CREATE TABLE replay_game_datasets (
    game_id     BIGINT NOT NULL REFERENCES replay_games(id),
    pair_id     BIGINT NOT NULL REFERENCES currency_pairs(id),
    dataset_id  BIGINT NOT NULL,
    PRIMARY KEY (game_id, pair_id),
    -- データセットの通貨ペアと行の通貨ペアが食い違う状態を DB で防ぐ（daily_rates と同じ）。
    FOREIGN KEY (dataset_id, pair_id) REFERENCES rate_datasets (id, pair_id)
);

-- replay_speed_changes: 速さ（×1 / ×2 / ×3）の変更履歴（design.md §13 scale-to-zero）。
-- 公開済み日数は「開始からの経過時間 × 速さ」を区間ごとに足し合わせて求める。
-- 変更時刻はサーバーが記録する（ブラウザの時刻は受け取らない）。
CREATE TABLE replay_speed_changes (
    id          BIGSERIAL    PRIMARY KEY,
    game_id     BIGINT       NOT NULL REFERENCES replay_games(id),
    changed_at  TIMESTAMPTZ  NOT NULL,
    speed       INT          NOT NULL              -- 1 / 2 / 3
);
CREATE INDEX idx_replay_speed_changes_game ON replay_speed_changes (game_id, changed_at);

-- replay_positions: 過去チャートモードの建玉（design.md §13「注文・損益・強制決済」）。
-- 分離マージン。建玉の大きさ = initial_margin × leverage。margin（現在の証拠金）は
-- 追加分を含む。手数料・損益の計算基準（取引規模）は initial_margin × leverage のまま
-- 変えない（追加しても取引規模・取得価格は変わらない。#103 追加決定）。
CREATE TABLE replay_positions (
    id             BIGSERIAL      PRIMARY KEY,
    game_id        BIGINT         NOT NULL REFERENCES replay_games(id),
    pair_id        BIGINT         NOT NULL REFERENCES currency_pairs(id),
    side           TEXT           NOT NULL,   -- long / short
    initial_margin NUMERIC(20,8)  NOT NULL,   -- 建玉時の証拠金（取引規模・手数料の基準）
    margin         NUMERIC(20,8)  NOT NULL,   -- 現在の証拠金（初期 + 追加分）
    leverage       INT            NOT NULL,   -- レバレッジ（整数。1 倍刻み）
    entry_price    NUMERIC(20,8)  NOT NULL,   -- 約定価格（公開済みの最新の終値）
    entry_day      INT            NOT NULL,   -- 建てた日（day_index の通し番号 0..79）
    status         TEXT           NOT NULL,   -- open / closed / liquidated
    closed_day     INT,           -- 決済した日（day_index の通し番号）
    close_price    NUMERIC(20,8), -- 決済価格
    pnl            NUMERIC(20,8), -- 実現損益（証拠金 + 損益 - 決済手数料。強制決済は0）
    created_at     TIMESTAMPTZ    NOT NULL DEFAULT now()
);
CREATE INDEX idx_replay_positions_game ON replay_positions (game_id);

-- replay_leverage_unlocks: 上限解放の段数（ゲーム × 通貨ペアごと）。
-- 1 段 = +10 倍。費用は 250 から段ごとに 2 倍（250 → 500 → 1000 → …）。ペアごとに数える。
CREATE TABLE replay_leverage_unlocks (
    game_id     BIGINT NOT NULL REFERENCES replay_games(id),
    pair_id     BIGINT NOT NULL REFERENCES currency_pairs(id),
    tier        INT    NOT NULL,   -- 解放済みの段数
    PRIMARY KEY (game_id, pair_id)
);
