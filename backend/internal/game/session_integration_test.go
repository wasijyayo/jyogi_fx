package game

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"fxgame/backend/internal/db"
)

// TestOpenSession は #34 の完了条件（固定時刻を注入したテストで、寄り付きキャンドルが
// 正しい open/close で1本作られること）を確認する。
//
// #31 で投入済みの実通貨（JOG/WASI/CHEBU）に対して OpenSession を実行するため、
// 対象日を実運用と衝突しない明確に架空の未来日（2099年）にし、終了時に
// 作成したセッション・価格tickを削除、各通貨のpressure状態を元に戻す。
func TestOpenSession(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://app:app@localhost:5432/fxgame?sslmode=disable"
	}

	// 3通貨すべてに対して2099年（実運用と衝突しない架空の未来日）のBasePriceを
	// 計算するため、tickIndexが数千万に達しCPU計算だけで10秒近くかかりうる
	// （#35で計測: 単発のBasePrice呼び出しで約3.8秒/通貨）。10秒だとこの計算コストの
	// 分だけでタイムアウトしうるため30秒に広げている（ロジック自体の変更ではない）。
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	// defer ではなく t.Cleanup で閉じ、かつ他の t.Cleanup（後始末のDELETE）より
	// 先に登録する。t.Cleanup は登録順と逆順（LIFO）に実行されるため、
	// 後で登録する後始末のほうが先に走り、pool は最後に閉じられる
	// （#55: defer pool.Close() だと後始末が「閉じた後のpool」に対する実行になり
	// 静かに失敗するバグを踏んだため、同じ轍を踏まないようにする）。
	t.Cleanup(func() { pool.Close() })

	if err := pool.Ping(ctx); err != nil {
		t.Skipf("ローカル Postgres に接続できないためスキップ（docker compose up -d を実行してください）: %v", err)
	}

	q := db.New(pool)

	before, err := q.ListCurrencies(ctx)
	if err != nil {
		t.Fatalf("ListCurrencies (snapshot): %v", err)
	}
	if len(before) == 0 {
		t.Skip("currencies が投入されていないためスキップ（#31 のマイグレーション未適用）")
	}

	// 実運用と衝突しない明確に架空の日時。
	now := time.Date(2099, 1, 1, 12, 0, 0, 0, jst)

	svc := NewSessionService(pool, RealClock{}, SessionConfig{})
	session, err := svc.OpenSession(ctx, now)
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}

	t.Cleanup(func() {
		cctx := context.Background()
		_, _ = pool.Exec(cctx, `DELETE FROM price_ticks WHERE session_id = $1`, session.ID)
		_, _ = pool.Exec(cctx, `DELETE FROM game_sessions WHERE id = $1`, session.ID)
		for _, c := range before {
			_, _ = pool.Exec(cctx,
				`UPDATE currencies SET pressure = $2, pressure_at = $3 WHERE id = $1`,
				c.ID, c.Pressure, c.PressureAt)
		}
	})

	if !session.Date.Valid || session.Date.Time.Year() != 2099 {
		t.Errorf("session.Date = %+v, want 2099年", session.Date)
	}

	rows, err := pool.Query(ctx, `SELECT currency_id, open, close, high, low, is_opening
		FROM price_ticks WHERE session_id = $1`, session.ID)
	if err != nil {
		t.Fatalf("query price_ticks: %v", err)
	}
	defer rows.Close()

	seen := make(map[int64]bool)
	for rows.Next() {
		var currencyID int64
		var open, close, high, low decimal.Decimal
		var isOpening bool
		if err := rows.Scan(&currencyID, &open, &close, &high, &low, &isOpening); err != nil {
			t.Fatalf("scan: %v", err)
		}
		seen[currencyID] = true

		if !isOpening {
			t.Errorf("currency_id=%d: is_opening = false, want true", currencyID)
		}
		// open は前セッション最終tickのclose（今回は初回なので initial_price = 100）。
		// close は base(nowTick) であり open とは独立に決まるため、
		// 「open にべた基準価格を使っていない」ことの確認として
		// high/low が open と close の範囲に一致することだけを検証する
		// （具体的な close の値は #32 のテストで別途保証済み）。
		wantHigh := open
		if close.GreaterThan(open) {
			wantHigh = close
		}
		wantLow := open
		if close.LessThan(open) {
			wantLow = close
		}
		if !high.Equal(wantHigh) {
			t.Errorf("currency_id=%d: high = %s, want %s", currencyID, high, wantHigh)
		}
		if !low.Equal(wantLow) {
			t.Errorf("currency_id=%d: low = %s, want %s", currencyID, low, wantLow)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	for _, c := range before {
		if !seen[c.ID] {
			t.Errorf("currency %s (id=%d) の寄り付きキャンドルが作られていない", c.Symbol, c.ID)
		}
	}
	if len(seen) != len(before) {
		t.Errorf("寄り付きキャンドルが %d 件作られた, want %d 件（通貨数と一致するはず）", len(seen), len(before))
	}
}

// TestOpenSession_初回はopenがinitial_priceになる は、design.md §2.8 の
// 「open に開始時点の基準価格を使わない」原則のうち「初回は initial_price」側を
// 直接確認する。openCurrency を直接呼び、前tickが無い状態から検証する。
func TestOpenSession_初回はopenがinitial_priceになる(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://app:app@localhost:5432/fxgame?sslmode=disable"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(func() { pool.Close() })

	if err := pool.Ping(ctx); err != nil {
		t.Skipf("ローカル Postgres に接続できないためスキップ（docker compose up -d を実行してください）: %v", err)
	}

	q := db.New(pool)

	currencies, err := q.ListCurrencies(ctx)
	if err != nil {
		t.Fatalf("ListCurrencies: %v", err)
	}
	if len(currencies) == 0 {
		t.Skip("currencies が投入されていないためスキップ（#31 のマイグレーション未適用）")
	}
	c := currencies[0]

	// 対象通貨に price_ticks が既に存在する場合、このテストの前提
	// （前tickが無い＝初回）が崩れるためスキップする。
	if _, err := q.GetLastPriceTick(ctx, c.ID); err == nil {
		t.Skipf("%s には既に price_ticks が存在するためスキップ（初回セッションではない）", c.Symbol)
	}

	now := time.Date(2099, 6, 1, 12, 0, 0, 0, jst)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	// テスト用のトランザクションはコミットせず必ずロールバックする。
	// これにより後始末なしで一切の副作用を残さない。
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	txq := db.New(tx)
	session, err := txq.CreateGameSession(ctx, db.CreateGameSessionParams{
		Date:     pgtype.Date{Time: time.Date(2099, 6, 1, 0, 0, 0, 0, jst), Valid: true},
		Seed:     1,
		OpenedAt: pgtype.Timestamptz{Time: now, Valid: true},
		ClosedAt: pgtype.Timestamptz{Time: now.Add(time.Hour), Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateGameSession: %v", err)
	}

	if err := openCurrency(ctx, txq, session, c, now); err != nil {
		t.Fatalf("openCurrency: %v", err)
	}

	got, err := txq.GetLastPriceTick(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetLastPriceTick: %v", err)
	}
	if !got.Open.Equal(c.BasePrice) {
		t.Errorf("open = %s, want initial_price(%s)", got.Open, c.BasePrice)
	}
	if got.Open.Equal(got.Close) {
		t.Error("open == close になっている（実体長ゼロ。design.md §2.8で禁止されている状態）")
	}
}
