package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"fxgame/backend/internal/db"
	"fxgame/backend/internal/discord"
	"fxgame/backend/internal/game"
	"fxgame/backend/internal/server"
)

// replaySeededPairs はマイグレーション 000009 の 4 ペアを返す。
func replaySeededPairs(t *testing.T, ctx context.Context, pool *pgxpool.Pool) []db.CurrencyPair {
	t.Helper()
	pairs, err := db.New(pool).ListCurrencyPairs(ctx)
	if err != nil {
		t.Fatalf("list currency pairs: %v", err)
	}
	var seeded []db.CurrencyPair
	for _, p := range pairs {
		switch p.Symbol {
		case "USD/JPY", "EUR/USD", "GBP/USD", "AUD/USD":
			seeded = append(seeded, p)
		}
	}
	if len(seeded) != 4 {
		t.Fatalf("want 4 seeded pairs, got %d", len(seeded))
	}
	return seeded
}

// replayWeekdays は start から連続する n 平日を返す。
func replayWeekdays(start time.Time, n int) []time.Time {
	out := make([]time.Time, 0, n)
	d := start
	for len(out) < n {
		if wd := d.Weekday(); wd != time.Saturday && wd != time.Sunday {
			out = append(out, d)
		}
		d = d.AddDate(0, 0, 1)
	}
	return out
}

// seedReplayRates は 4 ペアに 80 日分の日足（定価）を投入する。
func seedReplayRates(t *testing.T, pool *pgxpool.Pool) []db.CurrencyPair {
	t.Helper()
	ctx := context.Background()

	// 過去の失敗で残った行を子テーブルから順に掃除。
	for _, s := range []string{
		`DELETE FROM replay_positions`,
		`DELETE FROM replay_leverage_unlocks`,
		`DELETE FROM replay_speed_changes`,
		`DELETE FROM replay_game_rates`,
		`DELETE FROM replay_game_days`,
		`DELETE FROM replay_games`,
		`DELETE FROM daily_rates`,
		`DELETE FROM rate_datasets`,
		`DELETE FROM currency_pairs WHERE symbol NOT IN ('USD/JPY','EUR/USD','GBP/USD','AUD/USD')`,
	} {
		if _, err := pool.Exec(ctx, s); err != nil {
			t.Fatalf("pre-cleanup %q: %v", s, err)
		}
	}

	pairs := replaySeededPairs(t, ctx, pool)
	dates := replayWeekdays(time.Date(2015, 1, 5, 0, 0, 0, 0, time.UTC), 80)
	for _, p := range pairs {
		var datasetID int64
		if err := pool.QueryRow(ctx, `
			INSERT INTO rate_datasets (pair_id, source, filename, fetched_on, sha256, imported_at)
			VALUES ($1, 'test', 'test.json.gz', $2, $3, $4) RETURNING id`,
			p.ID,
			pgtype.Date{Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Valid: true},
			"server-test-sha-"+p.Symbol,
			pgtype.Timestamptz{Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Valid: true},
		).Scan(&datasetID); err != nil {
			t.Fatalf("insert dataset: %v", err)
		}
		for _, d := range dates {
			if _, err := pool.Exec(ctx, `
				INSERT INTO daily_rates (dataset_id, pair_id, date, open, high, low, close)
				VALUES ($1, $2, $3, 100, 105, 95, 100)`,
				datasetID, p.ID, pgtype.Date{Time: d, Valid: true},
			); err != nil {
				t.Fatalf("insert daily rate: %v", err)
			}
		}
	}

	t.Cleanup(func() {
		cctx := context.Background()
		for _, s := range []string{
			`DELETE FROM replay_positions`,
			`DELETE FROM replay_leverage_unlocks`,
			`DELETE FROM replay_speed_changes`,
			`DELETE FROM replay_game_rates`,
			`DELETE FROM replay_game_days`,
			`DELETE FROM replay_games`,
			`DELETE FROM daily_rates`,
			`DELETE FROM rate_datasets`,
		} {
			if _, err := pool.Exec(cctx, s); err != nil {
				t.Errorf("cleanup %q: %v", s, err)
			}
		}
	})
	return pairs
}

// newReplayMux は過去チャートモードの mux を作る。
func newReplayMux(pool *pgxpool.Pool, clock game.Clock) *http.ServeMux {
	authSvc := game.NewAuthService(pool, discord.OAuthConfig{}, game.RealClock{})
	replaySvc := game.NewReplayService(pool, clock)
	return server.NewMux(server.Config{
		Auth:          authSvc,
		Replay:        replaySvc,
		Clock:         clock,
		SecureCookies: true,
	})
}

// datePattern は日付らしき文字列（YYYY-MM-DD と RFC3339 の YYYY-MM-DDT...）に一致する。
var datePattern = regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)

// TestReplayAPI_NoFutureOrDates は経過時間より先の日足や正確な日付が、終了前のどの API
// レスポンスにも含まれないことを JSON レベルで確認する（#103 完了条件）。
func TestReplayAPI_NoFutureOrDates(t *testing.T) {
	pool := newTestPool(t)
	pairs := seedReplayRates(t, pool)

	now := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	clock := fixedClock{now: now}
	mux := newReplayMux(pool, clock)

	// 開始（ゲスト）。
	start := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/replay/games", strings.NewReader(`{"guestName":"tester"}`))
	mux.ServeHTTP(start, req)
	if start.Code != http.StatusOK {
		t.Fatalf("start status = %d, body=%s", start.Code, start.Body.String())
	}
	var started struct {
		GameId int64  `json:"gameId"`
		Token  string `json:"token"`
	}
	if err := json.Unmarshal(start.Body.Bytes(), &started); err != nil {
		t.Fatalf("decode start: %v", err)
	}
	if datePattern.MatchString(start.Body.String()) {
		t.Errorf("start response contains a date string: %s", start.Body.String())
	}
	// 開始レスポンスは開始前 20 日まで（day 0..19）。
	var startedFull struct {
		CandlesByPair []struct {
			Candles []struct {
				Day int `json:"day"`
			} `json:"candles"`
		} `json:"candlesByPair"`
	}
	if err := json.Unmarshal(start.Body.Bytes(), &startedFull); err != nil {
		t.Fatalf("decode start candles: %v", err)
	}
	for _, pc := range startedFull.CandlesByPair {
		for _, c := range pc.Candles {
			if c.Day > 19 {
				t.Errorf("start response leaks future candle day %d", c.Day)
			}
		}
	}

	auth := "Bearer " + started.Token
	usdID := pairIDForSymbol(t, pairs, "USD/JPY")

	// 状態取得（経過 0 秒 = 公開済み 0 日）。
	st := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/replay/games/"+itoa(started.GameId), nil)
	req.Header.Set("Authorization", auth)
	mux.ServeHTTP(st, req)
	if st.Code != http.StatusOK {
		t.Fatalf("state status = %d, body=%s", st.Code, st.Body.String())
	}
	if datePattern.MatchString(st.Body.String()) {
		t.Errorf("state response contains a date string: %s", st.Body.String())
	}
	var state struct {
		PublishedDays int `json:"publishedDays"`
		CandlesByPair []struct {
			Candles []struct {
				Day int `json:"day"`
			} `json:"candles"`
		} `json:"candlesByPair"`
	}
	if err := json.Unmarshal(st.Body.Bytes(), &state); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	if state.PublishedDays != 0 {
		t.Errorf("publishedDays = %d, want 0", state.PublishedDays)
	}
	for _, pc := range state.CandlesByPair {
		for _, c := range pc.Candles {
			if c.Day > 19 {
				t.Errorf("state response leaks future candle day %d", c.Day)
			}
		}
	}

	// 注文レスポンスも未来の価格・日付を含まない。
	ord := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/replay/games/"+itoa(started.GameId)+"/orders",
		strings.NewReader(`{"pairId":`+itoa(usdID)+`,"side":"long","margin":"100","leverage":"10"}`))
	req.Header.Set("Authorization", auth)
	mux.ServeHTTP(ord, req)
	if ord.Code != http.StatusOK {
		t.Fatalf("order status = %d, body=%s", ord.Code, ord.Body.String())
	}
	if datePattern.MatchString(ord.Body.String()) {
		t.Errorf("order response contains a date string: %s", ord.Body.String())
	}
}

// pairIDForSymbol は pairs から symbol の ID を返す。
func pairIDForSymbol(t *testing.T, pairs []db.CurrencyPair, symbol string) int64 {
	t.Helper()
	for _, p := range pairs {
		if p.Symbol == symbol {
			return p.ID
		}
	}
	t.Fatalf("pair %s not found", symbol)
	return 0
}

// itoa は int64 を文字列化する。
func itoa(v int64) string {
	return strconv.FormatInt(v, 10)
}
