package game

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"fxgame/backend/internal/db"
)

// EventImportService は過去チャートモード（design.md §13）の出来事（#116 REPLAY-6）を
// historical_events に取り込む手動バッチ。リクエストのたびに外部へ取りに行かない
// （CLAUDE.md §1）ため、あらかじめ用意した JSON ファイルを読む。
//
// 取り込みは候補を入れるだけで、既定では採用済み（adopted = true）にしない。
// ゲームが使うのは採用済みの行だけ。候補の選定と採用の確認は #119 で行う。
type EventImportService struct {
	pool *pgxpool.Pool
}

func NewEventImportService(pool *pgxpool.Pool) *EventImportService {
	return &EventImportService{pool: pool}
}

// EventImportResult は 1 ファイル分の取り込み結果。
type EventImportResult struct {
	// Inserted は新しく入れた出来事の件数。
	Inserted int
	// AlreadyExisted は同じ（出典・年・見出し）の行が既にあり、上書きしなかった件数。
	AlreadyExisted int
}

// eventJSONRecord は import-events の入力ファイル 1 件分。
// date は "YYYY-MM-DD"（日まで確定）、"YYYY-MM"（月まで）、"YYYY"（年まで）のいずれか。
// 日付の精度は date の形式から決める。
type eventJSONRecord struct {
	Date        string `json:"date"`
	Headline    string `json:"headline"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Source      string `json:"source"`
	SourceURL   string `json:"sourceUrl"`
	Adopted     *bool  `json:"adopted"`
}

// historicalEventInput は検証済みの 1 件分。
type historicalEventInput struct {
	year          int
	eventDate     pgtype.Date
	datePrecision string
	headline      string
	description   string
	category      string
	source        string
	sourceURL     pgtype.Text
	adopted       bool
}

// ImportFile は出来事の JSON ファイル（r）を 1 トランザクションで取り込む。
// 途中でエラーになればそのファイルは何も入れない。再実行しても行を増やさず、
// 既存の行（採用状態・説明など）は上書きしない。
func (s *EventImportService) ImportFile(ctx context.Context, r io.Reader) (EventImportResult, error) {
	var res EventImportResult

	inputs, err := parseHistoricalEvents(r)
	if err != nil {
		return res, err
	}
	if len(inputs) == 0 {
		return res, nil
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return res, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	for _, in := range inputs {
		_, err := q.CreateHistoricalEvent(ctx, db.CreateHistoricalEventParams{
			Year:          int32(in.year),
			EventDate:     in.eventDate,
			DatePrecision: in.datePrecision,
			Headline:      in.headline,
			Description:   in.description,
			Category:      in.category,
			Source:        in.source,
			SourceUrl:     in.sourceURL,
			Adopted:       in.adopted,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			// ON CONFLICT DO NOTHING。既にある行はそのまま（上書きしない）。
			res.AlreadyExisted++
			continue
		}
		if err != nil {
			return res, fmt.Errorf("insert historical event %q: %w", in.headline, err)
		}
		res.Inserted++
	}

	if err := tx.Commit(ctx); err != nil {
		return res, fmt.Errorf("commit: %w", err)
	}
	return res, nil
}

// parseHistoricalEvents は入力ファイル（JSON の配列）を復号・検証する。
// 違反があれば「どの行か」を含むエラーを返す。
func parseHistoricalEvents(r io.Reader) ([]historicalEventInput, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()

	var records []eventJSONRecord
	if err := dec.Decode(&records); err != nil {
		return nil, fmt.Errorf("decode json: %w", err)
	}

	inputs := make([]historicalEventInput, 0, len(records))
	for i, rec := range records {
		in, err := validateEventRecord(rec)
		if err != nil {
			return nil, fmt.Errorf("row %d: %w", i, err)
		}
		inputs = append(inputs, in)
	}
	return inputs, nil
}

// validateEventRecord は 1 件分を検証し、DB に入れる形にする。
func validateEventRecord(rec eventJSONRecord) (historicalEventInput, error) {
	year, eventDate, precision, err := parseEventDate(rec.Date)
	if err != nil {
		return historicalEventInput{}, err
	}

	headline := strings.TrimSpace(rec.Headline)
	if headline == "" {
		return historicalEventInput{}, errors.New("headline is required")
	}
	description := strings.TrimSpace(rec.Description)
	if description == "" {
		return historicalEventInput{}, errors.New("description is required")
	}
	category := strings.TrimSpace(rec.Category)
	if category == "" {
		return historicalEventInput{}, errors.New("category is required")
	}
	source := strings.TrimSpace(rec.Source)
	if source == "" {
		return historicalEventInput{}, errors.New("source is required")
	}

	var sourceURL pgtype.Text
	if u := strings.TrimSpace(rec.SourceURL); u != "" {
		sourceURL = pgtype.Text{String: u, Valid: true}
	}

	adopted := false
	if rec.Adopted != nil {
		adopted = *rec.Adopted
	}

	return historicalEventInput{
		year:          year,
		eventDate:     eventDate,
		datePrecision: precision,
		headline:      headline,
		description:   description,
		category:      category,
		source:        source,
		sourceURL:     sourceURL,
		adopted:       adopted,
	}, nil
}

// parseEventDate は "YYYY-MM-DD" / "YYYY-MM" / "YYYY" を読み、年・日付（日まで確定の
// 場合だけ有効）・精度を返す。月・年までしか分からない出来事は event_date を持たない。
func parseEventDate(value string) (year int, eventDate pgtype.Date, precision string, err error) {
	v := strings.TrimSpace(value)
	switch len(v) {
	case len("2006-01-02"):
		t, parseErr := time.Parse("2006-01-02", v)
		if parseErr != nil {
			return 0, pgtype.Date{}, "", fmt.Errorf("date: %w", parseErr)
		}
		return t.Year(), pgtype.Date{Time: t, Valid: true}, "day", nil
	case len("2006-01"):
		t, parseErr := time.Parse("2006-01", v)
		if parseErr != nil {
			return 0, pgtype.Date{}, "", fmt.Errorf("date: %w", parseErr)
		}
		return t.Year(), pgtype.Date{}, "month", nil
	case len("2006"):
		t, parseErr := time.Parse("2006", v)
		if parseErr != nil {
			return 0, pgtype.Date{}, "", fmt.Errorf("date: %w", parseErr)
		}
		return t.Year(), pgtype.Date{}, "year", nil
	default:
		return 0, pgtype.Date{}, "", fmt.Errorf("date must be YYYY-MM-DD, YYYY-MM, or YYYY: %q", value)
	}
}
