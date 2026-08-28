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
- **マージ後もブランチは削除しない**（`--delete-branch` を付けない。経緯を残すため）

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

**→ Walking Skeleton は完了済み（#5〜#12 すべてクローズ）。**
併せて Discord Bot の土台（`/interactions` + Ed25519 署名検証・#28）も本番稼働しており、
Discord Developer Portal への Interactions Endpoint URL 登録も完了している。

CI/CD の自動化は**手動デプロイが 1 回成功してから**。
先に組むと失敗時の原因切り分けができない。
→ 手動デプロイは成功済みだが、**CI（GitHub Actions）はまだ未構築**（`.github/` が無い）。
§4 の内容を組むのが次の候補。

進捗は GitHub Issues で追跡している。

| ラベル | 内容 |
|---|---|
| `walking-skeleton` | #5〜#12（完了） |
| `infra-setup` | 外部サービス設定・デプロイ基盤 |
| `discord-bot` | `/interactions`・スラッシュコマンド・通知 |
| `schema` | DBスキーマ・マイグレーション |
| `game-core` | 価格モデル・取引・イベント・経済 |
| `web` | React SPA（チャート・注文フォーム） |
| `design-decision` | 実装前に確定させる設計判断 |

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

# ビルド（フロントエンドを埋め込んだ単一バイナリ）
make build

# デプロイ（§2.1 参照）
gcloud run deploy fxgame --source . --region asia-northeast1 \
  --allow-unauthenticated --env-vars-file <環境変数ファイル>
```

Go はローカルで `go run` する。**Docker の中で動かさない**（ホットリロードが効かなくなる）。
Docker は「ローカル用 Postgres」と「本番用イメージのビルド」にのみ使う。

`make` が未インストールの環境（Git Bash 等）では中身のコマンドを直接叩く。

---

## 2.1 デプロイ（Cloud Run）

### 本番環境

| 項目 | 値 |
|---|---|
| URL | `https://fxgame-1046232958174.asia-northeast1.run.app` |
| GCP プロジェクト | `jyogi-fx` |
| サービス名 / リージョン | `fxgame` / `asia-northeast1` |
| DB | Neon（`DATABASE_URL`） |

**Discord Developer Portal に登録済みの URL:**

- Interactions Endpoint URL: `<本番URL>/interactions`
- OAuth2 Redirects: `<本番URL>/auth/discord/callback` と `http://localhost:8080/auth/discord/callback`

ローカルで別ポートを使う場合、そのポートの callback URL も Redirects に追加しないと
`OAuth2 redirect_uri が無効です` になる。

### 手順

環境変数は YAML ファイルにまとめて渡す（コマンド履歴にシークレットを残さないため）。
**このファイルはリポジトリ外（一時ディレクトリ）に置き、使用後は削除すること。**

```yaml
# 例: /tmp/cloudrun-env.yaml
DATABASE_URL: postgres://...
DISCORD_CLIENT_ID: "..."      # 数値だが文字列として渡す（クォート必須）
DISCORD_CLIENT_SECRET: ...
DISCORD_PUBLIC_KEY: ...
DISCORD_BOT_TOKEN: ...
DISCORD_REDIRECT_URI: https://<本番URL>/auth/discord/callback
```

```bash
gcloud run deploy fxgame --source . --region asia-northeast1 \
  --allow-unauthenticated --env-vars-file /tmp/cloudrun-env.yaml
```

### ハマりどころ

**`.gcloudignore` を消してはいけない。**
`gcloud` はこのファイルが無いと `.gitignore` を流用する。生成コード
（`backend/internal/apigen`・`internal/db` の sqlc 生成物）は `.gitignore` 対象なので、
アップロードされずに
`package fxgame/backend/internal/apigen is not in std` でビルドが失敗する。

現状は**ローカルの生成物をそのまま送っている**ため、デプロイ前に `make gen` を実行すること。
（本来は Dockerfile 内で生成するのが正しい。frontend は `npm run gen` で実施済み。
sqlc / oapi-codegen の導入とセットで移行したい。）

**Go のバージョンは `Dockerfile` と `go.mod` を揃える。**
ずれるとビルドが失敗する。

### デプロイ後の確認

```
/health                  -> 200 ok
/                        -> 200（SPAのindex.html。埋め込みが効いているか）
/assets/index-*.js       -> 200（フロントエンドのバンドル）
/api/me（Cookieなし）    -> 401
/interactions（偽署名）  -> 401  ← Discordの登録時チェックが要求する挙動
GET /interactions        -> 405
```

`/interactions` が **500** を返す場合は `DISCORD_PUBLIC_KEY` の設定漏れ
（401 は正常な署名拒否なので、両者は意図的に区別してある）。

### ビルドログの確認

```bash
gcloud builds list --limit 3 --region=asia-northeast1
gcloud builds log <BUILD_ID> --region=asia-northeast1
```

`--region` を付けないと「ビルドが見つからない」となるので注意。

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
