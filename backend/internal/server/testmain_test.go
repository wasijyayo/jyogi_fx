package server_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// sharedTestDBAdvisoryLockKey は、同じ DB を共有するテストパッケージ同士を直列化する
// ための Postgres のセッションレベル advisory lock のキー。game パッケージの
// testmain_test.go と同じキーを使う（テストのヘルパーはパッケージをまたげないため、
// 定数は両方に書く）。
//
// game と server のテストは同じ DB に対して replay 系テーブルや通貨ペアを広く
// DELETE/INSERT するため、パッケージを並列に走らせると互いの残骸で落ちることがある。
// パッケージ全体をこのロックで直列化する。historical_events を使う個別テストは、
// さらに細かいキー（historicalEventsAdvisoryLockKey）で直列化している。
const sharedTestDBAdvisoryLockKey int64 = 116000

// TestMain はテストパッケージ全体を sharedTestDBAdvisoryLockKey で直列化する。
// DB に接続できない環境ではロックせずにテストを実行する（各テストが接続時に Skip する）。
func TestMain(m *testing.M) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://app:app@localhost:5432/fxgame?sslmode=disable"
	}

	connectCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	conn, err := pgx.Connect(connectCtx, dsn)
	cancel()
	if err != nil {
		os.Exit(m.Run())
	}

	// 取得はブロックしてよい（他パッケージのテストが終わるのを待つ）。
	if _, err := conn.Exec(context.Background(), `SELECT pg_advisory_lock($1)`, sharedTestDBAdvisoryLockKey); err != nil {
		_ = conn.Close(context.Background())
		os.Exit(m.Run())
	}

	code := m.Run()

	unlockCtx, unlockCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if _, err := conn.Exec(unlockCtx, `SELECT pg_advisory_unlock($1)`, sharedTestDBAdvisoryLockKey); err != nil {
		fmt.Fprintf(os.Stderr, "pg_advisory_unlock: %v\n", err)
	}
	unlockCancel()
	_ = conn.Close(context.Background())
	os.Exit(code)
}
