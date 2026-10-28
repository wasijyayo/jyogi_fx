# AGENTS.md

AI エージェント（OpenCode など）向けの作業ルール。
プロジェクトの前提・設計方針・禁止事項は **CLAUDE.md に一本化**している（`opencode.json` の
`instructions` で毎回読み込まれる）。ここには CLAUDE.md に無い、エージェント向けの注意点だけを書く。

## 作業前に必ず読むもの

- `CLAUDE.md`（自動で読み込まれる）
- 実装に着手する前: `docs/design.md` の該当箇所
- 開発フロー・コマンド・Go の作法: `docs/dev-guide.md`
- 担当する Issue の本文（`gh issue view <番号>`）。書かれていない仕様は勝手に決めず、作業を止めて報告する

## 秘密情報と本番環境

- `.env` を読まない・`source` しない・コピーしない。`DATABASE_URL` は**本番 Neon** を指している
- DB を使うコマンド（テストなど）は `scripts/with-issue-db.sh` 経由で実行する。
  worktree 専用のローカル DB（`scripts/start-issue.sh` が用意した `.issue-db`）に向けるため。
  素の `go test` は共有の `fxgame` DB を使い、並列で作業している他の worktree と衝突する
- 本番環境（Neon・Cloud Run・Discord）に対する操作はしない

## Git と PR

- main へ直接 push しない。force push しない。PR をマージしない（マージは人間が判断する）
- ブランチ名・コミットメッセージ・PR 本文の書き方は、作業を依頼した側の指示に従う
- PR を出す前に、次がすべて通ることを自分で確認する。`make test` は共有 DB でテストが走るので使わず、
  次のコマンドをそのまま実行する
  - `cd backend && go build ./... && go vet ./... && golangci-lint run ./... && ../scripts/with-issue-db.sh go test ./...`
  - フロントエンドを触った場合: `cd frontend && npm run gen && npm run build`
- マイグレーションを追加した場合は、PR 本文に必ず明記する（本番への適用は人間が手動で行う）

## 生成物

- 次は生成物なので手で編集しない。元（SQL・`api/openapi.yaml`）を直して再生成する
  - sqlc: `backend/internal/db/` の `db.go`・`models.go`・`querier.go`・`*.sql.go`
    （同じディレクトリの `*_test.go` は手書きなので編集してよい）
  - oapi-codegen: `backend/internal/apigen/api.gen.go`
  - orval: `frontend/src/api/generated/`
- 生成物は `.gitignore` 対象なので、`git status` に出てこなくて正常

## 統合テストの後始末（過去に事故があった箇所）

- `pool.Close()` は `defer` ではなく `t.Cleanup` に登録し、後始末の DELETE より**先に**登録する
  （LIFO なので、後始末の DELETE が先に走るようにする）
- 外部キーがあるので、削除は子テーブルから順に行う。おおよその順序:
  `trades` → `positions` → `price_ticks` / `events` / `claims` / `daily_asset_snapshots`
  → `game_sessions` / `currencies` → `users`（正確な依存は `backend/db/migrations/` で確認する）
- 新しく書くテストでは、削除の失敗を `t.Errorf` などで検出する。既存のテストには
  `_, _ = pool.Exec(...)` で握りつぶしている箇所があるが、失敗に気づけないので真似しない
- 実在の通貨（`epoch_at` が現在に近いもの）で tick を何度も回すと、`BasePrice` の計算が極端に遅くなる。
  テストの日付に `epoch_at` を合わせた使い捨て通貨を INSERT して使う
- JST 12:00〜13:00 は UTC 3:00〜4:00。固定クロックの時刻はタイムゾーンを明示して作る
