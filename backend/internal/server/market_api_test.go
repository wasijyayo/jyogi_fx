package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"fxgame/backend/internal/discord"
	"fxgame/backend/internal/game"
	"fxgame/backend/internal/server"
)

// jst は取引時間のテストで使う JST のタイムゾーン。
var jst = time.FixedZone("JST", 9*60*60)

// newTestPool はローカル Postgres への接続を用意する。繋がらなければスキップする。
func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://app:app@localhost:5432/fxgame?sslmode=disable"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(func() { pool.Close() })

	if err := pool.Ping(ctx); err != nil {
		t.Skipf("ローカル Postgres に接続できないためスキップ（docker compose up -d を実行してください）: %v", err)
	}
	return pool
}

func newMux(t *testing.T, pool *pgxpool.Pool, clock game.Clock, alwaysOpen bool) *http.ServeMux {
	t.Helper()
	authSvc := game.NewAuthService(pool, discord.OAuthConfig{}, game.RealClock{})
	quoteSvc := game.NewQuoteService(pool, game.RealClock{})
	sessionSvc := game.NewSessionService(pool, game.RealClock{}, game.SessionConfig{AlwaysOpen: alwaysOpen})
	return server.NewMux(server.Config{
		Auth:          authSvc,
		Quote:         quoteSvc,
		Session:       sessionSvc,
		Clock:         clock,
		SecureCookies: true,
	})
}

// TestListCurrencies は GET /api/currencies が通貨一覧（JOG/WASI/CHEBU）を返すことを
// 確認する（#99 WEB-4 の `/` 画面のデータ源）。ログイン不要。
func TestListCurrencies(t *testing.T) {
	pool := newTestPool(t)
	mux := newMux(t, pool, game.RealClock{}, false)

	req := httptest.NewRequest(http.MethodGet, "/api/currencies", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var got []struct {
		Symbol          string  `json:"symbol"`
		Name            string  `json:"name"`
		CurrentPrice    string  `json:"currentPrice"`
		DayChangePercent *string `json:"dayChangePercent"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	symbols := map[string]bool{}
	for _, c := range got {
		if c.Symbol == "" || c.Name == "" || c.CurrentPrice == "" {
			t.Errorf("通貨の必須フィールドが空: %+v", c)
		}
		symbols[c.Symbol] = true
	}
	for _, want := range []string{"JOG", "WASI", "CHEBU"} {
		if !symbols[want] {
			t.Errorf("通貨一覧に %s が無い（currencies テーブルから作られること。CLAUDE.md §5.3）", want)
		}
	}
}

// TestGetCurrency は GET /api/currencies/{symbol} が存在する通貨を 200、存在しない
// 通貨を 404 で返すことを確認する。
func TestGetCurrency(t *testing.T) {
	pool := newTestPool(t)
	mux := newMux(t, pool, game.RealClock{}, false)

	t.Run("存在する通貨は200", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/currencies/JOG", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
		}
		var got struct {
			Symbol string `json:"symbol"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if got.Symbol != "JOG" {
			t.Errorf("symbol = %q, want %q", got.Symbol, "JOG")
		}
	})

	t.Run("存在しない通貨は404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/currencies/NOSUCH", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
		}
	})
}

// TestGetMarketStatus は GET /api/market/status がサーバー側の Clock と SessionConfig
// から計算した取引時間の状態を返すことを確認する（ブラウザの時計で計算しない）。
func TestGetMarketStatus(t *testing.T) {
	pool := newTestPool(t)

	t.Run("開催中は残り時間を返す", func(t *testing.T) {
		// 12:30 JST はセッション中（残り30分）。
		clock := fixedClock{now: time.Date(2026, 1, 1, 12, 30, 0, 0, jst)}
		mux := newMux(t, pool, clock, false)

		req := httptest.NewRequest(http.MethodGet, "/api/market/status", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
		}
		var got struct {
			Open             bool    `json:"open"`
			AlwaysOpen       bool    `json:"alwaysOpen"`
			RemainingMinutes *int    `json:"remainingMinutes"`
			NextStartAt      *string `json:"nextStartAt"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if !got.Open {
			t.Error("open = false, want true")
		}
		if got.AlwaysOpen {
			t.Error("alwaysOpen = true, want false")
		}
		if got.RemainingMinutes == nil || *got.RemainingMinutes != 30 {
			t.Errorf("remainingMinutes = %v, want 30", got.RemainingMinutes)
		}
		if got.NextStartAt != nil {
			t.Errorf("nextStartAt = %v, want nil（開催中に次の開始時刻は無い）", *got.NextStartAt)
		}
	})

	t.Run("時間外は次の開始時刻を返す", func(t *testing.T) {
		// 13:30 JST は時間外（次の開始は翌日12:00）。
		clock := fixedClock{now: time.Date(2026, 1, 1, 13, 30, 0, 0, jst)}
		mux := newMux(t, pool, clock, false)

		req := httptest.NewRequest(http.MethodGet, "/api/market/status", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
		}
		var got struct {
			Open             bool    `json:"open"`
			AlwaysOpen       bool    `json:"alwaysOpen"`
			RemainingMinutes *int    `json:"remainingMinutes"`
			NextStartAt      *string `json:"nextStartAt"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if got.Open {
			t.Error("open = true, want false")
		}
		if got.RemainingMinutes != nil {
			t.Errorf("remainingMinutes = %v, want nil", *got.RemainingMinutes)
		}
		if got.NextStartAt == nil {
			t.Fatal("nextStartAt = nil, want 次の開始時刻")
		}
	})

	t.Run("GAME_ALWAYS_OPEN時は常時開催", func(t *testing.T) {
		clock := fixedClock{now: time.Date(2026, 1, 1, 3, 0, 0, 0, jst)}
		mux := newMux(t, pool, clock, true)

		req := httptest.NewRequest(http.MethodGet, "/api/market/status", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		var got struct {
			Open             bool    `json:"open"`
			AlwaysOpen       bool    `json:"alwaysOpen"`
			RemainingMinutes *int    `json:"remainingMinutes"`
			NextStartAt      *string `json:"nextStartAt"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if !got.Open || !got.AlwaysOpen {
			t.Errorf("open=%v alwaysOpen=%v, want both true", got.Open, got.AlwaysOpen)
		}
		if got.RemainingMinutes != nil || got.NextStartAt != nil {
			t.Errorf("常時開催なのに remainingMinutes=%v / nextStartAt=%v が入っている", got.RemainingMinutes, got.NextStartAt)
		}
	})
}

// TestLogout は POST /auth/logout が sessions の行を削除し、ログイン時と同じ属性で
// Cookie を失効させることを確認する（CLAUDE.md §5.4）。
func TestLogout(t *testing.T) {
	pool := newTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const discordID = "web4-logout-user"
	const sessionID = "web4-logout-session"

	if _, err := pool.Exec(ctx,
		`INSERT INTO users (discord_id, display_name) VALUES ($1, $2)
		 ON CONFLICT (discord_id) DO UPDATE SET display_name = EXCLUDED.display_name`,
		discordID, "WEB-4 ログアウト確認ユーザー",
	); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO sessions_auth (id, user_id, expires_at) VALUES ($1, $2, now() + interval '1 day')
		 ON CONFLICT (id) DO UPDATE SET expires_at = EXCLUDED.expires_at`,
		sessionID, discordID,
	); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM sessions_auth WHERE id = $1`, sessionID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE discord_id = $1`, discordID)
	})

	mux := newMux(t, pool, game.RealClock{}, false)

	req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: sessionID})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusNoContent, rec.Body.String())
	}

	// セッション行が削除されていること。
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sessions_auth WHERE id = $1`, sessionID).Scan(&count); err != nil {
		t.Fatalf("count session: %v", err)
	}
	if count != 0 {
		t.Errorf("sessions_auth に行が残っている（count=%d）, want 0", count)
	}

	// ログイン時と同じ属性（HttpOnly・Secure・SameSite=Lax）で Cookie が失効していること。
	var sessionCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "session" {
			sessionCookie = c
		}
	}
	if sessionCookie == nil {
		t.Fatal("session cookie の失効（Set-Cookie）が返っていない")
	}
	if sessionCookie.Value != "" {
		t.Errorf("失効cookieの値 = %q, want 空", sessionCookie.Value)
	}
	if !sessionCookie.HttpOnly {
		t.Error("失効cookieが HttpOnly ではない")
	}
	if !sessionCookie.Secure {
		t.Error("失効cookieが Secure ではない")
	}
	if sessionCookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("失効cookieの SameSite = %v, want Lax", sessionCookie.SameSite)
	}
}
