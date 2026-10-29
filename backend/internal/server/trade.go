package server

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/shopspring/decimal"

	"fxgame/backend/internal/apigen"
	"fxgame/backend/internal/game"
)

// PlaceOrder は POST /api/orders（#46 WEB-2）。ログイン必須。リクエスト本文を読んで
// TradeService.PlaceOrder を呼ぶだけ（CLAUDE.md §3: ハンドラにルールを書かない）。
// ルール（時間判定・レバレッジ上限・残高・需給圧力）はすべて internal/game 側にある。
func (s *apiServer) PlaceOrder(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(w, r)
	if !ok {
		return
	}

	var req apigen.OrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	size, err := decimal.NewFromString(req.Size)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "数量は数値で入力してください")
		return
	}
	leverage, err := decimal.NewFromString(req.Leverage)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "レバレッジは数値で入力してください")
		return
	}

	// 通貨コードは Discord 側（/price のボタン）と同じく大文字化してから渡す。
	// 入力の正規化であり、通貨の存在判定などのルールは TradeService 側で行う。
	symbol := strings.ToUpper(strings.TrimSpace(req.Symbol))

	result, err := s.trade.PlaceOrder(r.Context(), s.clock.Now(), game.PlaceOrderParams{
		UserID:         user.DiscordID,
		CurrencySymbol: symbol,
		Side:           game.Side(req.Side),
		Size:           size,
		Leverage:       leverage,
	})
	if err != nil {
		writeOrderError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, apigen.OrderResult{
		Symbol:     symbol,
		Side:       result.Position.Side,
		Size:       result.Position.Size.String(),
		EntryPrice: result.Position.EntryPrice.String(),
		Leverage:   result.Position.Leverage.String(),
		NewBalance: result.NewBalance.String(),
	})
}

// ListPositions は GET /api/positions（#46 WEB-2）。ログイン必須。未決済ポジションを
// 含み損益込みで返す（TradeService.ListOpenPositions を呼ぶだけ）。
func (s *apiServer) ListPositions(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(w, r)
	if !ok {
		return
	}

	positions, err := s.trade.ListOpenPositions(r.Context(), s.clock.Now(), user.DiscordID)
	if err != nil {
		log.Printf("list positions: %v", err)
		writeAPIError(w, http.StatusInternalServerError, "internal error")
		return
	}

	out := make([]apigen.Position, 0, len(positions))
	for _, p := range positions {
		out = append(out, apigen.Position{
			Id:            p.Position.ID,
			Symbol:        p.CurrencySymbol,
			Side:          p.Position.Side,
			Size:          p.Position.Size.String(),
			EntryPrice:    p.Position.EntryPrice.String(),
			Leverage:      p.Position.Leverage.String(),
			CurrentPrice:  p.CurrentPrice.String(),
			UnrealizedPnl: p.UnrealizedPnL.String(),
			OpenedAt:      p.Position.OpenedAt.Time,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// ClosePosition は POST /api/positions/{positionId}/close（#46 WEB-2）。ログイン必須。
// TradeService.ClosePosition を呼ぶだけ。他人のポジションは UserID 絞り込みにより
// 自然に ErrPositionNotFound になる（trade.go の GetPositionForUpdate コメント参照）。
func (s *apiServer) ClosePosition(w http.ResponseWriter, r *http.Request, positionId int64) {
	user, ok := s.currentUser(w, r)
	if !ok {
		return
	}

	result, err := s.trade.ClosePosition(r.Context(), s.clock.Now(), game.ClosePositionParams{
		UserID:     user.DiscordID,
		PositionID: positionId,
	})
	if err != nil {
		writeCloseError(w, err)
		return
	}

	pnl := decimal.Zero
	if result.Position.Pnl.Valid {
		pnl = result.Position.Pnl.Decimal
	}
	writeJSON(w, http.StatusOK, apigen.CloseResult{
		Pnl:        pnl.String(),
		NewBalance: result.NewBalance.String(),
	})
}

// writeOrderError は PlaceOrder のサービス層エラーを HTTP ステータスに変換する。
// メッセージは Discord 側（commands.go の handleModalSubmit）と同じ文言に揃える
// （CLAUDE.md §4: 同じルールの見せ方を二重に設計しない）。
func writeOrderError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, game.ErrInvalidSide):
		writeAPIError(w, http.StatusBadRequest, "売買方向は long または short を指定してください")
	case errors.Is(err, game.ErrInvalidSize):
		writeAPIError(w, http.StatusBadRequest, "数量は 0 より大きく 1 兆未満の数で入力してください")
	case errors.Is(err, game.ErrInvalidLeverage):
		writeAPIError(w, http.StatusBadRequest, "レバレッジは 0 より大きく 1 兆未満の数で入力してください")
	case errors.Is(err, game.ErrCurrencyNotFound):
		writeAPIError(w, http.StatusNotFound, "その通貨は見つかりません")
	case errors.Is(err, game.ErrUserNotFound):
		writeAPIError(w, http.StatusNotFound, "ユーザー登録に失敗しました。時間をおいてもう一度お試しください")
	case errors.Is(err, game.ErrInsufficientBalance):
		writeAPIError(w, http.StatusConflict, "残高が不足しています")
	case errors.Is(err, game.ErrLeverageExceedsMax):
		writeAPIError(w, http.StatusConflict, "レバレッジが上限を超えています")
	case errors.Is(err, game.ErrNewOrdersClosed):
		writeAPIError(w, http.StatusConflict, "現在は新規注文を受け付けていません（取引時間外、または終了間際です）")
	default:
		log.Printf("place order: %v", err)
		writeAPIError(w, http.StatusInternalServerError, "internal error")
	}
}

// writeCloseError は ClosePosition のサービス層エラーを HTTP ステータスに変換する。
func writeCloseError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, game.ErrPositionNotFound):
		writeAPIError(w, http.StatusNotFound, "そのポジションは見つかりません")
	case errors.Is(err, game.ErrPositionAlreadyClosed):
		writeAPIError(w, http.StatusConflict, "そのポジションはすでに決済済みです")
	case errors.Is(err, game.ErrUserNotFound):
		writeAPIError(w, http.StatusNotFound, "ユーザーが見つかりません")
	case errors.Is(err, game.ErrCurrencyNotFound):
		writeAPIError(w, http.StatusNotFound, "その通貨は見つかりません")
	default:
		log.Printf("close position: %v", err)
		writeAPIError(w, http.StatusInternalServerError, "internal error")
	}
}
