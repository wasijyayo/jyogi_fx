package server_test

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"fxgame/backend/internal/server"
)

// newSignedRequest は Discord が送ってくるのと同じ形のリクエストを組み立てる。
// Discord は「タイムスタンプ + ボディ」に対して秘密鍵で署名する。
func newSignedRequest(t *testing.T, priv ed25519.PrivateKey, timestamp, body string) *http.Request {
	t.Helper()

	sig := ed25519.Sign(priv, []byte(timestamp+body))

	req := httptest.NewRequest(http.MethodPost, "/interactions", strings.NewReader(body))
	req.Header.Set("X-Signature-Ed25519", hex.EncodeToString(sig))
	req.Header.Set("X-Signature-Timestamp", timestamp)
	return req
}

func TestInteractions(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	mux := server.NewMux(server.Config{DiscordPublicKey: pub})

	t.Run("正しい署名のPINGにPONGを返す", func(t *testing.T) {
		req := newSignedRequest(t, priv, "1735689600", `{"type":1}`)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
		}

		var got struct {
			Type int `json:"type"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if got.Type != 1 {
			t.Errorf("type = %d, want 1 (PONG)", got.Type)
		}
	})

	// Discord は Bot 登録時にわざと不正な署名を送ってきて 401 が返ることを確認する。
	// ここが通らないと Interactions Endpoint URL の登録自体が失敗する。
	t.Run("署名が他人の鍵で作られていたら401", func(t *testing.T) {
		_, otherPriv, err := ed25519.GenerateKey(nil)
		if err != nil {
			t.Fatalf("GenerateKey: %v", err)
		}

		req := newSignedRequest(t, otherPriv, "1735689600", `{"type":1}`)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
	})

	t.Run("ボディが改ざんされていたら401", func(t *testing.T) {
		// 署名は元のボディに対して作り、実際に送るボディだけ差し替える
		// （中間者がボディを書き換えた状況を模す）
		req := newSignedRequest(t, priv, "1735689600", `{"type":1}`)
		req.Body = io.NopCloser(strings.NewReader(`{"type":2}`))

		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
	})

	t.Run("タイムスタンプが差し替えられていたら401", func(t *testing.T) {
		req := newSignedRequest(t, priv, "1735689600", `{"type":1}`)
		// 署名対象に含まれるタイムスタンプだけを別の値に変える（リプレイ攻撃を模す）
		req.Header.Set("X-Signature-Timestamp", "1799999999")

		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
	})

	t.Run("署名ヘッダが無ければ401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/interactions", strings.NewReader(`{"type":1}`))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
	})

	t.Run("署名が16進数として不正なら401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/interactions", strings.NewReader(`{"type":1}`))
		req.Header.Set("X-Signature-Ed25519", "not-hex")
		req.Header.Set("X-Signature-Timestamp", "1735689600")

		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
	})
}

// 公開鍵が未設定でも panic せず 401 を返すこと。
// ed25519.Verify は長さが 32 バイトでない公開鍵を渡すと panic するため、
// 事前に弾けているかを確認する。
func TestInteractions_公開鍵未設定でもpanicしない(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	mux := server.NewMux(server.Config{}) // DiscordPublicKey を設定しない

	req := newSignedRequest(t, priv, "1735689600", `{"type":1}`)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestParseDiscordPublicKey(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	t.Run("正しい16進文字列", func(t *testing.T) {
		got, err := server.ParseDiscordPublicKey(hex.EncodeToString(pub))
		if err != nil {
			t.Fatalf("ParseDiscordPublicKey: %v", err)
		}
		if !got.Equal(pub) {
			t.Error("parsed key does not match original")
		}
	})

	t.Run("16進数として不正", func(t *testing.T) {
		if _, err := server.ParseDiscordPublicKey("zzzz"); err == nil {
			t.Error("want error, got nil")
		}
	})

	t.Run("長さが足りない", func(t *testing.T) {
		if _, err := server.ParseDiscordPublicKey("abcd"); err == nil {
			t.Error("want error, got nil")
		}
	})
}
