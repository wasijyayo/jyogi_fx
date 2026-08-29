package server_test

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"fxgame/backend/internal/game"
	"fxgame/backend/internal/server"
)

// connectCommandsTestDB はコマンド系統合テスト専用のDB接続を用意する
// （discord_test.goと同じ作法。ローカルPostgresが無ければスキップ）。
func connectCommandsTestDB(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://app:app@localhost:5432/fxgame?sslmode=disable"
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("ローカル Postgres に接続できないためスキップ（docker compose up -d を実行してください）: %v", err)
	}
	return pool
}

// setupCommandsTestUser はテスト用にユーザー行を作る（trade_integration_test.goの
// setupTradeTestUserと同じ手法。internal/game外なのでSQLを直接書く）。
func setupCommandsTestUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID, displayName string, balance decimal.Decimal) {
	t.Helper()
	_, err := pool.Exec(ctx,
		`INSERT INTO users (discord_id, display_name, balance) VALUES ($1, $2, $3)
		 ON CONFLICT (discord_id) DO UPDATE SET display_name = EXCLUDED.display_name, balance = EXCLUDED.balance`,
		userID, displayName, balance)
	if err != nil {
		t.Fatalf("insert test user %s: %v", userID, err)
	}
}

// TestSlashCommands_情報参照系 は #41 CMD-1 の完了条件
// 「Discord上で各コマンドを実行し、正しい値がEmbedで返ること」を、実際に
// /interactions エンドポイントへ署名付きリクエストを送る形で確認する。
func TestSlashCommands_情報参照系(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := connectCommandsTestDB(t, ctx)

	userID := "test-cmd-balance-user"
	displayName := "テストユーザー"
	setupCommandsTestUser(t, ctx, pool, userID, displayName, decimal.NewFromInt(1500))
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE discord_id = $1`, userID)
	})

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	rankingSvc := game.NewRankingService(pool, game.RealClock{})
	profileSvc := game.NewProfileService(pool, game.RealClock{}, rankingSvc)
	mux := server.NewMux(server.Config{
		DiscordPublicKey: pub,
		Ranking:          rankingSvc,
		Profile:          profileSvc,
		Clock:            fixedClock{now: testNow},
	})

	send := func(t *testing.T, body string) map[string]any {
		t.Helper()
		req := newSignedRequest(t, priv, testTimestamp, body)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
		}
		var got map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode response: %v (body=%s)", err, rec.Body.String())
		}
		return got
	}

	firstEmbedField := func(t *testing.T, resp map[string]any, name string) string {
		t.Helper()
		data, _ := resp["data"].(map[string]any)
		embeds, _ := data["embeds"].([]any)
		if len(embeds) == 0 {
			t.Fatalf("embedsが空: %v", resp)
		}
		embed, _ := embeds[0].(map[string]any)
		fields, _ := embed["fields"].([]any)
		for _, f := range fields {
			field, _ := f.(map[string]any)
			if field["name"] == name {
				v, _ := field["value"].(string)
				return v
			}
		}
		t.Fatalf("field %q が見つからない: %v", name, embed)
		return ""
	}

	t.Run("/balance は自分の残高をephemeralなEmbedで返す", func(t *testing.T) {
		body := `{"type":2,"data":{"name":"balance"},"member":{"user":{"id":"` + userID + `"}}}`
		resp := send(t, body)

		data, _ := resp["data"].(map[string]any)
		flags, _ := data["flags"].(float64)
		if int(flags) != 1<<6 {
			t.Errorf("flags = %v, want ephemeral(64)", flags)
		}
		got := firstEmbedField(t, resp, displayName)
		if got != "1500.00" {
			t.Errorf("balance field = %q, want %q", got, "1500.00")
		}
	})

	t.Run("/rank は総資産ランキングを公開Embedで返し対象ユーザーを含む", func(t *testing.T) {
		body := `{"type":2,"data":{"name":"rank"},"member":{"user":{"id":"` + userID + `"}}}`
		resp := send(t, body)

		data, _ := resp["data"].(map[string]any)
		if _, hasFlags := data["flags"]; hasFlags && data["flags"].(float64) != 0 {
			t.Errorf("/rank は公開メッセージのはずだがflags = %v", data["flags"])
		}
		embeds, _ := data["embeds"].([]any)
		embed, _ := embeds[0].(map[string]any)
		desc, _ := embed["description"].(string)
		if !strings.Contains(desc, "<@"+userID+">") {
			t.Errorf("description = %q, want it to contain <@%s>", desc, userID)
		}
	})

	t.Run("/today はセッション未開始ならephemeralなエラーを返す", func(t *testing.T) {
		body := `{"type":2,"data":{"name":"today"},"member":{"user":{"id":"` + userID + `"}}}`
		resp := send(t, body)

		data, _ := resp["data"].(map[string]any)
		content, _ := data["content"].(string)
		if content == "" {
			t.Errorf("セッション未開始時のcontentが空: %v", resp)
		}
		flags, _ := data["flags"].(float64)
		if int(flags) != 1<<6 {
			t.Errorf("flags = %v, want ephemeral(64)", flags)
		}
	})

	t.Run("/profile は引数省略時に自分のプロフィールを公開Embedで返す", func(t *testing.T) {
		body := `{"type":2,"data":{"name":"profile"},"member":{"user":{"id":"` + userID + `"}}}`
		resp := send(t, body)

		data, _ := resp["data"].(map[string]any)
		if _, hasFlags := data["flags"]; hasFlags && data["flags"].(float64) != 0 {
			t.Errorf("/profile(自分) は公開メッセージのはずだがflags = %v", data["flags"])
		}
		got := firstEmbedField(t, resp, "残高")
		if got != "1500.00" {
			t.Errorf("残高 field = %q, want %q", got, "1500.00")
		}
	})

	t.Run("/profile [@user] は指定したユーザーのプロフィールを返す", func(t *testing.T) {
		body := `{"type":2,"data":{"name":"profile","options":[{"name":"user","value":"` + userID + `"}]},"member":{"user":{"id":"someone-else"}}}`
		resp := send(t, body)

		got := firstEmbedField(t, resp, "残高")
		if got != "1500.00" {
			t.Errorf("残高 field = %q, want %q", got, "1500.00")
		}
	})

	t.Run("未登録ユーザーの/balanceはephemeralなエラーメッセージ", func(t *testing.T) {
		body := `{"type":2,"data":{"name":"balance"},"member":{"user":{"id":"test-cmd-nonexistent-user"}}}`
		resp := send(t, body)

		data, _ := resp["data"].(map[string]any)
		content, _ := data["content"].(string)
		if content == "" {
			t.Errorf("未登録ユーザーへのcontentが空: %v", resp)
		}
	})
}
