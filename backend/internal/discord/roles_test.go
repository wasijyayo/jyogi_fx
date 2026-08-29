package discord_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"fxgame/backend/internal/discord"
)

// TestAddGuildMemberRole は design.md §6.10「ロール自動付与」の最初の実装
// （#84「人生の勝者」ロール）が使うPUTリクエストの形（パス・メソッド・認証ヘッダ）を確認する。
func TestAddGuildMemberRole(t *testing.T) {
	var gotPath, gotAuth, gotMethod string

	mux := http.NewServeMux()
	mux.HandleFunc("/guilds/guild-1/members/user-1/roles/role-1", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotMethod = r.Method
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := discord.MessagesConfig{BotToken: "the-token", APIBaseURL: srv.URL}

	if err := discord.AddGuildMemberRole(context.Background(), cfg, "guild-1", "user-1", "role-1"); err != nil {
		t.Fatalf("AddGuildMemberRole: %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("method = %q, want PUT", gotMethod)
	}
	if gotPath != "/guilds/guild-1/members/user-1/roles/role-1" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bot the-token" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bot the-token")
	}
}

// TestAddGuildMemberRole_Discordがエラーを返したら失敗として扱う は
// 呼び出し側（game.LifeWinnerService）がDB側の冪等フラグを更新せず、
// 次の決済で安全に再試行できる前提（エラーをそのまま返す）を確認する。
func TestAddGuildMemberRole_Discordがエラーを返したら失敗として扱う(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/guilds/guild-1/members/user-1/roles/role-1", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"Missing Permissions"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := discord.MessagesConfig{BotToken: "the-token", APIBaseURL: srv.URL}

	if err := discord.AddGuildMemberRole(context.Background(), cfg, "guild-1", "user-1", "role-1"); err == nil {
		t.Fatal("want error, got nil")
	}
}
