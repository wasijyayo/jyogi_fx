package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

// TestIsReservedPath はサーバー側の予約パス（/api /auth /internal）と SPA の画面ルートを
// 正しく区別できることを確認する。
func TestIsReservedPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/api/unknown", true},
		{"/api/me", true},
		{"/auth/unknown", true},
		{"/auth/logout", true},
		{"/internal/unknown", true},
		{"/currencies/JOG", false},
		{"/", false},
		{"/assets/index.js", false},
	}
	for _, tt := range tests {
		if got := isReservedPath(tt.path); got != tt.want {
			t.Errorf("isReservedPath(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

// TestNewStaticHandler_SPAFallback は /currencies/JOG のような画面ルートの直接アクセスで
// index.html を返し、存在しない /api/* や /auth/* は 404 を返すことを確認する（#99）。
func TestNewStaticHandler_SPAFallback(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html":       &fstest.MapFile{Data: []byte("<html>spa</html>")},
		"assets/index.js":  &fstest.MapFile{Data: []byte("console.log(1)")},
	}
	h := newStaticHandler(fsys)

	t.Run("画面ルートはindex.htmlを返す", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/currencies/JOG", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if ct := rec.Header().Get("Content-Type"); ct == "" || ct[:9] != "text/html" {
			t.Errorf("Content-Type = %q, want text/html", ct)
		}
		body, _ := io.ReadAll(rec.Result().Body)
		if string(body) != "<html>spa</html>" {
			t.Errorf("body = %q, want index.html の中身", string(body))
		}
	})

	t.Run("存在する静的ファイルはそのまま返す", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/assets/index.js", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
	})

	t.Run("存在しない/apiは404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/nonexistent", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
		}
	})

	t.Run("存在しない/authは404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/auth/nonexistent", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
		}
	})
}
