// Package server はこのアプリの HTTP ハンドラ層（① 入口 → ② サービス層を呼ぶだけの層）。
// ロジックを書かず、internal/game のサービス層を呼んで出力の形を変えるだけにする（CLAUDE.md §3）。
package server

import (
	"crypto/ed25519"
	"log"
	"net/http"

	"fxgame/backend/internal/game"
)

// Config は NewMux が必要とする依存をまとめたもの。
type Config struct {
	Auth    *game.AuthService
	Tick    *game.TickService
	Ranking *game.RankingService
	Profile *game.ProfileService
	Quote   *game.QuoteService
	Trade   *game.TradeService
	Claim   *game.ClaimService

	// SecureCookies が true のとき Cookie に Secure 属性を付ける。
	// Cloud Run（本番）では true、ローカル開発（http://localhost）では false にする。
	SecureCookies bool

	// DiscordPublicKey は /interactions の Ed25519 署名検証に使う公開鍵。
	// 未設定の場合、/interactions は 500 を返す（設定ミスと署名偽造を区別するため）。
	DiscordPublicKey ed25519.PublicKey

	// TickSharedSecret は /internal/tick の保護に使う共有シークレット（design.md §4）。
	// 外部から叩かれるとイベントが暴発するため必須。呼び出し側は
	// `Authorization: Bearer <secret>` ヘッダを付ける。
	TickSharedSecret string

	// Clock は署名タイムスタンプの鮮度検査・tick処理の現在時刻に使う
	// （CLAUDE.md §5.1: 時刻は必ず注入する）。未設定なら RealClock を使う。
	Clock game.Clock
}

// NewMux は HTTP ルーティングを組み立てる。
func NewMux(cfg Config) *http.ServeMux {
	if cfg.Clock == nil {
		cfg.Clock = game.RealClock{}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", handleHealth)
	registerAuthRoutes(mux, cfg)
	registerAPIRoutes(mux, cfg)
	registerDiscordRoutes(mux, cfg)
	registerTickRoutes(mux, cfg)

	static, err := NewStaticHandler()
	if err != nil {
		// フロントエンド未ビルドでも /health 等は動かしたいので、ここでは落とさない。
		log.Printf("static handler unavailable: %v", err)
	} else {
		mux.Handle("/", static)
	}

	return mux
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}
