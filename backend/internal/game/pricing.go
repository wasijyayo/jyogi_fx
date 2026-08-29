package game

import (
	"encoding/binary"
	"math"
	"time"

	"github.com/cespare/xxhash/v2"
	"github.com/shopspring/decimal"

	"fxgame/backend/internal/db"
)

// jst は日本標準時（UTC+9、夏時間なし）。IANA の tzdata（"Asia/Tokyo"）に依存すると
// tzdata を含まない最小コンテナで実行時に壊れうるため、固定オフセットで持つ
// （日本には夏時間が無いのでこれで常に正確）。
var jst = time.FixedZone("JST", 9*60*60)

const (
	// sessionStartMinuteJST / sessionDurationMinutes: 取引時間は JST 12:00〜13:00
	// （確定 #13。docs/design.md §7.10 他）。
	sessionStartMinuteJST  = 12 * 60
	sessionDurationMinutes = 60
	minutesPerDay          = 24 * 60
)

// zAt は (seed, tick) から標準正規分布に従う疑似乱数を生成する純粋関数
// （docs/design.md §2.7「z の生成」）。
//
// **rand.New(rand.NewSource(seed)) による逐次生成は禁止**（CLAUDE.md §5.7）。
// Go の math/rand は途中位置から再開できないため、逐次生成だとセッション開始の
// たびに通貨生成時点から全tickを引き直す必要が生じ、運用日数に比例して
// 計算量が増え続ける。ハッシュ + Box-Muller ならどの tick の値も
// 呼び出し順序に関係なく単独・O(1) で求まる。
func zAt(seed uint64, tick int64) float64 {
	var buf [16]byte
	binary.LittleEndian.PutUint64(buf[0:8], seed)
	binary.LittleEndian.PutUint64(buf[8:16], uint64(tick))
	h := xxhash.Sum64(buf[:])

	u1 := float64(h>>11) / float64(uint64(1)<<53)
	u2 := float64((h*2654435761)>>11) / float64(uint64(1)<<53)
	// u1 == 0 だと Log(0) = -Inf になるため、design.md 通り微小値を足して避ける。
	return math.Sqrt(-2*math.Log(u1+1e-15)) * math.Cos(2*math.Pi*u2)
}

// minuteOfDayJST は t の JST での「その日の何分目か」（0〜1439）を返す。
func minuteOfDayJST(t time.Time) int64 {
	j := t.In(jst)
	return int64(j.Hour()*60 + j.Minute())
}

// isInSession はそのtickがセッション中かどうかを返す。
// epochOffsetMinutes は通貨の epoch_at の JST 分オフセット（minuteOfDayJST の結果）。
// tick は1分刻みで進むため、tick + epochOffsetMinutes を 1440 で割った余りが
// そのtick時点の「JSTでの分」になる。time.Time を毎tick変換するより大幅に軽い。
func isInSession(epochOffsetMinutes, tick int64) bool {
	minuteOfDay := ((tick+epochOffsetMinutes)%minutesPerDay + minutesPerDay) % minutesPerDay
	return minuteOfDay >= sessionStartMinuteJST && minuteOfDay < sessionStartMinuteJST+sessionDurationMinutes
}

// scaleAt はそのtickがセッション中かどうかでボラティリティを抑制する係数を返す
// （docs/design.md §2.7「セッション外のボラティリティ抑制」）。
// セッション中は 1（抑制なし）、セッション外は off_session_scale を返す。
//
// design.md の仕様通り decimal.Decimal を返す形で公開するが、BasePrice の
// ホットループ内ではこれを直接呼ばず isInSession を使う。tickごとに
// decimal.Decimal を生成するコストが無視できなかったため（#32 のベンチマークで実測）。
func scaleAt(epochOffsetMinutes, tick int64, offSessionScale decimal.Decimal) decimal.Decimal {
	if isInSession(epochOffsetMinutes, tick) {
		return decimal.NewFromInt(1)
	}
	return offSessionScale
}

// BasePrice は docs/design.md §2.1/§2.7 の基準価格 base(n) を返す純粋関数。
//
//	base(n) = base₀ × exp( Σ_{i=1}^{n} volatility × scale(i) × z(seed, i) )
//
// tickIndex（＝currencyの epoch_at からの通算tick番号）のみに依存し、
// 呼び出し順序に依存しない。同じ (currency, tickIndex) には常に同じ値を返す。
//
// イベント（shock）による乗算項は §5.4 のとおり別の乗算項として扱う（#40 で追加）。
// ここでは §2.1 の基本式（乱数由来の基準価格）のみを実装する。
//
// 内部の総和・exp 計算は float64 で行う。CLAUDE.md §5.2 の「金額に float 禁止」は
// 残高・価格そのものの型に対する制約であり、exp/log/cos を要する乱数生成の内部計算は
// decimal.Decimal では表現できない（shopspring/decimal は超越関数を持たない）。
// 最終結果を decimal.Decimal に変換して返すことで、外部に見える価格の型は守る。
func BasePrice(c db.Currency, tickIndex int64) decimal.Decimal {
	if tickIndex <= 0 {
		return c.BasePrice
	}

	epochOffset := minuteOfDayJST(c.EpochAt.Time)
	seed := uint64(c.Seed)
	volatility := c.Volatility.InexactFloat64()
	offSessionScale := c.OffSessionScale.InexactFloat64()

	sum := 0.0
	for i := int64(1); i <= tickIndex; i++ {
		scale := offSessionScale
		if isInSession(epochOffset, i) {
			scale = 1
		}
		sum += volatility * scale * zAt(seed, i)
	}

	return c.BasePrice.Mul(decimal.NewFromFloat(math.Exp(sum)))
}
