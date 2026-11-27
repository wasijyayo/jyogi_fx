package game

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"fxgame/backend/internal/db"
)

// EventImportService は過去チャートモード（design.md §13）の出来事（#116 REPLAY-6）を
// historical_events に取り込むバッチ。リクエストのたびに外部へ取りに行かない
// （CLAUDE.md §1）ため、リポジトリに置いた年ごとの JSON ファイルを読む。
//
// 取り込みは「ファイルを正とする同期」である。ファイルにある出来事は採用済み
// （adopted = true）にし、渡したファイル全体に無い key の行は採用を外す（削除はしない）。
// 年ごとの JSON を PR で確認してマージすることで採用が確定し、本番への反映は
// deploy.yml が自動で行う（docs/dev-guide.md §2.3）。
type EventImportService struct {
	pool *pgxpool.Pool
}

func NewEventImportService(pool *pgxpool.Pool) *EventImportService {
	return &EventImportService{pool: pool}
}

// EventImportFile は import-events に渡す入力ファイル 1 件。
type EventImportFile struct {
	// Name はファイル名（例: "2016.json"）。ファイル名の年と出来事の年の一致を検証する。
	Name string
	// Data は UTF-8 の JSON（オブジェクトの配列）。
	Data []byte
}

// EventImportResult は 1 回の取り込み結果。
type EventImportResult struct {
	// Upserted は入れた（または上書きした）出来事の件数。
	Upserted int
	// Unadopted は今回のファイルに無いため採用を外した行の件数。
	Unadopted int64
	// Skipped はファイルが 0 個、または出来事が 0 件で、何も変えなかったことを表す。
	Skipped bool
}

// 取り込みの key に使える形式（英小文字・数字・ハイフン。連続・先頭末尾のハイフンは不可）。
var eventKeyPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ファイル名は年だけの <年>.json とする（例: 2016.json）。
var eventFileNamePattern = regexp.MustCompile(`^([0-9]{4})\.json$`)

// 見出し・説明に含めてはいけない月日の表記（「6月23日」「6/23」など）。
// 日付を出すと先読みに使えるため、新聞・カットインの文言からは月日を除く
// （design.md §13「先読み対策」）。
var eventMonthDayPattern = regexp.MustCompile(`[0-9]{1,2}月[0-9]{1,2}日|[0-9]{1,2}/[0-9]{1,2}`)

// eventJSONRecord は import-events の入力ファイル 1 件分。
// date は "YYYY-MM-DD"（日まで確定）、"YYYY-MM"（月まで）、"YYYY"（年まで）のいずれか。
// 日付の精度は date の形式から決める。ファイルにある出来事はすべて採用済みとして扱う。
type eventJSONRecord struct {
	Key         string `json:"key"`
	Date        string `json:"date"`
	Headline    string `json:"headline"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Source      string `json:"source"`
	SourceURL   string `json:"sourceUrl"`
}

// historicalEventInput は検証済みの 1 件分。
type historicalEventInput struct {
	key           string
	year          int
	eventDate     pgtype.Date
	datePrecision string
	headline      string
	description   string
	category      string
	source        string
	sourceURL     pgtype.Text
}

// Import は渡したファイル全体を 1 トランザクションで同期する。
// ファイルにある出来事は key で upsert して全項目を上書きし、採用済みにする。
// 渡したファイル全体に無い key の行は採用を外す（削除はしない。replay_game_events が
// 参照しているため）。
//
// ファイルが 0 個、または出来事が 0 件のときは、全件の採用を外す事故を防ぐため
// 何もせず正常終了する。1 件でも検証に失敗したら、DB を変更せずにエラーを返す
// （検証はトランザクションを開始する前に行う）。
func (s *EventImportService) Import(ctx context.Context, files []EventImportFile) (EventImportResult, error) {
	var res EventImportResult
	if len(files) == 0 {
		res.Skipped = true
		return res, nil
	}

	inputs, err := parseEventFiles(files)
	if err != nil {
		return res, err
	}
	if len(inputs) == 0 {
		res.Skipped = true
		return res, nil
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return res, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	keys := make([]string, 0, len(inputs))
	for _, in := range inputs {
		if _, err := q.UpsertHistoricalEvent(ctx, db.UpsertHistoricalEventParams{
			Key:           in.key,
			Year:          int32(in.year),
			EventDate:     in.eventDate,
			DatePrecision: in.datePrecision,
			Headline:      in.headline,
			Description:   in.description,
			Category:      in.category,
			Source:        in.source,
			SourceUrl:     in.sourceURL,
		}); err != nil {
			return res, fmt.Errorf("upsert historical event %q: %w", in.key, err)
		}
		keys = append(keys, in.key)
		res.Upserted++
	}

	res.Unadopted, err = q.UnadoptHistoricalEventsNotInKeys(ctx, keys)
	if err != nil {
		return res, fmt.Errorf("unadopt historical events: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return res, fmt.Errorf("commit: %w", err)
	}
	return res, nil
}

// parseEventFiles は全ファイルを復号・検証する。1 件でも違反があればエラーを返す。
// key は全ファイルを通じて一意でなければならない（同じ key を別のファイルに置かない）。
func parseEventFiles(files []EventImportFile) ([]historicalEventInput, error) {
	inputs := make([]historicalEventInput, 0)
	seenKeys := make(map[string]string, len(files))

	for _, file := range files {
		fileYear, err := eventFileYear(file.Name)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", file.Name, err)
		}

		records, err := decodeEventRecords(file.Data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", file.Name, err)
		}

		for i, rec := range records {
			in, err := validateEventRecord(rec)
			if err != nil {
				return nil, fmt.Errorf("%s row %d: %w", file.Name, i, err)
			}
			if in.year != fileYear {
				return nil, fmt.Errorf("%s row %d: date year %d does not match file name year %d",
					file.Name, i, in.year, fileYear)
			}
			if prev, ok := seenKeys[in.key]; ok {
				return nil, fmt.Errorf("duplicate key %q (%s and %s)", in.key, prev, file.Name)
			}
			seenKeys[in.key] = file.Name
			inputs = append(inputs, in)
		}
	}
	return inputs, nil
}

// eventFileYear はファイル名（例: 2016.json）から年を取り出す。
func eventFileYear(name string) (int, error) {
	base := filepath.Base(name)
	m := eventFileNamePattern.FindStringSubmatch(base)
	if m == nil {
		return 0, fmt.Errorf("file name must be YYYY.json: %q", base)
	}
	year, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, fmt.Errorf("file name year: %w", err)
	}
	return year, nil
}

// decodeEventRecords は JSON（オブジェクトの配列）を復号する。知らないフィールドはエラーにする。
func decodeEventRecords(data []byte) ([]eventJSONRecord, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	var records []eventJSONRecord
	if err := dec.Decode(&records); err != nil {
		return nil, fmt.Errorf("decode json: %w", err)
	}
	return records, nil
}

// validateEventRecord は 1 件分を検証し、DB に入れる形にする。
func validateEventRecord(rec eventJSONRecord) (historicalEventInput, error) {
	key := strings.TrimSpace(rec.Key)
	if key == "" {
		return historicalEventInput{}, errors.New("key is required")
	}
	if !eventKeyPattern.MatchString(key) {
		return historicalEventInput{}, fmt.Errorf(
			"key must be lowercase letters, digits, and hyphens: %q", key)
	}

	year, eventDate, precision, err := parseEventDate(rec.Date)
	if err != nil {
		return historicalEventInput{}, err
	}

	headline := strings.TrimSpace(rec.Headline)
	if headline == "" {
		return historicalEventInput{}, errors.New("headline is required")
	}
	if eventMonthDayPattern.MatchString(headline) {
		return historicalEventInput{}, fmt.Errorf("headline must not contain a month/day: %q", headline)
	}
	description := strings.TrimSpace(rec.Description)
	if description == "" {
		return historicalEventInput{}, errors.New("description is required")
	}
	if eventMonthDayPattern.MatchString(description) {
		return historicalEventInput{}, fmt.Errorf("description must not contain a month/day: %q", description)
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

	return historicalEventInput{
		key:           key,
		year:          year,
		eventDate:     eventDate,
		datePrecision: precision,
		headline:      headline,
		description:   description,
		category:      category,
		source:        source,
		sourceURL:     sourceURL,
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
