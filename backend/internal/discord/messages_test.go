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

	got, err := discord.CreateMessage(context.Background(), cfg, "chan-1", "📊 マーケット", nil)
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

	if err := discord.EditMessage(context.Background(), cfg, "chan-1", "msg-1", "更新後の本文", nil); err != nil {
		t.Fatalf("EditMessage: %v", err)
	}
	if gotMethod != http.MethodPatch {
		t.Errorf("method = %q, want PATCH", gotMethod)
	}
	if gotPath != "/channels/chan-1/messages/msg-1" {
		t.Errorf("path = %q", gotPath)
	}
}

// TestCreateMessage_componentsを送る は市場ティッカーの買う/売るボタン常設
// （issue #78）用に、componentsが渡された場合にリクエストボディへ含まれることを確認する。
func TestCreateMessage_componentsを送る(t *testing.T) {
	var gotBody struct {
		Components []discord.ActionRow `json:"components"`
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/channels/chan-1/messages", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "msg-1"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := discord.MessagesConfig{BotToken: "the-token", APIBaseURL: srv.URL}
	components := []discord.ActionRow{
		discord.NewActionRow(
			discord.Button{Type: 2, Style: discord.ButtonStyleSuccess, Label: "買う", CustomID: "order:long:JOG"},
			discord.Button{Type: 2, Style: discord.ButtonStyleDanger, Label: "売る", CustomID: "order:short:JOG"},
		),
	}

	if _, err := discord.CreateMessage(context.Background(), cfg, "chan-1", "content", components); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	if len(gotBody.Components) != 1 || len(gotBody.Components[0].Components) != 2 {
		t.Fatalf("components が正しく送られていない: %+v", gotBody.Components)
	}
	if gotBody.Components[0].Components[0].CustomID != "order:long:JOG" {
		t.Errorf("custom_id = %q, want order:long:JOG", gotBody.Components[0].Components[0].CustomID)
	}
}

// TestDeleteMessage は game.TickerService.Update の補償処理（投稿には成功したが
// IDの保存に失敗した場合に投稿を取り消す）が使うDELETEリクエストの形を確認する。
func TestDeleteMessage(t *testing.T) {
	var gotPath, gotMethod, gotAuth string

	mux := http.NewServeMux()
	mux.HandleFunc("/channels/chan-1/messages/msg-1", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := discord.MessagesConfig{BotToken: "the-token", APIBaseURL: srv.URL}

	if err := discord.DeleteMessage(context.Background(), cfg, "chan-1", "msg-1"); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	if gotMethod != http.MethodDelete {
		t.Errorf("method = %q, want DELETE", gotMethod)
	}
	if gotPath != "/channels/chan-1/messages/msg-1" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bot the-token" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bot the-token")
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

	if _, err := discord.CreateMessage(context.Background(), cfg, "chan-1", "content", nil); err == nil {
		t.Fatal("want error, got nil")
	}
}
