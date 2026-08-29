package game

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"fxgame/backend/internal/db"
)

// lastNewOrderMinuteJST は「終了1分前」の開始分（12:59）。この分に入ったら
// 新規注文を受け付けない（確定 #14/#48。design.md §7.10）。
// 決済（クローズ）はこの時間帯も許可する仕様のため、この定数・関連関数を
// 決済可否の判定に使い回さないこと。
const lastNewOrderMinuteJST = sessionStartMinuteJST + sessionDurationMinutes - 1

// SessionConfig は取引セッションの判定に必要な設定。
type SessionConfig struct {
	// AlwaysOpen は開発環境で「取引時間が常に開いている」モードを有効にする
	// （CLAUDE.md §5.1: これが無いと1日1時間しかテストできない）。
	// 本番では必ず false にすること。
	AlwaysOpen bool
}

// IsSessionOpen は now（JST換算）が取引時間（JST 12:00〜13:00毎日、確定 #13）内かを返す。
// 時刻は必ず引数で受ける（CLAUDE.md §5.1）。internal/game 配下で time.Now() を直接呼ばない。
func (cfg SessionConfig) IsSessionOpen(now time.Time) bool {
	if cfg.AlwaysOpen {
		return true
	}
	minuteOfDay := minuteOfDayJST(now)
	return minuteOfDay >= sessionStartMinuteJST && minuteOfDay < sessionStartMinuteJST+sessionDurationMinutes
}

// IsNewOrderAllowed はセッション中かつ終了1分前（12:59台）を過ぎていないかを返す
// （確定 #14/#48。design.md §7.10「駆け込み取引の制限」）。
//
// **新規注文にのみ使うこと。** 既存ポジションの決済（クローズ）は同時間帯も
// 許可する仕様のため、決済可否の判定にこの関数を使ってはいけない。
func (cfg SessionConfig) IsNewOrderAllowed(now time.Time) bool {
	if cfg.AlwaysOpen {
		return true
	}
	if !cfg.IsSessionOpen(now) {
		return false
	}
	return minuteOfDayJST(now) < lastNewOrderMinuteJST
}

// sessionDateJST は now（JST換算）が属する日付を game_sessions.date の型
// （pgtype.Date）で返す。GetGameSessionByDate で「本日のセッション行」を引く
// 呼び出し元（TickService.Tick・ClaimService.Claim）が同じ日付計算を
// 重複させないための共通ヘルパー。
func sessionDateJST(now time.Time) pgtype.Date {
	jstNow := now.In(jst)
	return pgtype.Date{
		Time:  time.Date(jstNow.Year(), jstNow.Month(), jstNow.Day(), 0, 0, 0, 0, jst),
		Valid: true,
	}
}

// elapsedTicks は epochAt から now までの経過tick数（1tick=1分）を返す。
// now が epochAt より前の場合は 0 を返す（クロックのずれ等に対する安全側の挙動）。
func elapsedTicks(epochAt, now time.Time) int64 {
	d := now.Sub(epochAt)
	if d < 0 {
		return 0
	}
	return int64(d / time.Minute)
}

// SessionService は取引セッションの判定・寄り付き処理を担当する（#34 PRICE-3）。
type SessionService struct {
	pool *pgxpool.Pool
	// clock は現時点ではどのメソッドからも使われていない
	// （IsSessionOpen/IsNewOrderAllowed/OpenSession はすべて now を引数で受ける）。
	// サービス層の構造体は必ず clock Clock を持つ（CLAUDE.md §5.1）ため、
	// 将来 now を引数に取らない便利メソッドを追加する時のために保持する。
	clock Clock
	cfg   SessionConfig
}

func NewSessionService(pool *pgxpool.Pool, clock Clock, cfg SessionConfig) *SessionService {
	return &SessionService{pool: pool, clock: clock, cfg: cfg}
}

func (s *SessionService) IsSessionOpen(now time.Time) bool     { return s.cfg.IsSessionOpen(now) }
func (s *SessionService) IsNewOrderAllowed(now time.Time) bool { return s.cfg.IsNewOrderAllowed(now) }

// OpenSession は寄り付き処理（design.md §2.7「寄り付き処理の順序」）を実行する。
//
// ここで実装するのは手順のうち 1〜4, 8:
//
//  1. 経過tick数を算出
//  2. base(nowTick) を算出
//  3. pressure を 0 にリセット
//  4. 寄り付きキャンドルを1本保存
//  8. セッション開始（game_sessions に当日行を作成）
//
// 手順 5〜7（持ち越し建玉の再評価・清算判定）は、この関数の**呼び出し元**
// （TickService.Tick）が OpenSession 成功後に LiquidationService を呼ぶ形で
// 実装した（#38 TRADE-3）。OpenSession 自身がロスカットの決済処理
// （#37 TradeService.ClosePosition）まで抱えると、TradeService → SessionService
// （PlaceOrderのセッション判定用）→ LiquidationService → TradeService という
// 循環依存になるため、あえてここでは呼ばない設計にしてある。
// 手順8（/claim用中央値算出）は claim 機能（#39）がまだ実装されていないため、
// 引き続き TODO として残す。
//
// 複数通貨をまたぐ処理は必ず全通貨をループする（CLAUDE.md §5.3）。
// Cloud Scheduler の重複実行に備え、全体を冪等にしてある
// （CreateGameSession は date の UNIQUE 制約で同じ行を返し、UpsertPriceTick は
// tick_index の UNIQUE 制約で安全に再実行できる。CLAUDE.md §5.5）。
func (s *SessionService) OpenSession(ctx context.Context, now time.Time) (db.GameSession, error) {
	jstNow := now.In(jst)
	sessionDate := time.Date(jstNow.Year(), jstNow.Month(), jstNow.Day(), 0, 0, 0, 0, jst)
	openedAt := time.Date(jstNow.Year(), jstNow.Month(), jstNow.Day(), 12, 0, 0, 0, jst)
	closedAt := time.Date(jstNow.Year(), jstNow.Month(), jstNow.Day(), 13, 0, 0, 0, jst)

	seed, err := randomSessionSeed()
	if err != nil {
		return db.GameSession{}, fmt.Errorf("generate session seed: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return db.GameSession{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	q := db.New(tx)

	session, err := q.CreateGameSession(ctx, db.CreateGameSessionParams{
		Date:     pgtype.Date{Time: sessionDate, Valid: true},
		Seed:     seed,
		OpenedAt: pgtype.Timestamptz{Time: openedAt, Valid: true},
		ClosedAt: pgtype.Timestamptz{Time: closedAt, Valid: true},
	})
	if err != nil {
		return db.GameSession{}, fmt.Errorf("create game session: %w", err)
	}

	currencies, err := q.ListCurrencies(ctx)
	if err != nil {
		return db.GameSession{}, fmt.Errorf("list currencies: %w", err)
	}

	for _, c := range currencies {
		if err := openCurrency(ctx, q, session, c, now); err != nil {
			return db.GameSession{}, fmt.Errorf("open currency %s: %w", c.Symbol, err)
		}
	}

	// 持ち越し建玉の再評価・清算判定は呼び出し元（TickService.Tick）が担当する
	// （#38。このファイル冒頭のOpenSessionコメント参照）。
	// TODO(#39): /claim用中央値算出。

	if err := tx.Commit(ctx); err != nil {
		return db.GameSession{}, fmt.Errorf("commit tx: %w", err)
	}
	return session, nil
}

// openCurrency は1通貨分の寄り付きキャンドル生成とpressureリセットを行う
// （design.md §2.8「寄り付きキャンドルの生成」）。
func openCurrency(ctx context.Context, q *db.Queries, session db.GameSession, c db.Currency, now time.Time) error {
	nowTick := elapsedTicks(c.EpochAt.Time, now)

	// open は前セッション最終tickのclose。初回（過去のtickが無い）は initial_price
	// （= currencies.base_price）にフォールバックする（確定 #19）。
	// **開始時点の基準価格を open にしてはいけない**（open=closeで実体長ゼロになり
	// 窓が見えなくなる。design.md §2.8）。
	openPrice := c.BasePrice
	last, err := q.GetLastPriceTick(ctx, c.ID)
	switch {
	case err == nil:
		openPrice = last.Close
	case errors.Is(err, pgx.ErrNoRows):
		// 初回セッション。openPrice は initial_price のまま。
	default:
		return fmt.Errorf("get last price tick: %w", err)
	}

	closePrice := BasePrice(c, nowTick)
	high := decimal.Max(openPrice, closePrice)
	low := decimal.Min(openPrice, closePrice)

	if err := q.UpsertPriceTick(ctx, db.UpsertPriceTickParams{
		CurrencyID: c.ID,
		SessionID:  session.ID,
		TickIndex:  nowTick,
		TickedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		BasePrice:  closePrice,
		Pressure:   decimal.Zero, // セッション外は注文が入らないため0（design.md §2.8）
		NetVolume:  decimal.Zero,
		Open:       openPrice,
		High:       high,
		Low:        low,
		Close:      closePrice,
		IsOpening:  true,
	}); err != nil {
		return fmt.Errorf("upsert opening price tick: %w", err)
	}

	// 前セッション終了時の圧力は破棄する（design.md §2.8: e^(-λ×1380) で
	// 既にほぼ完全消滅しているため実質的な影響もない）。
	if err := q.UpdateCurrencyPressure(ctx, db.UpdateCurrencyPressureParams{
		ID:         c.ID,
		Pressure:   decimal.Zero,
		PressureAt: pgtype.Timestamptz{Time: now, Valid: true},
	}); err != nil {
		return fmt.Errorf("reset pressure: %w", err)
	}

	return nil
}

// randomSessionSeed は game_sessions.seed 用の乱数を生成する。
// イベント抽選（#40）で使う想定（design.md §5.1）。BIGINT（符号付き64bit）に
// 収まるよう最上位ビットを落として非負の値にする。
func randomSessionSeed() (int64, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return 0, err
	}
	return int64(binary.LittleEndian.Uint64(buf[:]) &^ (1 << 63)), nil
}
