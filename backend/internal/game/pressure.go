package game

import (
	"math"
	"time"

	"github.com/shopspring/decimal"

	"fxgame/backend/internal/db"
)

// Pressure は docs/design.md §2.2 の需給圧力を、pressure_at からの経過時間分だけ
// 減衰させた「今の」値として返す。
//
//	圧力(now) = 圧力(pressure_at) × exp(-λ(now - pressure_at))
//
// BasePrice と違い、これは tick番号ではなく時刻に依存する状態の逐次更新であり
// 純粋関数ではない（design.md §3.1「シードは未来のため、price_ticksは過去のため」の
// どちらにも属さない、取引によって動く現在の状態）。
// 時刻は必ず引数で受ける（CLAUDE.md §5.1）。internal/game 配下で time.Now() を
// 直接呼んではいけない。
func Pressure(c db.Currency, now time.Time) decimal.Decimal {
	elapsed := now.Sub(c.PressureAt.Time)
	if elapsed <= 0 {
		// 時刻が巻き戻っている（クロックのずれ・古い pressure_at 等）場合は
		// 減衰させず現在値をそのまま返す。マイナス方向の経過時間で exp を計算すると
		// 圧力が増幅されてしまい安全側に倒れないため。
		return c.Pressure
	}

	decay := math.Exp(-c.Lambda.InexactFloat64() * elapsed.Minutes())
	return c.Pressure.Mul(decimal.NewFromFloat(decay))
}

// UpdatePressure は取引による需給圧力への影響を反映した新しい圧力を返す
// （docs/design.md §2.2）。
//
//	圧力(now) = 圧力(pressure_at) × exp(-λ(now-pressure_at)) + k × (signedVolume / liquidity)
//
// signedVolume は「買い注文なら正、売り注文なら負」の符号付き取引量として渡す
// （買い: 圧力 += k×(取引量/liquidity)、売り: 圧力 -= k×(取引量/liquidity)）。
//
// liquidity は通常 currency.Liquidity をそのまま渡す。liquidity_drain イベント中は
// 呼び出し側が currency.Liquidity に倍率（0.30。design.md §5.4）を掛けた値を渡すことで
// 差し替える。イベントテーブルの参照自体は #40 で実装するため、ここでは
// 「差し替え可能な引数にしておく」ところまでを担当する。
//
// この関数はDBを更新しない。呼び出し側（#36 の取引処理）が返り値を
// currencies.pressure / pressure_at(=now) として保存する。
func UpdatePressure(c db.Currency, now time.Time, signedVolume, liquidity decimal.Decimal) decimal.Decimal {
	decayed := Pressure(c, now)
	if liquidity.IsZero() {
		return decayed
	}
	impact := c.K.Mul(signedVolume).Div(liquidity)
	return decayed.Add(impact)
}

// CurrentPrice は表示価格を返す（docs/design.md §2.1 の2層構造）。
//
//	表示価格 = base(n) × (1 + 需給圧力)
//
// tickIndex は BasePrice（乱数由来の長期トレンド）用、now は Pressure（取引による
// 一時的な歪み）の減衰計算用。呼び出し側で両者の時刻を整合させること
// （通常は同じ time.Time から算出した tickIndex と now を渡す）。
func CurrentPrice(c db.Currency, tickIndex int64, now time.Time) decimal.Decimal {
	base := BasePrice(c, tickIndex)
	pressure := Pressure(c, now)
	return base.Mul(decimal.NewFromInt(1).Add(pressure))
}
