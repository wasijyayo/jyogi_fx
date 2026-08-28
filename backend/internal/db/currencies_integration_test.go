package db_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"fxgame/backend/internal/db"
)

// TestListCurrencies は #31 の完了条件（design.md §2.12 の3通貨がDBに存在し、
// 全通貨をループする形のコード = ListCurrencies から読み出せること）を確認する。
//
// 読み取り専用（INSERT/DELETEなし）のテストなので、誤って本番 DATABASE_URL に
// 向けて実行してしまっても書き込みは発生しない。
func TestListCurrencies(t *testing.T) {
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
	t.Cleanup(func() { pool.Close() })

	if err := pool.Ping(ctx); err != nil {
		t.Skipf("ローカル Postgres に接続できないためスキップ（docker compose up -d を実行してください）: %v", err)
	}

	q := db.New(pool)

	currencies, err := q.ListCurrencies(ctx)
	if err != nil {
		t.Fatalf("ListCurrencies: %v", err)
	}

	bySymbol := make(map[string]db.Currency, len(currencies))
	for _, c := range currencies {
		bySymbol[c.Symbol] = c
	}

	for _, want := range []struct {
		symbol     string
		volatility string
		seed       int64
	}{
		{"JOG", "0.0008", 1001},
		{"WASI", "0.0020", 2002},
		{"CHEBU", "0.0038", 3003},
	} {
		got, ok := bySymbol[want.symbol]
		if !ok {
			t.Errorf("currency %s not found via ListCurrencies", want.symbol)
			continue
		}
		if !got.Volatility.Equal(decimal.RequireFromString(want.volatility)) {
			t.Errorf("%s: volatility = %s, want %s", want.symbol, got.Volatility, want.volatility)
		}
		if got.Seed != want.seed {
			t.Errorf("%s: seed = %d, want %d", want.symbol, got.Seed, want.seed)
		}
		if got.CreatedBy.Valid {
			t.Errorf("%s: created_by should be NULL (system currency), got %q", want.symbol, got.CreatedBy.String)
		}
	}
}
