package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

// replayRankingCleanup はランキング API テストの後始末（子テーブルから順）。
var replayRankingCleanup = []string{
	`DELETE FROM replay_positions`,
	`DELETE FROM replay_leverage_unlocks`,
	`DELETE FROM replay_speed_changes`,
	`DELETE FROM replay_game_rates`,
	`DELETE FROM replay_game_days`,
	`DELETE FROM replay_games`,
}

// TestReplayAPI_Ranking は GET /api/replay/ranking がログインユーザー（アイコン付き）と
// ゲスト（ゲスト印付き・アイコンなし）を同じ一覧で返し、取得時に期限切れの未確定ゲームを
// 確定させることを確認する（#104 完了条件）。
func TestReplayAPI_Ranking(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	for _, s := range replayRankingCleanup {
		if _, err := pool.Exec(ctx, s); err != nil {
			t.Fatalf("pre-cleanup %q: %v", s, err)
		}
	}

	const userID = "777000000000000001"
	// ユーザーの後始末を先に登録する（LIFO で replay 系の削除が先に走る）。
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM users WHERE discord_id = $1`, userID); err != nil {
			t.Errorf("cleanup ranking user: %v", err)
		}
	})
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (discord_id, display_name, avatar) VALUES ($1, $2, $3)
		ON CONFLICT (discord_id) DO UPDATE SET display_name = EXCLUDED.display_name, avatar = EXCLUDED.avatar`,
		userID, "Ranking User", "avatar-hash"); err != nil {
		t.Fatalf("insert ranking user: %v", err)
	}
	t.Cleanup(func() {
		for _, s := range replayRankingCleanup {
			if _, err := pool.Exec(context.Background(), s); err != nil {
				t.Errorf("cleanup %q: %v", s, err)
			}
		}
	})

	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	finishedAt := now.Add(-time.Minute)

	// ログインユーザーの確定済みゲーム。
	if _, err := pool.Exec(ctx, `
		INSERT INTO replay_games (
			token_hash, user_id, guest_name, started_at, start_year, status,
			funds, final_funds, finished_at
		) VALUES ($1, $2, NULL, $3, 2015, 'finished', $4, $4, $5)`,
		"api-ranking-user", userID, finishedAt.Add(-180*time.Second), "999999002", finishedAt); err != nil {
		t.Fatalf("insert user game: %v", err)
	}
	// ゲストの確定済みゲーム。
	if _, err := pool.Exec(ctx, `
		INSERT INTO replay_games (
			token_hash, user_id, guest_name, started_at, start_year, status,
			funds, final_funds, finished_at
		) VALUES ($1, NULL, $2, $3, 2015, 'finished', $4, $4, $5)`,
		"api-ranking-guest", "guest-player", finishedAt.Add(-180*time.Second), "999999001", finishedAt); err != nil {
		t.Fatalf("insert guest game: %v", err)
	}
	// 期限切れだが未確定（status = playing）のゲーム。取得時に確定される。
	startedAt := now.Add(-400 * time.Second)
	if _, err := pool.Exec(ctx, `
		INSERT INTO replay_games (
			token_hash, user_id, guest_name, started_at, start_year, status, funds
		) VALUES ($1, NULL, $2, $3, 2015, 'playing', $4)`,
		"api-ranking-expired", "expired-player", startedAt, "999999003"); err != nil {
		t.Fatalf("insert playing game: %v", err)
	}

	mux := newReplayMux(pool, fixedClock{now: now})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/replay/ranking", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ranking status = %d, body=%s", rec.Code, rec.Body.String())
	}

	var entries []struct {
		Rank       int     `json:"rank"`
		Name       string  `json:"name"`
		IsGuest    bool    `json:"isGuest"`
		AvatarUrl  *string `json:"avatarUrl"`
		FinalFunds string  `json:"finalFunds"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &entries); err != nil {
		t.Fatalf("decode ranking: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("ranking length = %d, want 3: %s", len(entries), rec.Body.String())
	}
	// ゲストの avatarUrl はキー自体を省略せず null を返す。
	if !strings.Contains(rec.Body.String(), `"avatarUrl":null`) {
		t.Errorf("guest avatarUrl must be null in JSON: %s", rec.Body.String())
	}

	for i, e := range entries {
		if e.Rank != i+1 {
			t.Errorf("entries[%d].Rank = %d, want %d", i, e.Rank, i+1)
		}
	}

	// 表示順はスコアの降順。未確定だったゲームも確定されて先頭に来る。
	if entries[0].Name != "expired-player" || entries[0].FinalFunds != "999999003" {
		t.Errorf("entries[0] = %+v, want expired-player/999999003", entries[0])
	}
	if !entries[0].IsGuest {
		t.Error("expired-player must be marked as guest")
	}
	if entries[0].AvatarUrl != nil {
		t.Errorf("expired-player avatarUrl = %v, want nil", entries[0].AvatarUrl)
	}

	var userSeen, guestSeen bool
	for _, e := range entries {
		switch e.Name {
		case "Ranking User":
			userSeen = true
			if e.IsGuest {
				t.Error("Ranking User must not be marked as guest")
			}
			want := "https://cdn.discordapp.com/avatars/" + userID + "/avatar-hash.png"
			if e.AvatarUrl == nil || *e.AvatarUrl != want {
				t.Errorf("Ranking User avatarUrl = %v, want %s", e.AvatarUrl, want)
			}
			if e.FinalFunds != "999999002" {
				t.Errorf("Ranking User finalFunds = %s, want 999999002", e.FinalFunds)
			}
		case "guest-player":
			guestSeen = true
			if !e.IsGuest {
				t.Error("guest-player must be marked as guest")
			}
			if e.AvatarUrl != nil {
				t.Errorf("guest-player avatarUrl = %v, want nil", e.AvatarUrl)
			}
			if e.FinalFunds != "999999001" {
				t.Errorf("guest-player finalFunds = %s, want 999999001", e.FinalFunds)
			}
		}
	}
	if !userSeen || !guestSeen {
		t.Errorf("ranking is missing rows: user=%v guest=%v (%s)", userSeen, guestSeen, rec.Body.String())
	}

	// 期限切れのゲームは論理的な終了時刻（開始 + 180 秒）で確定している。
	var status string
	var gotFinishedAt time.Time
	var finalFunds decimal.Decimal
	if err := pool.QueryRow(ctx,
		`SELECT status, finished_at, final_funds FROM replay_games WHERE token_hash = $1`,
		"api-ranking-expired").Scan(&status, &gotFinishedAt, &finalFunds); err != nil {
		t.Fatalf("query expired game: %v", err)
	}
	if status != "finished" {
		t.Errorf("expired game status = %s, want finished", status)
	}
	if want := startedAt.Add(180 * time.Second); !gotFinishedAt.Equal(want) {
		t.Errorf("expired game finished_at = %s, want %s", gotFinishedAt, want)
	}
	if want := decimal.RequireFromString("999999003"); !finalFunds.Equal(want) {
		t.Errorf("expired game final_funds = %s, want %s", finalFunds, want)
	}
}
