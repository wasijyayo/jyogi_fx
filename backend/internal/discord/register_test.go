package discord_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"fxgame/backend/internal/discord"
)

func TestRegisterCommands(t *testing.T) {
	t.Run("ギルドID指定でギルド限定エンドポイントに登録する", func(t *testing.T) {
		var gotPath, gotAuth, gotMethod string
		var gotBody []discord.Command

		mux := http.NewServeMux()
		mux.HandleFunc("/applications/app-1/guilds/guild-1/commands", func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			gotAuth = r.Header.Get("Authorization")
			gotMethod = r.Method
			if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(gotBody)
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()

		cfg := discord.RegisterCommandsConfig{
			BotToken:      "the-token",
			ApplicationID: "app-1",
			GuildID:       "guild-1",
			APIBaseURL:    srv.URL,
		}

		if err := discord.RegisterCommands(context.Background(), cfg, discord.Commands); err != nil {
			t.Fatalf("RegisterCommands: %v", err)
		}

		if gotMethod != http.MethodPut {
			t.Errorf("method = %q, want PUT", gotMethod)
		}
		if gotPath != "/applications/app-1/guilds/guild-1/commands" {
			t.Errorf("path = %q", gotPath)
		}
		if gotAuth != "Bot the-token" {
			t.Errorf("Authorization = %q, want %q", gotAuth, "Bot the-token")
		}
		if len(gotBody) != len(discord.Commands) {
			t.Errorf("registered %d commands, want %d", len(gotBody), len(discord.Commands))
		}
	})

	t.Run("ギルドID未指定ならグローバルエンドポイントに登録する", func(t *testing.T) {
		var gotPath string

		mux := http.NewServeMux()
		mux.HandleFunc("/applications/app-1/commands", func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode([]discord.Command{})
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()

		cfg := discord.RegisterCommandsConfig{
			BotToken:      "the-token",
			ApplicationID: "app-1",
			APIBaseURL:    srv.URL,
		}

		if err := discord.RegisterCommands(context.Background(), cfg, discord.Commands); err != nil {
			t.Fatalf("RegisterCommands: %v", err)
		}
		if gotPath != "/applications/app-1/commands" {
			t.Errorf("path = %q, want global endpoint (guild id not in path)", gotPath)
		}
	})

	t.Run("Discordがエラーを返したら失敗として扱う", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/applications/app-1/commands", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"401: Unauthorized"}`))
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()

		cfg := discord.RegisterCommandsConfig{
			BotToken:      "bad-token",
			ApplicationID: "app-1",
			APIBaseURL:    srv.URL,
		}

		if err := discord.RegisterCommands(context.Background(), cfg, discord.Commands); err == nil {
			t.Fatal("want error, got nil")
		}
	})
}

// コマンド名の重複は Discord 側が 400 で弾くが、こちらは唯一の定義元（#29）なので
// 事前に気づけるようにしておく。
func TestCommands_名前が重複していない(t *testing.T) {
	seen := make(map[string]bool)
	for _, c := range discord.Commands {
		if seen[c.Name] {
			t.Errorf("duplicate command name: %q", c.Name)
		}
		seen[c.Name] = true
	}
}
