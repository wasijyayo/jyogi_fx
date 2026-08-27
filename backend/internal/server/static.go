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
	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	}), nil
}
