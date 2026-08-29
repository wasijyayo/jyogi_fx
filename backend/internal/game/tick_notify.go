package game

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"fxgame/backend/internal/db"
)

// この ファイル は TickService から呼ばれる #44 NOTIFY-2（自動通知）の配線をまとめる。
// 通知そのものの文面組み立ては notify.go の NotifyService が担当し、ここでは
// 「いつ・どのデータで呼ぶか」というオーケストレーションだけを行う（CLAUDE.md §3の
// 層分けと同じ考え方を internal/game 内でも踏襲）。
//
// 全てのメソッドは s.notify が nil でも安全（NotifyService.post 自体がnilセーフ。
// notify.goのコメント参照）。失敗しても tick 全体を失敗させずログのみ残し、
// 次のtickでの自然な回復に任せる（updateTickerと同じ方針。issue #44完了条件
// 「tickが二重に走っても同じ通知が2回投稿されないこと」）。

// notifyLiquidations は強制ロスカット通知（design.md §6.8）。
// LiquidateOpenPositionsが返す「このtickで新たに清算されたポジション」のみを
// 対象にするため、tickが二重に走っても既に closed_at が立ったポジションは
// 二度と返らず自然に冪等になる（liquidation.goのコメント参照）。
func (s *TickService) notifyLiquidations(ctx context.Context, q *db.Queries, currencies []db.Currency, liquidated []LiquidatedPosition) {
	if s.notify == nil || len(liquidated) == 0 {
		return
	}
	symbolByCurrencyID := make(map[int64]string, len(currencies))
	for _, c := range currencies {
		symbolByCurrencyID[c.ID] = c.Symbol
	}

	for _, lp := range liquidated {
		// roast_enabledが必要なため、GetUserByID（display_name等のみ）ではなく
		// フルロー取得のGetUserを使う（users.sqlのコメント参照）。
		user, err := q.GetUser(ctx, lp.Position.UserID)
		if err != nil {
			log.Printf("liquidation notify: get user %s: %v", lp.Position.UserID, err)
			continue
		}
		symbol := symbolByCurrencyID[lp.Position.CurrencyID]
		if err := s.notify.Liquidation(ctx, user.DisplayName, symbol, lp.Position.Leverage, user.RoastEnabled); err != nil {
			log.Printf("liquidation notify: %v", err)
		}
	}
}

// notifyEvents はイベントの予兆投稿・発火通知（design.md §5.3・§5.4）。
// 通貨ごとに全イベントを見て、このtickが「発火の1つ前」「発火その瞬間」に
// 該当するものだけ通知する。teased/resolvedフラグは通知の投稿に成功した場合のみ
// 更新する（Discord投稿に失敗したら次tickで再試行される。CLAUDE.md §5.5）。
func (s *TickService) notifyEvents(ctx context.Context, q *db.Queries, currencies []db.Currency, now time.Time) {
	if s.notify == nil {
		return
	}
	for _, c := range currencies {
		tickIndex := elapsedTicks(c.EpochAt.Time, now)
		events, err := q.ListEventsByCurrency(ctx, c.ID)
		if err != nil {
			log.Printf("event notify: list events for %s: %v", c.Symbol, err)
			continue
		}
		for _, e := range events {
			if !e.Teased && e.FireTick-1 == tickIndex {
				if err := s.notify.EventTeaser(ctx, c.Symbol); err != nil {
					log.Printf("event teaser notify: %v", err)
				} else if err := q.MarkEventTeased(ctx, e.ID); err != nil {
					log.Printf("mark event teased: %v", err)
				}
			}
			if !e.Resolved && e.FireTick == tickIndex {
				if err := s.notify.EventFired(ctx, e, c.Symbol); err != nil {
					log.Printf("event fired notify: %v", err)
				} else if err := q.MarkEventResolved(ctx, e.ID); err != nil {
					log.Printf("mark event resolved: %v", err)
				}
			}
		}
	}
}

// notifySessionOpen はセッション開始通知（design.md §2.8「セッション開始通知」。
// 清算処理の後に1件投稿する）。寄り付き（OpenSession）が直前に書いたばかりの
// 寄り付きキャンドルを読み直してギャップを組み立てる（openCurrency/OpenSessionの
// シグネチャを変えずに済むよう、書き込み後の状態を読み直す設計にしてある）。
func (s *TickService) notifySessionOpen(ctx context.Context, q *db.Queries, currencies []db.Currency) {
	if s.notify == nil {
		return
	}
	gaps, err := buildSessionGaps(ctx, q, currencies)
	if err != nil {
		log.Printf("session open notify: build gaps: %v", err)
		return
	}
	if err := s.notify.SessionOpen(ctx, gaps); err != nil {
		log.Printf("session open notify: %v", err)
	}
}

// buildSessionGaps は各通貨の直近2本の price_ticks（寄り付きキャンドルとその
// 直前のtick）から寄り付きギャップ情報を組み立てる（design.md §2.8）。
func buildSessionGaps(ctx context.Context, q *db.Queries, currencies []db.Currency) ([]SessionGap, error) {
	gaps := make([]SessionGap, 0, len(currencies))
	for _, c := range currencies {
		recent, err := q.ListRecentPriceTicks(ctx, db.ListRecentPriceTicksParams{CurrencyID: c.ID, Limit: 2})
		if err != nil {
			return nil, fmt.Errorf("list recent price ticks for %s: %w", c.Symbol, err)
		}
		if len(recent) == 0 {
			// 起こらないはずだが（寄り付き直後は必ず1本以上ある）、安全のためスキップする。
			continue
		}
		opening := recent[0] // tick_index降順のためrecent[0]が直近＝寄り付きtick

		// off-session の経過tick数 = 寄り付きtickとその直前tickのtick_index差。
		// 初回セッション（直前tickが無い）は算出できないため sigma=0（大きな窓判定なし）。
		var sigma decimal.Decimal
		if len(recent) >= 2 {
			elapsed := opening.TickIndex - recent[1].TickIndex
			sigma = c.Volatility.Mul(c.OffSessionScale).Mul(sqrtInt(elapsed))
		}

		gaps = append(gaps, SessionGap{
			Symbol: c.Symbol,
			Open:   opening.Open,
			Close:  opening.Close,
			Sigma:  sigma,
		})
	}
	return gaps, nil
}

// lastTickGraceWindow は「このtickがセッション最後のtickか」の判定に使う猶予。
// 通常はセッション終了のちょうど1分前（12:59台）が最後のtickになる
// （design.md §4: セッションは60分・毎分tick）。
const lastTickGraceWindow = time.Minute

// notifyClosingIfLastTick はセッション終了通知＋日次まとめ（design.md §6.7・§6.9）を、
// このtickがセッションの最後のtickであり、かつまだ投稿していない場合にだけ投稿する。
// game_sessions.closing_notified で冪等性を確保する（#44完了条件。events.teased/
// resolvedと同じパターン。migrations/000007参照）。
func (s *TickService) notifyClosingIfLastTick(ctx context.Context, q *db.Queries, session db.GameSession, now time.Time) {
	if session.ClosingNotified {
		return
	}
	remaining := session.ClosedAt.Time.Sub(now)
	if remaining > lastTickGraceWindow {
		return
	}

	if s.notify != nil {
		if err := s.notify.SessionClose(ctx); err != nil {
			log.Printf("session close notify: %v", err)
			return
		}
		// 既知の限界: SessionCloseの投稿自体は成功したのにこの直後の
		// notifyDailySummaryだけが失敗した場合、closing_notifiedがまだtrueに
		// なっていないため、tickの重複実行があると🌙セッション終了だけ2通目が
		// 投稿されうる（Tick自体はこの関数のエラーを見ないためHTTP側はエラーに
		// ならず、Cloud Schedulerが独自の理由で同じ最終tickを再送した場合のみ）。
		// 1日1回・この2工程だけの狭い窓かつ実害が軽微なため、ticker.go/#43で
		// 対応した「投稿はしたがID保存が失敗」のケースほど深刻ではないと判断し、
		// 今回はここまでの対応とする（フラグを分ける等の追加対応は必要になれば
		// 別途検討）。
		if err := s.notifyDailySummary(ctx, q, session, now); err != nil {
			log.Printf("daily summary notify: %v", err)
			return
		}
	}

	if err := q.MarkGameSessionClosingNotified(ctx, session.ID); err != nil {
		log.Printf("mark game session closing notified: %v", err)
	}
}

// notifyDailySummary は日次まとめ（design.md §6.9）。ユーザーとの相談のうえMVP版
// として「トップ3・本日の被害者・最大変動通貨」のみ実装した（notify.goの
// DailySummaryコメント参照。「賞」の追加やヒット判定はフル実装を別issueで検討）。
func (s *TickService) notifyDailySummary(ctx context.Context, q *db.Queries, session db.GameSession, now time.Time) error {
	if s.ranking == nil {
		return nil
	}
	entries, err := s.ranking.RankByTodayChange(ctx, now)
	if err != nil {
		if errors.Is(err, ErrNoSessionToday) {
			// 起こらないはずだが（このtickの時点でセッションは開いている）念のため。
			return nil
		}
		return fmt.Errorf("rank by today change: %w", err)
	}

	top := make([]DailySummaryEntry, 0, 3)
	for i := 0; i < len(entries) && i < 3; i++ {
		top = append(top, DailySummaryEntry{
			DisplayName:   entries[i].DisplayName,
			ChangeAmount:  entries[i].ChangeAmount,
			ChangePercent: entries[i].ChangePercent,
		})
	}
	var worst *DailySummaryEntry
	if len(entries) > 0 {
		last := entries[len(entries)-1]
		worst = &DailySummaryEntry{
			DisplayName:   last.DisplayName,
			ChangeAmount:  last.ChangeAmount,
			ChangePercent: last.ChangePercent,
		}
	}

	biggestMoveSymbol, biggestMovePercent, err := biggestCurrencyMove(ctx, q, session)
	if err != nil {
		log.Printf("daily summary: biggest currency move: %v", err)
		// 通貨の値動き集計に失敗しても、資産ランキングだけは投稿する。
	}

	return s.notify.DailySummary(ctx, top, worst, biggestMoveSymbol, biggestMovePercent)
}

// biggestCurrencyMove は本日いちばん動いた通貨（design.md §6.9「📈最大変動」）を
// 「寄り付きのcloseから現時点で最後に書かれたtickのcloseまでの変化率」で求める。
func biggestCurrencyMove(ctx context.Context, q *db.Queries, session db.GameSession) (symbol string, percent decimal.Decimal, err error) {
	currencies, err := q.ListCurrencies(ctx)
	if err != nil {
		return "", decimal.Zero, fmt.Errorf("list currencies: %w", err)
	}

	var bestAbs decimal.Decimal
	for _, c := range currencies {
		opening, err := q.GetOpeningPriceTick(ctx, db.GetOpeningPriceTickParams{SessionID: session.ID, CurrencyID: c.ID})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			return "", decimal.Zero, fmt.Errorf("get opening price tick for %s: %w", c.Symbol, err)
		}
		last, err := q.GetLastPriceTick(ctx, c.ID)
		if err != nil {
			return "", decimal.Zero, fmt.Errorf("get last price tick for %s: %w", c.Symbol, err)
		}
		if !opening.Close.IsPositive() {
			continue
		}
		change := last.Close.Sub(opening.Close).Div(opening.Close).Mul(decimal.NewFromInt(100))
		if change.Abs().GreaterThan(bestAbs) {
			bestAbs = change.Abs()
			symbol = c.Symbol
			percent = change
		}
	}
	return symbol, percent, nil
}
