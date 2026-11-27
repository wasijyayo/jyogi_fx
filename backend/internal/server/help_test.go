package server_test

import (
	"crypto/ed25519"
	"strings"
	"testing"

	"fxgame/backend/internal/server"
)

// TestHelpCommand は /help が遊び方の Embed を本人にだけ返し、
// PUBLIC_BASE_URL があるときだけ Web へのリンクボタンを付けることを確かめる。
// DB を使わないのでローカル Postgres が無くても走る。
func TestHelpCommand(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	body := `{"type":2,"data":{"name":"help"},"member":{"user":{"id":"test-help-user"}}}`

	t.Run("案内のEmbedがephemeralで返る", func(t *testing.T) {
		mux := server.NewMux(server.Config{DiscordPublicKey: pub, Clock: fixedClock{now: testNow}})
		resp := sendInteraction(t, mux, priv, body)

		data, _ := resp["data"].(map[string]any)
		if flags, _ := data["flags"].(float64); int(flags) != 64 {
			t.Errorf("flags = %v, want 64 (ephemeral)", data["flags"])
		}
		embeds, _ := data["embeds"].([]any)
		if len(embeds) != 1 {
			t.Fatalf("embeds = %d, want 1: %v", len(embeds), data)
		}
		embed, _ := embeds[0].(map[string]any)
		if title, _ := embed["title"].(string); !strings.Contains(title, "遊び方") {
			t.Errorf("title = %q, want 遊び方を含む", title)
		}
		if got := embedFieldValue(t, resp, 0, "🕛 取引時間"); !strings.Contains(got, "12:00〜13:00") {
			t.Errorf("取引時間 = %q", got)
		}
		if buttons := responseComponentButtons(resp); len(buttons) != 0 {
			t.Errorf("PUBLIC_BASE_URL未設定なのにボタンがある: %v", buttons)
		}
	})

	t.Run("PUBLIC_BASE_URL設定時はWebへのリンクボタンが付く", func(t *testing.T) {
		mux := server.NewMux(server.Config{DiscordPublicKey: pub, PublicBaseURL: "https://fx.example.run.app/", Clock: fixedClock{now: testNow}})
		resp := sendInteraction(t, mux, priv, body)

		var urls []string
		for _, b := range responseComponentButtons(resp) {
			if u, _ := b["url"].(string); u != "" {
				urls = append(urls, u)
			}
		}
		want := []string{"https://fx.example.run.app/", "https://fx.example.run.app/replay"}
		if strings.Join(urls, ",") != strings.Join(want, ",") {
			t.Errorf("urls = %v, want %v", urls, want)
		}
	})
}
