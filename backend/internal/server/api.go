package server

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"fxgame/backend/internal/apigen"
	"fxgame/backend/internal/game"
)

// apiServer は api/openapi.yaml から生成された apigen.ServerInterface の実装。
// ロジックは持たず internal/game を呼ぶだけ（CLAUDE.md §3）。
type apiServer struct {
	auth    *game.AuthService
	quote   *game.QuoteService
	session *game.SessionService
	trade   *game.TradeService
	clock   game.Clock
}

var _ apigen.ServerInterface = (*apiServer)(nil)

func registerAPIRoutes(mux *http.ServeMux, cfg Config) {
	apigen.HandlerFromMux(&apiServer{
		auth:    cfg.Auth,
		quote:   cfg.Quote,
		session: cfg.Session,
		trade:   cfg.Trade,
		clock:   cfg.Clock,
	}, mux)
}

// currentUser は Cookie セッションからログイン中ユーザーを引く（GET /api/me・注文・
// 建玉一覧・決済などのログイン必須エンドポイント共通の処理。CLAUDE.md §4 で同じロジックを
// 二重実装しない）。未ログイン・セッション切れなら 401 を書いて ok=false を返す。
func (s *apiServer) currentUser(w http.ResponseWriter, r *http.Request) (game.User, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		writeAPIError(w, http.StatusUnauthorized, "not logged in")
		return game.User{}, false
	}

	user, err := s.auth.CurrentUser(r.Context(), cookie.Value)
	if err != nil {
		if errors.Is(err, game.ErrSessionInvalid) {
			writeAPIError(w, http.StatusUnauthorized, "not logged in")
			return game.User{}, false
		}
		log.Printf("current user: %v", err)
		writeAPIError(w, http.StatusInternalServerError, "internal error")
		return game.User{}, false
	}
	return user, true
}

// GetMe は GET /api/me。Cookie セッションからユーザーを特定して返す。
func (s *apiServer) GetMe(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(w, r)
	if !ok {
		return
	}

	writeJSON(w, http.StatusOK, apigen.Me{
		DiscordId:   user.DiscordID,
		DisplayName: user.DisplayName,
		Balance:     user.Balance.String(),
	})
}

// ListCurrencies は GET /api/currencies。通貨一覧（現在価格と前日比）を返す。
// ログイン不要（公開）。通貨は currencies テーブルの全行から作る（CLAUDE.md §5.3）。
func (s *apiServer) ListCurrencies(w http.ResponseWriter, r *http.Request) {
	summaries, err := s.quote.ListCurrencySummaries(r.Context(), s.clock.Now())
	if err != nil {
		log.Printf("list currencies: %v", err)
		writeAPIError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, currencySummariesToAPI(summaries))
}

// GetCurrency は GET /api/currencies/{symbol}。通貨1件の表示情報を返す。ログイン不要。
func (s *apiServer) GetCurrency(w http.ResponseWriter, r *http.Request, symbol string) {
	summary, err := s.quote.GetCurrencySummary(r.Context(), s.clock.Now(), symbol)
	if err != nil {
		if errors.Is(err, game.ErrCurrencyNotFound) {
			writeAPIError(w, http.StatusNotFound, "currency not found")
			return
		}
		log.Printf("get currency: %v", err)
		writeAPIError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, currencySummaryToAPI(summary))
}

// GetCandles は GET /api/currencies/{symbol}/candles。ログイン不要（公開）。
// 足の種類（1分足/5分足）は resolution クエリパラメータで受ける（issue #45 指示）。
func (s *apiServer) GetCandles(w http.ResponseWriter, r *http.Request, symbol string, params apigen.GetCandlesParams) {
	if !params.Resolution.Valid() {
		writeAPIError(w, http.StatusBadRequest, "invalid resolution")
		return
	}

	result, err := s.quote.GetCandles(r.Context(), s.clock.Now(), symbol, string(params.Resolution))
	if err != nil {
		if errors.Is(err, game.ErrCurrencyNotFound) {
			writeAPIError(w, http.StatusNotFound, "currency not found")
			return
		}
		log.Printf("get candles: %v", err)
		writeAPIError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, candlesToAPI(result))
}

// GetMarketStatus は GET /api/market/status。取引時間の状態を返す。ログイン不要。
// 時刻はサーバー側の Clock から取り、ブラウザの時計では計算しない（#99 指示）。
func (s *apiServer) GetMarketStatus(w http.ResponseWriter, r *http.Request) {
	status := s.session.MarketStatus(s.clock.Now())
	writeJSON(w, http.StatusOK, apigen.MarketStatus{
		Open:             status.Open,
		AlwaysOpen:       status.AlwaysOpen,
		RemainingMinutes: status.RemainingMinutes,
		NextStartAt:      status.NextStartAt,
	})
}

// currencySummaryToAPI はサービス層の表示情報を API の型に変換する。
// decimal は文字列に変換して返す（CLAUDE.md §5.2）。
func currencySummaryToAPI(s game.CurrencySummary) apigen.CurrencySummary {
	out := apigen.CurrencySummary{
		Symbol:       s.Symbol,
		Name:         s.Name,
		CurrentPrice: s.CurrentPrice.String(),
	}
	if s.DayChangePercent != nil {
		// 前日比は小数2桁の文字列で返す（丸めはサービス層で済んでいる。§5.2 float禁止）。
		v := s.DayChangePercent.StringFixed(2)
		out.DayChangePercent = &v
	}
	return out
}

func currencySummariesToAPI(ss []game.CurrencySummary) []apigen.CurrencySummary {
	out := make([]apigen.CurrencySummary, len(ss))
	for i, s := range ss {
		out[i] = currencySummaryToAPI(s)
	}
	return out
}

// candlesToAPI はサービス層のローソク足・イベントを API の型に変換する。
// decimal は文字列に変換して返す（CLAUDE.md §5.2）。
func candlesToAPI(r game.CandlesResult) apigen.Candles {
	out := apigen.Candles{
		Symbol:     r.Symbol,
		Resolution: r.Resolution,
		Candles:    make([]apigen.Candle, 0, len(r.Candles)),
		Events:     make([]apigen.ChartEvent, 0, len(r.Events)),
	}
	for _, c := range r.Candles {
		out.Candles = append(out.Candles, apigen.Candle{
			Time:      c.Time,
			Open:      c.Open.String(),
			High:      c.High.String(),
			Low:       c.Low.String(),
			Close:     c.Close.String(),
			BasePrice: c.BasePrice.String(),
			IsOpening: c.IsOpening,
		})
	}
	for _, e := range r.Events {
		out.Events = append(out.Events, apigen.ChartEvent{
			Time:      e.Time,
			Type:      e.Type,
			Magnitude: e.Magnitude.String(),
		})
	}
	return out
}

func writeAPIError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, apigen.Error{Message: message})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write json response: %v", err)
	}
}
