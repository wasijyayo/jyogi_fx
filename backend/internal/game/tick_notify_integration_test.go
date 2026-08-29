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
	"github.com/shopspring/decimal"

	"fxgame/backend/internal/db"
	"fxgame/backend/internal/discord"
)

// recordingNotifyServer はモックDiscordサーバー。#通知チャンネルへの投稿をすべて
// 記録する（テスト対象がticker.goと違い常に新規投稿のみのため、投稿回数と内容の
// 履歴の両方を検証できるようにしてある）。
type recordingNotifyServer struct {
	posts []string
}

func newRecordingNotifyService(t *testing.T) (*NotifyService, *recordingNotifyServer) {
	t.Helper()
	rec := &recordingNotifyServer{}

	mux := http.NewServeMux()
	mux.HandleFunc("/channels/notify-chan/messages", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Content string `json:"content"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		rec.posts = append(rec.posts, body.Content)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "notify-msg"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	notify := NewNotifyService(discord.MessagesConfig{BotToken: "test-token", APIBaseURL: srv.URL}, "notify-chan")
	return notify, rec
}

// TestTickService_notifyEvents_予兆と発火が1回ずつ投稿され冪等 は #44完了条件
// 「tickが二重に走っても同じ通知が2回投稿されないこと」を、イベントの予兆/発火通知で確認する。
func TestTickService_notifyEvents_予兆と発火が1回ずつ投稿され冪等(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)
	q := db.New(pool)

	epoch := time.Date(2099, 4, 10, 12, 0, 0, 0, jst) // 実運用と衝突しない架空の未来日
	c := insertTestCurrency(t, ctx, pool, "EVTNOTIFY", 777001, epoch)

	sessionDate := pgtype.Date{Time: time.Date(2099, 4, 10, 0, 0, 0, 0, jst), Valid: true}
	t.Cleanup(func() {
		cctx := context.Background()
		if s, err := q.GetGameSessionByDate(cctx, sessionDate); err == nil {
			_, _ = pool.Exec(cctx, `DELETE FROM events WHERE session_id = $1`, s.ID)
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
	if err := openCurrency(ctx, q, session, c, epoch); err != nil { // tick_index=0
		t.Fatalf("openCurrency: %v", err)
	}

	// fire_tick=2: tick_index=1で予兆、tick_index=2で発火するはず（§5.3「発火の1つ前」）。
	if _, err := q.CreateEvent(ctx, db.CreateEventParams{
		SessionID: session.ID, CurrencyID: c.ID, Type: EventTypeShock,
		FireTick: 2, DurationTicks: 0, Magnitude: decimal.NewFromFloat(0.14),
	}); err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}

	notify, rec := newRecordingNotifyService(t)
	ts := &TickService{notify: notify}
	currencies := []db.Currency{c}

	teaserTick := epoch.Add(time.Minute) // tick_index=1
	ts.notifyEvents(ctx, q, currencies, teaserTick)
	if len(rec.posts) != 1 {
		t.Fatalf("予兆1回目: posts = %d件, want 1", len(rec.posts))
	}
	if !strings.Contains(rec.posts[0], "📡") || !strings.Contains(rec.posts[0], "EVTNOTIFY") {
		t.Errorf("予兆の内容が不正: %q", rec.posts[0])
	}

	// 同じtickで2回呼んでも（tickの重複実行を想定）予兆は増えないはず。
	ts.notifyEvents(ctx, q, currencies, teaserTick)
	if len(rec.posts) != 1 {
		t.Fatalf("予兆2回目（重複実行）: posts = %d件, want 1（冪等のはず）", len(rec.posts))
	}

	fireTick := epoch.Add(2 * time.Minute) // tick_index=2
	ts.notifyEvents(ctx, q, currencies, fireTick)
	if len(rec.posts) != 2 {
		t.Fatalf("発火1回目: posts = %d件, want 2", len(rec.posts))
	}
	if !strings.Contains(rec.posts[1], "EVTNOTIFY") || !strings.Contains(rec.posts[1], "+14.0%") {
		t.Errorf("発火の内容が不正: %q", rec.posts[1])
	}

	// 発火も同じtickの重複実行で増えないはず。
	ts.notifyEvents(ctx, q, currencies, fireTick)
	if len(rec.posts) != 2 {
		t.Fatalf("発火2回目（重複実行）: posts = %d件, want 2（冪等のはず）", len(rec.posts))
	}

	events, err := q.ListEventsByCurrency(ctx, c.ID)
	if err != nil || len(events) != 1 {
		t.Fatalf("ListEventsByCurrency: %v, len=%d", err, len(events))
	}
	if !events[0].Teased || !events[0].Resolved {
		t.Errorf("teased=%v resolved=%v, どちらもtrueのはず", events[0].Teased, events[0].Resolved)
	}
}

// TestTickService_notifySessionOpen は design.md §2.8「セッション開始通知」を、
// 実際に前日終値→寄り付きという2本のprice_ticksを積んだ状態から組み立てて確認する。
func TestTickService_notifySessionOpen(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)
	q := db.New(pool)

	epoch := time.Date(2099, 4, 14, 12, 0, 0, 0, jst)
	c := insertTestCurrency(t, ctx, pool, "GAPNOTIFY", 777004, epoch)

	sessionDate := pgtype.Date{Time: time.Date(2099, 4, 14, 0, 0, 0, 0, jst), Valid: true}
	t.Cleanup(func() {
		cctx := context.Background()
		if s, err := q.GetGameSessionByDate(cctx, sessionDate); err == nil {
			_, _ = pool.Exec(cctx, `DELETE FROM price_ticks WHERE session_id = $1`, s.ID)
			_, _ = pool.Exec(cctx, `DELETE FROM game_sessions WHERE id = $1`, s.ID)
		}
		_, _ = pool.Exec(cctx, `DELETE FROM currencies WHERE id = $1`, c.ID)
	})

	session, err := q.CreateGameSession(ctx, db.CreateGameSessionParams{
		Date: sessionDate, Seed: 1,
		OpenedAt: pgtype.Timestamptz{Time: epoch, Valid: true},
		ClosedAt: pgtype.Timestamptz{Time: epoch.Add(time.Hour), Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateGameSession: %v", err)
	}

	// 前セッション最終tick（tick_index=0）のcloseを100.00に固定する。
	if err := q.UpsertPriceTick(ctx, db.UpsertPriceTickParams{
		CurrencyID: c.ID, SessionID: session.ID, TickIndex: 0,
		TickedAt:  pgtype.Timestamptz{Time: epoch.Add(-24 * time.Hour), Valid: true},
		BasePrice: decimal.NewFromInt(100), Pressure: decimal.Zero, NetVolume: decimal.Zero,
		Open: decimal.NewFromInt(100), High: decimal.NewFromInt(100), Low: decimal.NewFromInt(100),
		Close: decimal.NewFromInt(100), IsOpening: true,
	}); err != nil {
		t.Fatalf("UpsertPriceTick(前日): %v", err)
	}
	// 寄り付きtick（tick_index=1380=23時間分）。closeを103.00にして3%のギャップを作る。
	if err := q.UpsertPriceTick(ctx, db.UpsertPriceTickParams{
		CurrencyID: c.ID, SessionID: session.ID, TickIndex: 1380,
		TickedAt:  pgtype.Timestamptz{Time: epoch, Valid: true},
		BasePrice: decimal.NewFromFloat(103), Pressure: decimal.Zero, NetVolume: decimal.Zero,
		Open: decimal.NewFromInt(100), High: decimal.NewFromFloat(103), Low: decimal.NewFromInt(100),
		Close: decimal.NewFromFloat(103), IsOpening: true,
	}); err != nil {
		t.Fatalf("UpsertPriceTick(寄り付き): %v", err)
	}

	notify, rec := newRecordingNotifyService(t)
	ts := &TickService{notify: notify}
	ts.notifySessionOpen(ctx, q, []db.Currency{c})

	if len(rec.posts) != 1 {
		t.Fatalf("posts = %d件, want 1", len(rec.posts))
	}
	content := rec.posts[0]
	if !strings.Contains(content, "GAPNOTIFY") || !strings.Contains(content, "100.00") || !strings.Contains(content, "103.00") {
		t.Errorf("content = %q", content)
	}
	if !strings.Contains(content, "+300 pips") {
		t.Errorf("pips表記が無い: %q", content)
	}
	// volatility=0.0020（insertTestCurrency固定値）・off_session_scale=0.05・
	// elapsed=1380tickなら sigma ≈ 0.0020×0.05×√1380 ≈ 0.00371、2σ≈0.74%。
	// 変化率3%はこれを大きく超えるため「大きな窓」表記になるはず。
	if !strings.Contains(content, "⚠️ セッション開始 — 大きな窓") {
		t.Errorf("大きな窓の見出しが無い: %q", content)
	}
}

// TestTickService_notifyLiquidations はロスカット通知が roast_enabled を尊重することを確認する
// （design.md §6.8）。
func TestTickService_notifyLiquidations(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)
	q := db.New(pool)

	epoch := time.Date(2099, 4, 11, 12, 0, 0, 0, jst)
	c := insertTestCurrency(t, ctx, pool, "LIQNOTIFY", 777002, epoch)

	userID := "test-liq-notify-user"
	_, _ = pool.Exec(ctx, `DELETE FROM users WHERE discord_id = $1`, userID)
	if _, err := q.UpsertUser(ctx, db.UpsertUserParams{DiscordID: userID, DisplayName: "ロスカ太郎"}); err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}
	// roast_enabledを切り替えるDiscordコマンドはまだ無い（design.mdにはフラグの
	// 存在のみ記載）ため、テストでは直接UPDATEする。
	if _, err := pool.Exec(ctx, `UPDATE users SET roast_enabled = FALSE WHERE discord_id = $1`, userID); err != nil {
		t.Fatalf("update roast_enabled: %v", err)
	}

	t.Cleanup(func() {
		cctx := context.Background()
		_, _ = pool.Exec(cctx, `DELETE FROM currencies WHERE id = $1`, c.ID)
		_, _ = pool.Exec(cctx, `DELETE FROM users WHERE discord_id = $1`, userID)
	})

	notify, rec := newRecordingNotifyService(t)
	ts := &TickService{notify: notify}

	liquidated := []LiquidatedPosition{
		{Position: db.Position{UserID: userID, CurrencyID: c.ID, Leverage: decimal.NewFromInt(25)}},
	}
	ts.notifyLiquidations(ctx, q, []db.Currency{c}, liquidated)

	if len(rec.posts) != 1 {
		t.Fatalf("posts = %d件, want 1", len(rec.posts))
	}
	if !strings.Contains(rec.posts[0], "ロスカ太郎") || !strings.Contains(rec.posts[0], "LIQNOTIFY") || !strings.Contains(rec.posts[0], "25") {
		t.Errorf("content = %q", rec.posts[0])
	}
	// roast_enabled=falseなので中立文言（演出絵文字を含まない）のはず。
	if strings.ContainsAny(rec.posts[0], "💥💀🎇🪦🔥") {
		t.Errorf("roast_enabled=falseなのに演出文言が使われた: %q", rec.posts[0])
	}
}

// TestTickService_notifyClosingIfLastTick_冪等 は #44完了条件そのもの
// 「tickが二重に走っても同じ通知が2回投稿されないこと」を、セッション終了通知＋
// 日次まとめについて確認する（migrations/000007のclosing_notifiedフラグ）。
func TestTickService_notifyClosingIfLastTick_冪等(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)
	q := db.New(pool)

	sessionDate := pgtype.Date{Time: time.Date(2099, 4, 12, 0, 0, 0, 0, jst), Valid: true}
	opened := time.Date(2099, 4, 12, 12, 0, 0, 0, jst)
	closed := opened.Add(time.Hour)

	t.Cleanup(func() {
		cctx := context.Background()
		if s, err := q.GetGameSessionByDate(cctx, sessionDate); err == nil {
			_, _ = pool.Exec(cctx, `DELETE FROM game_sessions WHERE id = $1`, s.ID)
		}
	})

	session, err := q.CreateGameSession(ctx, db.CreateGameSessionParams{
		Date: sessionDate, Seed: 1,
		OpenedAt: pgtype.Timestamptz{Time: opened, Valid: true},
		ClosedAt: pgtype.Timestamptz{Time: closed, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateGameSession: %v", err)
	}

	notify, rec := newRecordingNotifyService(t)
	// rankingはnilのままでよい（notifyDailySummaryはs.ranking==nilなら何もせず
	// 資産ランキング部分だけスキップする。日次まとめ本体はs.notify経由で呼ばれる）。
	ts := &TickService{notify: notify}

	lastTick := closed.Add(-time.Minute) // 12:59。design.md §4の最後のtick。
	ts.notifyClosingIfLastTick(ctx, q, session, lastTick)

	// rankingがnilの場合、notifyDailySummaryは早期returnし日次まとめ自体は投稿しない
	// （notifyDailySummaryのコメント参照）。ここではSessionCloseの1件のみ期待する。
	if len(rec.posts) != 1 {
		t.Fatalf("1回目: posts = %d件, want 1（セッション終了通知のみ）", len(rec.posts))
	}
	if !strings.Contains(rec.posts[0], "🌙 セッション終了") {
		t.Errorf("content = %q", rec.posts[0])
	}

	saved, err := q.GetGameSessionByDate(ctx, sessionDate)
	if err != nil {
		t.Fatalf("GetGameSessionByDate: %v", err)
	}
	if !saved.ClosingNotified {
		t.Fatal("closing_notified = false, want true")
	}

	// tickの重複実行を想定し、同じ最終tickをもう一度渡す。既にclosing_notified=trueの
	// session行を使うため（呼び出し元TickServiceが毎回GetGameSessionByDateで読み直す
	// のと同じ状況）、2回目は何も投稿しないはず。
	ts.notifyClosingIfLastTick(ctx, q, saved, lastTick)
	if len(rec.posts) != 1 {
		t.Fatalf("2回目（重複実行）: posts = %d件, want 1（増えないはず）", len(rec.posts))
	}
}

// TestTradeService_PlaceOrder_大口取引通知 は design.md §6.7 の大口取引通知が
// 閾値以上でだけ発火することを確認する。
func TestTradeService_PlaceOrder_大口取引通知(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)
	q := db.New(pool)

	epoch := time.Date(2099, 4, 13, 12, 0, 0, 0, jst)
	c := insertTestCurrency(t, ctx, pool, "BIGTRADE", 777003, epoch)

	userID := "test-large-trade-user"
	_, _ = pool.Exec(ctx, `DELETE FROM trades WHERE user_id = $1`, userID)
	_, _ = pool.Exec(ctx, `DELETE FROM positions WHERE user_id = $1`, userID)
	_, _ = pool.Exec(ctx, `DELETE FROM users WHERE discord_id = $1`, userID)
	setupTradeTestUser(t, ctx, q, userID, decimal.NewFromInt(1000))

	t.Cleanup(func() {
		cctx := context.Background()
		_, _ = pool.Exec(cctx, `DELETE FROM trades WHERE user_id = $1`, userID)
		_, _ = pool.Exec(cctx, `DELETE FROM positions WHERE user_id = $1`, userID)
		_, _ = pool.Exec(cctx, `DELETE FROM currencies WHERE id = $1`, c.ID)
		_, _ = pool.Exec(cctx, `DELETE FROM users WHERE discord_id = $1`, userID)
	})

	sessionSvc := NewSessionService(pool, RealClock{}, SessionConfig{})

	t.Run("閾値を超えたら通知される", func(t *testing.T) {
		notify, rec := newRecordingNotifyService(t)
		// 閾値をほぼゼロにして、どんな取引でも「大口」扱いになるようにする。
		tradeSvc := NewTradeService(pool, RealClock{}, sessionSvc, notify, decimal.NewFromFloat(0.0001), decimal.Zero, nil)

		_, err := tradeSvc.PlaceOrder(ctx, epoch, PlaceOrderParams{
			UserID: userID, CurrencySymbol: "BIGTRADE", Side: SideLong,
			Size: decimal.NewFromInt(1), Leverage: decimal.NewFromInt(1),
		})
		if err != nil {
			t.Fatalf("PlaceOrder: %v", err)
		}
		if len(rec.posts) != 1 {
			t.Fatalf("posts = %d件, want 1", len(rec.posts))
		}
		if !strings.Contains(rec.posts[0], userID) || !strings.Contains(rec.posts[0], "BIGTRADE") || !strings.Contains(rec.posts[0], "大量購入") {
			t.Errorf("content = %q", rec.posts[0])
		}
	})

	t.Run("閾値未満なら通知されない", func(t *testing.T) {
		notify, rec := newRecordingNotifyService(t)
		// 現実的にまず届かない閾値にして、普通の注文では通知されないことを確認する。
		tradeSvc := NewTradeService(pool, RealClock{}, sessionSvc, notify, decimal.NewFromInt(1000), decimal.Zero, nil)

		_, err := tradeSvc.PlaceOrder(ctx, epoch.Add(time.Minute), PlaceOrderParams{
			UserID: userID, CurrencySymbol: "BIGTRADE", Side: SideLong,
			Size: decimal.NewFromInt(1), Leverage: decimal.NewFromInt(1),
		})
		if err != nil {
			t.Fatalf("PlaceOrder: %v", err)
		}
		if len(rec.posts) != 0 {
			t.Fatalf("posts = %d件, want 0（閾値未満）: %v", len(rec.posts), rec.posts)
		}
	})
}

// TestTradeService_ClosePosition_利益確定通知 は #82 の利益確定通知が、
// 閾値pips以上の「利益」でだけ発火し、閾値未満や損失方向（値動き幅は大きくても
// 利益ではない）では発火しないことを確認する。
func TestTradeService_ClosePosition_利益確定通知(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)
	q := db.New(pool)

	epoch := time.Date(2099, 4, 15, 12, 0, 0, 0, jst)
	c := insertTestCurrency(t, ctx, pool, "PROFITNOTIFY", 777005, epoch)

	userID := "test-profit-notify-user"
	_, _ = pool.Exec(ctx, `DELETE FROM trades WHERE user_id = $1`, userID)
	_, _ = pool.Exec(ctx, `DELETE FROM positions WHERE user_id = $1`, userID)
	_, _ = pool.Exec(ctx, `DELETE FROM users WHERE discord_id = $1`, userID)
	setupTradeTestUser(t, ctx, q, userID, decimal.NewFromInt(1000))

	t.Cleanup(func() {
		cctx := context.Background()
		_, _ = pool.Exec(cctx, `DELETE FROM trades WHERE user_id = $1`, userID)
		_, _ = pool.Exec(cctx, `DELETE FROM positions WHERE user_id = $1`, userID)
		_, _ = pool.Exec(cctx, `DELETE FROM currencies WHERE id = $1`, c.ID)
		_, _ = pool.Exec(cctx, `DELETE FROM users WHERE discord_id = $1`, userID)
	})

	sessionSvc := NewSessionService(pool, RealClock{}, SessionConfig{})

	// TestClosePosition_損益が計算され残高と圧力に反映されるのpressureOverrideと同じ手法。
	// 建玉直後に圧力を直接上書きすることで決済価格（=建値×(1+override)）を制御する。
	overridePressureAndClose := func(t *testing.T, tradeSvc *TradeService, now time.Time, pressure decimal.Decimal) {
		t.Helper()
		openResult, err := tradeSvc.PlaceOrder(ctx, now, PlaceOrderParams{
			UserID: userID, CurrencySymbol: "PROFITNOTIFY", Side: SideLong,
			Size: decimal.NewFromInt(1), Leverage: decimal.NewFromInt(1),
		})
		if err != nil {
			t.Fatalf("PlaceOrder: %v", err)
		}
		if err := q.UpdateCurrencyPressure(ctx, db.UpdateCurrencyPressureParams{
			ID: c.ID, Pressure: pressure, PressureAt: pgtype.Timestamptz{Time: now, Valid: true},
		}); err != nil {
			t.Fatalf("UpdateCurrencyPressure: %v", err)
		}
		if _, err := tradeSvc.ClosePosition(ctx, now, ClosePositionParams{
			UserID: userID, PositionID: openResult.Position.ID,
		}); err != nil {
			t.Fatalf("ClosePosition: %v", err)
		}
	}

	t.Run("200pips以上の利益なら通知される", func(t *testing.T) {
		notify, rec := newRecordingNotifyService(t)
		tradeSvc := NewTradeService(pool, RealClock{}, sessionSvc, notify, decimal.Zero, decimal.NewFromInt(200), nil)

		// ロング+3%（建値100→約103、300pips）で閾値200pipsを超える。
		overridePressureAndClose(t, tradeSvc, epoch, decimal.NewFromFloat(0.03))

		if len(rec.posts) != 1 {
			t.Fatalf("posts = %d件, want 1", len(rec.posts))
		}
		if !strings.Contains(rec.posts[0], userID) || !strings.Contains(rec.posts[0], "PROFITNOTIFY") || !strings.Contains(rec.posts[0], "pips") {
			t.Errorf("content = %q", rec.posts[0])
		}
	})

	t.Run("閾値未満なら通知されない", func(t *testing.T) {
		notify, rec := newRecordingNotifyService(t)
		tradeSvc := NewTradeService(pool, RealClock{}, sessionSvc, notify, decimal.Zero, decimal.NewFromInt(200), nil)

		// ロング+0.5%（約50pips）で閾値200pipsに届かない。
		overridePressureAndClose(t, tradeSvc, epoch.Add(time.Minute), decimal.NewFromFloat(0.005))

		if len(rec.posts) != 0 {
			t.Fatalf("posts = %d件, want 0（閾値未満）: %v", len(rec.posts), rec.posts)
		}
	})

	t.Run("損失方向なら値動き幅が大きくても通知されない", func(t *testing.T) {
		notify, rec := newRecordingNotifyService(t)
		tradeSvc := NewTradeService(pool, RealClock{}, sessionSvc, notify, decimal.Zero, decimal.NewFromInt(200), nil)

		// ロング-5%（約-500pips）。値動き幅は閾値を大きく超えるが、ロングにとっては
		// 損失方向なので通知されないはず。
		overridePressureAndClose(t, tradeSvc, epoch.Add(2*time.Minute), decimal.NewFromFloat(-0.05))

		if len(rec.posts) != 0 {
			t.Fatalf("posts = %d件, want 0（損失方向）: %v", len(rec.posts), rec.posts)
		}
	})
}
