package server

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"
)

// interactionType は Discord が送ってくる Interaction の種別。
// interactionCallbackType（こちらが返す応答の種別）とは**別の enum** なので、
// 型を分けて取り違えをコンパイラに検出させる。
// 値の意味は Discord 側が定義したもの（docs/design.md §6.2 の HTTP Interactions 方式）。
type interactionType int

const (
	interactionPing               interactionType = 1 // Discord からの疎通確認
	interactionApplicationCommand interactionType = 2 // スラッシュコマンド（#29 以降）
	interactionMessageComponent   interactionType = 3 // ボタン・セレクトメニュー（#42）
	interactionAutocomplete       interactionType = 4
	interactionModalSubmit        interactionType = 5 // モーダル送信（#42）
)

// interactionCallbackType はこちらが返す応答の種別。
// **interactionType とは別の enum。** 特に callbackPong は PING への応答にしか使えず、
// スラッシュコマンド等に返すと Discord に拒否され「インタラクションに失敗しました」になる。
type interactionCallbackType int

const (
	callbackPong               interactionCallbackType = 1 // PING への応答専用
	callbackChannelMessage     interactionCallbackType = 4 // メッセージを返す
	callbackDeferredChannelMsg interactionCallbackType = 5 // 「考え中…」（3秒制限の回避用・#42）
)

// messageFlagEphemeral は「本人にだけ見えるメッセージ」を表す Discord のフラグ。
const messageFlagEphemeral = 1 << 6

// maxInteractionBody は読み込むボディの上限。
// 署名検証のためにボディ全体をメモリに載せる必要があるので上限を設ける。
// Discord の Interaction ペイロードは大きくても数十KB程度。
const maxInteractionBody = 256 << 10 // 256KiB

// interactionMaxSkew は署名タイムスタンプの許容ずれ幅。
// これを超えて古い（または未来の）リクエストはリプレイとみなして拒否する。
const interactionMaxSkew = 5 * time.Minute

type interactionRequest struct {
	Type interactionType         `json:"type"`
	Data *interactionCommandData `json:"data,omitempty"`
	// Member はギルド（サーバー）内で実行された場合に入る。DMの場合は代わりに
	// User が入る（Discordの仕様。design.md §6.1）。呼び出しユーザーのID解決は
	// interactionUserID を使うこと（両方を直接見ない）。
	Member *interactionMember `json:"member,omitempty"`
	User   *interactionUser   `json:"user,omitempty"`
}

// interactionMember はギルド内実行時にDiscordが送ってくるメンバー情報のうち
// ユーザーID解決に必要な部分だけを持つ。
type interactionMember struct {
	User *interactionUser `json:"user,omitempty"`
}

type interactionUser struct {
	ID string `json:"id"`
}

// interactionCommandData はスラッシュコマンド実行時に Discord が送ってくる data。
// Name は internal/discord.Commands の CommandXxx 定数と同じ値になる
// （#29: コマンド定義は internal/discord に 1 箇所にまとめ、名前をここで対応づける）。
// 実際のコマンドごとの分岐・処理は commands.go（#41 / #42）で実装する。
type interactionCommandData struct {
	Name    string                     `json:"name"`
	Options []interactionCommandOption `json:"options,omitempty"`
}

// interactionCommandOption はスラッシュコマンドの引数1つ分。
// Value は文字列として受ける（USER型オプションの値はDiscordのスノーフレークIDが
// 文字列で入る。STRING型もそのまま文字列なので、MVPコマンドの範囲では型を
// 分ける必要が無い）。
type interactionCommandOption struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type interactionResponse struct {
	Type interactionCallbackType  `json:"type"`
	Data *interactionResponseData `json:"data,omitempty"`
}

type interactionResponseData struct {
	Content string         `json:"content,omitempty"`
	Embeds  []discordEmbed `json:"embeds,omitempty"`
	Flags   int            `json:"flags,omitempty"`
}

// discordEmbed はDiscordのEmbedオブジェクトのうち、MVPコマンド（#41/#42）で
// 使うフィールドだけを持つ。
type discordEmbed struct {
	Title       string              `json:"title,omitempty"`
	Description string              `json:"description,omitempty"`
	Color       int                 `json:"color,omitempty"`
	Fields      []discordEmbedField `json:"fields,omitempty"`
}

type discordEmbedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

// interactionUserID は呼び出しユーザーのDiscordユーザーIDを解決する
// （design.md §6.1「Discordのペイロードから取得できるユーザーIDをそのまま
// users.discord_idとして使う」）。ギルド内実行ならmember.user.id、DMならuser.id。
func interactionUserID(req interactionRequest) (string, bool) {
	if req.Member != nil && req.Member.User != nil && req.Member.User.ID != "" {
		return req.Member.User.ID, true
	}
	if req.User != nil && req.User.ID != "" {
		return req.User.ID, true
	}
	return "", false
}

func registerDiscordRoutes(mux *http.ServeMux, cfg Config) {
	mux.HandleFunc("POST /interactions", handleInteractions(cfg))
	// POST 以外は 405 を返す。これを登録しないと GET が SPA のキャッチオール（"/"）に
	// 落ちて index.html を 200 で返してしまう。
	mux.HandleFunc("/interactions", handleInteractionsMethodNotAllowed)
}

func handleInteractionsMethodNotAllowed(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Allow", http.MethodPost)
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

// handleInteractions は Discord からの Interaction を受ける唯一の入口。
//
// このURLはインターネットに公開されるため、Ed25519 署名検証で
// 「本当に Discord が送ったリクエストか」を必ず確認する（design.md §6.2）。
// 検証を通らないものは 401 を返す。Discord は Bot 登録時に
// わざと不正な署名を送って 401 が返ることを確認するため、ここは必須。
func handleInteractions(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 公開鍵の設定漏れは「設定ミス」であって「署名の偽造」ではない。
		// 401 で返すと偽造リクエストと区別がつかず本番で気づけないため、500 にする。
		if len(cfg.DiscordPublicKey) != ed25519.PublicKeySize {
			http.Error(w, "discord public key is not configured", http.StatusInternalServerError)
			return
		}

		// ヘッダの検査は読み込み前に済ませる（不正なリクエストにメモリを使わないため）。
		signature, timestamp, ok := discordSignatureHeaders(r)
		if !ok {
			http.Error(w, "invalid request signature", http.StatusUnauthorized)
			return
		}

		// 署名対象にタイムスタンプが含まれていても、鮮度を見なければリプレイは防げない。
		// 一度盗まれた正当なリクエストが何度でも通ってしまうため、ここで期限を切る。
		if !withinSkew(timestamp, cfg.Clock.Now(), interactionMaxSkew) {
			http.Error(w, "invalid request signature", http.StatusUnauthorized)
			return
		}

		// ボディは一度読むと空になるため、先に全部読んでから
		// 署名検証とJSONパースの両方でこの body を使い回す。
		// MaxBytesReader は上限超過を「切り詰め」ではなくエラーとして返すので、
		// 署名不一致（401）と過大なボディ（413）を取り違えずに済む。
		r.Body = http.MaxBytesReader(w, r.Body, maxInteractionBody)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "failed to read body", http.StatusBadRequest)
			return
		}

		if !verifyDiscordSignature(cfg.DiscordPublicKey, signature, timestamp, body) {
			http.Error(w, "invalid request signature", http.StatusUnauthorized)
			return
		}

		var req interactionRequest
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}

		switch req.Type {
		case interactionPing:
			writeJSON(w, http.StatusOK, interactionResponse{Type: callbackPong})
		case interactionApplicationCommand:
			// design.md §6.2「3秒以内に応答が必要」。/balance /rank /today /profile は
			// 20人規模の集計で軽いため、deferred応答（type:5）は使わず直接返す
			// （§6.2「20人規模の集計なら通常は直接返して問題ない」）。
			data := handleSlashCommand(r.Context(), cfg, req, cfg.Clock.Now())
			writeJSON(w, http.StatusOK, interactionResponse{
				Type: callbackChannelMessage,
				Data: &data,
			})
		default:
			// ボタン・モーダル（#42）等、まだ実装していないInteraction種別。
			// **ここで callbackPong を返してはいけない。** PONG は PING 専用の
			// callback type なので、Discord に拒否され利用者には
			// 「インタラクションに失敗しました」と表示される。
			// 実装が済むまでは本人にだけ見えるメッセージで未実装を伝える。
			commandName := ""
			if req.Data != nil {
				commandName = req.Data.Name
			}
			log.Printf("discord interaction: unhandled type %d (command=%q)", req.Type, commandName)
			writeJSON(w, http.StatusOK, interactionResponse{
				Type: callbackChannelMessage,
				Data: &interactionResponseData{
					Content: "このコマンドはまだ実装されていません。",
					Flags:   messageFlagEphemeral,
				},
			})
		}
	}
}

// discordSignatureHeaders は署名関連ヘッダを取り出して16進デコードする。
// 欠如・不正な形式はここで弾く（ボディを読む前に済ませたい安価な検査）。
func discordSignatureHeaders(r *http.Request) (signature []byte, timestamp string, ok bool) {
	signatureHex := r.Header.Get("X-Signature-Ed25519")
	timestamp = r.Header.Get("X-Signature-Timestamp")
	if signatureHex == "" || timestamp == "" {
		return nil, "", false
	}

	signature, err := hex.DecodeString(signatureHex)
	if err != nil {
		return nil, "", false
	}
	return signature, timestamp, true
}

// withinSkew はタイムスタンプ（Unix秒）が now から skew 以内かを判定する。
// 過去方向だけでなく未来方向も見る（時計のずれや細工されたタイムスタンプへの備え）。
func withinSkew(timestamp string, now time.Time, skew time.Duration) bool {
	sec, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false
	}

	diff := now.Sub(time.Unix(sec, 0))
	if diff < 0 {
		diff = -diff
	}
	return diff <= skew
}

// verifyDiscordSignature は Discord の Ed25519 署名を検証する。
//
// Discord は「タイムスタンプ + ボディ」を連結した文字列に対して
// 秘密鍵で署名しているので、こちらは同じものを組み立てて公開鍵で検証する。
// タイムスタンプを署名対象に含めることでタイムスタンプ自体の改ざんを防ぎ、
// 併せて呼び出し側で withinSkew による鮮度検査を行うことでリプレイ攻撃を防ぐ。
//
// publicKey の長さは呼び出し前に検査済みであること
// （32バイトでない鍵を ed25519.Verify に渡すと panic する）。
func verifyDiscordSignature(publicKey ed25519.PublicKey, signature []byte, timestamp string, body []byte) bool {
	signed := make([]byte, 0, len(timestamp)+len(body))
	signed = append(signed, timestamp...)
	signed = append(signed, body...)

	return ed25519.Verify(publicKey, signed, signature)
}

// ParseDiscordPublicKey は環境変数の16進文字列を ed25519.PublicKey に変換する。
// 設定ミスはリクエストのたびに panic する原因になるため、起動時にここで弾く。
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
