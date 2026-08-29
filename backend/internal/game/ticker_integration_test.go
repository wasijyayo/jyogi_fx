package game

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"fxgame/backend/internal/db"
	"fxgame/backend/internal/discord"
)

// TestTickerService_Update は #43 NOTIFY-1 の完了条件
// 「セッション中、1つのメッセージが毎分更新され続けること（新規投稿が増えないこと）」を確認する。
//
// 1回目の呼び出しは新規投稿（POST）し、返ってきたメッセージIDを game_sessions に保存する。
// 2回目以降は保存したIDを編集（PATCH）するだけで、POSTが増えないことを確認する。
func TestTickerService_Update(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)
	q := db.New(pool)

	epoch := time.Date(2099, 3, 1, 12, 0, 0, 0, jst) // 実運用と衝突しない架空の未来日
	c := insertTestCurrency(t, ctx, pool, "TICKERSVC", 555001, epoch)

	sessionDate := pgtype.Date{Time: time.Date(2099, 3, 1, 0, 0, 0, 0, jst), Valid: true}
	t.Cleanup(func() {
		cctx := context.Background()
		if s, err := q.GetGameSessionByDate(cctx, sessionDate); err == nil {
			_, _ = pool.Exec(cctx, `DELETE FROM price_ticks WHERE session_id = $1`, s.ID)
			_, _ = pool.Exec(cctx, `DELETE FROM game_sessions WHERE id = $1`, s.ID)
		}
		_, _ = pool.Exec(cctx, `DELETE FROM currencies WHERE id = $1`, c.ID)
	})

	session, err := q.CreateGameSession(ctx, db.CreateGameSessionParams{
		Date:     sessionDate,
		Seed:     1,
		OpenedAt: pgtype.Timestamptz{Time: epoch, Valid: true},
		ClosedAt: pgtype.Timestamptz{Time: epoch.Add(time.Hour), Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateGameSession: %v", err)
	}

	if err := openCurrency(ctx, q, session, c, epoch); err != nil {
		t.Fatalf("openCurrency(寄り付き): %v", err)
	}
	now := epoch.Add(time.Minute)
	if err := writePriceTick(ctx, q, session, c, now); err != nil {
		t.Fatalf("writePriceTick: %v", err)
	}

	var createCalls, editCalls int
	var lastContent string
	mux := http.NewServeMux()
	mux.HandleFunc("/channels/ticker-chan/messages", func(w http.ResponseWriter, r *http.Request) {
		createCalls++
		var body struct {
			Content string `json:"content"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		lastContent = body.Content
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "ticker-msg-1"})
	})
	mux.HandleFunc("/channels/ticker-chan/messages/ticker-msg-1", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Errorf("method = %s, want PATCH", r.Method)
		}
		editCalls++
		var body struct {
			Content string `json:"content"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		lastContent = body.Content
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "ticker-msg-1"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ticker := NewTickerService(pool, RealClock{}, discord.MessagesConfig{
		BotToken:   "test-token",
		APIBaseURL: srv.URL,
	}, "ticker-chan")

	if err := ticker.Update(ctx, now, session); err != nil {
		t.Fatalf("Update(1回目): %v", err)
	}
	if createCalls != 1 {
		t.Fatalf("createCalls = %d, want 1", createCalls)
	}
	if !strings.Contains(lastContent, c.Symbol) {
		t.Errorf("content に通貨シンボル %q が含まれない: %s", c.Symbol, lastContent)
	}
	if !strings.Contains(lastContent, tickerDivider) {
		t.Errorf("content に区切り線が含まれない: %s", lastContent)
	}

	saved, err := q.GetGameSessionByDate(ctx, sessionDate)
	if err != nil {
		t.Fatalf("GetGameSessionByDate: %v", err)
	}
	if !saved.TickerMsgID.Valid || saved.TickerMsgID.String != "ticker-msg-1" {
		t.Fatalf("ticker_msg_id が保存されていない: %+v", saved.TickerMsgID)
	}

	// 2回目: 保存済みIDを編集するだけで、新規投稿は増えないはず。
	if err := ticker.Update(ctx, now.Add(time.Minute), saved); err != nil {
		t.Fatalf("Update(2回目): %v", err)
	}
	if createCalls != 1 {
		t.Errorf("createCalls = %d, want 1（2回目は編集のはず。新規投稿が増えるとチャンネルが荒れる）", createCalls)
	}
	if editCalls != 1 {
		t.Errorf("editCalls = %d, want 1", editCalls)
	}
}
