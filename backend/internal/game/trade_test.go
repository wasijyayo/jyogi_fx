package game

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"fxgame/backend/internal/db"
)

// validOrderParams は「検証を1項目ずつ壊す」テスト用の基準となる正常値。
// AlwaysOpen なセッションで呼ぶ限り、この値そのものは全ての入力検証を通過する
// （実際にDBへ到達するとpoolがnilなので、その手前で弾かれることをテストする）。
func validOrderParams() PlaceOrderParams {
	return PlaceOrderParams{
		UserID:         "u1",
		CurrencySymbol: "JOG",
		Side:           SideLong,
		Size:           decimal.NewFromInt(10),
		Leverage:       decimal.NewFromInt(5),
	}
}

// TestPlaceOrder_入力検証はDBに触る前に弾かれる は、
// pool=nil の TradeService でも検証エラーがDBアクセス(nilパニック)より先に返ることを確認する。
func TestPlaceOrder_入力検証はDBに触る前に弾かれる(t *testing.T) {
	// AlwaysOpen: true にして、セッション判定ではなく検証対象の項目だけを切り出して確認する。
	sessionSvc := NewSessionService(nil, RealClock{}, SessionConfig{AlwaysOpen: true})
	svc := NewTradeService(nil, RealClock{}, sessionSvc, nil, decimal.Zero, decimal.Zero, nil)
	now := time.Date(2026, 1, 1, 12, 30, 0, 0, jst)

	tests := []struct {
		name    string
		mutate  func(p PlaceOrderParams) PlaceOrderParams
		wantErr error
	}{
		{
			name:    "sideがlong/short以外",
			mutate:  func(p PlaceOrderParams) PlaceOrderParams { p.Side = "buy"; return p },
			wantErr: ErrInvalidSide,
		},
		{
			name:    "sizeが0",
			mutate:  func(p PlaceOrderParams) PlaceOrderParams { p.Size = decimal.Zero; return p },
			wantErr: ErrInvalidSize,
		},
		{
			name:    "sizeが負",
			mutate:  func(p PlaceOrderParams) PlaceOrderParams { p.Size = decimal.NewFromInt(-1); return p },
			wantErr: ErrInvalidSize,
		},
		{
			name:    "leverageが0",
			mutate:  func(p PlaceOrderParams) PlaceOrderParams { p.Leverage = decimal.Zero; return p },
			wantErr: ErrInvalidLeverage,
		},
		{
			name:    "leverageが負",
			mutate:  func(p PlaceOrderParams) PlaceOrderParams { p.Leverage = decimal.NewFromInt(-1); return p },
			wantErr: ErrInvalidLeverage,
		},
		// 指数表記の巨大な値・極小の値は、ロック中に巨大な整数を作らないよう DB の前で弾く。
		{
			name:    "sizeが巨大な指数",
			mutate:  func(p PlaceOrderParams) PlaceOrderParams { p.Size = decimal.RequireFromString("1e100000000"); return p },
			wantErr: ErrInvalidSize,
		},
		{
			name:    "sizeが極小の指数",
			mutate:  func(p PlaceOrderParams) PlaceOrderParams { p.Size = decimal.RequireFromString("1e-100000000"); return p },
			wantErr: ErrInvalidSize,
		},
		{
			name:    "sizeが1兆以上",
			mutate:  func(p PlaceOrderParams) PlaceOrderParams { p.Size = decimal.New(1, 12); return p },
			wantErr: ErrInvalidSize,
		},
		{
			name:    "leverageが指数の上限",
			mutate:  func(p PlaceOrderParams) PlaceOrderParams { p.Leverage = decimal.RequireFromString("1e2147483647"); return p },
			wantErr: ErrInvalidLeverage,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.PlaceOrder(context.Background(), now, tt.mutate(validOrderParams()))
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("PlaceOrder error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// TestOrderValueInRange は数量・レバレッジの範囲チェックの境界を確認する。
func TestOrderValueInRange(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{"10", true},
		{"0.5", true},
		{"999999999999.99999999", true},
		{"10.000000000", true}, // 末尾の 0 で指数が -9 になっても通す
		{"1000000000000", false},
		{"1e12", false},
		{"1e13", false},
		{"1e-19", false},
	}
	for _, tt := range tests {
		if got := orderValueInRange(decimal.RequireFromString(tt.value)); got != tt.want {
			t.Errorf("orderValueInRange(%s) = %v, want %v", tt.value, got, tt.want)
		}
	}
}

// TestPlaceOrder_セッション外は新規注文を拒否する は #34 の IsNewOrderAllowed を
// 使っていること（確定#14/#48）を、DBに触らずに確認する。
func TestPlaceOrder_セッション外は新規注文を拒否する(t *testing.T) {
	sessionSvc := NewSessionService(nil, RealClock{}, SessionConfig{}) // AlwaysOpen なし
	svc := NewTradeService(nil, RealClock{}, sessionSvc, nil, decimal.Zero, decimal.Zero, nil)

	tests := []struct {
		name string
		now  time.Time
	}{
		{"セッション外(深夜)", time.Date(2026, 1, 1, 3, 0, 0, 0, jst)},
		{"終了1分前(12:59台、確定#14/#48)", time.Date(2026, 1, 1, 12, 59, 0, 0, jst)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.PlaceOrder(context.Background(), tt.now, validOrderParams())
			if !errors.Is(err, ErrNewOrdersClosed) {
				t.Errorf("PlaceOrder error = %v, want %v", err, ErrNewOrdersClosed)
			}
		})
	}
}

// TestPositionPnLPips は design.md §2.8 のpips定義（(close-open)/0.01）と同じ式で
// 値動き幅を求めること、PositionPnLと違いsizeを掛けないことを確認する（#82）。
func TestPositionPnLPips(t *testing.T) {
	tests := []struct {
		name       string
		side       Side
		entryPrice decimal.Decimal
		exitPrice  decimal.Decimal
		want       decimal.Decimal
	}{
		{"ロング+値上がり=利益pips", SideLong, decimal.NewFromInt(100), decimal.NewFromFloat(102.5), decimal.NewFromInt(250)},
		{"ロング+値下がり=損失pips(マイナス)", SideLong, decimal.NewFromInt(100), decimal.NewFromFloat(98), decimal.NewFromInt(-200)},
		{"ショート+値下がり=利益pips", SideShort, decimal.NewFromInt(100), decimal.NewFromFloat(97), decimal.NewFromInt(300)},
		{"ショート+値上がり=損失pips(マイナス)", SideShort, decimal.NewFromInt(100), decimal.NewFromFloat(101), decimal.NewFromInt(-100)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := db.Position{Side: string(tt.side), EntryPrice: tt.entryPrice, Size: decimal.NewFromInt(999)} // sizeが結果に影響しないことも兼ねて確認
			got := PositionPnLPips(p, tt.exitPrice)
			if !got.Equal(tt.want) {
				t.Errorf("PositionPnLPips = %s, want %s", got, tt.want)
			}
		})
	}
}
