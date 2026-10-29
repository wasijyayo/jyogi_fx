package server

import (
	"io/fs"
	"net/http"
	"strings"

	backendassets "fxgame/backend"
)

// NewStaticHandler は Vite のビルド成果物（backend/web/、backend.WebFS に embed 済み）を
// SPA として配信するハンドラを作る。
// 存在しないパスは index.html にフォールバックする（クライアントサイドルーティング対応）。
func NewStaticHandler() (http.Handler, error) {
	sub, err := fs.Sub(backendassets.WebFS, "web")
	if err != nil {
		return nil, err
	}
	return newStaticHandler(sub), nil
}

// newStaticHandler は SPA 配信ハンドラの本体。sub を注入できるようにして
// テストから差し替えられるようにしている（本番は NewStaticHandler が WebFS を渡す）。
func newStaticHandler(sub fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// API・認証・内部エンドポイントの予約済みプレフィックスに一致するパスが
		// ここまで到達した場合、それは未定義のパスなので index.html を返さず 404 にする
		// （#99: 存在しない /api/* や /auth/* で SPA の index.html を返さない）。
		if isReservedPath(r.URL.Path) {
			http.NotFound(w, r)
			return
		}

		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "."
		}
		if _, err := fs.Stat(sub, p); err != nil {
			// 静的ファイルが無いパス（例: SPA側のクライアントサイドルート）は
			// index.html を返し、あとはReact Router等に任せる。
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			fileServer.ServeHTTP(w, r2)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

// isReservedPath はサーバー側のエンドポイント用に予約されたパスかを返す。
// これらは SPA の画面ルート（/currencies/JOG 等）と重ならないため、
// 未定義なら index.html を返すのではなく 404 にすべき。
func isReservedPath(path string) bool {
	return strings.HasPrefix(path, "/api/") ||
		strings.HasPrefix(path, "/auth/") ||
		strings.HasPrefix(path, "/internal/")
}
