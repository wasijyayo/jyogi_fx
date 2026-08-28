-- design.md §2.12（確定値 #16）の3通貨をシステム通貨として投入する（#31 SCHEMA-2）。
-- created_by は指定しない（NULL = システム通貨。CLAUDE.md §5.3。コードに定数として埋め込まない）。
--
-- epoch_at / created_at は列の DEFAULT（now()）に任せる。これにより
-- 「投入した瞬間」がそのまま tick_index=0 の基準時刻になる（§2.9: epoch_at は以降変更禁止）。
-- pressure も列の DEFAULT（0）に任せる。pressure_at は DEFAULT を持たない列なので
-- 明示的に now() を入れる。
--
-- off_session_scale / max_leverage / fee_rate は列の DEFAULT と同値だが、
-- §2.12 で確定した値であることをこの1箇所で明示するためあえて書き出す。
--
-- ON CONFLICT (symbol) DO NOTHING で再実行可能にする（#31 完了条件）。
-- 再度このSQLを流しても、稼働中の pressure 等を上書きせず既存行はそのまま残る。
INSERT INTO currencies
    (symbol, name, base_price, drift, volatility, lambda, k, liquidity,
     pressure_at, off_session_scale, seed, max_leverage, fee_rate)
VALUES
    ('JOG',   'JOG（安定）',     100.00, 0, 0.0008, 0.1386294361, 0.0045, 75000, now(), 0.0500, 1001, 10.00, 0.000500),
    ('WASI',  'WASI（中間）',    100.00, 0, 0.0020, 0.1732867951, 0.0100, 40000, now(), 0.0500, 2002, 10.00, 0.000500),
    ('CHEBU', 'CHEBU（大荒れ）', 100.00, 0, 0.0038, 0.2310490602, 0.0180, 18000, now(), 0.0500, 3003, 10.00, 0.000500)
ON CONFLICT (symbol) DO NOTHING;
