-- ゲーム本体のスキーマ（docs/design.md §8）。
-- design-decision は #17（シーズン長）を除きすべて確定した（epic #20 参照）ため、
-- 000001_init で保留していた経済・価格・取引まわりのテーブルを一括で追加する（#30）。
--
-- 金額・価格・数量はすべて NUMERIC（float禁止。CLAUDE.md §5.2）。
-- 通貨はハードコードせず currencies テーブルの行として扱う（CLAUDE.md §5.3）。

-- users: 経済まわりの列を追加する。
--
-- balance / season_id は design.md §8 のドラフトには DEFAULT の記載が無いが、
-- ここでは付ける。理由:
--   - balance の DEFAULT 1000 は §7.0 で確定した初期資金そのもの。
--     既存の UpsertUser（internal/game/auth.go, #10 WS-6）は discord_id / display_name
--     しか INSERT しないため、DEFAULT が無いと初回ログインが NOT NULL 制約違反で落ちる。
--     DEFAULT を初期資金と同値にすることで、design.md §6.1 の
--     「初回ログイン時に初期資金を付与する」を崩さずに列を追加できる。
--   - season_id はシーズン制自体が #17 で保留中のため、1 を
--     「シーズン未導入」を表す仮のプレースホルダとして使う。
-- 実際の取引ロジック（#36〜）で残高を更新する際は、この DEFAULT に頼らず
-- 明示的に UPDATE すること。
ALTER TABLE users
    ADD COLUMN balance       NUMERIC(20,8) NOT NULL DEFAULT 1000.00000000,
    ADD COLUMN season_id     BIGINT        NOT NULL DEFAULT 1,
    ADD COLUMN roast_enabled BOOLEAN       NOT NULL DEFAULT TRUE;

CREATE TABLE currencies (
    id                 BIGSERIAL     PRIMARY KEY,
    symbol             TEXT          NOT NULL UNIQUE,
    name               TEXT          NOT NULL,
    base_price         NUMERIC(20,8) NOT NULL,   -- initial_price（epoch_at 時点の基準価格。§2.12）
    drift              NUMERIC(20,8) NOT NULL,   -- トレンドの向き
    volatility         NUMERIC(20,8) NOT NULL,
    lambda             NUMERIC(20,8) NOT NULL,   -- 減衰速度（tick=1分あたり。§2.12）
    k                  NUMERIC(20,8) NOT NULL,   -- 取引の価格影響度
    liquidity          NUMERIC(20,8) NOT NULL,   -- 流動性深度
    pressure           NUMERIC(20,8) NOT NULL DEFAULT 0,  -- セッション中のみ動く需給圧力の現在値
    pressure_at        TIMESTAMPTZ   NOT NULL,
    off_session_scale  NUMERIC(6,4)  NOT NULL DEFAULT 0.0500,  -- §2.7
    seed               BIGINT        NOT NULL,                 -- §2.7 zAt() 用
    epoch_at           TIMESTAMPTZ   NOT NULL DEFAULT now(),    -- tick_index = 0 の時刻。変更禁止
    max_leverage       NUMERIC(6,2)  NOT NULL DEFAULT 10.00,    -- §7.0 デフォルトの通貨別上書き
    fee_rate           NUMERIC(8,6)  NOT NULL DEFAULT 0.000500, -- §7.0 デフォルトの通貨別上書き
    created_by         TEXT          REFERENCES users(discord_id),  -- NULL = システム通貨
    created_at         TIMESTAMPTZ   NOT NULL DEFAULT now()
);

CREATE TABLE game_sessions (          -- 1 日 1 行
    id              BIGSERIAL   PRIMARY KEY,
    date            DATE        NOT NULL UNIQUE,
    seed            BIGINT      NOT NULL,
    opened_at       TIMESTAMPTZ NOT NULL,
    closed_at       TIMESTAMPTZ NOT NULL,
    ticker_msg_id   TEXT                       -- 編集対象のティッカーメッセージ
);

CREATE TABLE events (          -- §5.4。price_ticks 統一後のtickベースモデルに整合
    id              BIGSERIAL   PRIMARY KEY,
    session_id      BIGINT      NOT NULL REFERENCES game_sessions(id),
    currency_id     BIGINT      NOT NULL REFERENCES currencies(id),
    type            TEXT        NOT NULL,      -- shock / vol_up / liquidity_drain
    fire_tick       BIGINT      NOT NULL,      -- 発火するセッション内tick番号（1〜60）
    duration_ticks  INT         NOT NULL DEFAULT 0,  -- shockは0（瞬間）。vol_up/liquidity_drainは3〜5
    magnitude       NUMERIC(20,8) NOT NULL,
    -- shock: 価格への乗数オフセット（例 +0.14 = +14%, -0.14 = -14%）
    -- vol_up: volatility 倍率（例 3.0）
    -- liquidity_drain: liquidity 倍率（例 0.30 = 平常の30%）
    teased          BOOLEAN     NOT NULL DEFAULT FALSE,  -- 予兆メッセージ投稿済みか（通知の冪等性用）
    resolved        BOOLEAN     NOT NULL DEFAULT FALSE,  -- 発火通知投稿済みか（通知の冪等性用）
    UNIQUE (currency_id, fire_tick)
);

CREATE TABLE positions (
    id              BIGSERIAL   PRIMARY KEY,
    user_id         TEXT        NOT NULL REFERENCES users(discord_id),
    currency_id     BIGINT      NOT NULL REFERENCES currencies(id),
    side            TEXT        NOT NULL,      -- long / short
    size            NUMERIC(20,8) NOT NULL,
    entry_price     NUMERIC(20,8) NOT NULL,
    leverage        NUMERIC(20,8) NOT NULL,
    opened_at       TIMESTAMPTZ NOT NULL,
    closed_at       TIMESTAMPTZ,
    pnl             NUMERIC(20,8)
);

CREATE TABLE trades (
    id              BIGSERIAL   PRIMARY KEY,
    user_id         TEXT        NOT NULL REFERENCES users(discord_id),
    currency_id     BIGINT      NOT NULL REFERENCES currencies(id),
    position_id     BIGINT      REFERENCES positions(id),
    side            TEXT        NOT NULL,
    size            NUMERIC(20,8) NOT NULL,
    price           NUMERIC(20,8) NOT NULL,    -- 約定価格
    fee             NUMERIC(20,8) NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE price_ticks (             -- §2.8。セッション中の60tick + 寄り付き1本のみ保存
    id           BIGSERIAL   PRIMARY KEY,
    currency_id  BIGINT      NOT NULL REFERENCES currencies(id),
    session_id   BIGINT      NOT NULL REFERENCES game_sessions(id),
    tick_index   BIGINT      NOT NULL,  -- epoch_at からの通算tick番号
    ticked_at    TIMESTAMPTZ NOT NULL,

    base_price   NUMERIC(20,10) NOT NULL,  -- 圧力を含まない基準価格
    pressure     NUMERIC(20,10) NOT NULL,  -- そのtick終了時点の圧力
    net_volume   NUMERIC(20,10) NOT NULL,  -- そのtickの買い-売り差額

    open         NUMERIC(20,10) NOT NULL,
    high         NUMERIC(20,10) NOT NULL,
    low          NUMERIC(20,10) NOT NULL,
    close        NUMERIC(20,10) NOT NULL,

    is_opening   BOOLEAN NOT NULL DEFAULT FALSE,  -- 寄り付きキャンドルか

    UNIQUE (currency_id, tick_index)
);

CREATE INDEX idx_price_ticks_session ON price_ticks (session_id, currency_id, tick_index);
