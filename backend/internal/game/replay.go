package game

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"fxgame/backend/internal/db"
)

// 過去チャートモード（design.md §13）のゲーム進行（#103 REPLAY-3）を担当するサービス層。
// 通常モードの TradeService とは別。プレイヤーの売買で価格は動かず、取り込んだ過去レート
// （#102 REPLAY-2）を 3 秒ごとに 1 営業日ずつ公開していく 1 人用ゲーム。

// ゲームパラメータ。design.md §13 で確定した値（#103 マネージャー決定）。
// 手数料率はコードに置かず currency_pairs.fee_rate（#102 → #103 マイグレーションで追加）に持つ。
const (
	// replayInitialFunds は 1 ゲームの仮想資金（§13「資金・スコア・ランキング」）。
	replayInitialFunds = 1000
	// replayMinMargin は最低注文証拠金（§13「注文・損益・強制決済」の追加決定）。
	replayMinMargin = 1
	// replayIntervalDays は 1 ゲームの区間（60 営業日。§13「対象と期間」）。
	replayIntervalDays = 60
	// replayPreDays は開始前から見せる営業日数（§13「対象と期間」）。
	replayPreDays = 20
	// replayStandardDayDuration は標準の速さ（×1）で 1 営業日を公開する間隔（§13「1 ゲームの流れ」）。
	replayStandardDayDuration = 3 * time.Second
	// replayLeverageUnlockBaseCost は上限解放 1 段目の費用（§13。以降は段ごとに 2 倍）。
	replayLeverageUnlockBaseCost = 250
	// replayLeverageUnlockStep は上限解放 1 段あたりの +レバレッジ（§13）。
	replayLeverageUnlockStep = 10
	// replayMinSpeed / replayMaxSpeed は速さの選択肢（×1・×2・×3。追加決定）。
	replayMinSpeed = 1
	replayMaxSpeed = 3
)

// ゲーム・建玉の状態。replay_games.status / replay_positions.status の値と一致させる。
const (
	replayStatusPlaying       = "playing"
	replayStatusFinished      = "finished"
	replayStatusEarlyFinished = "early_finished"

	replayPositionOpen       = "open"
	replayPositionClosed     = "closed"
	replayPositionLiquidated = "liquidated"
)

var (
	// ErrReplayGameNotFound はゲームが存在しない、またはトークンが合わない場合に返る
	// （他人のゲームIDの存在有無を漏らさないため区別しない）。
	ErrReplayGameNotFound = errors.New("replay game not found")
	// ErrReplayNoInterval は開始できる区間が無い（取り込み済みデータ不足）場合に返る。
	ErrReplayNoInterval = errors.New("no playable interval")
	// ErrReplayInvalidGuestName はゲスト名が 1〜16 文字でない場合に返る。
	ErrReplayInvalidGuestName = errors.New("guest name must be 1-16 characters")
	// ErrReplayGameFinished は終了済みゲームに対して操作した場合に返る。
	ErrReplayGameFinished = errors.New("replay game already finished")
	// ErrReplayInvalidMargin は証拠金が 1 以上の整数でない場合に返る。
	ErrReplayInvalidMargin = errors.New("margin must be a positive integer")
	// ErrReplayInvalidLeverage はレバレッジが 1 以上の整数でない場合に返る。
	ErrReplayInvalidLeverage = errors.New("leverage must be a positive integer")
	// ErrReplayInvalidAmount は追加額が 1 以上の整数でない場合に返る。
	ErrReplayInvalidAmount = errors.New("amount must be a positive integer")
	// ErrReplayInvalidSpeed は速さが 1/2/3 のどれでもない場合に返る。
	ErrReplayInvalidSpeed = errors.New("speed must be 1, 2, or 3")
	// ErrReplayPairNotFound は通貨ペアが存在しない場合に返る。
	ErrReplayPairNotFound = errors.New("replay pair not found")
	// ErrReplayPositionNotFound は建玉が存在しない、または指定ゲームのものでない場合に返る。
	ErrReplayPositionNotFound = errors.New("replay position not found")
	// ErrReplayPositionClosed はすでに決済済みの建玉を操作した場合に返る。
	ErrReplayPositionClosed = errors.New("replay position already closed")
	// ErrReplayOppositePosition は同じ通貨ペアに反対方向の建玉がある場合に返る。
	ErrReplayOppositePosition = errors.New("close existing positions of this pair first")
)

// ReplayService は過去チャートモードのゲーム進行（#103 REPLAY-3）を担当する。
// CLAUDE.md §5.1: 時刻は clock から受け取り、time.Now() を直接呼ばない。
type ReplayService struct {
	pool  *pgxpool.Pool
	clock Clock
	// mu は rng を同時アクセスから守る（rand.Rand はスレッドセーフでない）。
	mu  sync.Mutex
	// rng は区間選びの乱数源。テストでは固定シードで差し替えて区間を固定できる。
	rng *rand.Rand
}

// NewReplayService は ReplayService を作る。rng は crypto/rand からシードする
// （区間を推測されにくくする。テストでは svc.rng を差し替える）。
func NewReplayService(pool *pgxpool.Pool, clock Clock) *ReplayService {
	seed, err := randomSessionSeed()
	if err != nil {
		// crypto/rand が壊れることは通常ない。万が一でも起動は続けられるよう 0 にフォールバック。
		seed = 0
	}
	return &ReplayService{
		pool:  pool,
		clock: clock,
		rng:   rand.New(rand.NewSource(seed)),
	}
}

// ---- 純粋関数（テスト容易性のためロジックを分離）----

// replayPublishedDays は開始からの経過時間と速さの変更履歴から、公開済みの区間営業日数
// （0..60）を返す（design.md §13 scale-to-zero）。速さは ×1 から始まり、変更履歴の
// 時刻ごとに切り替わる。経過時間 × 速さ を区間ごとに足し合わせて 3 秒で割る。
func replayPublishedDays(startedAt time.Time, changes []db.ReplaySpeedChange, now time.Time) int {
	var effective time.Duration
	speed := 1
	from := startedAt
	for _, ch := range changes {
		t := ch.ChangedAt.Time
		if t.After(from) {
			effective += t.Sub(from) * time.Duration(speed)
			from = t
		}
		speed = int(ch.Speed)
	}
	if now.After(from) {
		effective += now.Sub(from) * time.Duration(speed)
	}
	days := int(effective / replayStandardDayDuration)
	if days < 0 {
		days = 0
	}
	if days > replayIntervalDays {
		days = replayIntervalDays
	}
	return days
}

// replayCurrentSpeed は now 時点で有効な速さを返す（変更履歴の最後の値。無ければ ×1）。
func replayCurrentSpeed(changes []db.ReplaySpeedChange) int {
	speed := 1
	for _, ch := range changes {
		speed = int(ch.Speed)
	}
	return speed
}

// replayNotional は建玉の大きさ = 当初証拠金 × レバレッジ（design.md §13）。
// 損益・手数料の基準となり、証拠金の追加では変えない。
func replayNotional(initialMargin decimal.Decimal, leverage int32) decimal.Decimal {
	return initialMargin.Mul(decimal.NewFromInt(int64(leverage))).Round(8)
}

// replayLiquidationLine は損失が証拠金に達する価格（強制決済ライン）を返す。
// long: entry × (1 − margin/notional)、short: entry × (1 + margin/notional)。
// margin は追加分を含む現在の証拠金。notional は取引規模（当初証拠金 × レバレッジ）で固定。
func replayLiquidationLine(side Side, entryPrice, margin, notional decimal.Decimal) decimal.Decimal {
	ratio := margin.Div(notional)
	if side == SideShort {
		return entryPrice.Mul(decimal.NewFromInt(1).Add(ratio)).Round(8)
	}
	return entryPrice.Mul(decimal.NewFromInt(1).Sub(ratio)).Round(8)
}

// replayPnL は指定価格での損益 = 取引規模 × 価格の変化率（design.md §13）。
// long: notional × (price/entry − 1)、short: notional × (entry/price − 1)。
func replayPnL(side Side, entryPrice, price, notional decimal.Decimal) decimal.Decimal {
	rate := price.Sub(entryPrice).Div(entryPrice)
	if side == SideShort {
		rate = entryPrice.Sub(price).Div(entryPrice)
	}
	return notional.Mul(rate).Round(8)
}

// replayFee は片道手数料 = 取引規模 × 手数料率（design.md §13）。新規・決済とも建てたときの
// 取引規模（当初証拠金 × レバレッジ）に掛ける。
func replayFee(notional, feeRate decimal.Decimal) decimal.Decimal {
	return notional.Mul(feeRate).Round(8)
}

// replayCloseReturn は決済で戻る額 = max(0, 証拠金 + 損益 − 決済手数料)（design.md §13）。
// 資金はマイナスにならない。
func replayCloseReturn(margin, pnl, closeFee decimal.Decimal) decimal.Decimal {
	ret := margin.Add(pnl).Sub(closeFee)
	if ret.IsNegative() {
		return decimal.Zero
	}
	return ret.Round(8)
}

// replayUnlockCost は上限解放の費用 = 250 × 2^tier（段ごとに 2 倍。design.md §13）。
func replayUnlockCost(tier int) decimal.Decimal {
	return decimal.NewFromInt(replayLeverageUnlockBaseCost).
		Mul(decimal.NewFromInt(2).Pow(decimal.NewFromInt(int64(tier))))
}

// minOrderCost は「最低注文額と新規手数料を払える最小の注文」の費用（早期終了判定に使う）。
// 最低証拠金 1 × 最低レバレッジ 1 の取引規模 1 に対する手数料率が最小のペアで計算する。
func minOrderCost(pairs []db.CurrencyPair) decimal.Decimal {
	if len(pairs) == 0 {
		return decimal.NewFromInt(replayMinMargin)
	}
	minFee := pairs[0].FeeRate
	for _, p := range pairs[1:] {
		if p.FeeRate.LessThan(minFee) {
			minFee = p.FeeRate
		}
	}
	// 証拠金 1 + 取引規模 1 × 手数料率。
	return decimal.NewFromInt(replayMinMargin).Add(decimal.NewFromInt(replayMinMargin).Mul(minFee))
}

// businessDaysBefore は from（その日の 0 時）から平日（月〜金）を n 日さかのぼった日付を返す。
// 「今日から平日を数えて 60 日以上前」の境界（design.md §13「対象と期間」）に使う。
func businessDaysBefore(from time.Time, n int) time.Time {
	d := from
	for counted := 0; counted < n; {
		d = d.AddDate(0, 0, -1)
		wd := d.Weekday()
		if wd != time.Saturday && wd != time.Sunday {
			counted++
		}
	}
	return d
}

// dateOnlyUTC は時刻を UTC の日付（0 時 0 分）に丸める。
func dateOnlyUTC(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// intersectDates は複数の日付集合（昇順・重複なし）の共通部分を、最初の集合の順序で返す。
func intersectDates(sets [][]time.Time) []time.Time {
	if len(sets) == 0 {
		return nil
	}
	count := make(map[string]int)
	for _, set := range sets {
		seen := make(map[string]bool, len(set))
		for _, d := range set {
			key := d.Format("2006-01-02")
			if !seen[key] {
				seen[key] = true
				count[key]++
			}
		}
	}
	n := len(sets)
	common := make([]time.Time, 0)
	for _, d := range sets[0] {
		if count[d.Format("2006-01-02")] == n {
			common = append(common, d)
		}
	}
	return common
}

// ---- ゲーム状態を表す型（server 層へ返す境界）----

// ReplayPair は通貨ペア 1 件分の表示情報。
type ReplayPair struct {
	PairID       int64
	Symbol       string
	BaseLeverage decimal.Decimal
	MaxLeverage  decimal.Decimal
	FeeRate      decimal.Decimal
}

// ReplayCandle は公開済みの日足 1 本（終了前は日付を持たない）。
type ReplayCandle struct {
	Day   int
	Open  decimal.Decimal
	High  decimal.Decimal
	Low   decimal.Decimal
	Close decimal.Decimal
	// Date は正確な日付。終了前は nil（先読み対策）。
	Date *time.Time
}

// ReplayPairCandles は通貨ペア 1 件分の公開済み日足。
type ReplayPairCandles struct {
	PairID  int64
	Candles []ReplayCandle
}

// ReplayPositionView は建玉 1 件分の表示情報（強制決済ライン込み）。
type ReplayPositionView struct {
	ID              int64
	PairID          int64
	Symbol          string
	Side            Side
	InitialMargin   decimal.Decimal
	Margin          decimal.Decimal
	Leverage        int
	EntryPrice      decimal.Decimal
	EntryDay        int
	LiquidationLine decimal.Decimal
	Status          string
	ClosePrice      *decimal.Decimal
	Pnl             *decimal.Decimal
}

// ReplayState はゲーム状態の取得結果。
type ReplayState struct {
	GameID        int64
	Status        string
	StartYear     int
	Funds         decimal.Decimal
	Speed         int
	PublishedDays int
	Pairs         []ReplayPair
	Positions     []ReplayPositionView
	Candles       []ReplayPairCandles
	// 以下は終了後にのみセット（先読み対策で終了前は nil）。
	IntervalStart *time.Time
	IntervalEnd   *time.Time
	FinalFunds    *decimal.Decimal
}

// ReplayStartResult はゲーム開始の結果（トークンを含む）。
type ReplayStartResult struct {
	GameID        int64
	Token         string
	StartYear     int
	Funds         decimal.Decimal
	Speed         int
	PublishedDays int
	Pairs         []ReplayPair
	Candles       []ReplayPairCandles
}

// replayCtx はゲーム処理で共通に使う派生データ（通貨ペア・日付・データセットの対応）。
type replayCtx struct {
	pairs         []db.CurrencyPair
	pairByID      map[int64]db.CurrencyPair
	days          []db.ReplayGameDay
	dayByIndex    map[int32]time.Time
	datasetByPair map[int64]int64
}

// loadReplayCtx はゲームの派生データを読み込む。
func loadReplayCtx(ctx context.Context, q *db.Queries, gameID int64) (*replayCtx, error) {
	pairs, err := q.ListCurrencyPairs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list currency pairs: %w", err)
	}
	pairByID := make(map[int64]db.CurrencyPair, len(pairs))
	for _, p := range pairs {
		pairByID[p.ID] = p
	}

	datasets, err := q.ListReplayGameDatasets(ctx, gameID)
	if err != nil {
		return nil, fmt.Errorf("list game datasets: %w", err)
	}
	datasetByPair := make(map[int64]int64, len(datasets))
	for _, d := range datasets {
		datasetByPair[d.PairID] = d.DatasetID
	}

	days, err := q.ListReplayGameDays(ctx, gameID)
	if err != nil {
		return nil, fmt.Errorf("list game days: %w", err)
	}
	dayByIndex := make(map[int32]time.Time, len(days))
	for _, d := range days {
		dayByIndex[d.DayIndex] = d.Date.Time
	}

	return &replayCtx{
		pairs:         pairs,
		pairByID:      pairByID,
		days:          days,
		dayByIndex:    dayByIndex,
		datasetByPair: datasetByPair,
	}, nil
}

// pick は [0, n) の乱数を返す（区間選び）。スレッドセーフ。
func (s *ReplayService) pick(n int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rng.Intn(n)
}

// token 関連。トークンは開始時に一度だけ返し、DB には SHA-256 ハッシュだけ保存する
// （design.md §13「先読み対策」）。
func generateReplayToken() (string, error) {
	return randomToken(32)
}

func hashReplayToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// ---- ゲーム開始 ----

// StartGame は 1 ゲームを開始し、ゲームトークンを発行する（#103 REPLAY-3）。
// userID が非空ならログインユーザー、空ならゲスト名（前後の空白を除いて 1〜16 文字）を必須とする。
func (s *ReplayService) StartGame(ctx context.Context, userID, guestName string) (ReplayStartResult, error) {
	now := s.clock.Now()

	var playerUserID pgtype.Text
	var playerGuestName pgtype.Text
	if userID != "" {
		playerUserID = pgtype.Text{String: userID, Valid: true}
	} else {
		name := strings.TrimSpace(guestName)
		if name == "" || utf8.RuneCountInString(name) > 16 {
			return ReplayStartResult{}, ErrReplayInvalidGuestName
		}
		playerGuestName = pgtype.Text{String: name, Valid: true}
	}

	token, err := generateReplayToken()
	if err != nil {
		return ReplayStartResult{}, fmt.Errorf("generate token: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ReplayStartResult{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	pairs, datasets, days, err := s.selectInterval(ctx, q, now)
	if err != nil {
		return ReplayStartResult{}, err
	}

	game, err := q.CreateReplayGame(ctx, db.CreateReplayGameParams{
		TokenHash: hashReplayToken(token),
		UserID:    playerUserID,
		GuestName: playerGuestName,
		StartedAt: pgtype.Timestamptz{Time: now, Valid: true},
		StartYear: int32(days[replayPreDays].Year()),
		Funds:     decimal.NewFromInt(replayInitialFunds),
	})
	if err != nil {
		return ReplayStartResult{}, fmt.Errorf("create replay game: %w", err)
	}

	for i, date := range days {
		if err := q.CreateReplayGameDay(ctx, db.CreateReplayGameDayParams{
			GameID:   game.ID,
			DayIndex: int32(i),
			Date:     pgtype.Date{Time: date, Valid: true},
		}); err != nil {
			return ReplayStartResult{}, fmt.Errorf("create replay game day: %w", err)
		}
	}
	for pairID, datasetID := range datasets {
		if err := q.CreateReplayGameDataset(ctx, db.CreateReplayGameDatasetParams{
			GameID:    game.ID,
			PairID:    pairID,
			DatasetID: datasetID,
		}); err != nil {
			return ReplayStartResult{}, fmt.Errorf("create replay game dataset: %w", err)
		}
	}

	// 開始時のレスポンス（開始前 20 日の日足を含む）。
	pairsInfo := make([]ReplayPair, 0, len(pairs))
	for _, p := range pairs {
		pairsInfo = append(pairsInfo, ReplayPair{
			PairID:       p.ID,
			Symbol:       p.Symbol,
			BaseLeverage: p.MaxLeverage,
			MaxLeverage:  p.MaxLeverage,
			FeeRate:      p.FeeRate,
		})
	}

	candles, err := s.buildCandles(ctx, q, datasets, days, 0, replayPreDays-1, false)
	if err != nil {
		return ReplayStartResult{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return ReplayStartResult{}, fmt.Errorf("commit tx: %w", err)
	}

	return ReplayStartResult{
		GameID:        game.ID,
		Token:         token,
		StartYear:     int(game.StartYear),
		Funds:         game.Funds,
		Speed:         1,
		PublishedDays: 0,
		Pairs:         pairsInfo,
		Candles:       candles,
	}, nil
}

// selectInterval は区間（開始前 20 日 + 区間 60 日の計 80 営業日）を選ぶ。
// 対象は currency_pairs の全行（コードにペアを書かない。CLAUDE.md §5.3）。各ペアについて
// ゲーム開始時点で一番新しく取り込まれたデータセットを使い、全ペアに値がある共通営業日を
// 日付順に並べ、連続する 80 日をランダムに選ぶ。区間の最終日は 2015-01-01 以降かつ
// 今日から 60 営業日以上前（design.md §13「対象と期間」）。
func (s *ReplayService) selectInterval(ctx context.Context, q *db.Queries, now time.Time) ([]db.CurrencyPair, map[int64]int64, []time.Time, error) {
	pairs, err := q.ListCurrencyPairs(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("list currency pairs: %w", err)
	}
	if len(pairs) == 0 {
		return nil, nil, nil, ErrReplayNoInterval
	}

	datasets := make(map[int64]int64, len(pairs))
	dateSets := make([][]time.Time, 0, len(pairs))
	for _, p := range pairs {
		ds, err := q.GetLatestRateDatasetForPair(ctx, p.ID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, nil, nil, ErrReplayNoInterval
			}
			return nil, nil, nil, fmt.Errorf("get latest dataset for %s: %w", p.Symbol, err)
		}
		datasets[p.ID] = ds.ID
		dates, err := q.ListDailyRateDatesByDataset(ctx, ds.ID)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("list dates for %s: %w", p.Symbol, err)
		}
		ts := make([]time.Time, len(dates))
		for i, d := range dates {
			ts[i] = d.Time
		}
		dateSets = append(dateSets, ts)
	}

	common := intersectDates(dateSets)
	if len(common) < replayPreDays+replayIntervalDays {
		return nil, nil, nil, ErrReplayNoInterval
	}

	minDate := time.Date(2015, 1, 1, 0, 0, 0, 0, time.UTC)
	cutoff := businessDaysBefore(dateOnlyUTC(now), 60)

	type window struct {
		days []time.Time
	}
	var windows []window
	for i := 0; i+replayPreDays+replayIntervalDays <= len(common); i++ {
		end := common[i+replayPreDays+replayIntervalDays-1]
		if end.Before(minDate) {
			continue
		}
		if end.After(cutoff) {
			continue
		}
		windows = append(windows, window{days: common[i : i+replayPreDays+replayIntervalDays]})
	}
	if len(windows) == 0 {
		return nil, nil, nil, ErrReplayNoInterval
	}

	chosen := windows[s.pick(len(windows))]
	return pairs, datasets, chosen.days, nil
}

// buildCandles は各ペアの公開済み日足（day_index が [from, to] の範囲）を返す。
// includeDates が true のとき（終了後）日付も入れる。
//
// 注意: 共通営業日は祝日などでカレンダー上は連続しない（あるペアだけ値が無い日は含まれない）ため、
// 日付範囲で引いた daily_rates を単純に index で対応させず、日付をキーに対応させる。
func (s *ReplayService) buildCandles(ctx context.Context, q *db.Queries, datasets map[int64]int64, days []time.Time, from, to int, includeDates bool) ([]ReplayPairCandles, error) {
	result := make([]ReplayPairCandles, 0, len(datasets))
	pairs, err := q.ListCurrencyPairs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list currency pairs: %w", err)
	}
	for _, p := range pairs {
		datasetID, ok := datasets[p.ID]
		if !ok {
			continue
		}
		rates, err := q.ListDailyRatesByDatasetAndDateRange(ctx, db.ListDailyRatesByDatasetAndDateRangeParams{
			DatasetID: datasetID,
			Date:      pgtype.Date{Time: days[from], Valid: true},
			Date_2:    pgtype.Date{Time: days[to], Valid: true},
		})
		if err != nil {
			return nil, fmt.Errorf("list daily rates for %s: %w", p.Symbol, err)
		}
		byDate := make(map[string]db.DailyRate, len(rates))
		for _, r := range rates {
			byDate[r.Date.Time.Format("2006-01-02")] = r
		}
		candles := make([]ReplayCandle, 0, to-from+1)
		for i := from; i <= to; i++ {
			r, ok := byDate[days[i].Format("2006-01-02")]
			if !ok {
				continue
			}
			c := ReplayCandle{
				Day:   i,
				Open:  r.Open,
				High:  r.High,
				Low:   r.Low,
				Close: r.Close,
			}
			if includeDates {
				d := days[i]
				c.Date = &d
			}
			candles = append(candles, c)
		}
		result = append(result, ReplayPairCandles{PairID: p.ID, Candles: candles})
	}
	return result, nil
}

// ---- 遅延評価（scale-to-zero との整合。design.md §13）----

// advance は「リクエストが来た時点」でまとめて行う処理を実行する。
// 順番は「公開済みの日までの強制決済」→「終了していれば確定」→「早期終了の判定」。
// 呼び出し側はゲームの行をロック済みであること。公開済み区間営業日数（0..60）を返す。
func (s *ReplayService) advance(ctx context.Context, q *db.Queries, game db.ReplayGame, c *replayCtx, now time.Time) (db.ReplayGame, int, error) {
	changes, err := q.ListReplaySpeedChanges(ctx, game.ID)
	if err != nil {
		return game, 0, fmt.Errorf("list speed changes: %w", err)
	}
	published := replayPublishedDays(game.StartedAt.Time, changes, now)

	if game.Status != replayStatusPlaying {
		return game, published, nil
	}

	currentDay := int32(replayPreDays - 1 + published)

	// 1. 強制決済（建玉を持った日の次の公開日から、公開済みの日まで判定）。
	funds, err := s.liquidate(ctx, q, game, c, currentDay)
	if err != nil {
		return game, published, err
	}
	game.Funds = funds

	// 2. 終了していれば確定（最後の日の終値で未決済建玉を決済）。
	if published >= replayIntervalDays {
		funds, err := s.settleAll(ctx, q, game, c)
		if err != nil {
			return game, published, err
		}
		game.Funds = funds
		game.Status = replayStatusFinished
		game.FinalFunds = decimal.NullDecimal{Decimal: funds, Valid: true}
		game.FinishedAt = pgtype.Timestamptz{Time: now, Valid: true}
		if err := q.FinishReplayGame(ctx, db.FinishReplayGameParams{
			ID:         game.ID,
			Status:     replayStatusFinished,
			FinalFunds: game.FinalFunds,
			FinishedAt: game.FinishedAt,
		}); err != nil {
			return game, published, fmt.Errorf("finish replay game: %w", err)
		}
		return game, published, nil
	}

	// 3. 早期終了（建玉がなく、未使用資金で有効な注文を 1 つも払えない）。
	remaining, err := q.ListOpenReplayPositions(ctx, game.ID)
	if err != nil {
		return game, published, fmt.Errorf("list open positions: %w", err)
	}
	if len(remaining) == 0 && game.Funds.LessThan(minOrderCost(c.pairs)) {
		game.Status = replayStatusEarlyFinished
		game.FinalFunds = decimal.NullDecimal{Decimal: game.Funds, Valid: true}
		game.FinishedAt = pgtype.Timestamptz{Time: now, Valid: true}
		if err := q.FinishReplayGame(ctx, db.FinishReplayGameParams{
			ID:         game.ID,
			Status:     replayStatusEarlyFinished,
			FinalFunds: game.FinalFunds,
			FinishedAt: game.FinishedAt,
		}); err != nil {
			return game, published, fmt.Errorf("finish replay game: %w", err)
		}
	}
	return game, published, nil
}

// liquidate は公開済みの日まで強制決済を判定する。戻り値は決済を反映した未使用資金。
func (s *ReplayService) liquidate(ctx context.Context, q *db.Queries, game db.ReplayGame, c *replayCtx, currentDay int32) (decimal.Decimal, error) {
	funds := game.Funds
	positions, err := q.ListOpenReplayPositions(ctx, game.ID)
	if err != nil {
		return funds, fmt.Errorf("list open positions: %w", err)
	}
	for _, pos := range positions {
		pair := c.pairByID[pos.PairID]
		datasetID := c.datasetByPair[pos.PairID]
		notional := replayNotional(pos.InitialMargin, pos.Leverage)
		line := replayLiquidationLine(Side(pos.Side), pos.EntryPrice, pos.Margin, notional)

		// 建玉を持った日の次の公開日（entry_day+1）から判定する。
		for d := pos.EntryDay + 1; d <= currentDay; d++ {
			date, ok := c.dayByIndex[d]
			if !ok {
				continue
			}
			rate, err := q.GetDailyRateByDatasetAndDate(ctx, db.GetDailyRateByDatasetAndDateParams{
				DatasetID: datasetID,
				Date:      pgtype.Date{Time: date, Valid: true},
			})
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					continue
				}
				return funds, fmt.Errorf("get daily rate: %w", err)
			}
			// 買いは安値、売りは高値がラインに届いたら強制決済。
			hit := false
			if Side(pos.Side) == SideLong {
				hit = rate.Low.LessThanOrEqual(line)
			} else {
				hit = rate.High.GreaterThanOrEqual(line)
			}
			if !hit {
				continue
			}

			// ラインの値段で決済。損失は証拠金までに抑える（決済で戻る額 = 0）。
			pnl := replayPnL(Side(pos.Side), pos.EntryPrice, line, notional)
			fee := replayFee(notional, pair.FeeRate)
			returned := replayCloseReturn(pos.Margin, pnl, fee)
			funds = funds.Add(returned)

			if _, err := q.CloseReplayPosition(ctx, db.CloseReplayPositionParams{
				ID:         pos.ID,
				Status:     replayPositionLiquidated,
				ClosedDay:  pgtype.Int4{Int32: d, Valid: true},
				ClosePrice: decimal.NullDecimal{Decimal: line, Valid: true},
				Pnl:        decimal.NullDecimal{Decimal: pnl, Valid: true},
			}); err != nil {
				return funds, fmt.Errorf("liquidate position: %w", err)
			}
			break
		}
	}
	if err := q.UpdateReplayGameFunds(ctx, db.UpdateReplayGameFundsParams{
		ID:    game.ID,
		Funds: funds,
	}); err != nil {
		return funds, fmt.Errorf("update funds: %w", err)
	}
	return funds, nil
}

// settleAll は終了時に未決済建玉を最後の日（day_index 79）の終値で決済する。
func (s *ReplayService) settleAll(ctx context.Context, q *db.Queries, game db.ReplayGame, c *replayCtx) (decimal.Decimal, error) {
	funds := game.Funds
	positions, err := q.ListOpenReplayPositions(ctx, game.ID)
	if err != nil {
		return funds, fmt.Errorf("list open positions: %w", err)
	}
	finalDay := int32(replayPreDays - 1 + replayIntervalDays)
	for _, pos := range positions {
		pair := c.pairByID[pos.PairID]
		datasetID := c.datasetByPair[pos.PairID]
		date := c.dayByIndex[finalDay]
		rate, err := q.GetDailyRateByDatasetAndDate(ctx, db.GetDailyRateByDatasetAndDateParams{
			DatasetID: datasetID,
			Date:      pgtype.Date{Time: date, Valid: true},
		})
		if err != nil {
			return funds, fmt.Errorf("get final daily rate: %w", err)
		}
		notional := replayNotional(pos.InitialMargin, pos.Leverage)
		pnl := replayPnL(Side(pos.Side), pos.EntryPrice, rate.Close, notional)
		fee := replayFee(notional, pair.FeeRate)
		returned := replayCloseReturn(pos.Margin, pnl, fee)
		funds = funds.Add(returned)

		if _, err := q.CloseReplayPosition(ctx, db.CloseReplayPositionParams{
			ID:         pos.ID,
			Status:     replayPositionClosed,
			ClosedDay:  pgtype.Int4{Int32: finalDay, Valid: true},
			ClosePrice: decimal.NullDecimal{Decimal: rate.Close, Valid: true},
			Pnl:        decimal.NullDecimal{Decimal: pnl, Valid: true},
		}); err != nil {
			return funds, fmt.Errorf("close position: %w", err)
		}
	}
	if err := q.UpdateReplayGameFunds(ctx, db.UpdateReplayGameFundsParams{
		ID:    game.ID,
		Funds: funds,
	}); err != nil {
		return funds, fmt.Errorf("update funds: %w", err)
	}
	return funds, nil
}

// ---- ゲーム操作 ----

// getGameForUpdate はトークンを検証してゲームの行をロックする（同時リクエストで
// 二重処理しないため）。トークンが合わない場合は存在しないゲームと区別せず
// ErrReplayGameNotFound を返す（他人のゲームIDの存在有無を漏らさない）。
func (s *ReplayService) getGameForUpdate(ctx context.Context, q *db.Queries, token string, gameID int64) (db.ReplayGame, error) {
	game, err := q.GetReplayGameByIDForUpdate(ctx, gameID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.ReplayGame{}, ErrReplayGameNotFound
		}
		return db.ReplayGame{}, fmt.Errorf("get replay game: %w", err)
	}
	if game.TokenHash != hashReplayToken(token) {
		return db.ReplayGame{}, ErrReplayGameNotFound
	}
	return game, nil
}

// GetState はゲーム状態を取得する。遅延評価（強制決済・確定・早期終了）を先に行う。
func (s *ReplayService) GetState(ctx context.Context, token string, gameID int64) (ReplayState, error) {
	now := s.clock.Now()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ReplayState{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	game, err := s.getGameForUpdate(ctx, q, token, gameID)
	if err != nil {
		return ReplayState{}, err
	}
	c, err := loadReplayCtx(ctx, q, game.ID)
	if err != nil {
		return ReplayState{}, err
	}
	game, published, err := s.advance(ctx, q, game, c, now)
	if err != nil {
		return ReplayState{}, err
	}

	state, err := s.buildState(ctx, q, game, c, published)
	if err != nil {
		return ReplayState{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return ReplayState{}, fmt.Errorf("commit tx: %w", err)
	}
	return state, nil
}

// buildState はゲームの現在状態を組み立てる。
func (s *ReplayService) buildState(ctx context.Context, q *db.Queries, game db.ReplayGame, c *replayCtx, published int) (ReplayState, error) {
	changes, err := q.ListReplaySpeedChanges(ctx, game.ID)
	if err != nil {
		return ReplayState{}, fmt.Errorf("list speed changes: %w", err)
	}
	speed := replayCurrentSpeed(changes)

	unlocks, err := q.ListReplayLeverageUnlocks(ctx, game.ID)
	if err != nil {
		return ReplayState{}, fmt.Errorf("list leverage unlocks: %w", err)
	}
	unlockByPair := make(map[int64]int32, len(unlocks))
	for _, u := range unlocks {
		unlockByPair[u.PairID] = u.Tier
	}

	pairs := make([]ReplayPair, 0, len(c.pairs))
	for _, p := range c.pairs {
		maxLev := p.MaxLeverage.Add(decimal.NewFromInt(int64(unlockByPair[p.ID])).Mul(decimal.NewFromInt(replayLeverageUnlockStep)))
		pairs = append(pairs, ReplayPair{
			PairID:       p.ID,
			Symbol:       p.Symbol,
			BaseLeverage: p.MaxLeverage,
			MaxLeverage:  maxLev,
			FeeRate:      p.FeeRate,
		})
	}

	positions, err := q.ListOpenReplayPositions(ctx, game.ID)
	if err != nil {
		return ReplayState{}, fmt.Errorf("list open positions: %w", err)
	}
	posViews := make([]ReplayPositionView, 0, len(positions))
	for _, pos := range positions {
		posViews = append(posViews, replayPositionView(pos, c.pairByID[pos.PairID].Symbol))
	}

	finished := game.Status != replayStatusPlaying
	days := make([]time.Time, len(c.days))
	for _, d := range c.days {
		days[d.DayIndex] = d.Date.Time
	}
	currentDay := replayPreDays - 1 + published
	if finished {
		// 終了後は全区間（80 日）を日付付きで返す（結果画面）。
		currentDay = len(days) - 1
	}
	candles, err := s.buildCandles(ctx, q, c.datasetByPair, days, 0, currentDay, finished)
	if err != nil {
		return ReplayState{}, err
	}

	state := ReplayState{
		GameID:        game.ID,
		Status:        game.Status,
		StartYear:     int(game.StartYear),
		Funds:         game.Funds,
		Speed:         speed,
		PublishedDays: published,
		Pairs:         pairs,
		Positions:     posViews,
		Candles:       candles,
	}

	if finished {
		start := days[replayPreDays]
		end := days[len(days)-1]
		finalFunds := game.FinalFunds.Decimal
		state.IntervalStart = &start
		state.IntervalEnd = &end
		state.FinalFunds = &finalFunds
	}
	return state, nil
}

// replayPositionView は建玉 1 件分の表示情報を組み立てる（強制決済ライン込み）。
func replayPositionView(pos db.ReplayPosition, symbol string) ReplayPositionView {
	notional := replayNotional(pos.InitialMargin, pos.Leverage)
	line := replayLiquidationLine(Side(pos.Side), pos.EntryPrice, pos.Margin, notional)
	v := ReplayPositionView{
		ID:              pos.ID,
		PairID:          pos.PairID,
		Symbol:          symbol,
		Side:            Side(pos.Side),
		InitialMargin:   pos.InitialMargin,
		Margin:          pos.Margin,
		Leverage:        int(pos.Leverage),
		EntryPrice:      pos.EntryPrice,
		EntryDay:        int(pos.EntryDay),
		LiquidationLine: line,
		Status:          pos.Status,
	}
	if pos.ClosePrice.Valid {
		cp := pos.ClosePrice.Decimal
		v.ClosePrice = &cp
	}
	if pos.Pnl.Valid {
		p := pos.Pnl.Decimal
		v.Pnl = &p
	}
	return v
}

// ReplayOrderParams は過去チャートモードの注文入力。
type ReplayOrderParams struct {
	PairID   int64
	Side     Side
	Margin   decimal.Decimal
	Leverage decimal.Decimal
}

// ReplayOrderResult は注文成立時の結果。
type ReplayOrderResult struct {
	Position ReplayPositionView
	Funds    decimal.Decimal
}

// PlaceOrder は注文する。約定は公開済みの最新の終値（サーバーが決める。ブラウザから
// 価格を指定させない。design.md §13「先読み対策」）。
func (s *ReplayService) PlaceOrder(ctx context.Context, token string, gameID int64, p ReplayOrderParams) (ReplayOrderResult, error) {
	if p.Side != SideLong && p.Side != SideShort {
		return ReplayOrderResult{}, ErrInvalidSide
	}
	if !isPositiveInteger(p.Margin) {
		return ReplayOrderResult{}, ErrReplayInvalidMargin
	}
	if !isPositiveInteger(p.Leverage) {
		return ReplayOrderResult{}, ErrReplayInvalidLeverage
	}

	now := s.clock.Now()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ReplayOrderResult{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	game, err := s.getGameForUpdate(ctx, q, token, gameID)
	if err != nil {
		return ReplayOrderResult{}, err
	}
	c, err := loadReplayCtx(ctx, q, game.ID)
	if err != nil {
		return ReplayOrderResult{}, err
	}
	game, published, err := s.advance(ctx, q, game, c, now)
	if err != nil {
		return ReplayOrderResult{}, err
	}
	if game.Status != replayStatusPlaying {
		return ReplayOrderResult{}, ErrReplayGameFinished
	}

	pair, ok := c.pairByID[p.PairID]
	if !ok {
		return ReplayOrderResult{}, ErrReplayPairNotFound
	}

	// 現在の上限（初期上限 + 解放段数 × 10）。
	unlock, err := q.GetReplayLeverageUnlock(ctx, db.GetReplayLeverageUnlockParams{
		GameID: game.ID,
		PairID: p.PairID,
	})
	tier := int32(0)
	switch {
	case err == nil:
		tier = unlock.Tier
	case errors.Is(err, pgx.ErrNoRows):
		// tier 0 のまま。
	default:
		return ReplayOrderResult{}, fmt.Errorf("get leverage unlock: %w", err)
	}
	maxLev := pair.MaxLeverage.Add(decimal.NewFromInt(int64(tier)).Mul(decimal.NewFromInt(replayLeverageUnlockStep)))
	if p.Leverage.GreaterThan(maxLev) {
		return ReplayOrderResult{}, ErrLeverageExceedsMax
	}

	// 同じペアの反対方向の建玉は許可しない（追加決定）。既存の当該ペアの建玉を
	// すべて決済してからでないと反対方向は注文できない。
	if err := s.checkOpposite(ctx, q, game.ID, p.PairID, p.Side); err != nil {
		return ReplayOrderResult{}, err
	}

	currentDay := int32(replayPreDays - 1 + published)
	date := c.dayByIndex[currentDay]
	datasetID := c.datasetByPair[p.PairID]
	rate, err := q.GetDailyRateByDatasetAndDate(ctx, db.GetDailyRateByDatasetAndDateParams{
		DatasetID: datasetID,
		Date:      pgtype.Date{Time: date, Valid: true},
	})
	if err != nil {
		return ReplayOrderResult{}, fmt.Errorf("get current daily rate: %w", err)
	}
	entryPrice := rate.Close

	initialMargin := p.Margin
	leverage := int32(p.Leverage.IntPart())
	notional := replayNotional(initialMargin, leverage)
	fee := replayFee(notional, pair.FeeRate)
	totalCost := initialMargin.Add(fee)

	if game.Funds.LessThan(totalCost) {
		return ReplayOrderResult{}, ErrInsufficientBalance
	}
	newFunds := game.Funds.Sub(totalCost)

	pos, err := q.CreateReplayPosition(ctx, db.CreateReplayPositionParams{
		GameID:        game.ID,
		PairID:        p.PairID,
		Side:          string(p.Side),
		InitialMargin: initialMargin,
		Leverage:      leverage,
		EntryPrice:    entryPrice,
		EntryDay:      currentDay,
	})
	if err != nil {
		return ReplayOrderResult{}, fmt.Errorf("create replay position: %w", err)
	}
	if err := q.UpdateReplayGameFunds(ctx, db.UpdateReplayGameFundsParams{
		ID:    game.ID,
		Funds: newFunds,
	}); err != nil {
		return ReplayOrderResult{}, fmt.Errorf("update funds: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return ReplayOrderResult{}, fmt.Errorf("commit tx: %w", err)
	}

	return ReplayOrderResult{
		Position: replayPositionView(pos, pair.Symbol),
		Funds:    newFunds,
	}, nil
}

// checkOpposite は同じペアに反対方向の未決済建玉があるかを返す。
func (s *ReplayService) checkOpposite(ctx context.Context, q *db.Queries, gameID, pairID int64, side Side) error {
	positions, err := q.ListOpenReplayPositions(ctx, gameID)
	if err != nil {
		return fmt.Errorf("list open positions: %w", err)
	}
	for _, pos := range positions {
		if pos.PairID == pairID && Side(pos.Side) != side {
			return ErrReplayOppositePosition
		}
	}
	return nil
}

// ReplayCloseResult は決済の結果。
type ReplayCloseResult struct {
	Position ReplayPositionView
	Pnl      decimal.Decimal
	Funds    decimal.Decimal
}

// ClosePosition は建玉を決済する（公開済みの最新の終値で約定）。
func (s *ReplayService) ClosePosition(ctx context.Context, token string, gameID, positionID int64) (ReplayCloseResult, error) {
	now := s.clock.Now()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ReplayCloseResult{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	game, err := s.getGameForUpdate(ctx, q, token, gameID)
	if err != nil {
		return ReplayCloseResult{}, err
	}
	c, err := loadReplayCtx(ctx, q, game.ID)
	if err != nil {
		return ReplayCloseResult{}, err
	}
	game, published, err := s.advance(ctx, q, game, c, now)
	if err != nil {
		return ReplayCloseResult{}, err
	}
	if game.Status != replayStatusPlaying {
		return ReplayCloseResult{}, ErrReplayGameFinished
	}

	pos, err := q.GetReplayPositionForUpdate(ctx, db.GetReplayPositionForUpdateParams{
		ID:     positionID,
		GameID: game.ID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ReplayCloseResult{}, ErrReplayPositionNotFound
		}
		return ReplayCloseResult{}, fmt.Errorf("get replay position: %w", err)
	}
	if pos.Status != replayPositionOpen {
		return ReplayCloseResult{}, ErrReplayPositionClosed
	}

	pair := c.pairByID[pos.PairID]
	currentDay := int32(replayPreDays - 1 + published)
	date := c.dayByIndex[currentDay]
	datasetID := c.datasetByPair[pos.PairID]
	rate, err := q.GetDailyRateByDatasetAndDate(ctx, db.GetDailyRateByDatasetAndDateParams{
		DatasetID: datasetID,
		Date:      pgtype.Date{Time: date, Valid: true},
	})
	if err != nil {
		return ReplayCloseResult{}, fmt.Errorf("get current daily rate: %w", err)
	}
	exitPrice := rate.Close

	notional := replayNotional(pos.InitialMargin, pos.Leverage)
	pnl := replayPnL(Side(pos.Side), pos.EntryPrice, exitPrice, notional)
	fee := replayFee(notional, pair.FeeRate)
	returned := replayCloseReturn(pos.Margin, pnl, fee)
	newFunds := game.Funds.Add(returned)

	closed, err := q.CloseReplayPosition(ctx, db.CloseReplayPositionParams{
		ID:         pos.ID,
		Status:     replayPositionClosed,
		ClosedDay:  pgtype.Int4{Int32: currentDay, Valid: true},
		ClosePrice: decimal.NullDecimal{Decimal: exitPrice, Valid: true},
		Pnl:        decimal.NullDecimal{Decimal: pnl, Valid: true},
	})
	if err != nil {
		return ReplayCloseResult{}, fmt.Errorf("close replay position: %w", err)
	}
	if err := q.UpdateReplayGameFunds(ctx, db.UpdateReplayGameFundsParams{
		ID:    game.ID,
		Funds: newFunds,
	}); err != nil {
		return ReplayCloseResult{}, fmt.Errorf("update funds: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return ReplayCloseResult{}, fmt.Errorf("commit tx: %w", err)
	}

	return ReplayCloseResult{
		Position: replayPositionView(closed, pair.Symbol),
		Pnl:      pnl,
		Funds:    newFunds,
	}, nil
}

// ReplayAddMarginResult は証拠金追加の結果。
type ReplayAddMarginResult struct {
	Position ReplayPositionView
	Funds    decimal.Decimal
}

// AddMargin は建玉に証拠金を追加する。取引規模・取得価格は変えず、損失に耐える資金だけを
// 増やす（追加額は未使用資金から差し引く。追加分も損失の対象）。
func (s *ReplayService) AddMargin(ctx context.Context, token string, gameID, positionID int64, amount decimal.Decimal) (ReplayAddMarginResult, error) {
	if !isPositiveInteger(amount) {
		return ReplayAddMarginResult{}, ErrReplayInvalidAmount
	}

	now := s.clock.Now()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ReplayAddMarginResult{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	game, err := s.getGameForUpdate(ctx, q, token, gameID)
	if err != nil {
		return ReplayAddMarginResult{}, err
	}
	c, err := loadReplayCtx(ctx, q, game.ID)
	if err != nil {
		return ReplayAddMarginResult{}, err
	}
	game, _, err = s.advance(ctx, q, game, c, now)
	if err != nil {
		return ReplayAddMarginResult{}, err
	}
	if game.Status != replayStatusPlaying {
		return ReplayAddMarginResult{}, ErrReplayGameFinished
	}

	pos, err := q.GetReplayPositionForUpdate(ctx, db.GetReplayPositionForUpdateParams{
		ID:     positionID,
		GameID: game.ID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ReplayAddMarginResult{}, ErrReplayPositionNotFound
		}
		return ReplayAddMarginResult{}, fmt.Errorf("get replay position: %w", err)
	}
	if pos.Status != replayPositionOpen {
		return ReplayAddMarginResult{}, ErrReplayPositionClosed
	}

	if game.Funds.LessThan(amount) {
		return ReplayAddMarginResult{}, ErrInsufficientBalance
	}
	newFunds := game.Funds.Sub(amount)
	newMargin := pos.Margin.Add(amount)

	updated, err := q.AddReplayPositionMargin(ctx, db.AddReplayPositionMarginParams{
		ID:     pos.ID,
		Margin: newMargin,
	})
	if err != nil {
		return ReplayAddMarginResult{}, fmt.Errorf("add replay position margin: %w", err)
	}
	if err := q.UpdateReplayGameFunds(ctx, db.UpdateReplayGameFundsParams{
		ID:    game.ID,
		Funds: newFunds,
	}); err != nil {
		return ReplayAddMarginResult{}, fmt.Errorf("update funds: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return ReplayAddMarginResult{}, fmt.Errorf("commit tx: %w", err)
	}

	return ReplayAddMarginResult{
		Position: replayPositionView(updated, c.pairByID[pos.PairID].Symbol),
		Funds:    newFunds,
	}, nil
}

// ReplayUnlockLeverageResult は上限解放の結果。
type ReplayUnlockLeverageResult struct {
	PairID      int64
	Tier        int
	MaxLeverage decimal.Decimal
	Cost        decimal.Decimal
	Funds       decimal.Decimal
}

// UnlockLeverage は通貨ペアのレバレッジ上限を 1 段解放する。費用は 250 から段ごとに 2 倍で
// ペアごとに数える。払った資金は戻らない（そのゲームの中だけ有効）。
func (s *ReplayService) UnlockLeverage(ctx context.Context, token string, gameID, pairID int64) (ReplayUnlockLeverageResult, error) {
	now := s.clock.Now()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ReplayUnlockLeverageResult{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	game, err := s.getGameForUpdate(ctx, q, token, gameID)
	if err != nil {
		return ReplayUnlockLeverageResult{}, err
	}
	c, err := loadReplayCtx(ctx, q, game.ID)
	if err != nil {
		return ReplayUnlockLeverageResult{}, err
	}
	game, _, err = s.advance(ctx, q, game, c, now)
	if err != nil {
		return ReplayUnlockLeverageResult{}, err
	}
	if game.Status != replayStatusPlaying {
		return ReplayUnlockLeverageResult{}, ErrReplayGameFinished
	}

	pair, ok := c.pairByID[pairID]
	if !ok {
		return ReplayUnlockLeverageResult{}, ErrReplayPairNotFound
	}

	unlock, err := q.GetReplayLeverageUnlock(ctx, db.GetReplayLeverageUnlockParams{
		GameID: game.ID,
		PairID: pairID,
	})
	tier := int32(0)
	switch {
	case err == nil:
		tier = unlock.Tier
	case errors.Is(err, pgx.ErrNoRows):
		// tier 0 のまま。
	default:
		return ReplayUnlockLeverageResult{}, fmt.Errorf("get leverage unlock: %w", err)
	}

	cost := replayUnlockCost(int(tier))
	if game.Funds.LessThan(cost) {
		return ReplayUnlockLeverageResult{}, ErrInsufficientBalance
	}
	newFunds := game.Funds.Sub(cost)
	newTier := tier + 1

	upserted, err := q.UpsertReplayLeverageUnlock(ctx, db.UpsertReplayLeverageUnlockParams{
		GameID: game.ID,
		PairID: pairID,
		Tier:   newTier,
	})
	if err != nil {
		return ReplayUnlockLeverageResult{}, fmt.Errorf("upsert leverage unlock: %w", err)
	}
	if err := q.UpdateReplayGameFunds(ctx, db.UpdateReplayGameFundsParams{
		ID:    game.ID,
		Funds: newFunds,
	}); err != nil {
		return ReplayUnlockLeverageResult{}, fmt.Errorf("update funds: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return ReplayUnlockLeverageResult{}, fmt.Errorf("commit tx: %w", err)
	}

	maxLev := pair.MaxLeverage.Add(decimal.NewFromInt(int64(newTier)).Mul(decimal.NewFromInt(replayLeverageUnlockStep)))
	return ReplayUnlockLeverageResult{
		PairID:      pairID,
		Tier:        int(upserted.Tier),
		MaxLeverage: maxLev,
		Cost:        cost,
		Funds:       newFunds,
	}, nil
}

// ReplayChangeSpeedResult は速さ変更の結果。
type ReplayChangeSpeedResult struct {
	Speed         int
	PublishedDays int
}

// ChangeSpeed は速さを変更する（×1・×2・×3。一時停止なし）。変更時刻と速さをサーバーが記録し、
// 公開済み日数はそれまでの経過と合わせて計算する。
func (s *ReplayService) ChangeSpeed(ctx context.Context, token string, gameID int64, speed int) (ReplayChangeSpeedResult, error) {
	if speed < replayMinSpeed || speed > replayMaxSpeed {
		return ReplayChangeSpeedResult{}, ErrReplayInvalidSpeed
	}

	now := s.clock.Now()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ReplayChangeSpeedResult{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	game, err := s.getGameForUpdate(ctx, q, token, gameID)
	if err != nil {
		return ReplayChangeSpeedResult{}, err
	}
	c, err := loadReplayCtx(ctx, q, game.ID)
	if err != nil {
		return ReplayChangeSpeedResult{}, err
	}
	game, published, err := s.advance(ctx, q, game, c, now)
	if err != nil {
		return ReplayChangeSpeedResult{}, err
	}
	if game.Status != replayStatusPlaying {
		return ReplayChangeSpeedResult{}, ErrReplayGameFinished
	}

	if err := q.CreateReplaySpeedChange(ctx, db.CreateReplaySpeedChangeParams{
		GameID:    game.ID,
		ChangedAt: pgtype.Timestamptz{Time: now, Valid: true},
		Speed:     int32(speed),
	}); err != nil {
		return ReplayChangeSpeedResult{}, fmt.Errorf("create replay speed change: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return ReplayChangeSpeedResult{}, fmt.Errorf("commit tx: %w", err)
	}

	// 変更は今この瞬間から有効なので、この時点の公開済み日数は変わらない。
	return ReplayChangeSpeedResult{Speed: speed, PublishedDays: published}, nil
}

// isPositiveInteger は d が 1 以上の整数かを返す（証拠金・レバレッジ・追加額の検証）。
func isPositiveInteger(d decimal.Decimal) bool {
	if !d.IsPositive() || !d.IsInteger() {
		return false
	}
	return true
}
