package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestGetCandles_HTTP は GET /api/currencies/{symbol}/candles の HTTP 配線（ルーティング・
// パラメータ検証・JSON の形）を確認する。ロジック本体（集約・イベント漏洩防止）は
// internal/game の chart_integration_test.go で検証している。
func TestGetCandles_HTTP(t *testing.T) {
	pool := newTestPool(t)

	t.Run("不正なresolutionは400", func(t *testing.T) {
		mux := newMux(t, pool, fixedClock{now: time.Date(2099, 7, 1, 12, 5, 0, 0, jst)}, false)
		req := httptest.NewRequest(http.MethodGet, "/api/currencies/JOG/candles?resolution=15m", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
		}
	})

	t.Run("存在しない通貨は404", func(t *testing.T) {
		mux := newMux(t, pool, fixedClock{now: time.Date(2099, 7, 1, 12, 5, 0, 0, jst)}, false)
		req := httptest.NewRequest(http.MethodGet, "/api/currencies/NOSUCH/candles?resolution=1m", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusNotFound, rec.Body.String())
		}
	})

	t.Run("1mを200で返す（decimalは文字列）", func(t *testing.T) {
		ctx := context.Background()
		epoch := time.Date(2099, 7, 1, 12, 0, 0, 0, jst)

		// 使い捨て通貨 + 当日セッション + 寄り付き1本を投入する。
		if _, err := pool.Exec(ctx, `
			INSERT INTO currencies (symbol, name, base_price, drift, volatility, lambda, k, liquidity,
				pressure, pressure_at, off_session_scale, seed, epoch_at, max_leverage, fee_rate)
			VALUES ('HTTPCHRT', 'HTTPチャート', 100, 0, 0.0020, 0.1732867951, 0.0100, 40000,
				0, $1, 0.0500, 424242, $1, 10.00, 0.000500)`,
			epoch); err != nil {
			t.Fatalf("seed currency: %v", err)
		}
		var currencyID int64
		if err := pool.QueryRow(ctx, `SELECT id FROM currencies WHERE symbol = 'HTTPCHRT'`).Scan(&currencyID); err != nil {
			t.Fatalf("select currency id: %v", err)
		}
		var sessionID int64
		if err := pool.QueryRow(ctx, `
			INSERT INTO game_sessions (date, seed, opened_at, closed_at)
			VALUES ($1, 1, $2, $3) RETURNING id`,
			epoch, epoch, epoch.Add(time.Hour)).Scan(&sessionID); err != nil {
			t.Fatalf("seed game session: %v", err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO price_ticks (currency_id, session_id, tick_index, ticked_at,
				base_price, pressure, net_volume, open, high, low, close, is_opening)
			VALUES ($1, $2, 0, $3, 100, 0, 0, 100, 100, 100, 100, TRUE)`,
			currencyID, sessionID, epoch); err != nil {
			t.Fatalf("seed price tick: %v", err)
		}

		t.Cleanup(func() {
			cctx := context.Background()
			if _, err := pool.Exec(cctx, `DELETE FROM price_ticks WHERE currency_id = $1`, currencyID); err != nil {
				t.Errorf("cleanup: delete price_ticks: %v", err)
			}
			if _, err := pool.Exec(cctx, `DELETE FROM game_sessions WHERE id = $1`, sessionID); err != nil {
				t.Errorf("cleanup: delete game_sessions: %v", err)
			}
			if _, err := pool.Exec(cctx, `DELETE FROM currencies WHERE id = $1`, currencyID); err != nil {
				t.Errorf("cleanup: delete currencies: %v", err)
			}
		})

		mux := newMux(t, pool, fixedClock{now: time.Date(2099, 7, 1, 12, 5, 0, 0, jst)}, false)
		req := httptest.NewRequest(http.MethodGet, "/api/currencies/HTTPCHRT/candles?resolution=1m", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
		}

		var got struct {
			Symbol     string `json:"symbol"`
			Resolution string `json:"resolution"`
			Candles    []struct {
				Time      string `json:"time"`
				Open      string `json:"open"`
				BasePrice string `json:"basePrice"`
				IsOpening bool   `json:"isOpening"`
			} `json:"candles"`
			Events []json.RawMessage `json:"events"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if got.Symbol != "HTTPCHRT" || got.Resolution != "1m" {
			t.Errorf("symbol=%s resolution=%s, want HTTPCHRT / 1m", got.Symbol, got.Resolution)
		}
		if len(got.Candles) != 1 {
			t.Fatalf("candles 本数 = %d, want 1", len(got.Candles))
		}
		c := got.Candles[0]
		if c.Open != "100" || c.BasePrice != "100" {
			t.Errorf("decimalが文字列で返っていない: open=%q basePrice=%q", c.Open, c.BasePrice)
		}
		if !c.IsOpening {
			t.Error("isOpening = false, want true")
		}
		if c.Time == "" {
			t.Error("time が空")
		}
	})
}
