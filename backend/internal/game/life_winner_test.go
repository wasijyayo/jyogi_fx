package game

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"fxgame/backend/internal/db"
	"fxgame/backend/internal/discord"
)

// TestLifeWinnerService_nilレシーバは何もせずパニックしない は、TradeServiceに
// LifeWinnerServiceを設定しない（未設定環境・多くの既存テスト）でも安全に
// 呼べることを確認する（NotifyService.postと同じnilセーフの考え方）。
func TestLifeWinnerService_nilレシーバは何もせずパニックしない(t *testing.T) {
	var s *LifeWinnerService
	s.GrantIfEligible(context.Background(), "u1", "太郎", false, decimal.NewFromInt(99999))
}

// newTestLifeWinnerServer はギルドロール付与用のモックDiscordサーバーを組み立てる。
func newTestLifeWinnerServer(t *testing.T) (*httptest.Server, *int) {
	t.Helper()
	calls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/guilds/guild-1/members/u1/roles/role-1", func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &calls
}

// TestLifeWinnerService_GrantIfEligible_ガード は、閾値未満・既に付与済み・
// guildID/roleID未設定のいずれでもDiscord APIを呼ばないことを確認する
// （#84完了条件「DISCORD_GUILD_ID/DISCORD_LIFE_WINNER_ROLE_ID未設定でも
// 既存の決済・通知フローに影響しない」）。
func TestLifeWinnerService_GrantIfEligible_ガード(t *testing.T) {
	srv, calls := newTestLifeWinnerServer(t)
	messages := discord.MessagesConfig{BotToken: "test-token", APIBaseURL: srv.URL}

	tests := []struct {
		name           string
		guildID        string
		roleID         string
		threshold      decimal.Decimal
		alreadyGranted bool
		lifetimePips   decimal.Decimal
	}{
		{"閾値未満", "guild-1", "role-1", decimal.NewFromInt(10000), false, decimal.NewFromInt(9999)},
		{"既に付与済み", "guild-1", "role-1", decimal.NewFromInt(10000), true, decimal.NewFromInt(20000)},
		{"guildID未設定", "", "role-1", decimal.NewFromInt(10000), false, decimal.NewFromInt(20000)},
		{"roleID未設定", "guild-1", "", decimal.NewFromInt(10000), false, decimal.NewFromInt(20000)},
		{"閾値がゼロ以下(無効化)", "guild-1", "role-1", decimal.Zero, false, decimal.NewFromInt(20000)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			*calls = 0
			s := NewLifeWinnerService(nil, messages, nil, tt.guildID, tt.roleID, tt.threshold)
			s.GrantIfEligible(context.Background(), "u1", "太郎", tt.alreadyGranted, tt.lifetimePips)
			if *calls != 0 {
				t.Errorf("Discord APIが呼ばれた: calls = %d, want 0", *calls)
			}
		})
	}
}

// TestLifeWinnerService_GrantIfEligible_閾値到達で付与しDBフラグと通知が更新される は
// #84の完了条件そのもの（「閾値到達で1回だけロールが付与され、通知が1回だけ
// 投稿される」）を確認する。
func TestLifeWinnerService_GrantIfEligible_閾値到達で付与しDBフラグと通知が更新される(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)
	q := db.New(pool)

	userID := "u1"
	_, _ = pool.Exec(ctx, `DELETE FROM users WHERE discord_id = $1`, userID)
	if _, err := q.UpsertUser(ctx, db.UpsertUserParams{DiscordID: userID, DisplayName: "太郎"}); err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE discord_id = $1`, userID)
	})

	srv, calls := newTestLifeWinnerServer(t)
	messages := discord.MessagesConfig{BotToken: "test-token", APIBaseURL: srv.URL}
	notify, rec := newRecordingNotifyService(t)

	s := NewLifeWinnerService(pool, messages, notify, "guild-1", "role-1", decimal.NewFromInt(10000))
	s.GrantIfEligible(ctx, userID, "太郎", false, decimal.NewFromInt(10000))

	if *calls != 1 {
		t.Fatalf("ロール付与APIの呼び出し回数 = %d, want 1", *calls)
	}
	if len(rec.posts) != 1 {
		t.Fatalf("通知投稿数 = %d, want 1", len(rec.posts))
	}
	if !containsAll(rec.posts[0], "太郎", "10000pips", "人生の勝者") {
		t.Errorf("通知content = %q", rec.posts[0])
	}

	got, err := q.GetUser(ctx, userID)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if !got.LifeWinnerGranted {
		t.Error("life_winner_granted = false, want true")
	}
}

// containsAll は s が全ての substrs を含むかを返す小さなテストヘルパー。
func containsAll(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
