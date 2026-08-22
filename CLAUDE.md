# CLAUDE.md

このファイルは Claude Code が起動時に自動で読み込む。
**常に守るべき前提・規約・禁止事項**のみをここに集約する（トークン節約のため、
毎回読み込む必要のない詳細は別ファイルに分離している）。

- ゲーム設計の詳細（価格モデルの数式、イベント、Discord 演出、DB スキーマ）は
  `docs/design.md` を参照すること。**実装に着手する前に必ず読むこと。**
- 開発フロー・Walking Skeletonの手順・コマンド一覧・Go実装の作法・CI設定は
  `docs/dev-guide.md` を参照すること。**該当する作業に着手する前に必ず読むこと。**

どちらも無条件では読み込まれない。関係する作業を始める前に明示的に読むこと。

---

## 1. プロジェクト概要

Discord 連携型の **FX トレーディングゲーム**。

- 想定ユーザー: **最大 20 人程度**（クローズドな Discord サーバー内）
- 取引可能時間: **1 日 1 時間のみ**（それ以外の時間は取引不可）
- 通貨価格は**プレイヤーの売買によって実際に変動する**（これがゲームの核心）
- Web でチャートを見ながら取引し、Discord でランキング・残高確認・決済を行う

### 最重要の設計方針

> **サーバーは常時起動しない。** リクエストが来たときだけ起動する
> scale-to-zero 構成で、無料枠内に収めることを最優先とする。

この制約がアーキテクチャのほぼ全てを規定している。以下を破る実装を提案しないこと。

- ❌ バックグラウンドで常時動くワーカー / goroutine による定期実行
- ❌ インメモリのセッションストア / キャッシュ（インスタンスは常に落ちる前提）
- ❌ SQLite などのローカルファイル DB（ファイルシステムは揮発する）
- ❌ Discord Gateway（WebSocket 常時接続）方式の Bot

---

## 2. 技術スタック

### バックエンド

| 項目 | 採用 |
|---|---|
| 言語 | Go 1.23+ |
| HTTP | 標準 `net/http` の `ServeMux`（メソッド・パス変数対応済み。フレームワーク不要） |
| DB | PostgreSQL（本番: Neon / ローカル: Docker） |
| クエリ | `sqlc`（SQL を書いて型付き Go を生成。手書き ORM は使わない） |
| マイグレーション | `golang-migrate` |
| API 生成 | `oapi-codegen`（OpenAPI → Go サーバインタフェース） |
| 数値 | `shopspring/decimal`（**float 禁止**。後述） |
| テスト | `httptest` + `testcontainers` |

### フロントエンド

| 項目 | 採用 |
|---|---|
| ビルド | Vite + React + TypeScript |
| サーバ状態 | TanStack Query |
| ルーティング | TanStack Router |
| UI 状態 | Zustand（必要になったら） |
| API クライアント | `orval`（OpenAPI → TS クライアント + TanStack Query hooks） |
| テスト | Vitest + Playwright（E2E は主要導線のみ数本） |

### インフラ

| 項目 | 採用 |
|---|---|
| 実行環境 | Google Cloud Run（scale-to-zero） |
| DB | Neon（サーバレス Postgres・無料枠） |
| 定期実行 | Cloud Scheduler（取引時間中のみ毎分 tick） |
| CI / デプロイ | GitHub Actions |
| 配信形態 | **Go の `embed` で SPA をバイナリに同梱した単一バイナリ** |

---

## 3. ディレクトリ構成

```
fxgame/
├── CLAUDE.md
├── compose.yaml               ローカル用 Postgres
├── Dockerfile                 マルチステージ（Cloud Run 用）
├── Makefile
├── .env.example               ← コミットする
├── .env                       ← コミットしない
├── api/
│   └── openapi.yaml           API 契約（実装より先に書く）
├── docs/
│   └── design.md              ゲーム設計の詳細
├── backend/
│   ├── go.mod
│   ├── cmd/app/main.go        サブコマンド分岐のみ
│   ├── db/
│   │   ├── migrations/        golang-migrate の SQL
│   │   └── queries/           sqlc に食わせる SQL
│   ├── internal/
│   │   ├── server/            ① HTTP ハンドラ層
│   │   │   ├── api.go           OpenAPI 生成インタフェースの実装
│   │   │   ├── discord.go       Ed25519 署名検証 + コマンド振り分け
│   │   │   ├── tick.go          毎分の処理
│   │   │   └── static.go        embed した SPA の配信
│   │   ├── game/              ② サービス層（本体・ここが最重要）
│   │   │   ├── clock.go         時刻抽象化
│   │   │   ├── pricing.go       価格計算（純粋関数）
│   │   │   ├── trade.go         売買のルール
│   │   │   ├── session.go       取引時間の判定
│   │   │   ├── event.go         イベント抽選・発火
│   │   │   └── ranking.go       集計
│   │   ├── db/                ③ sqlc 生成物（**手で編集しない**）
│   │   └── discord/           Discord API 呼び出し（通知・ロール付与）
│   └── web/                   frontend のビルド結果（embed 対象・gitignore）
└── frontend/
    └── src/
```

### 層の責務

```
① server   入力を受け取り出力を JSON/Embed にする。ロジックを書かない
    ↓
② game     ゲームのルール。**書くべきコードの大半はここ**
    ↓
③ db       sqlc 生成物。手書きしない
```

**`internal/game` にロジックを集約すること。** ハンドラにルールを書かない。

---

## 4. 4 つの入口はすべて同じサービス層を呼ぶ

| 入口 | パス | 認証方式 |
|---|---|---|
| Web API | `/api/*` | Cookie セッション |
| Discord | `/interactions` | Ed25519 署名検証 |
| tick | `/internal/tick` | 共有シークレット or OIDC |
| SPA | `/*` | なし（embed した静的ファイル） |

同じロジックを Web 用 / Discord 用に二重実装しないこと。
サービス層の関数は 1 つで、**出力の見せ方だけを分ける**。

```go
// これが 1 つあるだけ
func (s *Service) GetBalance(ctx context.Context, userID string) (Balance, error)

// Web ハンドラ                     // Discord ハンドラ
b, _ := svc.GetBalance(ctx, uid)    b, _ := svc.GetBalance(ctx, uid)
json.NewEncoder(w).Encode(b)        return discordEmbed(b)
```

---

## 5. 絶対に守るルール

### 5.1 時刻は必ず注入する（最重要）

このゲームはロジックのほぼ全てが時刻依存。`time.Now()` を直接呼ぶと
**1 日 1 時間しかテストできない**という事態になる。

```go
type Clock interface {
    Now() time.Time
}
```

- サービス層の構造体は必ず `clock Clock` を持つ
- 時刻依存の関数は `Tick(ctx, now time.Time)` のように**時刻を引数で受ける**
- テストでは固定時刻 / 倍速クロックを注入する
- 開発環境では「取引時間が常に開いている」モードを用意する

**`time.Now()` を `internal/game` 配下で直接呼ぶ実装は却下。**

### 5.2 金額に float を使わない

`float64` / `DOUBLE PRECISION` を金額・価格・数量に使わない。

- Go: `shopspring/decimal`
- Postgres: `NUMERIC(20,8)`
- sqlc の設定で `NUMERIC` → `decimal.Decimal` にマップする

理由: 残高が `99.99999999` になる、合計が合わない、再現性が壊れる。

### 5.3 通貨をハードコードしない

`USD` `JPY` などを定数やコード上の分岐に書かない。
**すべて `currencies` テーブルの行として扱う**（システム通貨も同様）。

将来「ユーザーが自分の通貨を作成する」機能を追加する前提。
`created_by` が NULL ならシステム通貨、値が入っていればユーザー作成通貨。

処理は常に「全通貨をループする」形で書く。通貨が 100 種になっても
tick の回数が増えない設計にすること。

### 5.4 セッションは DB に置く

インスタンスは常に落ちる。Cookie には署名済みセッション ID のみを入れ、
実体は `sessions` テーブルに持つ。

Cookie 属性: `HttpOnly` / `Secure` / `SameSite=Lax`

### 5.5 tick は冪等にする

Cloud Scheduler は失敗・重複・遅延しうる。

- キャンドル書き込みは `ON CONFLICT DO UPDATE`（UPSERT）
- イベント発火は `resolved` フラグで二重発火を防ぐ
- 「毎分必ず 1 回走る」と仮定しない。欠損は許容し、描画側で補間する

### 5.6 秘密情報をコミットしない

`.env` は `.gitignore` 済み。`.env.example` のみコミットする。
Discord のトークンを 1 度でもコミットすると履歴に残る。

---

## 6. 開発の進め方

- **スキーマファースト。** `openapi.yaml` を書く → `make gen` → Go/React 実装。実装を先に書いて OpenAPI を後追いしない
- **縦切りで進める。** レイヤーごとでなく機能ごとに端から端まで通す
- **Walking Skeleton が最優先タスク。** ゲームロジックより先に配管（DB・デプロイ・認証・API生成）を通す
- CI/CD の自動化は手動デプロイが 1 回成功してから

手順・コマンド・Go実装の作法・CI設定の詳細は `docs/dev-guide.md` を参照。
進捗は GitHub Issues（`infra-setup` / `walking-skeleton` ラベル、epic は #20）で追跡している。

---

## 7. 未確定事項

ゲームパラメータ等の未確定事項は GitHub Issues の `design-decision` ラベルで管理する。
**実装前に必ず確認し、勝手に決めない。** 詳細は `docs/design.md` §12 も参照。
