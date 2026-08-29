package game

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"fxgame/backend/internal/db"
	"fxgame/backend/internal/discord"
)

// newTestNotifyService はモックDiscordサーバーに投稿する NotifyService を組み立てる。
// 呼び出し側は返り値の *contentCapture で最後に投稿された本文を検証できる。
type contentCapture struct {
	calls   int
	content string
}

func newTestNotifyService(t *testing.T) (*NotifyService, *contentCapture) {
	t.Helper()
	capture := &contentCapture{}

	mux := http.NewServeMux()
	mux.HandleFunc("/channels/notify-chan/messages", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Content string `json:"content"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		capture.calls++
		capture.content = body.Content
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "notify-msg"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	notify := NewNotifyService(discord.MessagesConfig{BotToken: "test-token", APIBaseURL: srv.URL}, "notify-chan")
	return notify, capture
}

// TestNotifyService_nilレシーバは何もせずエラーも返さない は、TradeService/TickService
// にNotifyServiceを設定しない（未設定環境・多くの既存テスト）でも安全に呼べることを確認する。
func TestNotifyService_nilレシーバは何もせずエラーも返さない(t *testing.T) {
	var n *NotifyService
	if err := n.LargeTrade(context.Background(), "太郎", "JOG", SideLong, decimal.NewFromFloat(3.2)); err != nil {
		t.Errorf("nilレシーバでエラーが返った: %v", err)
	}
}

// TestNotifyService_LargeTrade は design.md §6.7「〇〇がJOGを大量購入。価格が+3.2%変動」の
// 文面を確認する。
func TestNotifyService_LargeTrade(t *testing.T) {
	notify, capture := newTestNotifyService(t)

	t.Run("買いは大量購入", func(t *testing.T) {
		if err := notify.LargeTrade(context.Background(), "太郎", "JOG", SideLong, decimal.NewFromFloat(3.2)); err != nil {
			t.Fatalf("LargeTrade: %v", err)
		}
		if !strings.Contains(capture.content, "太郎") || !strings.Contains(capture.content, "JOG") ||
			!strings.Contains(capture.content, "大量購入") || !strings.Contains(capture.content, "+3.2%") {
			t.Errorf("content = %q", capture.content)
		}
	})

	t.Run("売りは大量売却でマイナス表記", func(t *testing.T) {
		if err := notify.LargeTrade(context.Background(), "花子", "CHEBU", SideShort, decimal.NewFromFloat(-5.5)); err != nil {
			t.Fatalf("LargeTrade: %v", err)
		}
		if !strings.Contains(capture.content, "大量売却") || !strings.Contains(capture.content, "-5.5%") {
			t.Errorf("content = %q", capture.content)
		}
	})
}

// TestNotifyService_Liquidation はroast_enabledでいじり文言/中立文言を切り替えることを確認する
// （design.md §6.8「いじりの強度は個別に切れるようにする」）。
func TestNotifyService_Liquidation(t *testing.T) {
	notify, capture := newTestNotifyService(t)

	t.Run("roast_enabled=trueはいじりテンプレのどれか", func(t *testing.T) {
		if err := notify.Liquidation(context.Background(), "太郎", "WASI", decimal.NewFromInt(10), true); err != nil {
			t.Fatalf("Liquidation: %v", err)
		}
		found := false
		for _, tmpl := range roastTemplates {
			if capture.content == fmt.Sprintf(tmpl, "太郎", "WASI", "10") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("content = %q はroastTemplatesのいずれとも一致しない", capture.content)
		}
	})

	t.Run("roast_enabled=falseは中立文言", func(t *testing.T) {
		if err := notify.Liquidation(context.Background(), "花子", "JOG", decimal.NewFromInt(5), false); err != nil {
			t.Fatalf("Liquidation: %v", err)
		}
		want := fmt.Sprintf(neutralLiquidationTemplate, "花子", "JOG", "5")
		if capture.content != want {
			t.Errorf("content = %q, want %q", capture.content, want)
		}
		if strings.ContainsAny(capture.content, "💥💀🎇🪦🔥") {
			t.Errorf("中立文言に演出絵文字が含まれている: %q", capture.content)
		}
	})
}

// TestNotifyService_EventTeaser はテンプレのいずれかが使われ、通貨シンボルを含むことを確認する
// （design.md §5.3。種別は明かさない仕様のためシンボル以外は検証しない）。
func TestNotifyService_EventTeaser(t *testing.T) {
	notify, capture := newTestNotifyService(t)
	if err := notify.EventTeaser(context.Background(), "CHEBU"); err != nil {
		t.Fatalf("EventTeaser: %v", err)
	}
	if !strings.Contains(capture.content, "CHEBU") {
		t.Errorf("content = %q に通貨シンボルが含まれない", capture.content)
	}
	if !strings.Contains(capture.content, "📡") {
		t.Errorf("content = %q に予兆の絵文字が含まれない", capture.content)
	}
}

// TestNotifyService_EventFired は種別ごとに内容が変わることを確認する（design.md §5.4）。
func TestNotifyService_EventFired(t *testing.T) {
	notify, capture := newTestNotifyService(t)

	t.Run("shock上昇は+表記", func(t *testing.T) {
		e := db.Event{Type: EventTypeShock, Magnitude: decimal.NewFromFloat(0.14)}
		if err := notify.EventFired(context.Background(), e, "CHEBU"); err != nil {
			t.Fatalf("EventFired: %v", err)
		}
		if !strings.Contains(capture.content, "+14.0%") {
			t.Errorf("content = %q", capture.content)
		}
	})

	t.Run("shock下落はマイナス表記", func(t *testing.T) {
		e := db.Event{Type: EventTypeShock, Magnitude: decimal.NewFromFloat(-0.08)}
		if err := notify.EventFired(context.Background(), e, "WASI"); err != nil {
			t.Fatalf("EventFired: %v", err)
		}
		if !strings.Contains(capture.content, "-8.0%") {
			t.Errorf("content = %q", capture.content)
		}
	})

	t.Run("vol_upは持続tick数を含む", func(t *testing.T) {
		e := db.Event{Type: EventTypeVolUp, DurationTicks: 4}
		if err := notify.EventFired(context.Background(), e, "JOG"); err != nil {
			t.Fatalf("EventFired: %v", err)
		}
		if !strings.Contains(capture.content, "JOG") || !strings.Contains(capture.content, "4分") {
			t.Errorf("content = %q", capture.content)
		}
	})

	t.Run("liquidity_drainは持続tick数を含む", func(t *testing.T) {
		e := db.Event{Type: EventTypeLiquidityDrain, DurationTicks: 5}
		if err := notify.EventFired(context.Background(), e, "CHEBU"); err != nil {
			t.Fatalf("EventFired: %v", err)
		}
		if !strings.Contains(capture.content, "CHEBU") || !strings.Contains(capture.content, "5分") {
			t.Errorf("content = %q", capture.content)
		}
	})
}

// TestSessionGap_isLarge は design.md §2.8「2σ超えなら大きな窓」の境界を確認する。
func TestSessionGap_isLarge(t *testing.T) {
	g := SessionGap{Open: decimal.NewFromInt(100), Close: decimal.NewFromFloat(102.5), Sigma: decimal.NewFromFloat(0.01)} // 2σ=2%
	if !g.isLarge() {
		t.Errorf("変化率2.5%%はsigma=1%%の2σ(2%%)を超えるはずなのにisLarge=false")
	}
	g2 := SessionGap{Open: decimal.NewFromInt(100), Close: decimal.NewFromFloat(101), Sigma: decimal.NewFromFloat(0.01)}
	if g2.isLarge() {
		t.Errorf("変化率1%%は2σ(2%%)を超えないはずなのにisLarge=true")
	}
}

// TestNotifyService_SessionOpen は design.md §2.8 の通常時/大きな窓時の見出し切り替えを確認する。
func TestNotifyService_SessionOpen(t *testing.T) {
	notify, capture := newTestNotifyService(t)

	t.Run("通常時の見出し", func(t *testing.T) {
		gaps := []SessionGap{
			{Symbol: "JOG", Open: decimal.NewFromFloat(100.42), Close: decimal.NewFromFloat(100.57), Sigma: decimal.NewFromFloat(0.01)},
		}
		if err := notify.SessionOpen(context.Background(), gaps); err != nil {
			t.Fatalf("SessionOpen: %v", err)
		}
		if !strings.Contains(capture.content, "📈 セッション開始") {
			t.Errorf("content = %q", capture.content)
		}
		if strings.Contains(capture.content, "大きな窓") {
			t.Errorf("通常の変化幅なのに「大きな窓」表記が出た: %q", capture.content)
		}
	})

	t.Run("2σ超えは大きな窓の見出しと警告マーク", func(t *testing.T) {
		gaps := []SessionGap{
			{Symbol: "CHEBU", Open: decimal.NewFromInt(100), Close: decimal.NewFromFloat(103), Sigma: decimal.NewFromFloat(0.01)},
		}
		if err := notify.SessionOpen(context.Background(), gaps); err != nil {
			t.Fatalf("SessionOpen: %v", err)
		}
		if !strings.Contains(capture.content, "⚠️ セッション開始 — 大きな窓") {
			t.Errorf("content = %q に大きな窓の見出しが無い", capture.content)
		}
		var currencyLine string
		for _, line := range strings.Split(capture.content, "\n") {
			if strings.HasPrefix(line, "CHEBU") {
				currencyLine = line
			}
		}
		if !strings.HasSuffix(strings.TrimRight(currencyLine, " "), "⚠️") {
			t.Errorf("CHEBUの行末に⚠️が無い: %q", currencyLine)
		}
	})
}

// TestNotifyService_SessionClose は design.md §6.7 の固定文言（個別建玉を晒さない）を確認する。
func TestNotifyService_SessionClose(t *testing.T) {
	notify, capture := newTestNotifyService(t)
	if err := notify.SessionClose(context.Background()); err != nil {
		t.Fatalf("SessionClose: %v", err)
	}
	if !strings.Contains(capture.content, "🌙 セッション終了") {
		t.Errorf("content = %q", capture.content)
	}
	if strings.Contains(capture.content, "position") || strings.Contains(capture.content, "ポジションID") {
		t.Errorf("個別の建玉内容が含まれている: %q", capture.content)
	}
}

// TestNotifyService_DailySummary は design.md §6.9 のMVP版フォーマット
// （トップ3・被害者・最大変動通貨）を確認する。
func TestNotifyService_DailySummary(t *testing.T) {
	notify, capture := newTestNotifyService(t)

	top := []DailySummaryEntry{
		{DisplayName: "太郎", ChangeAmount: decimal.NewFromInt(142300), ChangePercent: decimal.NewFromFloat(38.2)},
		{DisplayName: "花子", ChangeAmount: decimal.NewFromInt(88100), ChangePercent: decimal.NewFromFloat(20.1)},
	}
	worst := &DailySummaryEntry{DisplayName: "次郎", ChangePercent: decimal.NewFromFloat(-92.0)}

	if err := notify.DailySummary(context.Background(), top, worst, "CHEBU", decimal.NewFromFloat(14.3)); err != nil {
		t.Fatalf("DailySummary: %v", err)
	}
	for _, want := range []string{"🏁 本日の取引終了", "🥇 太郎", "🥈 花子", "💀 本日の被害者: 次郎", "📈 最大変動: CHEBU +14.3%"} {
		if !strings.Contains(capture.content, want) {
			t.Errorf("content に %q が含まれない: %s", want, capture.content)
		}
	}
}
