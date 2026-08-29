package discord_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"fxgame/backend/internal/discord"
)

// TestCreateMessage は #43 NOTIFY-1「専用チャンネルに1つのメッセージを投稿」の
// POSTリクエストの形（パス・メソッド・認証ヘッダ・本文）を確認する。
func TestCreateMessage(t *testing.T) {
	var gotPath, gotAuth, gotMethod string
	var gotBody struct {
		Content string `json:"content"`
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/channels/chan-1/messages", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotMethod = r.Method
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "msg-1"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := discord.MessagesConfig{BotToken: "the-token", APIBaseURL: srv.URL}

	got, err := discord.CreateMessage(context.Background(), cfg, "chan-1", "📊 マーケット")
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	if got != "msg-1" {
		t.Errorf("messageID = %q, want %q", got, "msg-1")
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/channels/chan-1/messages" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bot the-token" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bot the-token")
	}
	if gotBody.Content != "📊 マーケット" {
		t.Errorf("content = %q", gotBody.Content)
	}
}

// TestEditMessage は「新規投稿ではなく編集」（design.md §6.4）のPATCHリクエストを確認する。
func TestEditMessage(t *testing.T) {
	var gotPath, gotMethod string

	mux := http.NewServeMux()
	mux.HandleFunc("/channels/chan-1/messages/msg-1", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "msg-1"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := discord.MessagesConfig{BotToken: "the-token", APIBaseURL: srv.URL}

	if err := discord.EditMessage(context.Background(), cfg, "chan-1", "msg-1", "更新後の本文"); err != nil {
		t.Fatalf("EditMessage: %v", err)
	}
	if gotMethod != http.MethodPatch {
		t.Errorf("method = %q, want PATCH", gotMethod)
	}
	if gotPath != "/channels/chan-1/messages/msg-1" {
		t.Errorf("path = %q", gotPath)
	}
}

// TestCreateMessage_Discordがエラーを返したら失敗として扱う は
// 「Discord API のレート制限に注意」（issue #43）に対する呼び出し側の前提
// （エラーをそのまま返すのでticker_msg_idを更新せず、次tickで再試行できる）を確認する。
func TestCreateMessage_Discordがエラーを返したら失敗として扱う(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/channels/chan-1/messages", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"message":"You are being rate limited.","retry_after":1.0}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := discord.MessagesConfig{BotToken: "the-token", APIBaseURL: srv.URL}

	if _, err := discord.CreateMessage(context.Background(), cfg, "chan-1", "content"); err == nil {
		t.Fatal("want error, got nil")
	}
}
