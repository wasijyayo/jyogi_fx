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
	callbackDeferredChannelMsg interactionCallbackType = 5 // 「考え中…」（3秒制限の回避用。#42では未使用。discord.goのhandleInteractionsコメント参照）
	callbackModal              interactionCallbackType = 9 // モーダルを開く（#42: /priceの買う/売るボタン→数量入力）
)

// Discordのボタンスタイル（#42）。
const (
	buttonStyleSecondary = 2 // 灰色。中立的な操作（キャンセル等）
	buttonStyleSuccess   = 3 // 緑。買い・確定などポジティブな操作
	buttonStyleDanger    = 4 // 赤。売り・決済などの破壊的操作
)

// Discordのテキスト入力スタイル（モーダル用。#42）。
const textInputStyleShort = 1 // 1行入力

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

// interactionCommandData はスラッシュコマンド・ボタン・モーダル送信のいずれでも
// Discordが送ってくる data。Interactionの種類によって埋まるフィールドが異なる
// （スラッシュコマンド: Name/Options。ボタン: CustomID。モーダル送信:
// CustomID/Components）。実際のコマンドごとの分岐・処理は commands.go（#41 / #42）
// で実装する。
type interactionCommandData struct {
	// Name は internal/discord.Commands の CommandXxx 定数と同じ値になる
	// （#29: コマンド定義は internal/discord に 1 箇所にまとめ、名前をここで対応づける）。
	Name    string                     `json:"name,omitempty"`
	Options []interactionCommandOption `json:"options,omitempty"`

	// CustomID はボタン押下・モーダル送信時にこちらが指定した識別子がそのまま
	// 返ってくる（#42）。「操作の種類:パラメータ」の形式で自前にエンコードする
	// （例 "close:123"・"order:long:JOG"）。commands.goのcustomID関連ヘルパー参照。
	CustomID string `json:"custom_id,omitempty"`

	// Components はモーダル送信時、各テキスト入力の値がAction Row経由で入れ子で
	// 返ってくる（#42）。interactionModalValue で custom_id をキーに引く。
	Components []interactionComponentRow `json:"components,omitempty"`
}

// interactionComponentRow はモーダル送信データの1 Action Row 分。
type interactionComponentRow struct {
	Components []interactionComponentValue `json:"components"`
}

// interactionComponentValue はモーダルのテキスト入力1つの送信値。
type interactionComponentValue struct {
	CustomID string `json:"custom_id"`
	Value    string `json:"value"`
}

// interactionModalValue はモーダル送信データから custom_id を指定して入力値を取り出す。
func interactionModalValue(data *interactionCommandData, customID string) (string, bool) {
	if data == nil {
		return "", false
	}
	for _, row := range data.Components {
		for _, v := range row.Components {
			if v.CustomID == customID {
				return v.Value, true
			}
		}
	}
	return "", false
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
	Content    string              `json:"content,omitempty"`
	Embeds     []discordEmbed      `json:"embeds,omitempty"`
	Components []discordActionRow  `json:"components,omitempty"`
	Flags      int                 `json:"flags,omitempty"`
}

// discordActionRow はボタンを並べる行（type:1。#42）。Discordの仕様上、
// ボタンは必ずAction Rowに入れ子にする必要があり、直接componentsには置けない。
type discordActionRow struct {
	Type       int             `json:"type"` // 1固定
	Components []discordButton `json:"components"`
}

// discordButton はDiscordのボタンコンポーネント（type:2。#42）。
type discordButton struct {
	Type     int    `json:"type"` // 2固定
	Style    int    `json:"style"`
	Label    string `json:"label"`
	CustomID string `json:"custom_id"`
}

// newActionRow は1つのボタンだけを持つAction Rowを作る簡易ヘルパー。
// #42で必要になるボタンは常に「1行1ボタン」（決済ボタン等）のため、
// 呼び出し側で毎回Action Rowの入れ子を書かなくて済むようにする。
func newActionRow(buttons ...discordButton) discordActionRow {
	return discordActionRow{Type: 1, Components: buttons}
}

// interactionModalResponse はモーダルを開く応答（callback type:9。#42）。
// interactionResponse（type:4等）とは data の形が異なるため別の型にする。
type interactionModalResponse struct {
	Type interactionCallbackType `json:"type"`
	Data *interactionModalData   `json:"data"`
}

type interactionModalData struct {
	CustomID   string                `json:"custom_id"`
	Title      string                `json:"title"`
	Components []discordTextInputRow `json:"components"`
}

type discordTextInputRow struct {
	Type       int                `json:"type"` // 1固定
	Components []discordTextInput `json:"components"`
}

// discordTextInput はモーダルのテキスト入力コンポーネント（type:4。#42）。
type discordTextInput struct {
	Type        int    `json:"type"` // 4固定
	CustomID    string `json:"custom_id"`
	Label       string `json:"label"`
	Style       int    `json:"style"`
	Required    bool   `json:"required"`
	Placeholder string `json:"placeholder,omitempty"`
}

func newTextInputRow(input discordTextInput) discordTextInputRow {
	return discordTextInputRow{Type: 1, Components: []discordTextInput{input}}
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
			// design.md §6.2「3秒以内に応答が必要」。#41/#42のコマンドはいずれも
			// 20人規模の集計・単発のDB更新で軽いため、deferred応答（type:5）は
			// 使わず直接返す（§6.2「20人規模の集計なら通常は直接返して問題ない」）。
			// deferred応答はCloud Run上で「初回応答を返した後、非同期で後続処理を
			// 続けてfollowup webhookを叩く」構成が必要になり、scale-to-zero
			// （CLAUDE.md冒頭）と相性が悪い（応答直後にインスタンスが縮退しうる）
			// ため、#42では採用を見送った。
			data := handleSlashCommand(r.Context(), cfg, req, cfg.Clock.Now())
			writeJSON(w, http.StatusOK, interactionResponse{
				Type: callbackChannelMessage,
				Data: &data,
			})
		case interactionMessageComponent:
			// ボタン押下（#42: /priceの買う/売る、/positionsの決済・確認）。
			// custom_idのプレフィックスに応じて通常メッセージかモーダルかが変わるため
			// handleMessageComponentがレスポンス全体（any）を組み立てる。
			writeJSON(w, http.StatusOK, handleMessageComponent(r.Context(), cfg, req, cfg.Clock.Now()))
		case interactionModalSubmit:
			// モーダル送信（#42: /priceのボタン→開いた数量入力モーダルの送信）。
			data := handleModalSubmit(r.Context(), cfg, req, cfg.Clock.Now())
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
