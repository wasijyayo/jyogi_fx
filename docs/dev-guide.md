# 開発フローガイド

CLAUDE.md には常に守るべきルールのみを置き、
「作業に着手する時に参照すればいい」手順・コマンド・CI設定はこちらに集約する。

**このファイルは起動時に自動では読み込まれない。** 該当する作業（Walking Skeleton実装、
コマンド実行、CI設定など）に着手する前に読むこと。

---

## 1. 開発フロー

### スキーマファースト

```
1. openapi.yaml を書く（契約）
        ↓
2. make gen で両端のコードを生成
        ↓
3. Go は生成インタフェースを実装、React は生成 hooks を呼ぶだけ
```

**実装を先に書いてから OpenAPI を後追いで書かない。**

`operationId` は必ず付ける（生成される関数名になる）。
`required` を必ず書く（書かないと Go 側がポインタ地獄、TS 側が optional 地獄になる）。

Discord の `/interactions` は Discord 側が定義したスキーマなので
**OpenAPI には書かない**。手書きハンドラで受ける。

### Issue ごとにブランチを切る

GitHub Issue に対応する作業は **`main` に直接コミットせず、必ずブランチを切ってから実装する。**

- ブランチ名は `wasijyayo/issueN`（N は issue 番号）
- コミットメッセージの先頭行は `WS-N: 内容` のように issue のタイトルに合わせる
- 実装が終わったら PR を作成し、本文に `closes #N` を書く
- PR をマージして issue をクローズする（マージコミット、squash はしない）

参考: PR #21（`wasijyayo/issue8`, issue #8 / WS-4）が実例。

### 縦切りで進める

レイヤーごと（全 API → 全画面）ではなく、**機能ごとに端から端まで**通す。

```
スキーマ追加 → 生成 → Go 実装 → React 実装 → 動作確認
```

### Walking Skeleton（最優先タスク）

ゲームロジックより先に**配管を通す**こと。

1. `docker compose up` で Postgres が立つ
2. `/health` が 200 を返すだけの Go サーバ
3. **Cloud Run に手動デプロイして 200 を確認**（← ここを最初に通す）
4. DB スキーマを書き golang-migrate で適用
5. sqlc で 1 クエリ生成し Go から呼べることを確認
6. Discord OAuth ログイン + Cookie セッション発行
7. `openapi.yaml` に `GET /api/me` だけ書いて両端を生成
8. React から `/api/me` を叩いて自分の名前が表示される

**ここまでで認証・DB・API 生成・デプロイが全通しになる。**
以降は `internal/game` に機能を足すだけになる。

CI/CD の自動化は**手動デプロイが 1 回成功してから**。
先に組むと失敗時の原因切り分けができない。

進捗は GitHub Issues（#1〜#12、`infra-setup` / `walking-skeleton` ラベル、
epic は #20）で追跡している。

---

## 2. コマンド

```bash
# ローカル DB
docker compose up -d          # Postgres 起動
docker compose down           # 停止（データは残る）
docker compose down -v        # 停止 + データ削除
docker compose exec db psql -U app -d fxgame

# 開発
make dev                      # compose 起動 + go run
make gen                      # sqlc + oapi-codegen + orval を一括実行
make migrate-up
make migrate-down
make test

# デプロイ（初回は手動）
gcloud run deploy --source .
```

Go はローカルで `go run` する。**Docker の中で動かさない**（ホットリロードが効かなくなる）。
Docker は「ローカル用 Postgres」と「本番用イメージのビルド」にのみ使う。

---

## 3. Go 実装の約束

- 全ての関数の第 1 引数は `ctx context.Context`
- エラーは戻り値で返す。`if err != nil { return err }` が並ぶのが正常
- インタフェースは必要になってから切る（例外: `Clock` は最初から）
- 複数の DB 操作は必ずトランザクションで囲む

```go
func (s *Service) PlaceOrder(ctx context.Context, ...) error {
    tx, err := s.pool.Begin(ctx)
    if err != nil { return err }
    defer tx.Rollback(ctx)   // コミットされていなければ巻き戻る

    q := s.queries.WithTx(tx)
    // ... 複数の操作 ...

    return tx.Commit(ctx)
}
```

残高だけ減ってポジションが作られない、という状態を絶対に作らないこと。

---

## 4. CI

GitHub Actions で以下を回す。

- `go vet` / `golangci-lint`
- `go test`
- **生成コードの差分チェック**: `make gen && git diff --exit-code`
  （スキーマを変えたのに生成し忘れた状態を弾く）
