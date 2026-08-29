package game

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"fxgame/backend/internal/db"
	"fxgame/backend/internal/discord"
)

// tickerDivider は design.md §6.4 の市場ティッカー例に出てくる区切り線。
const tickerDivider = "━━━━━━━━━━━━━━━━━━"

// TickerService は #43 NOTIFY-1（市場ティッカー）を担当する。
// design.md §6.4「専用チャンネルに1つのメッセージを投稿し、毎分のtickでそれを
// 編集し続ける」。CLAUDE.md §4「4つの入口はすべて同じサービス層を呼ぶ」のうち、
// tick 入口だけが使う通知（Web/Discordコマンド/他のtick処理からは呼ばれない）。
type TickerService struct {
	pool *pgxpool.Pool
	// clock は現時点ではどのメソッドからも使われていない（Update は now を
	// 引数で受け取る。CLAUDE.md §5.1）。他のサービスと同じ理由で保持する。
	clock     Clock
	messages  discord.MessagesConfig
	channelID string
}

func NewTickerService(pool *pgxpool.Pool, clock Clock, messages discord.MessagesConfig, channelID string) *TickerService {
	return &TickerService{pool: pool, clock: clock, messages: messages, channelID: channelID}
}

// Update はティッカーメッセージを最新の状態に書き換える。TickService.Tick から
// 毎tick呼ばれる（design.md §4 手順5）。
//
// 冪等性（issue #43完了条件）: session.TickerMsgID が未設定（NULL）なら新規投稿して
// そのIDを game_sessions に保存し、以後はそのIDを編集し続ける。呼び出し元
// （TickService.Tick）はこの関数のエラーをログするだけでtick全体は失敗させない。
// Discord APIが失敗しても session.TickerMsgID が更新されないままなので、
// 次のtickで再び新規投稿を試み自然に回復する（CLAUDE.md §5.5と同じ考え方）。
func (s *TickerService) Update(ctx context.Context, now time.Time, session db.GameSession) error {
	q := db.New(s.pool)

	currencies, err := q.ListCurrencies(ctx)
	if err != nil {
		return fmt.Errorf("list currencies: %w", err)
	}

	content, err := buildTickerContent(ctx, q, now, session, currencies)
	if err != nil {
		return fmt.Errorf("build ticker content: %w", err)
	}
	// 各通貨の買う/売るボタンを常設する（issue #78。design.md §6.11の見直し。
	// /priceがephemeralになった代わりに、常に見えるティッカーからも売買を
	// 始められるようにする）。EditMessageのコメントのとおり、編集のたびに
	// 同じcomponentsを渡し続けないとボタンが消えうる。
	components := tickerComponents(currencies)

	if session.TickerMsgID.Valid && session.TickerMsgID.String != "" {
		// 新規投稿ではなく編集（design.md §6.4「チャンネルが荒れないため」）。
		return discord.EditMessage(ctx, s.messages, s.channelID, session.TickerMsgID.String, content, components)
	}

	messageID, err := discord.CreateMessage(ctx, s.messages, s.channelID, content, components)
	if err != nil {
		return fmt.Errorf("create ticker message: %w", err)
	}
	if err := q.UpdateGameSessionTickerMsgID(ctx, db.UpdateGameSessionTickerMsgIDParams{
		ID:          session.ID,
		TickerMsgID: pgtype.Text{String: messageID, Valid: true},
	}); err != nil {
		// 投稿(POST)自体は成功したのに保存だけ失敗した状態を放置すると、
		// session.TickerMsgIDがNULLのままなので次tickでまた新規投稿してしまい、
		// 「新規投稿が増えない」という完了条件が崩れる。投稿したメッセージを
		// 削除して「まだ投稿していない」状態に巻き戻し、次tickでの再投稿を
		// 安全にする（discord.DeleteMessageのコメント参照）。
		if delErr := discord.DeleteMessage(ctx, s.messages, s.channelID, messageID); delErr != nil {
			return fmt.Errorf("save ticker message id: %w (補償削除にも失敗、孤児メッセージが残っている可能性: %v)", err, delErr)
		}
		return fmt.Errorf("save ticker message id: %w (投稿済みメッセージは補償削除済み)", err)
	}
	return nil
}

// buildTickerContent は design.md §6.4 のフォーマットでティッカー本文を組み立てる。
//
//	📊 マーケット  21:23 (残り37分)
//	━━━━━━━━━━━━━━━━━━
//	JOG    100.44  ▲+1.2%  ▁▂▄▆█▇▅▄
//
//	WASI    98.10  ▼-0.8%  █▇▆▄▃▂▁▂
//
//	CHEBU  103.02  ▲+3.4%  ▁▁▂▃▅▇██
//
// design.md本文の見た目は列が揃っているが、Discordの通常メッセージは等幅フォント
// ではないため実際に揃えて表示するにはコードブロックが必要になる。この
// コードブロック化はdesign.mdに明記のない表示上の実装判断。行間の空行も同様に、
// スパークラインが最大の高さ（█）になると行同士が詰まって見づらいという
// フィードバック（issue #75）に対応した表示上の調整。
func buildTickerContent(ctx context.Context, q *db.Queries, now time.Time, session db.GameSession, currencies []db.Currency) (string, error) {
	remainingMinutes := int64(session.ClosedAt.Time.Sub(now) / time.Minute)
	if remainingMinutes < 0 {
		remainingMinutes = 0
	}

	var b strings.Builder
	fmt.Fprintf(&b, "📊 マーケット  %s (残り%d分)\n", now.In(jst).Format("15:04"), remainingMinutes)
	b.WriteString(tickerDivider)
	b.WriteString("\n```\n")

	// 通貨の行の間に空行を挟む。スパークラインが最大の高さ（█）になると
	// 行同士が詰まって見づらいというフィードバックへの対応（issue #75）。
	for i, c := range currencies {
		if i > 0 {
			b.WriteByte('\n')
		}
		row, err := tickerRow(ctx, q, c)
		if err != nil {
			return "", fmt.Errorf("ticker row for %s: %w", c.Symbol, err)
		}
		b.WriteString(row)
		b.WriteByte('\n')
	}
	b.WriteString("```")

	return b.String(), nil
}

// tickerRow は1通貨分の行を組み立てる。design.md §2.10「Discordはcloseのみ使う」の
// とおり、直前に書き込まれた price_ticks の close だけを見る（ライブ価格の再計算は
// しない）。変動率は直前1tick分のclose同士の比較とする（/priceのQuoteServiceと
// 同じ解釈。design.mdに基準期間の明記が無いため揃えてある）。
func tickerRow(ctx context.Context, q *db.Queries, c db.Currency) (string, error) {
	recent, err := q.ListRecentPriceTicks(ctx, db.ListRecentPriceTicksParams{
		CurrencyID: c.ID,
		Limit:      sparklineTickCount,
	})
	if err != nil {
		return "", err
	}
	if len(recent) == 0 {
		// 寄り付き前。Tickはセッション開始（寄り付き）後にしかティッカーを
		// 更新しないため通常は起きないが、安全のためクラッシュせず表示する。
		return fmt.Sprintf("%-6s  ---", c.Symbol), nil
	}

	// recent は tick_index 降順（最新が先頭）。sparkline用に時系列順へ並べ替える。
	closes := make([]decimal.Decimal, len(recent))
	for i, t := range recent {
		closes[len(recent)-1-i] = t.Close
	}

	current := recent[0].Close
	changePercent := decimal.Zero
	if len(recent) >= 2 && recent[1].Close.IsPositive() {
		changePercent = current.Sub(recent[1].Close).Div(recent[1].Close).Mul(decimal.NewFromInt(100))
	}

	arrow, sign := "―", ""
	switch {
	case changePercent.IsPositive():
		arrow, sign = "▲", "+"
	case changePercent.IsNegative():
		arrow, sign = "▼", "-"
	}

	return fmt.Sprintf("%-6s %8s  %s%s%s%%  %s",
		c.Symbol, current.StringFixed(2), arrow, sign, changePercent.Abs().StringFixed(1), sparkline(closes)), nil
}

// tickerComponents は各通貨の「買う」「売る」ボタンを1通貨1行（Action Row）で
// 組み立てる（issue #78）。custom_idは/priceの応答（commands.go）と同じ
// "order:<long|short>:<symbol>" 形式にすることで、既存のhandleOrderButtonが
// ティッカー由来のボタンもそのまま処理できるようにしてある（押した人自身の
// 注文フローに入る。/priceを実行した人・ティッカーを見ている人を区別しない）。
func tickerComponents(currencies []db.Currency) []discord.ActionRow {
	rows := make([]discord.ActionRow, 0, len(currencies))
	for _, c := range currencies {
		rows = append(rows, discord.NewActionRow(
			discord.Button{
				Type: 2, Style: discord.ButtonStyleSuccess, Label: c.Symbol + "を買う",
				CustomID: discord.EncodeCustomID(discord.CustomIDOrderButton, string(SideLong), c.Symbol),
			},
			discord.Button{
				Type: 2, Style: discord.ButtonStyleDanger, Label: c.Symbol + "を売る",
				CustomID: discord.EncodeCustomID(discord.CustomIDOrderButton, string(SideShort), c.Symbol),
			},
		))
	}
	return rows
}
