package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"fxgame/backend/internal/discord"
	"fxgame/backend/internal/game"
	"fxgame/backend/internal/server"
)

// TestGetMe は WS-7 の完了条件（/api/me がユーザー情報を返す）を
// ローカル Postgres に対して確認する。
func TestGetMe(t *testing.T) {
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
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		t.Skipf("ローカル Postgres に接続できないためスキップ（docker compose up -d を実行してください）: %v", err)
	}

	const discordID = "ws7-verify-user"
	const sessionID = "ws7-verify-session"

	if _, err := pool.Exec(ctx,
		`INSERT INTO users (discord_id, display_name) VALUES ($1, $2)
		 ON CONFLICT (discord_id) DO UPDATE SET display_name = EXCLUDED.display_name`,
		discordID, "WS-7 動作確認ユーザー",
	); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO sessions_auth (id, user_id, expires_at) VALUES ($1, $2, now() + interval '1 day')
		 ON CONFLICT (id) DO UPDATE SET expires_at = EXCLUDED.expires_at`,
		sessionID, discordID,
	); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM sessions_auth WHERE id = $1`, sessionID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE discord_id = $1`, discordID)
	})

	authSvc := game.NewAuthService(pool, discord.OAuthConfig{}, game.RealClock{})
	mux := server.NewMux(server.Config{Auth: authSvc, SecureCookies: true})

	t.Run("no cookie", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
	})

	t.Run("valid session", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: sessionID})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
		}

		var got struct {
			DiscordID   string `json:"discordId"`
			DisplayName string `json:"displayName"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if got.DiscordID != discordID {
			t.Errorf("discordId = %q, want %q", got.DiscordID, discordID)
		}
		if got.DisplayName != "WS-7 動作確認ユーザー" {
			t.Errorf("displayName = %q, want %q", got.DisplayName, "WS-7 動作確認ユーザー")
		}
	})
}
