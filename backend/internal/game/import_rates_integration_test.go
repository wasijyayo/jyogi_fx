package game

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"fxgame/backend/internal/db"
)

// gzipJSON はテスト用に JSON バイト列を gzip する（実ファイルはリポジトリに置かない）。
func gzipJSON(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(data); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

// insertTestPair は取り込みテスト専用に、実データの4ペアを使わない使い捨ての
// 通貨ペア行を作る。後始末は子テーブル（daily_rates → rate_datasets）から順に消す。
func insertTestPair(t *testing.T, ctx context.Context, pool *pgxpool.Pool, symbol, base, quote, maxLeverage string) db.CurrencyPair {
	t.Helper()

	// 前回のテスト失敗等で同名の行が残っていた場合に備え、先に掃除する
	// （currency_pairs.symbol は UNIQUE 制約があるため、残っていると INSERT が失敗する）。
	_, _ = pool.Exec(ctx,
		`DELETE FROM daily_rates WHERE pair_id IN (SELECT id FROM currency_pairs WHERE symbol = $1)`, symbol)
	_, _ = pool.Exec(ctx,
		`DELETE FROM rate_datasets WHERE pair_id IN (SELECT id FROM currency_pairs WHERE symbol = $1)`, symbol)
	_, _ = pool.Exec(ctx, `DELETE FROM currency_pairs WHERE symbol = $1`, symbol)

	var p db.CurrencyPair
	row := pool.QueryRow(ctx, `
		INSERT INTO currency_pairs (symbol, base_currency, quote_currency, max_leverage)
		VALUES ($1, $2, $3, $4)
		RETURNING id, symbol, base_currency, quote_currency, max_leverage, created_at`,
		symbol, base, quote, maxLeverage)
	if err := row.Scan(&p.ID, &p.Symbol, &p.BaseCurrency, &p.QuoteCurrency, &p.MaxLeverage, &p.CreatedAt); err != nil {
		t.Fatalf("insert test pair %s: %v", symbol, err)
	}

	t.Cleanup(func() {
		cctx := context.Background()
		if _, err := pool.Exec(cctx, `DELETE FROM daily_rates WHERE pair_id = $1`, p.ID); err != nil {
			t.Errorf("cleanup: delete daily_rates: %v", err)
		}
		if _, err := pool.Exec(cctx, `DELETE FROM rate_datasets WHERE pair_id = $1`, p.ID); err != nil {
			t.Errorf("cleanup: delete rate_datasets: %v", err)
		}
		if _, err := pool.Exec(cctx, `DELETE FROM currency_pairs WHERE id = $1`, p.ID); err != nil {
			t.Errorf("cleanup: delete currency_pairs: %v", err)
		}
	})

	return p
}

func fetchedOn() time.Time {
	return time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
}

// TestRateImport_件数と週末除外 は、1 ファイル（月〜日 7 日分）を取り込むと
// 平日 5 本だけが入り、週末 2 本が飛ばされることを確認する（#102 完了条件）。
func TestRateImport_件数と週末除外(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)
	pair := insertTestPair(t, ctx, pool, "TST/USD", "TST", "USD", "25")

	svc := NewRateImportService(pool, RealClock{})

	mon := dayUnix(t, 2015, 1, 5) // 月
	data := array(
		rateRec(mon+0*86400, "1.00", "1.10", "0.90", "1.05"), // 月
		rateRec(mon+1*86400, "1.05", "1.15", "0.95", "1.10"), // 火
		rateRec(mon+2*86400, "1.10", "1.20", "1.00", "1.15"), // 水
		rateRec(mon+3*86400, "1.15", "1.25", "1.05", "1.20"), // 木
		rateRec(mon+4*86400, "1.20", "1.30", "1.10", "1.25"), // 金
		rateRec(mon+5*86400, "1.25", "1.35", "1.15", "1.30"), // 土
		rateRec(mon+6*86400, "1.30", "1.40", "1.20", "1.35"), // 日
	)

	res, err := svc.ImportFile(ctx, pair.Symbol, fetchedOn(), "2015.json.gz", bytes.NewReader(gzipJSON(t, data)))
	if err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	if res.Inserted != 5 {
		t.Errorf("Inserted = %d, want 5", res.Inserted)
	}
	if res.SkippedWeekend != 2 {
		t.Errorf("SkippedWeekend = %d, want 2", res.SkippedWeekend)
	}
	if res.AlreadyExisted != 0 {
		t.Errorf("AlreadyExisted = %d, want 0", res.AlreadyExisted)
	}

	var rates, datasets int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM daily_rates WHERE pair_id = $1`, pair.ID).Scan(&rates); err != nil {
		t.Fatalf("count daily_rates: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM rate_datasets WHERE pair_id = $1`, pair.ID).Scan(&datasets); err != nil {
		t.Fatalf("count rate_datasets: %v", err)
	}
	if rates != 5 {
		t.Errorf("daily_rates = %d, want 5", rates)
	}
	if datasets != 1 {
		t.Errorf("rate_datasets = %d, want 1", datasets)
	}
}

// TestRateImport_再実行で増えない は、同じファイルを 2 回取り込んでも
// 件数もデータセット数も増えないことを確認する（#102 完了条件）。
func TestRateImport_再実行で増えない(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)
	pair := insertTestPair(t, ctx, pool, "TST/USD", "TST", "USD", "25")

	svc := NewRateImportService(pool, RealClock{})

	data := array(rateRec(dayUnix(t, 2015, 1, 5), "1.00", "1.10", "0.90", "1.05")) // 月のみ1本
	gz := gzipJSON(t, data)

	res1, err := svc.ImportFile(ctx, pair.Symbol, fetchedOn(), "2015.json.gz", bytes.NewReader(gz))
	if err != nil {
		t.Fatalf("1回目 ImportFile: %v", err)
	}
	if res1.Inserted != 1 || res1.AlreadyExisted != 0 {
		t.Fatalf("1回目 = inserted %d / already %d, want 1 / 0", res1.Inserted, res1.AlreadyExisted)
	}

	res2, err := svc.ImportFile(ctx, pair.Symbol, fetchedOn(), "2015.json.gz", bytes.NewReader(gz))
	if err != nil {
		t.Fatalf("2回目 ImportFile: %v", err)
	}
	if res2.Inserted != 0 || res2.AlreadyExisted != 1 {
		t.Fatalf("2回目 = inserted %d / already %d, want 0 / 1", res2.Inserted, res2.AlreadyExisted)
	}

	var rates, datasets int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM daily_rates WHERE pair_id = $1`, pair.ID).Scan(&rates); err != nil {
		t.Fatalf("count daily_rates: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM rate_datasets WHERE pair_id = $1`, pair.ID).Scan(&datasets); err != nil {
		t.Fatalf("count rate_datasets: %v", err)
	}
	if rates != 1 {
		t.Errorf("daily_rates = %d, want 1（再実行で増えている）", rates)
	}
	if datasets != 1 {
		t.Errorf("rate_datasets = %d, want 1（再実行で増えている）", datasets)
	}
}

// TestRateImport_無いペアはエラー は、currency_pairs に無いペアを指定すると
// ErrCurrencyPairNotFound になることを確認する。
func TestRateImport_無いペアはエラー(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)

	svc := NewRateImportService(pool, RealClock{})

	data := array(rateRec(dayUnix(t, 2015, 1, 5), "1.00", "1.10", "0.90", "1.05"))
	_, err := svc.ImportFile(ctx, "ZZZ/ZZZ", fetchedOn(), "2015.json.gz", bytes.NewReader(gzipJSON(t, data)))
	if !errors.Is(err, ErrCurrencyPairNotFound) {
		t.Fatalf("ImportFile(無いペア) = %v, want ErrCurrencyPairNotFound", err)
	}
}

// TestRateImport_途中のエラーで何も入らない は、途中の行で DB エラー
// （NUMERIC(20,8) の桁溢れ）が起きた場合、そのファイルは何も入らないことを確認する。
// 1 本目が正常に入った後に 2 本目で失敗するケースで、トランザクションの
// ロールバックを検証する（#102 完了条件「途中のエラーで何も入れない」）。
func TestRateImport_途中のエラーで何も入らない(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)
	pair := insertTestPair(t, ctx, pool, "TST/USD", "TST", "USD", "25")

	svc := NewRateImportService(pool, RealClock{})

	huge := "100000000000000000000" // 21桁 → NUMERIC(20,8) で桁溢れ（Go の検証は通る）
	data := array(
		rateRec(dayUnix(t, 2015, 1, 5), "1.00", "1.10", "0.90", "1.05"),
		rateRec(dayUnix(t, 2015, 1, 6), huge, huge, huge, huge),
	)
	if _, err := svc.ImportFile(ctx, pair.Symbol, fetchedOn(), "2015.json.gz", bytes.NewReader(gzipJSON(t, data))); err == nil {
		t.Fatal("ImportFile = nil, want error")
	}

	var rates, datasets int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM daily_rates WHERE pair_id = $1`, pair.ID).Scan(&rates); err != nil {
		t.Fatalf("count daily_rates: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM rate_datasets WHERE pair_id = $1`, pair.ID).Scan(&datasets); err != nil {
		t.Fatalf("count rate_datasets: %v", err)
	}
	if rates != 0 {
		t.Errorf("daily_rates = %d, want 0（途中のエラーで残っている）", rates)
	}
	if datasets != 0 {
		t.Errorf("rate_datasets = %d, want 0（途中のエラーで残っている）", datasets)
	}
}
