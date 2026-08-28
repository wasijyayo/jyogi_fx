package server_test

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"fxgame/backend/internal/server"
)

// fixedClock は署名タイムスタンプの鮮度検査をテストするための固定クロック
// （CLAUDE.md §5.1: 時刻は必ず注入する）。
type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

// テスト内の「現在時刻」と、それに対応する正当な署名タイムスタンプ。
var (
	testNow       = time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	testTimestamp = strconv.FormatInt(testNow.Unix(), 10)
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

	mux := server.NewMux(server.Config{
		DiscordPublicKey: pub,
		Clock:            fixedClock{now: testNow},
	})

	t.Run("正しい署名のPINGにPONGを返す", func(t *testing.T) {
		req := newSignedRequest(t, priv, testTimestamp, `{"type":1}`)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
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

	// PONG は PING 専用の callback type。スラッシュコマンド（type:2）に PONG を返すと
	// Discord に拒否され「インタラクションに失敗しました」と表示されるため、
	// 未実装でも type:4（メッセージ応答）を返さなければならない。
	t.Run("PING以外にはPONGではなくメッセージ応答を返す", func(t *testing.T) {
		for _, interactionType := range []int{2, 3, 5} {
			body := `{"type":` + strconv.Itoa(interactionType) + `}`
			req := newSignedRequest(t, priv, testTimestamp, body)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("type %d: status = %d, want %d", interactionType, rec.Code, http.StatusOK)
			}

			var got struct {
				Type int `json:"type"`
				Data struct {
					Content string `json:"content"`
					Flags   int    `json:"flags"`
				} `json:"data"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("type %d: decode response: %v", interactionType, err)
			}
			if got.Type == 1 {
				t.Errorf("type %d: PONG(1) を返している。Discord に拒否される", interactionType)
			}
			if got.Type != 4 {
				t.Errorf("type %d: callback type = %d, want 4 (CHANNEL_MESSAGE_WITH_SOURCE)", interactionType, got.Type)
			}
			if got.Data.Content == "" {
				t.Errorf("type %d: content が空", interactionType)
			}
			if got.Data.Flags != 1<<6 {
				t.Errorf("type %d: flags = %d, want %d (ephemeral)", interactionType, got.Data.Flags, 1<<6)
			}
		}
	})

	// #29: スラッシュコマンド名は internal/discord.Commands の定義と対応づけられるよう、
	// data.name をログに残す（実際の実行分岐は #41 / #42）。
	t.Run("スラッシュコマンドのdata.nameをログに残す", func(t *testing.T) {
		var logBuf bytes.Buffer
		log.SetOutput(&logBuf)
		t.Cleanup(func() { log.SetOutput(os.Stderr) })

		body := `{"type":2,"data":{"name":"balance"}}`
		req := newSignedRequest(t, priv, testTimestamp, body)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if !strings.Contains(logBuf.String(), `command="balance"`) {
			t.Errorf("log = %q, want it to contain command=%q", logBuf.String(), "balance")
		}
	})

	// Discord は Bot 登録時にわざと不正な署名を送ってきて 401 が返ることを確認する。
	// ここが通らないと Interactions Endpoint URL の登録自体が失敗する。
	t.Run("署名が他人の鍵で作られていたら401", func(t *testing.T) {
		_, otherPriv, err := ed25519.GenerateKey(nil)
		if err != nil {
			t.Fatalf("GenerateKey: %v", err)
		}

		req := newSignedRequest(t, otherPriv, testTimestamp, `{"type":1}`)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
	})

	t.Run("ボディが改ざんされていたら401", func(t *testing.T) {
		// 署名は元のボディに対して作り、実際に送るボディだけ差し替える
		// （中間者がボディを書き換えた状況を模す）
		req := newSignedRequest(t, priv, testTimestamp, `{"type":1}`)
		req.Body = io.NopCloser(strings.NewReader(`{"type":2}`))

		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
	})

	t.Run("タイムスタンプが差し替えられていたら401", func(t *testing.T) {
		req := newSignedRequest(t, priv, testTimestamp, `{"type":1}`)
		// 署名対象に含まれるタイムスタンプだけを別の値に変える
		req.Header.Set("X-Signature-Timestamp", strconv.FormatInt(testNow.Add(time.Minute).Unix(), 10))

		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
	})

	// 署名自体は正当だが古いリクエスト。鮮度検査がないと何度でも再送できてしまう
	// （#42 で決済コマンドが入ると、リプレイが取引を再実行しうる）。
	t.Run("署名は正当でも古いリクエストは401", func(t *testing.T) {
		oldTimestamp := strconv.FormatInt(testNow.Add(-10*time.Minute).Unix(), 10)
		req := newSignedRequest(t, priv, oldTimestamp, `{"type":1}`)

		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d（リプレイ攻撃を防げていない）", rec.Code, http.StatusUnauthorized)
		}
	})

	t.Run("未来すぎるタイムスタンプも401", func(t *testing.T) {
		future := strconv.FormatInt(testNow.Add(10*time.Minute).Unix(), 10)
		req := newSignedRequest(t, priv, future, `{"type":1}`)

		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
	})

	t.Run("許容範囲内のずれは通る", func(t *testing.T) {
		recent := strconv.FormatInt(testNow.Add(-1*time.Minute).Unix(), 10)
		req := newSignedRequest(t, priv, recent, `{"type":1}`)

		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
	})

	t.Run("タイムスタンプが数値でなければ401", func(t *testing.T) {
		req := newSignedRequest(t, priv, "not-a-number", `{"type":1}`)

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
		req.Header.Set("X-Signature-Timestamp", testTimestamp)

		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
	})

	t.Run("署名は正当でもJSONが壊れていたら400", func(t *testing.T) {
		req := newSignedRequest(t, priv, testTimestamp, `{"type":`)

		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
		}
	})

	// ボディ過大は「切り詰めて署名不一致（401）」ではなく 413 として区別できること。
	t.Run("ボディが大きすぎたら413", func(t *testing.T) {
		huge := `{"type":1,"pad":"` + strings.Repeat("a", 512<<10) + `"}`
		req := newSignedRequest(t, priv, testTimestamp, huge)

		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
		}
	})

	// GET を登録しないと SPA のキャッチオール（"/"）に落ちて 200 を返してしまう。
	t.Run("GETは405", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/interactions", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
		}
		if allow := rec.Header().Get("Allow"); allow != http.MethodPost {
			t.Errorf("Allow = %q, want POST", allow)
		}
	})
}

// 公開鍵未設定は「設定ミス」であって「署名の偽造」ではないので、
// 401 ではなく 500 を返す（401 だと偽造リクエストと区別がつかず本番で気づけない）。
// また 32 バイトでない鍵を ed25519.Verify に渡すと panic するため、そこも防げていること。
func TestInteractions_公開鍵未設定なら500(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	mux := server.NewMux(server.Config{Clock: fixedClock{now: testNow}})

	req := newSignedRequest(t, priv, testTimestamp, `{"type":1}`)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
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

	// 未設定（空文字）も起動時に弾けること。
	t.Run("空文字", func(t *testing.T) {
		if _, err := server.ParseDiscordPublicKey(""); err == nil {
			t.Error("want error, got nil")
		}
	})
}
