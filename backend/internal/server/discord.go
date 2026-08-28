package server

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
)

// Discord Interaction の type（Discord 側が定義した値）。
// docs/design.md §6.2 のとおり Gateway ではなく HTTP Interactions 方式を使う。
const (
	interactionTypePing = 1 // Discord からの疎通確認
	interactionTypePong = 1 // PING に対する応答（値は同じ 1）
)

// maxInteractionBody は読み込むボディの上限。
// 署名検証のためにボディ全体をメモリに載せるので、念のため上限を設ける。
const maxInteractionBody = 1 << 20 // 1MiB

type interactionRequest struct {
	Type int `json:"type"`
}

type interactionResponse struct {
	Type int `json:"type"`
}

func registerDiscordRoutes(mux *http.ServeMux, cfg Config) {
	mux.HandleFunc("POST /interactions", handleInteractions(cfg))
}

// handleInteractions は Discord からの Interaction を受ける唯一の入口。
//
// このURLはインターネットに公開されるため、Ed25519 署名検証で
// 「本当に Discord が送ったリクエストか」を必ず確認する（design.md §6.2）。
// 検証を通らないものは 401 を返す。Discord は Bot 登録時に
// わざと不正な署名を送って 401 が返ることを確認するため、ここは必須。
func handleInteractions(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// ボディは一度読むと空になるため、先に全部読んでから
		// 署名検証とJSONパースの両方でこの body を使い回す。
		body, err := io.ReadAll(io.LimitReader(r.Body, maxInteractionBody))
		if err != nil {
			http.Error(w, "failed to read body", http.StatusBadRequest)
			return
		}

		if !verifyDiscordSignature(cfg.DiscordPublicKey, r, body) {
			http.Error(w, "invalid request signature", http.StatusUnauthorized)
			return
		}

		var req interactionRequest
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}

		switch req.Type {
		case interactionTypePing:
			writeJSON(w, http.StatusOK, interactionResponse{Type: interactionTypePong})
		default:
			// スラッシュコマンド等は後続 issue（#29, #41, #42）で実装する。
			// 未知の type でも Discord にエラーを返さないよう 200 で受け流す。
			log.Printf("discord interaction: unhandled type %d", req.Type)
			writeJSON(w, http.StatusOK, interactionResponse{Type: interactionTypePong})
		}
	}
}

// verifyDiscordSignature は Discord の Ed25519 署名を検証する。
//
// Discord は「タイムスタンプ + ボディ」を連結した文字列に対して
// 秘密鍵で署名しているので、こちらは同じものを組み立てて公開鍵で検証する。
// タイムスタンプを署名対象に含めるのは、過去のリクエストを盗んで
// 再送するリプレイ攻撃を防ぐため。
func verifyDiscordSignature(publicKey ed25519.PublicKey, r *http.Request, body []byte) bool {
	// 公開鍵が未設定・長さ不正のまま ed25519.Verify を呼ぶと panic するため、
	// ここで弾く（起動時にも checkDiscordPublicKey で確認している）。
	if len(publicKey) != ed25519.PublicKeySize {
		log.Printf("discord signature: public key is not configured correctly (len=%d)", len(publicKey))
		return false
	}

	signatureHex := r.Header.Get("X-Signature-Ed25519")
	timestamp := r.Header.Get("X-Signature-Timestamp")
	if signatureHex == "" || timestamp == "" {
		return false
	}

	signature, err := hex.DecodeString(signatureHex)
	if err != nil {
		return false
	}

	signed := make([]byte, 0, len(timestamp)+len(body))
	signed = append(signed, timestamp...)
	signed = append(signed, body...)

	return ed25519.Verify(publicKey, signed, signature)
}

// ParseDiscordPublicKey は環境変数の16進文字列を ed25519.PublicKey に変換する。
// 設定ミス（空・長さ不正）はリクエストのたびに panic する原因になるため、
// 起動時にここで弾いて気づけるようにする。
func ParseDiscordPublicKey(hexKey string) (ed25519.PublicKey, error) {
	raw, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("decode discord public key: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("discord public key must be %d bytes, got %d",
			ed25519.PublicKeySize, len(raw))
	}
	return ed25519.PublicKey(raw), nil
}
