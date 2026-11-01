-- 過去チャートモードのランキング（design.md §13「資金・スコア・ランキング」）で使う
-- Discord のアイコン（avatar hash）を users に保存する（#104 REPLAY-4）。
--
-- Discord の /users/@me はアイコン未設定なら avatar に null を返す。その場合は NULL を
-- 保存し、表示時に Discord のデフォルトアイコン（ID から決まる番号）を使う。
-- アイコン URL の組み立ては internal/game の純粋関数で行う（DB には hash だけ持つ）。
ALTER TABLE users
    ADD COLUMN avatar TEXT;
