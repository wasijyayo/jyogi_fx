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
	var lastComponents []discord.ActionRow
	mux := http.NewServeMux()
	mux.HandleFunc("/channels/ticker-chan/messages", func(w http.ResponseWriter, r *http.Request) {
		createCalls++
		var body struct {
			Content    string              `json:"content"`
			Components []discord.ActionRow `json:"components"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		lastContent = body.Content
		lastComponents = body.Components
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "ticker-msg-1"})
	})
	mux.HandleFunc("/channels/ticker-chan/messages/ticker-msg-1", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Errorf("method = %s, want PATCH", r.Method)
		}
		editCalls++
		var body struct {
			Content    string              `json:"content"`
			Components []discord.ActionRow `json:"components"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		lastContent = body.Content
		lastComponents = body.Components
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
	// 各通貨の買う/売るボタンが常設されていること（issue #78）。
	// 実通貨（JOG/WASI/CHEBU）+テスト用通貨で複数行あるはず。
	if len(lastComponents) == 0 {
		t.Fatal("componentsが空: 買う/売るボタンが付いていない")
	}
	found := false
	for _, row := range lastComponents {
		for _, btn := range row.Components {
			if btn.CustomID == "order:long:"+c.Symbol {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("テスト通貨 %s の買うボタン（custom_id=order:long:%s）が見つからない: %+v", c.Symbol, c.Symbol, lastComponents)
	}
	// 通貨の行の間に空行が入っていること（issue #75。スパークラインが最大の
	// 高さ（█）になると行同士が詰まって見づらいというフィードバックへの対応）。
	// 実通貨（JOG/WASI/CHEBU）+テスト用通貨で複数行あるため、コードブロック内に
	// 空行（連続する改行）が最低1箇所はあるはず。
	if !strings.Contains(lastContent, "\n\n") {
		t.Errorf("通貨の行の間に空行が無い: %s", lastContent)
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

// TestTickerService_Update_ID保存に失敗したら投稿を補償削除する は CodeRabbit の指摘
// （PR #71）どおり、投稿(POST)自体は成功したのに続く ticker_msg_id の保存だけが
// 失敗した場合に、投稿したメッセージを削除して「まだ投稿していない」状態へ
// 巻き戻すこと（次tickで孤児メッセージを増やさず安全に再投稿できること）を確認する。
//
// DB書き込みだけを確実に失敗させるため、CHECK制約を一時的に追加し、モックDiscord
// サーバーが返すメッセージIDをその制約に違反する値にする（プール全体を壊すと
// CreateMessageより前の読み取り(ListCurrencies等)まで失敗してしまい「投稿自体は
// 成功した」状況を再現できないため、この方法をとる）。
func TestTickerService_Update_ID保存に失敗したら投稿を補償削除する(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)
	q := db.New(pool)

	epoch := time.Date(2099, 3, 2, 12, 0, 0, 0, jst) // 実運用と衝突しない架空の未来日
	c := insertTestCurrency(t, ctx, pool, "TICKERCOMP", 555002, epoch)

	sessionDate := pgtype.Date{Time: time.Date(2099, 3, 2, 0, 0, 0, 0, jst), Valid: true}
	t.Cleanup(func() {
		cctx := context.Background()
		_, _ = pool.Exec(cctx, `ALTER TABLE game_sessions DROP CONSTRAINT IF EXISTS ticker_msg_id_test_guard`)
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

	const forcedFailureMsgID = "orphan-msg-1"
	if _, err := pool.Exec(ctx, `ALTER TABLE game_sessions ADD CONSTRAINT ticker_msg_id_test_guard
		CHECK (ticker_msg_id IS DISTINCT FROM '`+forcedFailureMsgID+`')`); err != nil {
		t.Fatalf("add test guard constraint: %v", err)
	}

	var createCalls, deleteCalls int
	mux := http.NewServeMux()
	mux.HandleFunc("/channels/ticker-chan/messages", func(w http.ResponseWriter, r *http.Request) {
		createCalls++
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": forcedFailureMsgID})
	})
	mux.HandleFunc("/channels/ticker-chan/messages/"+forcedFailureMsgID, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %s, want DELETE", r.Method)
		}
		deleteCalls++
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ticker := NewTickerService(pool, RealClock{}, discord.MessagesConfig{
		BotToken:   "test-token",
		APIBaseURL: srv.URL,
	}, "ticker-chan")

	if err := ticker.Update(ctx, epoch, session); err == nil {
		t.Fatal("want error（ID保存がCHECK制約違反で失敗するはず）, got nil")
	}
	if createCalls != 1 {
		t.Errorf("createCalls = %d, want 1", createCalls)
	}
	if deleteCalls != 1 {
		t.Errorf("deleteCalls = %d, want 1（補償削除が呼ばれるはず）", deleteCalls)
	}

	saved, err := q.GetGameSessionByDate(ctx, sessionDate)
	if err != nil {
		t.Fatalf("GetGameSessionByDate: %v", err)
	}
	if saved.TickerMsgID.Valid {
		t.Errorf("ticker_msg_id = %+v, want 未設定（補償削除後もNULLのままのはず。"+
			"設定されていると次tickが編集を試みて404になる）", saved.TickerMsgID)
	}
}
