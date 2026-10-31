package server

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	openapiTypes "github.com/oapi-codegen/runtime/types"
	"github.com/shopspring/decimal"

	"fxgame/backend/internal/apigen"
	"fxgame/backend/internal/game"
)

// bearerToken は Authorization: Bearer <token> ヘッダーからゲームトークンを取り出す。
func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(h, prefix))
	if token == "" {
		return "", false
	}
	return token, true
}

// StartReplayGame は POST /api/replay/games。ログイン不要（ゲスト可）。ログインしていれば
// users を参照し、していなければゲスト名を必須にする（design.md §13）。
func (s *apiServer) StartReplayGame(w http.ResponseWriter, r *http.Request) {
	var req apigen.ReplayStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	userID := ""
	guestName := ""
	if req.GuestName != nil {
		guestName = *req.GuestName
	}

	// Cookie セッションがあって有効ならログインユーザーとして扱う。無効・期限切れは
	// ゲスト扱い（ログインなしでも遊べるため 401 にはしない）。
	if cookie, err := r.Cookie(sessionCookieName); err == nil && cookie.Value != "" {
		if user, err := s.auth.CurrentUser(r.Context(), cookie.Value); err == nil {
			userID = user.DiscordID
		}
	}

	result, err := s.replay.StartGame(r.Context(), userID, guestName)
	if err != nil {
		writeReplayError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, replayStartedToAPI(result))
}

// GetReplayGameState は GET /api/replay/games/{gameId}。ゲーム状態を返す（遅延評価込み）。
func (s *apiServer) GetReplayGameState(w http.ResponseWriter, r *http.Request, gameId int64) {
	token, ok := bearerToken(r)
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, "missing or invalid token")
		return
	}

	state, err := s.replay.GetState(r.Context(), token, gameId)
	if err != nil {
		writeReplayError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, replayStateToAPI(state))
}

// PlaceReplayOrder は POST /api/replay/games/{gameId}/orders。
func (s *apiServer) PlaceReplayOrder(w http.ResponseWriter, r *http.Request, gameId int64) {
	token, ok := bearerToken(r)
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, "missing or invalid token")
		return
	}

	var req apigen.ReplayOrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	margin, err := decimal.NewFromString(req.Margin)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "証拠金は数値で入力してください")
		return
	}
	leverage, err := decimal.NewFromString(req.Leverage)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "レバレッジは数値で入力してください")
		return
	}

	result, err := s.replay.PlaceOrder(r.Context(), token, gameId, game.ReplayOrderParams{
		PairID:   req.PairId,
		Side:     game.Side(req.Side),
		Margin:   margin,
		Leverage: leverage,
	})
	if err != nil {
		writeReplayError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, apigen.ReplayOrderResult{
		Position: replayPositionToAPI(result.Position),
		Funds:    result.Funds.String(),
	})
}

// CloseReplayPosition は POST /api/replay/games/{gameId}/positions/{positionId}/close。
func (s *apiServer) CloseReplayPosition(w http.ResponseWriter, r *http.Request, gameId int64, positionId int64) {
	token, ok := bearerToken(r)
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, "missing or invalid token")
		return
	}

	result, err := s.replay.ClosePosition(r.Context(), token, gameId, positionId)
	if err != nil {
		writeReplayError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, apigen.ReplayCloseResult{
		Position: replayPositionToAPI(result.Position),
		Pnl:      result.Pnl.String(),
		Funds:    result.Funds.String(),
	})
}

// AddReplayMargin は POST /api/replay/games/{gameId}/positions/{positionId}/margin。
func (s *apiServer) AddReplayMargin(w http.ResponseWriter, r *http.Request, gameId int64, positionId int64) {
	token, ok := bearerToken(r)
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, "missing or invalid token")
		return
	}

	var req apigen.ReplayAddMarginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	amount, err := decimal.NewFromString(req.Amount)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "追加額は数値で入力してください")
		return
	}

	result, err := s.replay.AddMargin(r.Context(), token, gameId, positionId, amount)
	if err != nil {
		writeReplayError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, apigen.ReplayAddMarginResult{
		Position: replayPositionToAPI(result.Position),
		Funds:    result.Funds.String(),
	})
}

// UnlockReplayLeverage は POST /api/replay/games/{gameId}/leverage。
func (s *apiServer) UnlockReplayLeverage(w http.ResponseWriter, r *http.Request, gameId int64) {
	token, ok := bearerToken(r)
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, "missing or invalid token")
		return
	}

	var req apigen.ReplayUnlockLeverageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	result, err := s.replay.UnlockLeverage(r.Context(), token, gameId, req.PairId)
	if err != nil {
		writeReplayError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, apigen.ReplayUnlockLeverageResult{
		PairId:      result.PairID,
		Tier:        result.Tier,
		MaxLeverage: result.MaxLeverage.String(),
		Cost:        result.Cost.String(),
		Funds:       result.Funds.String(),
	})
}

// ChangeReplaySpeed は POST /api/replay/games/{gameId}/speed。
func (s *apiServer) ChangeReplaySpeed(w http.ResponseWriter, r *http.Request, gameId int64) {
	token, ok := bearerToken(r)
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, "missing or invalid token")
		return
	}

	var req apigen.ReplayChangeSpeedRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	result, err := s.replay.ChangeSpeed(r.Context(), token, gameId, req.Speed)
	if err != nil {
		writeReplayError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, apigen.ReplayChangeSpeedResult{
		Speed:         result.Speed,
		PublishedDays: result.PublishedDays,
	})
}

// writeReplayError は過去チャートモードのサービス層エラーを HTTP ステータスに変換する。
func writeReplayError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, game.ErrReplayInvalidGuestName),
		errors.Is(err, game.ErrReplayInvalidMargin),
		errors.Is(err, game.ErrReplayInvalidLeverage),
		errors.Is(err, game.ErrReplayInvalidAmount),
		errors.Is(err, game.ErrReplayInvalidSpeed),
		errors.Is(err, game.ErrInvalidSide):
		writeAPIError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, game.ErrReplayGameNotFound),
		errors.Is(err, game.ErrReplayPairNotFound),
		errors.Is(err, game.ErrReplayPositionNotFound):
		writeAPIError(w, http.StatusNotFound, "not found")
	case errors.Is(err, game.ErrReplayGameFinished),
		errors.Is(err, game.ErrReplayPositionClosed),
		errors.Is(err, game.ErrReplayOppositePosition),
		errors.Is(err, game.ErrLeverageExceedsMax),
		errors.Is(err, game.ErrInsufficientBalance):
		writeAPIError(w, http.StatusConflict, err.Error())
	case errors.Is(err, game.ErrReplayNoInterval):
		writeAPIError(w, http.StatusConflict, err.Error())
	default:
		log.Printf("replay: %v", err)
		writeAPIError(w, http.StatusInternalServerError, "internal error")
	}
}

// ---- 型変換（decimal は文字列に。CLAUDE.md §5.2）----

func replayPairToAPI(p game.ReplayPair) apigen.ReplayPair {
	return apigen.ReplayPair{
		PairId:       p.PairID,
		Symbol:       p.Symbol,
		BaseLeverage: p.BaseLeverage.String(),
		MaxLeverage:  p.MaxLeverage.String(),
		FeeRate:      p.FeeRate.String(),
	}
}

func replayPairsToAPI(ps []game.ReplayPair) []apigen.ReplayPair {
	out := make([]apigen.ReplayPair, 0, len(ps))
	for _, p := range ps {
		out = append(out, replayPairToAPI(p))
	}
	return out
}

func replayCandlesToAPI(pc game.ReplayPairCandles) apigen.ReplayPairCandles {
	out := apigen.ReplayPairCandles{PairId: pc.PairID, Candles: make([]apigen.ReplayCandle, 0, len(pc.Candles))}
	for _, c := range pc.Candles {
		api := apigen.ReplayCandle{
			Day:   c.Day,
			Open:  c.Open.String(),
			High:  c.High.String(),
			Low:   c.Low.String(),
			Close: c.Close.String(),
		}
		if c.Date != nil {
			api.Date = &openapiTypes.Date{Time: *c.Date}
		}
		out.Candles = append(out.Candles, api)
	}
	return out
}

func replayPairCandlesToAPI(pcs []game.ReplayPairCandles) []apigen.ReplayPairCandles {
	out := make([]apigen.ReplayPairCandles, 0, len(pcs))
	for _, pc := range pcs {
		out = append(out, replayCandlesToAPI(pc))
	}
	return out
}

func replayPositionToAPI(p game.ReplayPositionView) apigen.ReplayPosition {
	out := apigen.ReplayPosition{
		Id:              p.ID,
		PairId:          p.PairID,
		Symbol:          p.Symbol,
		Side:            string(p.Side),
		InitialMargin:   p.InitialMargin.String(),
		Margin:          p.Margin.String(),
		Leverage:        p.Leverage,
		EntryPrice:      p.EntryPrice.String(),
		EntryDay:        p.EntryDay,
		LiquidationLine: p.LiquidationLine.String(),
		Status:          p.Status,
	}
	if p.ClosePrice != nil {
		v := p.ClosePrice.String()
		out.ClosePrice = &v
	}
	if p.Pnl != nil {
		v := p.Pnl.String()
		out.Pnl = &v
	}
	return out
}

func replayPositionsToAPI(ps []game.ReplayPositionView) []apigen.ReplayPosition {
	out := make([]apigen.ReplayPosition, 0, len(ps))
	for _, p := range ps {
		out = append(out, replayPositionToAPI(p))
	}
	return out
}

func replayStateToAPI(s game.ReplayState) apigen.ReplayGameState {
	out := apigen.ReplayGameState{
		GameId:        s.GameID,
		Status:        s.Status,
		StartYear:     s.StartYear,
		Funds:         s.Funds.String(),
		Speed:         s.Speed,
		PublishedDays: s.PublishedDays,
		Pairs:         replayPairsToAPI(s.Pairs),
		Positions:     replayPositionsToAPI(s.Positions),
		CandlesByPair: replayPairCandlesToAPI(s.Candles),
	}
	if s.IntervalStart != nil {
		out.IntervalStart = &openapiTypes.Date{Time: *s.IntervalStart}
	}
	if s.IntervalEnd != nil {
		out.IntervalEnd = &openapiTypes.Date{Time: *s.IntervalEnd}
	}
	if s.FinalFunds != nil {
		v := s.FinalFunds.String()
		out.FinalFunds = &v
	}
	return out
}

func replayStartedToAPI(s game.ReplayStartResult) apigen.ReplayGameStarted {
	return apigen.ReplayGameStarted{
		GameId:        s.GameID,
		Token:         s.Token,
		StartYear:     s.StartYear,
		Funds:         s.Funds.String(),
		Speed:         s.Speed,
		PublishedDays: s.PublishedDays,
		Pairs:         replayPairsToAPI(s.Pairs),
		CandlesByPair: replayPairCandlesToAPI(s.Candles),
	}
}
