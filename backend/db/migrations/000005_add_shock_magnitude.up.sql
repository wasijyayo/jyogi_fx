-- shock イベント（#40 EVENT-1。design.md §5.2「shockのmagnitude」）の通貨ごとの
-- 固定強度。CLAUDE.md §5.3「通貨をハードコードしない」に従い、コード上でsymbolに
-- 分岐するのではなく currencies テーブルの列として持つ（max_leverage/fee_rateと同じ扱い）。
--
-- DEFAULT 0.0800 は3通貨のうち「中間」（WASI）の値。将来ユーザーが通貨を作成した
-- 場合（§10.1、未実装）の無難な初期値として使う。
ALTER TABLE currencies
    ADD COLUMN shock_magnitude NUMERIC(6,4) NOT NULL DEFAULT 0.0800;

-- 3通貨（確定値 #16 → design.md §5.2の表）を明示的に上書きする。
UPDATE currencies SET shock_magnitude = 0.0400 WHERE symbol = 'JOG';
UPDATE currencies SET shock_magnitude = 0.0800 WHERE symbol = 'WASI';
UPDATE currencies SET shock_magnitude = 0.1400 WHERE symbol = 'CHEBU';
