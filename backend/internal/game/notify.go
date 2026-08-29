package game

import (
	"context"
	"fmt"
	"math"
	"math/rand"

	"github.com/shopspring/decimal"

	"fxgame/backend/internal/db"
	"fxgame/backend/internal/discord"
)

// NotifyService は #通知 チャンネルへの一方向発信（#44 NOTIFY-2、design.md §6.7〜6.9）を
// 担当する。design.md §6.11「#通知はBotからの一方向発信」のとおり、TickerService
// （#ティッカー・1メッセージを編集し続ける）とは違い常に新規投稿のみで編集はしない。
//
// ここで選ぶランダムテンプレ（ロスカット演出・イベント予兆等）は価格計算に一切
// 影響しない演出専用の乱数のため、CLAUDE.md §5.7「価格の乱数はtick番号の純粋関数に
// する」の対象外（対象は価格の基準となる乱数のみ）。math/rand をそのまま使ってよい。
type NotifyService struct {
	messages  discord.MessagesConfig
	channelID string
}

func NewNotifyService(messages discord.MessagesConfig, channelID string) *NotifyService {
	return &NotifyService{messages: messages, channelID: channelID}
}

// post は#通知チャンネルへ新規投稿する（編集はしない）。
// design.md §6.11「#通知への投稿では@everyone/@hereを使わない」ため、呼び出し元は
// 絶対にcontentへそれらを含めないこと（個々のテンプレはユーザーIDを含まない
// プレーンテキストのみで組み立てている）。
//
// nilレシーバでも安全（NotifyService未設定環境向け。TickService/TradeServiceの
// notifyフィールドがnilのままでも呼び出し側がnilチェックを省略できるようにするため）。
func (n *NotifyService) post(ctx context.Context, content string) error {
	if n == nil {
		return nil
	}
	_, err := discord.CreateMessage(ctx, n.messages, n.channelID, content)
	return err
}

// LargeTrade は大口取引通知（design.md §6.7 MVP必須。「〇〇がJOGを大量購入。
// 価格が+3.2%変動」）。“大口”の判定基準（価格インパクト%）はdesign.mdに定義が
// 無かったためユーザーに確認して決定した（呼び出し元 TradeService.PlaceOrder が
// 閾値判定を行い、超えた場合だけこれを呼ぶ）。
func (n *NotifyService) LargeTrade(ctx context.Context, displayName, symbol string, side Side, impactPercent decimal.Decimal) error {
	action := "大量購入"
	if side == SideShort {
		action = "大量売却"
	}
	sign := "+"
	if impactPercent.IsNegative() {
		sign = ""
	}
	content := fmt.Sprintf("📢 %s が %s を%s。価格が %s%s%% 変動",
		displayName, symbol, action, sign, impactPercent.StringFixed(1))
	return n.post(ctx, content)
}

// roastTemplates は強制ロスカット時のいじりテンプレ（design.md §6.8「ロスカットの
// 演出」。テンプレをランダム化して飽きを防ぐ）。%s は表示名・通貨シンボル・
// レバレッジの順。
var roastTemplates = []string{
	"💥 %s の %s ポジションが消滅しました\n   レバレッジ %s 倍。勇気ある判断でした。安らかに。",
	"💀 %s が %s で退場しました\n   レバレッジ %s 倍は伝説になった。",
	"🎇 %s の %s ポジションがロスカット\n   レバレッジ %s 倍、本日の花火が上がりました。",
	"🪦 %s の %s ポジションが力尽きました\n   レバレッジ %s 倍。また明日、頑張ろう。",
	"🔥 %s の %s ポジションが強制決済されました\n   レバレッジ %s 倍。一敗地に塗れし者よ、安らかに。",
}

// neutralLiquidationTemplate は roast_enabled=false のユーザー向け（design.md §6.8
// 「いじりの強度は個別に切れるようにする」）。いじらず事実だけを伝える。
const neutralLiquidationTemplate = "%s の %s ポジションが強制決済されました（レバレッジ %s 倍）。"

// Liquidation は強制ロスカット通知（design.md §6.8）。roastEnabled が false の
// ユーザーはいじらない中立文言にする。
func (n *NotifyService) Liquidation(ctx context.Context, displayName, symbol string, leverage decimal.Decimal, roastEnabled bool) error {
	template := neutralLiquidationTemplate
	if roastEnabled {
		template = roastTemplates[rand.Intn(len(roastTemplates))] //nolint:gosec // 演出用の乱数（コメント参照）
	}
	content := fmt.Sprintf(template, displayName, symbol, leverage.StringFixed(0))
	return n.post(ctx, content)
}

// eventTeaserTemplates は予兆メッセージのテンプレ（design.md §5.3「テンプレを
// 5〜10個持たせランダム選択」）。種別を明かさず「何かが起きそう」とだけ伝える。
// %s は通貨シンボル。
var eventTeaserTemplates = []string{
	"📡 市場に何かの気配がします\n   ……%s の様子がおかしい。何かは言いません。",
	"📡 市場に何かの気配がします\n   ……%s を見ている人は、目を離さない方がいいかもしれません。",
	"📡 市場に何かの気配がします\n   ……%s 周辺がざわついています。",
	"📡 市場に何かの気配がします\n   ……%s に何か起きそうな予感。詳細は言えません。",
	"📡 市場に何かの気配がします\n   ……%s の様子を注視しておくことをおすすめします。",
}

// EventTeaser はイベント発火の1tick前に投げる予兆メッセージ（design.md §5.3。
// 発火の1つ前のtickで呼ぶのは呼び出し元 TickService の役割）。
func (n *NotifyService) EventTeaser(ctx context.Context, symbol string) error {
	template := eventTeaserTemplates[rand.Intn(len(eventTeaserTemplates))] //nolint:gosec // 演出用の乱数
	return n.post(ctx, fmt.Sprintf(template, symbol))
}

// shockUpTemplates/shockDownTemplates/volUpTemplates/liquidityDrainTemplates は
// イベント発火通知のテンプレ（design.mdに文面の指定は無いため今回の実装判断）。
// 予兆と違いイベントは既に確定した事実なので、種別・数値を隠さず伝える。
var (
	shockUpTemplates = []string{
		"🚀 %s が急騰！ 価格が %s%% 動きました",
		"🚀 %s に強い買いが入り、一気に %s%% 上昇しました",
		"🚀 %s が跳ねました。%s%% の急騰です",
	}
	shockDownTemplates = []string{
		"📉 %s が急落！ 価格が %s%% 動きました",
		"📉 %s に強い売りが入り、一気に %s%% 下落しました",
		"💥 %s が崩れました。%s%% の急落です",
	}
	volUpTemplates = []string{
		"🌪️ %s のボラティリティが上昇中！ 荒れ模様が続きます（あと%d分）",
		"🌪️ %s が揺れています。値動きが激しくなる予兆です（あと%d分）",
		"🌪️ %s の値動きが不安定に。しばらく注意が必要です（あと%d分）",
	}
	liquidityDrainTemplates = []string{
		"🏜️ %s の流動性が枯渇中！ 少ない取引量でも価格が動きやすくなっています（あと%d分）",
		"🏜️ %s の板が薄くなっています。大口の一手が効きやすい状態です（あと%d分）",
		"🏜️ %s で流動性ドレインが発生。価格インパクトが強まっています（あと%d分）",
	}
)

// EventFired はイベント発火時に投げる通知（design.md §5.4「resolved＝発火通知の
// 冪等性用」）。予兆と違い種別ごとに内容を変える。
func (n *NotifyService) EventFired(ctx context.Context, e db.Event, symbol string) error {
	var content string
	switch e.Type {
	case EventTypeShock:
		templates := shockUpTemplates
		if e.Magnitude.IsNegative() {
			templates = shockDownTemplates
		}
		template := templates[rand.Intn(len(templates))] //nolint:gosec // 演出用の乱数
		sign := ""
		if e.Magnitude.IsPositive() {
			sign = "+"
		}
		content = fmt.Sprintf(template, symbol, sign+e.Magnitude.Mul(decimal.NewFromInt(100)).StringFixed(1))
	case EventTypeVolUp:
		template := volUpTemplates[rand.Intn(len(volUpTemplates))] //nolint:gosec // 演出用の乱数
		content = fmt.Sprintf(template, symbol, e.DurationTicks)
	case EventTypeLiquidityDrain:
		template := liquidityDrainTemplates[rand.Intn(len(liquidityDrainTemplates))] //nolint:gosec // 演出用の乱数
		content = fmt.Sprintf(template, symbol, e.DurationTicks)
	default:
		// 未知の種別（将来の拡張漏れ等）。通知を諦めるより素通しの方が安全。
		content = fmt.Sprintf("⚡ %s で市場イベントが発生しました", symbol)
	}
	return n.post(ctx, content)
}

// SessionGap はセッション開始通知（design.md §2.8「セッション開始通知」）1通貨分の
// 寄り付きギャップ情報。buildSessionGaps（tick.go）が組み立てる。
type SessionGap struct {
	Symbol      string
	Open, Close decimal.Decimal
	// Sigma は off-session の経過tick数から算出した標準偏差（design.md §2.8の式）。
	// ゼロなら「大きな窓」判定を行わない（初回セッション等、算出できない場合の安全策）。
	Sigma decimal.Decimal
}

// changeRate は (Close-Open)/Open。Openが0以下なら0を返す（ゼロ除算回避の安全策）。
func (g SessionGap) changeRate() decimal.Decimal {
	if !g.Open.IsPositive() {
		return decimal.Zero
	}
	return g.Close.Sub(g.Open).Div(g.Open)
}

// isLarge は design.md §2.8「2σ超えなら大きな窓」の判定。
func (g SessionGap) isLarge() bool {
	if !g.Sigma.IsPositive() {
		return false
	}
	return g.changeRate().Abs().GreaterThan(g.Sigma.Mul(decimal.NewFromInt(2)))
}

// pips は design.md §2.8「pips = (close-open)/0.01、整数に丸め」。
func (g SessionGap) pips() int64 {
	return g.Close.Sub(g.Open).Div(decimal.NewFromFloat(0.01)).Round(0).IntPart()
}

// SessionOpen はセッション開始通知（design.md §2.8「セッション開始通知」。
// 清算処理の後に1件投稿する。呼び出しタイミングはTickService.Tickの役割）。
func (n *NotifyService) SessionOpen(ctx context.Context, gaps []SessionGap) error {
	title := "📈 セッション開始"
	anyLarge := false
	for _, g := range gaps {
		if g.isLarge() {
			anyLarge = true
			break
		}
	}
	if anyLarge {
		title = "⚠️ セッション開始 — 大きな窓"
	}

	content := title + "\n\n"
	for _, g := range gaps {
		sign := "+"
		if g.pips() < 0 {
			sign = ""
		}
		changeSign := "+"
		changePercent := g.changeRate().Mul(decimal.NewFromInt(100))
		if changePercent.IsNegative() {
			changeSign = ""
		}
		mark := ""
		if g.isLarge() {
			mark = "  ⚠️"
		}
		content += fmt.Sprintf("%-6s %8s → %8s   %s%d pips  (%s%s%%)%s\n",
			g.Symbol, g.Open.StringFixed(2), g.Close.StringFixed(2), sign, g.pips(), changeSign, changePercent.StringFixed(2), mark)
	}
	content += "\n一晩の値動きです。"

	return n.post(ctx, content)
}

// SessionClose はセッション終了通知（design.md §6.7。個別の建玉内容は晒さない
// ——翌日の狙い撃ちを防ぐため——固定文言）。
func (n *NotifyService) SessionClose(ctx context.Context) error {
	const content = "🌙 セッション終了\n\n" +
		"本日の結果は追ってお知らせします。\n" +
		"持ち越し中の建玉がある方はご注意ください。\n" +
		"明日の寄り付きまでに価格が変動します。"
	return n.post(ctx, content)
}

// DailySummaryEntry は日次まとめ（design.md §6.9）1行分。RankingService.
// RankByTodayChangeのTodayRankEntryをそのまま渡せるよう同じ形にしている。
type DailySummaryEntry struct {
	DisplayName   string
	ChangeAmount  decimal.Decimal
	ChangePercent decimal.Decimal
}

// DailySummary は日次まとめ（design.md §6.9）。ユーザーとの相談のうえMVP版として
// 実装した範囲は「🥇🥈🥉トップ3・💀本日の被害者・📈最大変動通貨」まで。
// 「最大の負け」「最速ロスカット」「最も無謀だったレバレッジ」等の追加の賞や、
// 「〜の暴落を的中させたのは1名のみ」のような的中判定はdesign.mdに算出方法の
// 定義が無く実装コストも大きいため今回は含めない（フル実装は別issueで検討）。
func (n *NotifyService) DailySummary(ctx context.Context, top []DailySummaryEntry, worst *DailySummaryEntry, biggestMoveSymbol string, biggestMovePercent decimal.Decimal) error {
	content := "🏁 本日の取引終了\n\n"

	medals := []string{"🥇", "🥈", "🥉"}
	if len(top) == 0 {
		content += "本日の参加者がいませんでした。\n"
	}
	for i, e := range top {
		if i >= len(medals) {
			break
		}
		sign := "+"
		if e.ChangeAmount.IsNegative() {
			sign = ""
		}
		content += fmt.Sprintf("%s %s      %s%s (%s%s%%)\n",
			medals[i], e.DisplayName, sign, e.ChangeAmount.StringFixed(2), sign, e.ChangePercent.StringFixed(1))
	}

	if worst != nil {
		content += fmt.Sprintf("\n💀 本日の被害者: %s（%s%%）\n", worst.DisplayName, worst.ChangePercent.StringFixed(1))
	}
	if biggestMoveSymbol != "" {
		sign := "+"
		if biggestMovePercent.IsNegative() {
			sign = ""
		}
		content += fmt.Sprintf("📈 最大変動: %s %s%s%%\n", biggestMoveSymbol, sign, biggestMovePercent.StringFixed(1))
	}

	return n.post(ctx, content)
}

// sqrtInt は経過tick数（int64、非負）の平方根をdecimalで返す小さなヘルパー
// （design.md §2.8「sigma = volatility × offSessionScale × sqrt(elapsedTicks)」）。
func sqrtInt(n int64) decimal.Decimal {
	if n <= 0 {
		return decimal.Zero
	}
	return decimal.NewFromFloat(math.Sqrt(float64(n)))
}
