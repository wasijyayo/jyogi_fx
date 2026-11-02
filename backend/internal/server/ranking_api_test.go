package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

// cleanupReplayRankingRows はこのテストで INSERT したゲーム・ユーザーだけを削除する。
// go test ./... はパッケージを並列に走らせるため、WHERE 無しの DELETE は使わない
// （他のパッケージのテストデータを消してしまう）。削除は子テーブルから順に行い、
// 失敗は t.Errorf で検出する。
func cleanupReplayRankingRows(t *testing.T, pool *pgxpool.Pool, gameIDs []int64, userIDs []string) {
	t.Helper()
	cctx := context.Background()
	if len(gameIDs) > 0 {
		for _, table := range []string{
			"replay_leverage_unlock_payments",
			"replay_positions",
			"replay_leverage_unlocks",
			"replay_speed_changes",
			"replay_game_rates",
			"replay_game_days",
			"replay_game_events",
		} {
			query := "DELETE FROM " + table + " WHERE game_id = ANY($1)"
			if _, err := pool.Exec(cctx, query, gameIDs); err != nil {
				t.Errorf("cleanup %s: %v", table, err)
			}
		}
		if _, err := pool.Exec(cctx, `DELETE FROM replay_games WHERE id = ANY($1)`, gameIDs); err != nil {
			t.Errorf("cleanup replay_games: %v", err)
		}
	}
	if len(userIDs) > 0 {
		if _, err := pool.Exec(cctx, `DELETE FROM users WHERE discord_id = ANY($1)`, userIDs); err != nil {
			t.Errorf("cleanup users: %v", err)
		}
	}
}

// TestReplayAPI_Ranking は GET /api/replay/ranking がログインユーザー（アイコン付き）と
// ゲスト（ゲスト印付き・アイコンなし）を同じ一覧で返し、取得時に期限切れの未確定ゲームを
// 確定させることを確認する（#104 完了条件）。
//
// 時計は他のテストのゲーム開始時刻（2026 年）より前の 2025-06-01 にする。ランキング取得は
// status = playing のゲームをすべて advance するため、未来に開始した他テストのゲームは
// 公開日数 0 のまま影響を受けない。スコアは 900000000 台の大きな値を使い、全体の一覧では
// なく自分の行どうしの相対順序だけを検証する（並列実行される他パッケージのデータに左右
// されないため）。
func TestReplayAPI_Ranking(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	const userID = "777000000000000001"
	gameIDs := make([]int64, 0, 3)
	userIDs := []string{userID}
	// 挿入前から登録しておく（途中で失敗しても挿入済みの行は消す）。
	t.Cleanup(func() { cleanupReplayRankingRows(t, pool, gameIDs, userIDs) })

	if _, err := pool.Exec(ctx, `
		INSERT INTO users (discord_id, display_name, avatar) VALUES ($1, $2, $3)
		ON CONFLICT (discord_id) DO UPDATE SET display_name = EXCLUDED.display_name, avatar = EXCLUDED.avatar`,
		userID, "Ranking User", "avatar-hash"); err != nil {
		t.Fatalf("insert ranking user: %v", err)
	}

	now := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	finishedAt := now.Add(-time.Minute)

	// ログインユーザーの確定済みゲーム。
	var id int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO replay_games (
			token_hash, user_id, guest_name, started_at, start_year, status,
			funds, final_funds, finished_at
		) VALUES ($1, $2, NULL, $3, 2015, 'finished', $4, $4, $5)
		RETURNING id`,
		"api-ranking-user", userID, finishedAt.Add(-180*time.Second), "999999991", finishedAt).Scan(&id); err != nil {
		t.Fatalf("insert user game: %v", err)
	}
	gameIDs = append(gameIDs, id)

	// ゲストの確定済みゲーム。
	if err := pool.QueryRow(ctx, `
		INSERT INTO replay_games (
			token_hash, user_id, guest_name, started_at, start_year, status,
			funds, final_funds, finished_at
		) VALUES ($1, NULL, $2, $3, 2015, 'finished', $4, $4, $5)
		RETURNING id`,
		"api-ranking-guest", "guest-player", finishedAt.Add(-180*time.Second), "999999990", finishedAt).Scan(&id); err != nil {
		t.Fatalf("insert guest game: %v", err)
	}
	gameIDs = append(gameIDs, id)

	// 期限切れだが未確定（status = playing）のゲーム。テスト時計の 10 分前に開始した
	// ものにし、取得時に確定されることを確認する。
	startedAt := now.Add(-10 * time.Minute)
	if err := pool.QueryRow(ctx, `
		INSERT INTO replay_games (
			token_hash, user_id, guest_name, started_at, start_year, status, funds
		) VALUES ($1, NULL, $2, $3, 2015, 'playing', $4)
		RETURNING id`,
		"api-ranking-expired", "expired-player", startedAt, "999999992").Scan(&id); err != nil {
		t.Fatalf("insert playing game: %v", err)
	}
	gameIDs = append(gameIDs, id)

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
	if len(entries) > 20 {
		t.Fatalf("ranking length = %d, want <= 20", len(entries))
	}
	// ゲストの avatarUrl はキー自体を省略せず null を返す。
	if !strings.Contains(rec.Body.String(), `"avatarUrl":null`) {
		t.Errorf("guest avatarUrl must be null in JSON: %s", rec.Body.String())
	}

	for i, e := range entries {
		if e.Rank != i+1 {
			t.Errorf("entries[%d].Rank = %d, want %d", i, e.Rank, i+1)
		}
		if i > 0 {
			prev, err := decimal.NewFromString(entries[i-1].FinalFunds)
			if err != nil {
				t.Fatalf("decode previous finalFunds %q: %v", entries[i-1].FinalFunds, err)
			}
			cur, err := decimal.NewFromString(e.FinalFunds)
			if err != nil {
				t.Fatalf("decode finalFunds %q: %v", e.FinalFunds, err)
			}
			if cur.GreaterThan(prev) {
				t.Errorf("ranking is not sorted by finalFunds: %s then %s", entries[i-1].FinalFunds, e.FinalFunds)
			}
		}
	}

	// 自分の行を名前とスコアで探し、相対順序と表示内容を確認する。
	indexOf := func(name, funds string) int {
		for i, e := range entries {
			if e.Name == name && e.FinalFunds == funds {
				return i
			}
		}
		return -1
	}
	expiredIdx := indexOf("expired-player", "999999992")
	userIdx := indexOf("Ranking User", "999999991")
	guestIdx := indexOf("guest-player", "999999990")
	if expiredIdx < 0 || userIdx < 0 || guestIdx < 0 {
		t.Fatalf("ranking is missing test rows (expired=%d user=%d guest=%d): %s",
			expiredIdx, userIdx, guestIdx, rec.Body.String())
	}
	if expiredIdx >= userIdx || userIdx >= guestIdx {
		t.Errorf("ranking order wrong: expired=%d user=%d guest=%d", expiredIdx, userIdx, guestIdx)
	}

	expired := entries[expiredIdx]
	if !expired.IsGuest {
		t.Error("expired-player must be marked as guest")
	}
	if expired.AvatarUrl != nil {
		t.Errorf("expired-player avatarUrl = %v, want nil", expired.AvatarUrl)
	}

	user := entries[userIdx]
	if user.IsGuest {
		t.Error("Ranking User must not be marked as guest")
	}
	wantAvatar := "https://cdn.discordapp.com/avatars/" + userID + "/avatar-hash.png"
	if user.AvatarUrl == nil || *user.AvatarUrl != wantAvatar {
		t.Errorf("Ranking User avatarUrl = %v, want %s", user.AvatarUrl, wantAvatar)
	}

	guest := entries[guestIdx]
	if !guest.IsGuest {
		t.Error("guest-player must be marked as guest")
	}
	if guest.AvatarUrl != nil {
		t.Errorf("guest-player avatarUrl = %v, want nil", guest.AvatarUrl)
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
	if want := decimal.RequireFromString("999999992"); !finalFunds.Equal(want) {
		t.Errorf("expired game final_funds = %s, want %s", finalFunds, want)
	}
}
