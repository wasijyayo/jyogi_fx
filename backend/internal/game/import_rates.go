package game

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"fxgame/backend/internal/db"
)

// ErrCurrencyPairNotFound は --pair で指定した通貨ペアが currency_pairs に無い場合に返る。
var ErrCurrencyPairNotFound = errors.New("currency pair not found")

// rateSource は取り込み元の識別名（design.md §13「データソース」）。現状このツールが
// 取り込むのは EV Trading Labs の日足のみ。通貨ペア名や上限値（CLAUDE.md §5.3）と違い、
// これはデータの属性であってコードに埋めてよい。
const rateSource = "EV Trading Labs"

// RateImportService は過去レート（EV Trading Labs の日足 D1）の取り込みを担当する（#102 REPLAY-2）。
// 取り込みはリクエストのたびに走る処理ではなく、手動で実行するバッチ（CLAUDE.md §1）。
type RateImportService struct {
	pool  *pgxpool.Pool
	clock Clock
}

func NewRateImportService(pool *pgxpool.Pool, clock Clock) *RateImportService {
	return &RateImportService{pool: pool, clock: clock}
}

// ImportFileResult は 1 ファイル分の取り込み結果。
type ImportFileResult struct {
	// Inserted は新しく入れた日足の件数（平日のみ）。
	Inserted int
	// SkippedWeekend は土曜・日曜で飛ばした件数（保存しない。§13）。
	SkippedWeekend int
	// AlreadyExisted は取り込み済みで上書きしなかった日足の件数。
	AlreadyExisted int
}

// ImportFile は EV Trading Labs の日足 .json.gz 1 ファイル（r）を取り込む。
//
// filename は記録用（rate_datasets.filename）。fetchedOn は取得日（--fetched-on）。
// 1 ファイルを 1 トランザクションで取り込み、途中でエラーになればそのファイルは
// 何も入れない。取り込み済みのレートは上書きしない（CLAUDE.md §5.5・#102）。
func (s *RateImportService) ImportFile(ctx context.Context, pairSymbol string, fetchedOn time.Time, filename string, r io.Reader) (ImportFileResult, error) {
	var res ImportFileResult

	// --pair は必須（ファイル名にも中身にも通貨ペアが無い）。先に解決して
	// 存在しないペアでファイルを読む前に落とす。
	q := db.New(s.pool)
	pair, err := q.GetCurrencyPairBySymbol(ctx, pairSymbol)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return res, ErrCurrencyPairNotFound
		}
		return res, fmt.Errorf("get currency pair: %w", err)
	}

	raw, err := io.ReadAll(r)
	if err != nil {
		return res, fmt.Errorf("read file: %w", err)
	}

	// ファイル内容（.json.gz の生バイト）の SHA-256。同じ内容のファイルを
	// 同じデータセットとして扱うための鍵（#102）。
	sum := sha256.Sum256(raw)
	sha := hex.EncodeToString(sum[:])

	data, err := gunzip(raw)
	if err != nil {
		return res, err
	}

	bars, err := parseRates(data)
	if err != nil {
		return res, err
	}
	weekdays, skipped := splitWeekdays(bars)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return res, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := db.New(tx)

	ds, err := qtx.CreateRateDataset(ctx, db.CreateRateDatasetParams{
		PairID:     pair.ID,
		Source:     rateSource,
		Filename:   filename,
		FetchedOn:  pgtype.Date{Time: fetchedOn, Valid: true},
		Sha256:     sha,
		ImportedAt: pgtype.Timestamptz{Time: s.clock.Now(), Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// 既に同じ（pair_id, sha256）のデータセットがある。その ID を使う。
		ds, err = qtx.GetRateDatasetByPairAndSha256(ctx, db.GetRateDatasetByPairAndSha256Params{
			PairID: pair.ID,
			Sha256: sha,
		})
	}
	if err != nil {
		return res, fmt.Errorf("create dataset: %w", err)
	}

	for _, b := range weekdays {
		_, err := qtx.InsertDailyRate(ctx, db.InsertDailyRateParams{
			DatasetID: ds.ID,
			PairID:    pair.ID,
			Date:      pgtype.Date{Time: b.date, Valid: true},
			Open:      b.open,
			High:      b.high,
			Low:       b.low,
			Close:     b.close,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			res.AlreadyExisted++
			continue
		}
		if err != nil {
			return res, fmt.Errorf("insert daily rate (date %s): %w", b.date.Format("2006-01-02"), err)
		}
		res.Inserted++
	}

	if err := tx.Commit(ctx); err != nil {
		return res, fmt.Errorf("commit: %w", err)
	}

	res.SkippedWeekend = skipped
	return res, nil
}

// gunzip は .json.gz の内容を展開する。
func gunzip(data []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("gzip: %w", err)
	}
	defer func() { _ = zr.Close() }()
	out, err := io.ReadAll(zr)
	if err != nil {
		return nil, fmt.Errorf("gzip decompress: %w", err)
	}
	return out, nil
}

// rateJSONRecord は EV Trading Labs の日足 JSON の 1 行分。
// o/h/l/c は bid（ask 側の ao/ah/al/ac と出来高 v は使わない。#102 確認結果）。
// 数値は json.Number で受け、float を経由せず decimal に変換する（CLAUDE.md §5.2）。
type rateJSONRecord struct {
	TS json.Number `json:"ts"`
	O  json.Number `json:"o"`
	H  json.Number `json:"h"`
	L  json.Number `json:"l"`
	C  json.Number `json:"c"`
}

// dailyBar は検証済みの 1 日分の日足。
type dailyBar struct {
	date    time.Time // ts の UTC 日付
	weekend bool
	open    decimal.Decimal
	high    decimal.Decimal
	low     decimal.Decimal
	close   decimal.Decimal
}

// parseRates は EV Trading Labs の日足 JSON（レコードの配列）を復号・検証する。
// 違反があれば「どの行か」を含むエラーを返す。週末の行は除外せず weekend フラグ付きで返す
// （除外は splitWeekdays で行う。検証はすべての行に適用する）。
func parseRates(data []byte) ([]dailyBar, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()

	var records []rateJSONRecord
	if err := dec.Decode(&records); err != nil {
		return nil, fmt.Errorf("decode json: %w", err)
	}

	bars := make([]dailyBar, 0, len(records))
	seen := make(map[string]int, len(records)) // "2006-01-02" → 行番号
	for i, rec := range records {
		bar, err := validateRateRecord(rec)
		if err != nil {
			return nil, fmt.Errorf("row %d: %w", i, err)
		}
		key := bar.date.Format("2006-01-02")
		if prev, ok := seen[key]; ok {
			return nil, fmt.Errorf("row %d: duplicate date %s (first at row %d)", i, key, prev)
		}
		seen[key] = i
		bars = append(bars, bar)
	}
	return bars, nil
}

// validateRateRecord は 1 行分を検証する。
func validateRateRecord(rec rateJSONRecord) (dailyBar, error) {
	ts, err := rec.TS.Int64()
	if err != nil {
		return dailyBar{}, fmt.Errorf("ts is not an integer: %w", err)
	}
	t := time.Unix(ts, 0).UTC()
	if t.Hour() != 0 || t.Minute() != 0 || t.Second() != 0 {
		return dailyBar{}, fmt.Errorf("ts %d is not 00:00 UTC", ts)
	}

	open, err := decimalFromJSONNumber(rec.O)
	if err != nil {
		return dailyBar{}, fmt.Errorf("o: %w", err)
	}
	high, err := decimalFromJSONNumber(rec.H)
	if err != nil {
		return dailyBar{}, fmt.Errorf("h: %w", err)
	}
	low, err := decimalFromJSONNumber(rec.L)
	if err != nil {
		return dailyBar{}, fmt.Errorf("l: %w", err)
	}
	closeVal, err := decimalFromJSONNumber(rec.C)
	if err != nil {
		return dailyBar{}, fmt.Errorf("c: %w", err)
	}

	for _, v := range []struct {
		name string
		d    decimal.Decimal
	}{
		{"o", open}, {"h", high}, {"l", low}, {"c", closeVal},
	} {
		if !v.d.IsPositive() {
			return dailyBar{}, fmt.Errorf("%s must be positive, got %s", v.name, v.d)
		}
	}

	// OHLC の整合性: 高値 ≥ 始値・終値・安値、安値 ≤ 始値・終値。
	if high.LessThan(open) {
		return dailyBar{}, fmt.Errorf("high %s < open %s", high, open)
	}
	if high.LessThan(closeVal) {
		return dailyBar{}, fmt.Errorf("high %s < close %s", high, closeVal)
	}
	if high.LessThan(low) {
		return dailyBar{}, fmt.Errorf("high %s < low %s", high, low)
	}
	if low.GreaterThan(open) {
		return dailyBar{}, fmt.Errorf("low %s > open %s", low, open)
	}
	if low.GreaterThan(closeVal) {
		return dailyBar{}, fmt.Errorf("low %s > close %s", low, closeVal)
	}

	return dailyBar{
		date:    t,
		weekend: isWeekend(t),
		open:    open,
		high:    high,
		low:     low,
		close:   closeVal,
	}, nil
}

// decimalFromJSONNumber は json.Number を decimal.Decimal に変換する（float を経由しない）。
func decimalFromJSONNumber(n json.Number) (decimal.Decimal, error) {
	return decimal.NewFromString(n.String())
}

// isWeekend は UTC の曜日が土曜・日曜かを返す（#102: 週末の行は保存しない）。
func isWeekend(t time.Time) bool {
	wd := t.Weekday()
	return wd == time.Saturday || wd == time.Sunday
}

// splitWeekdays は平日の行と、飛ばした週末の件数を返す。
func splitWeekdays(bars []dailyBar) (weekdays []dailyBar, skipped int) {
	weekdays = make([]dailyBar, 0, len(bars))
	for _, b := range bars {
		if b.weekend {
			skipped++
		} else {
			weekdays = append(weekdays, b)
		}
	}
	return weekdays, skipped
}
