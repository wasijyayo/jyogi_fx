package server_test

import (
	"context"
	"crypto/ed25519"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"fxgame/backend/internal/discord"
	"fxgame/backend/internal/game"
	"fxgame/backend/internal/server"
)

// TestInteractions_未登録ユーザーの自動登録 は #86 の完了条件
// 「Webに一度もログインしていないDiscordユーザーが、/balance等からそのまま
// プレイを始められること」を、署名付きリクエストで確認する。
func TestInteractions_未登録ユーザーの自動登録(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := connectCommandsTestDB(t, ctx)

	newUserID := "test-autoreg-new-user"
	webUserID := "test-autoreg-web-user"
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE discord_id IN ($1, $2)`, newUserID, webUserID)
	})
	// 前回の実行が途中で落ちて残っていても「未登録」から始められるようにする。
	_, _ = pool.Exec(ctx, `DELETE FROM users WHERE discord_id = $1`, newUserID)

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	rankingSvc := game.NewRankingService(pool, game.RealClock{})
	profileSvc := game.NewProfileService(pool, game.RealClock{}, rankingSvc)
	mux := server.NewMux(server.Config{
		DiscordPublicKey: pub,
		Auth:             game.NewAuthService(pool, discord.OAuthConfig{}, game.RealClock{}),
		Ranking:          rankingSvc,
		Profile:          profileSvc,
		Clock:            fixedClock{now: testNow},
	})

	t.Run("未登録ユーザーの/balanceは初期資金1000で登録されて返る", func(t *testing.T) {
		body := `{"type":2,"data":{"name":"balance"},"member":{"user":{"id":"` + newUserID + `","username":"autoreg"}}}`
		resp := sendInteraction(t, mux, priv, body)

		if got := embedFieldValue(t, resp, 0, "autoreg"); got != "1000.00" {
			t.Errorf("balance field = %q, want %q", got, "1000.00")
		}
	})

	t.Run("2回目以降のInteractionで初期資金が二重付与されない", func(t *testing.T) {
		// 取引等で残高が動いた状態を作ってから、もう一度Interactionを送る。
		if _, err := pool.Exec(ctx, `UPDATE users SET balance = 250 WHERE discord_id = $1`, newUserID); err != nil {
			t.Fatalf("update balance: %v", err)
		}
		body := `{"type":2,"data":{"name":"balance"},"member":{"user":{"id":"` + newUserID + `","username":"autoreg"}}}`
		resp := sendInteraction(t, mux, priv, body)

		if got := embedFieldValue(t, resp, 0, "autoreg"); got != "250.00" {
			t.Errorf("balance field = %q, want %q", got, "250.00")
		}
	})

	t.Run("Web登録済みユーザーの残高・表示名は変わらない", func(t *testing.T) {
		setupCommandsTestUser(t, ctx, pool, webUserID, "Web表示名", decimal.NewFromInt(1500))
		body := `{"type":2,"data":{"name":"balance"},"member":{"user":{"id":"` + webUserID + `","username":"discord-name"}}}`
		resp := sendInteraction(t, mux, priv, body)

		if got := embedFieldValue(t, resp, 0, "Web表示名"); got != "1500.00" {
			t.Errorf("balance field = %q, want %q", got, "1500.00")
		}
	})
}
