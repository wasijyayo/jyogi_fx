package server

import (
	"crypto/subtle"
	"log"
	"net/http"
	"strings"
)

// tickAuthPrefix は共有シークレットを渡す際のヘッダ形式（`Authorization: Bearer <secret>`）。
// Cloud Scheduler の HTTP ターゲットでヘッダを1つ追加するだけで設定できる。
const tickAuthPrefix = "Bearer "

func registerTickRoutes(mux *http.ServeMux, cfg Config) {
	mux.HandleFunc("POST /internal/tick", handleTick(cfg))
	// POST 以外は 405 を返す。登録しないと GET が SPA のキャッチオール（"/"）に
	// 落ちて index.html を 200 で返してしまう（discord.go の登録と同じ理由）。
	mux.HandleFunc("/internal/tick", handleTickMethodNotAllowed)
}

func handleTickMethodNotAllowed(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Allow", http.MethodPost)
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

// handleTick は Cloud Scheduler から毎分叩かれる想定のエンドポイント（design.md §4）。
//
// **このURLはインターネットに公開される。** 外部から叩かれるとイベントが暴発するため
// （design.md #35「エンドポイントの保護は必須」）、共有シークレットで保護する。
// cfg.TickSharedSecret は main.go の起動時に必須チェック済みの前提（未設定なら
// 起動自体が失敗する。ParseDiscordPublicKey と同じフェイルファストの方針）。
func handleTick(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !validTickSecret(cfg.TickSharedSecret, r.Header.Get("Authorization")) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		if err := cfg.Tick.Tick(r.Context(), cfg.Clock.Now()); err != nil {
			log.Printf("tick failed: %v", err)
			http.Error(w, "tick failed", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}
}

// validTickSecret は Authorization ヘッダの `Bearer <secret>` を定数時間で比較する。
// タイミング攻撃対策に subtle.ConstantTimeCompare を使う（discord.go の署名検証と同じ発想）。
func validTickSecret(secret, authHeader string) bool {
	token, ok := strings.CutPrefix(authHeader, tickAuthPrefix)
	if !ok || token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(secret)) == 1
}
