package game

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"fxgame/backend/internal/db"
)

// TickService は毎分の tick 処理（#35 TICK-1、design.md §4）を担当する。
// CLAUDE.md §4「4つの入口はすべて同じサービス層を呼ぶ」の tick 版の入口。
type TickService struct {
	pool *pgxpool.Pool
	// clock は現時点ではどのメソッドからも使われていない（Tick は now を引数で
	// 受け取る。CLAUDE.md §5.1）。SessionService と同じ理由で、将来 now を
	// 引数に取らない便利メソッドを追加する時のために保持する。
	clock       Clock
	session     *SessionService
	liquidation *LiquidationService
}

func NewTickService(pool *pgxpool.Pool, clock Clock, session *SessionService, liquidation *LiquidationService) *TickService {
	return &TickService{pool: pool, clock: clock, session: session, liquidation: liquidation}
}

// Tick は毎分呼ばれる処理の入口（design.md §4「毎分tickが担当する処理」）。
//
//	1. イベントの予兆投稿 / 発火判定   → TODO(#40 EVENT-1)
//	2. 指値・逆指値の約定判定          → TODO(#36/#37 TRADE-1/2)
//	3. ロスカット判定                  → このIssue(#38)で実装
//	4. price_ticks 書き込み            → #35で実装
//	5. 市場ティッカーメッセージの編集  → TODO(#43 NOTIFY-1)
//
// 該当する機能（イベント・指値注文）がまだ実装されていないため、1・2・5 は
// 手順の位置だけを TODO コメントとして残し、3・4 を実装する。
//
// セッション外は何もしない（design.md §9.10/§9.12: 常時起動しない構成のため、
// セッション外のtickは保存せず base(n) の再計算で賄う）。ロスカット判定
// （手順3）もこの早期returnより後にあるため、セッション外は判定されない
// （design.md §7.1 B案「セッション外では判定しない」・確定#38の完了条件）。
//
// 冪等（CLAUDE.md §5.5）: Cloud Scheduler の重複実行・遅延を前提に、同じ now で
// 何度呼ばれても price_ticks が壊れないようにしてある（UpsertPriceTick の
// ON CONFLICT。design.md §8）。ロスカットも同じ now で再実行されて構わない
// （LiquidationService.LiquidateOpenPositions のコメント参照）。
// tick の欠損（呼ばれない分）も許容する（§3.6）。
func (s *TickService) Tick(ctx context.Context, now time.Time) error {
	if !s.session.IsSessionOpen(now) {
		return nil
	}

	jstNow := now.In(jst)
	sessionDate := pgtype.Date{
		Time:  time.Date(jstNow.Year(), jstNow.Month(), jstNow.Day(), 0, 0, 0, 0, jst),
		Valid: true,
	}

	q := db.New(s.pool)
	gameSession, err := q.GetGameSessionByDate(ctx, sessionDate)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// 本日最初のtick。寄り付き処理（#34 SessionService.OpenSession）に委譲する。
		// OpenSession がこのtickのtick_indexに寄り付きキャンドルを書くため、
		// この呼び出しでは以降の通常tick書き込みは行わない（同じtick_indexに
		// 寄り付きと通常の2つの意味で書き込むと窓の意図がぼやけるため）。
		// 次のtick（1分後）から通常の書き込みに入る。
		if _, err := s.session.OpenSession(ctx, now); err != nil {
			return fmt.Errorf("open session: %w", err)
		}
		// OpenSession が全通貨のpressureを0にリセットした直後にロスカット判定を
		// 行うことで、「持ち越し建玉を翌日の寄り付きでまとめて判定する」
		// （design.md §2.7 寄り付き処理順序6〜7・§7.1 B案）を実現する。
		// 判定に使う価格は寄り付き価格（base(n)。pressureは0）になる。
		if _, err := s.liquidation.LiquidateOpenPositions(ctx, now); err != nil {
			return fmt.Errorf("liquidate open positions at opening: %w", err)
		}
		return nil
	case err != nil:
		return fmt.Errorf("get game session: %w", err)
	}

	// ロスカット判定（手順3）。price_ticks書き込み（手順4）より前に行う
	// （design.md §4の手順順序）。LiquidationServiceは独自にトランザクションを
	// 管理する（#37のClosePositionを1ポジションずつ再利用するため。
	// liquidation.go のコメント参照）ため、この後のtickの書き込みトランザクション
	// より前・かつその外側で呼ぶ。
	if _, err := s.liquidation.LiquidateOpenPositions(ctx, now); err != nil {
		return fmt.Errorf("liquidate open positions: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)
	txq := db.New(tx)

	// 全通貨をループして1トランザクションで書き込む（design.md §11.3。
	// 通貨が100種になってもtickの実行回数は1回のまま）。
	currencies, err := txq.ListCurrencies(ctx)
	if err != nil {
		return fmt.Errorf("list currencies: %w", err)
	}

	// TODO(#40 EVENT-1): イベントの予兆投稿 / 発火判定をここに追加する。
	// TODO(#36/#37 TRADE-1/2): 指値・逆指値の約定判定をここに追加する。

	for _, c := range currencies {
		if err := writePriceTick(ctx, txq, gameSession, c, now); err != nil {
			return fmt.Errorf("write price tick for %s: %w", c.Symbol, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}

	// TODO(#43 NOTIFY-1): 市場ティッカーメッセージの編集をここに追加する。

	return nil
}

// writePriceTick は1通貨分の毎分キャンドルを書き込む
// （design.md §2.8「OHLCの定義」・§3.2）。
//
//	open  = 前tickの close（直前の寄り付きtick or 通常tick）
//	close = そのtick時点の計算価格。取引の有無に関わらず必ず入れる
//	        （取引ベースでOHLCを作ると取引が無い分に穴が空くため。§3.2）
//	high  = max(open, close)
//	low   = min(open, close)
//
// 1tick内に価格の推移は存在しないため1分足にヒゲは生えない（§2.8・§9.15）。
func writePriceTick(ctx context.Context, q *db.Queries, session db.GameSession, c db.Currency, now time.Time) error {
	tickIndex := elapsedTicks(c.EpochAt.Time, now)

	// このtickに来る時点で必ず直前のtick（寄り付き、または前回の通常tick）が
	// 存在する。Tick はセッション未オープン時に OpenSession へ委譲しており、
	// OpenSession が各通貨に寄り付きキャンドルを1本書いてから呼ばれるため。
	last, err := q.GetLastPriceTick(ctx, c.ID)
	if err != nil {
		return fmt.Errorf("get last price tick: %w", err)
	}
	open := last.Close

	// price = base(n) × (1 + pressure) の計算（pressure.go の CurrentPrice と同じ式）。
	// base_price 列にも base(n) 自体を保存する必要があるため、ここでは
	// CurrentPrice を呼ばず BasePrice の結果を使い回す
	// （BasePrice は経過tick数に比例した計算量があるため二重に呼ばない。pricing.go）。
	base := BasePrice(c, tickIndex)
	pressure := Pressure(c, now)
	price := base.Mul(decimal.NewFromInt(1).Add(pressure))

	high := decimal.Max(open, price)
	low := decimal.Min(open, price)

	return q.UpsertPriceTick(ctx, db.UpsertPriceTickParams{
		CurrencyID: c.ID,
		SessionID:  session.ID,
		TickIndex:  tickIndex,
		TickedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		BasePrice:  base,
		Pressure:   pressure,
		// NetVolume: 成行注文がまだ実装されていない（#36 TRADE-1）ため常に0。
		// 実装後はそのtickの買い-売り差額（符号付き）をここに渡す。
		NetVolume: decimal.Zero,
		Open:      open,
		High:      high,
		Low:       low,
		Close:     price,
		IsOpening: false,
	})
}
