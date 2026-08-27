package game_test

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
)

// fixedClock は CLAUDE.md §5.1 のとおり時刻を注入するためのテスト用実装。
type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

// TestAuthService_HandleCallback は WS-6 の完了条件（Discord OAuth ログインで
// ユーザー作成 + Cookie セッション発行までが動く）を、ローカル Postgres と
// 偽の Discord API サーバーに対して確認する。
func TestAuthService_HandleCallback(t *testing.T) {
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

	const discordUserID = "ws6-verify-user"

	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "dummy-token"})
	})
	mux.HandleFunc("GET /user", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(discord.User{ID: discordUserID, Username: "WS-6 動作確認ユーザー"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	oauth := discord.OAuthConfig{
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		RedirectURI:  "http://localhost:8080/auth/discord/callback",
		TokenURL:     srv.URL + "/token",
		UserURL:      srv.URL + "/user",
	}
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	authSvc := game.NewAuthService(pool, oauth, fixedClock{now: now})

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM sessions_auth WHERE user_id = $1`, discordUserID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE discord_id = $1`, discordUserID)
	})

	session, err := authSvc.HandleCallback(ctx, "the-code")
	if err != nil {
		t.Fatalf("HandleCallback: %v", err)
	}

	if session.UserID != discordUserID {
		t.Errorf("UserID = %q, want %q", session.UserID, discordUserID)
	}
	if session.ID == "" {
		t.Error("session ID is empty")
	}
	wantExpiry := now.Add(30 * 24 * time.Hour)
	if !session.ExpiresAt.Equal(wantExpiry) {
		t.Errorf("ExpiresAt = %v, want %v", session.ExpiresAt, wantExpiry)
	}

	// users テーブルに作成されたことを確認
	var displayName string
	if err := pool.QueryRow(ctx, `SELECT display_name FROM users WHERE discord_id = $1`, discordUserID).Scan(&displayName); err != nil {
		t.Fatalf("select user: %v", err)
	}
	if displayName != "WS-6 動作確認ユーザー" {
		t.Errorf("display_name = %q, want %q", displayName, "WS-6 動作確認ユーザー")
	}

	// sessions_auth に発行されたことを確認
	var userID string
	if err := pool.QueryRow(ctx, `SELECT user_id FROM sessions_auth WHERE id = $1`, session.ID).Scan(&userID); err != nil {
		t.Fatalf("select session: %v", err)
	}
	if userID != discordUserID {
		t.Errorf("sessions_auth.user_id = %q, want %q", userID, discordUserID)
	}
}
