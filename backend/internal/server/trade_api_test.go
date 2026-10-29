package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"fxgame/backend/internal/discord"
	"fxgame/backend/internal/game"
	"fxgame/backend/internal/server"
)

// newTradeMux は TradeService を配線した mux を作る（#46 WEB-2 の注文・建玉・決済 API の
// テスト用）。GAME_ALWAYS_OPEN 相当（AlwaysOpen=true）にして、時間に関係なく注文を
// 通せるようにする。notify / lifeWinner は無効（nil）にして通知・ロール付与を呼ばない。
func newTradeMux(t *testing.T, pool *pgxpool.Pool, clock game.Clock) *http.ServeMux {
	t.Helper()
	authSvc := game.NewAuthService(pool, discord.OAuthConfig{}, game.RealClock{})
	sessionSvc := game.NewSessionService(pool, game.RealClock{}, game.SessionConfig{AlwaysOpen: true})
	tradeSvc := game.NewTradeService(pool, game.RealClock{}, sessionSvc, nil, decimal.Zero, decimal.Zero, nil)
	return server.NewMux(server.Config{
		Auth:          authSvc,
		Session:       sessionSvc,
		Trade:         tradeSvc,
		Clock:         clock,
		SecureCookies: true,
	})
}

// TestTradeAPI は注文 → 建玉一覧 → 決済の一連の API を HTTP 越しに確認する（#46 WEB-2）。
// 未ログインは 401、注文成功で建玉が作られ、決済で一覧から消えることを見る。
func TestTradeAPI(t *testing.T) {
	pool := newTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 使い捨て通貨の epoch_at を固定時刻に合わせ、tickIndex=0 で約定価格=base_price=100 に
	// する（AGENTS.md: 実在通貨で tick を回すと BasePrice が極端に遅くなる問題を避ける）。
	now := time.Date(2099, 4, 1, 12, 0, 0, 0, jst)
	clock := fixedClock{now: now}

	const userID = "web2-trade-user"
	const sessionID = "web2-trade-session"
	const symbol = "WEB2CUR"

	var currencyID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO currencies (symbol, name, base_price, drift, volatility, lambda, k, liquidity,
			pressure_at, off_session_scale, seed, max_leverage, fee_rate, epoch_at)
		VALUES ($1, $2, 100, 0, 0.001, 0.1, 0.01, 100000, now(), 0.05, 999, 10, 0.0005, $3)
		RETURNING id`,
		symbol, symbol, now,
	).Scan(&currencyID); err != nil {
		t.Fatalf("insert currency: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO users (discord_id, display_name) VALUES ($1, $2)
		 ON CONFLICT (discord_id) DO UPDATE SET display_name = EXCLUDED.display_name`,
		userID, "WEB-2 テストユーザー",
	); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO sessions_auth (id, user_id, expires_at) VALUES ($1, $2, now() + interval '1 day')
		 ON CONFLICT (id) DO UPDATE SET expires_at = EXCLUDED.expires_at`,
		sessionID, userID,
	); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	// 後始末は子テーブルから順に（AGENTS.md: trades → positions → sessions_auth → currencies → users）。
	t.Cleanup(func() {
		cctx := context.Background()
		cleanup := []struct {
			sql  string
			args []any
		}{
			{`DELETE FROM trades WHERE user_id = $1`, []any{userID}},
			{`DELETE FROM positions WHERE user_id = $1`, []any{userID}},
			{`DELETE FROM sessions_auth WHERE id = $1`, []any{sessionID}},
			{`DELETE FROM currencies WHERE id = $1`, []any{currencyID}},
			{`DELETE FROM users WHERE discord_id = $1`, []any{userID}},
		}
		for _, c := range cleanup {
			if _, err := pool.Exec(cctx, c.sql, c.args...); err != nil {
				t.Errorf("cleanup %q: %v", c.sql, err)
			}
		}
	})

	mux := newTradeMux(t, pool, clock)
	cookie := &http.Cookie{Name: "session", Value: sessionID}

	t.Run("未ログインの注文・建玉一覧は401", func(t *testing.T) {
		for _, tc := range []struct {
			method, path, body string
		}{
			{http.MethodPost, "/api/orders", `{"symbol":"WEB2CUR","side":"long","size":"10","leverage":"2"}`},
			{http.MethodGet, "/api/positions", ""},
		} {
			var req *http.Request
			if tc.body != "" {
				req = httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			} else {
				req = httptest.NewRequest(tc.method, tc.path, nil)
			}
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("%s %s = %d, want %d", tc.method, tc.path, rec.Code, http.StatusUnauthorized)
			}
		}
	})

	t.Run("注文が成立し、建玉一覧に現れ、決済で消える", func(t *testing.T) {
		// 注文。
		req := httptest.NewRequest(http.MethodPost, "/api/orders",
			strings.NewReader(`{"symbol":"WEB2CUR","side":"long","size":"10","leverage":"2"}`))
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
		}
		var order struct {
			Symbol     string `json:"symbol"`
			Side       string `json:"side"`
			EntryPrice string `json:"entryPrice"`
			NewBalance string `json:"newBalance"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &order); err != nil {
			t.Fatalf("decode order: %v", err)
		}
		if order.Symbol != symbol || order.Side != "long" {
			t.Errorf("order = %+v, want %s/long", order, symbol)
		}
		if entry, err := decimal.NewFromString(order.EntryPrice); err != nil || !entry.Equal(decimal.NewFromInt(100)) {
			t.Errorf("entryPrice = %q, want 100 (err=%v)", order.EntryPrice, err)
		}
		if order.NewBalance == "" {
			t.Error("newBalance が空")
		}

		// 建玉一覧に1件現れる。
		lrec := listPositions(t, mux, cookie)
		var positions []struct {
			ID            int64  `json:"id"`
			Symbol        string `json:"symbol"`
			Side          string `json:"side"`
			Size          string `json:"size"`
			UnrealizedPnl string `json:"unrealizedPnl"`
		}
		if err := json.Unmarshal(lrec.Body.Bytes(), &positions); err != nil {
			t.Fatalf("decode positions: %v", err)
		}
		if len(positions) != 1 {
			t.Fatalf("positions = %d件, want 1件", len(positions))
		}
		if positions[0].Symbol != symbol || positions[0].Side != "long" {
			t.Errorf("position = %+v, want %s/long", positions[0], symbol)
		}
		if size, err := decimal.NewFromString(positions[0].Size); err != nil || !size.Equal(decimal.NewFromInt(10)) {
			t.Errorf("size = %q, want 10 (err=%v)", positions[0].Size, err)
		}
		if positions[0].UnrealizedPnl == "" {
			t.Error("unrealizedPnl が空")
		}

		// 決済。
		creq := httptest.NewRequest(http.MethodPost, "/api/positions/"+strconv.FormatInt(positions[0].ID, 10)+"/close", nil)
		creq.AddCookie(cookie)
		crec := httptest.NewRecorder()
		mux.ServeHTTP(crec, creq)
		if crec.Code != http.StatusOK {
			t.Fatalf("close status = %d, want %d, body=%s", crec.Code, http.StatusOK, crec.Body.String())
		}
		var closed struct {
			Pnl        string `json:"pnl"`
			NewBalance string `json:"newBalance"`
		}
		if err := json.Unmarshal(crec.Body.Bytes(), &closed); err != nil {
			t.Fatalf("decode close: %v", err)
		}
		if closed.Pnl == "" || closed.NewBalance == "" {
			t.Errorf("close result が空: %+v", closed)
		}

		// 決済後は建玉一覧が空になる。
		after := listPositions(t, mux, cookie)
		var remaining []json.RawMessage
		if err := json.Unmarshal(after.Body.Bytes(), &remaining); err != nil {
			t.Fatalf("decode positions(2): %v", err)
		}
		if len(remaining) != 0 {
			t.Errorf("決済後の建玉 = %d件, want 0件", len(remaining))
		}
	})
}

// listPositions は GET /api/positions を Cookie 付きで叩いてレスポンスを返す。
func listPositions(t *testing.T, mux *http.ServeMux, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/positions", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list positions status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	return rec
}
