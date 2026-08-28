package db_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"fxgame/backend/internal/db"
)

// TestGetUserByID は WS-5 の完了条件（生成された sqlc の関数を Go から呼び出し、
// DB の値が取得できること）を確認する。
//
// docker compose up -d でローカル Postgres を起動し、
// backend/db/migrations/000001_init.up.sql を適用した状態で実行する。
// DATABASE_URL が未設定の場合は compose.yaml の既定値を使う。
func TestGetUserByID(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://app:app@localhost:5432/fxgame?sslmode=disable"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	// defer ではなく t.Cleanup で閉じる。t.Cleanup は登録順と逆順（LIFO）に実行されるため、
	// ここで先に登録しておくことで、後から登録する削除処理（t.Cleanup）より必ず後に
	// pool.Close() が走る。defer だとテスト関数が return した時点で即座に閉じてしまい、
	// 後続の t.Cleanup での削除が「閉じた後の pool」に対する実行になって静かに失敗し、
	// テストデータが（本番Neonに向けた場合は本番に）残り続けるバグになる。
	t.Cleanup(func() { pool.Close() })

	if err := pool.Ping(ctx); err != nil {
		t.Skipf("ローカル Postgres に接続できないためスキップ（docker compose up -d を実行してください）: %v", err)
	}

	const discordID = "sqlc-ws5-verify-user"

	_, err = pool.Exec(ctx,
		`INSERT INTO users (discord_id, display_name) VALUES ($1, $2)
		 ON CONFLICT (discord_id) DO UPDATE SET display_name = EXCLUDED.display_name`,
		discordID, "WS-5 動作確認ユーザー",
	)
	if err != nil {
		t.Fatalf("seed insert: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE discord_id = $1`, discordID)
	})

	q := db.New(pool)

	got, err := q.GetUserByID(ctx, discordID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}

	if got.DiscordID != discordID {
		t.Errorf("DiscordID = %q, want %q", got.DiscordID, discordID)
	}
	if got.DisplayName != "WS-5 動作確認ユーザー" {
		t.Errorf("DisplayName = %q, want %q", got.DisplayName, "WS-5 動作確認ユーザー")
	}
}
