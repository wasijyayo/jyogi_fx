package discord

import "strings"

// ボタン・Action Row の型は Discord Interactions API のメッセージ内コンポーネントの形。
// internal/server（スラッシュコマンドの応答）と internal/game（市場ティッカーへの
// 買う/売るボタン常設。design.md §6.11の見直し）の両方から使うため、
// CLAUDE.md §3の層構造（server → game → db）を壊さないよう、ここ internal/discord に
// 置く（discord.MessagesConfig/discord.Command と同じ「共有Discord API型」の扱い）。

// ボタンスタイル（Discordの仕様上の値）。
const (
	ButtonStyleSecondary = 2 // 灰色。中立的な操作（キャンセル等）
	ButtonStyleSuccess   = 3 // 緑。買い・確定などポジティブな操作
	ButtonStyleDanger    = 4 // 赤。売り・決済などの破壊的操作
)

// ActionRow はボタンを並べる行（type:1）。Discordの仕様上、ボタンは必ず
// Action Rowに入れ子にする必要があり、直接componentsには置けない。
type ActionRow struct {
	Type       int      `json:"type"` // 1固定
	Components []Button `json:"components"`
}

// Button はDiscordのボタンコンポーネント（type:2）。
type Button struct {
	Type     int    `json:"type"` // 2固定
	Style    int    `json:"style"`
	Label    string `json:"label"`
	CustomID string `json:"custom_id"`
}

// NewActionRow はボタンをまとめて1つのAction Rowにする簡易ヘルパー。
func NewActionRow(buttons ...Button) ActionRow {
	return ActionRow{Type: 1, Components: buttons}
}

// CustomIDOrderButton は「買う/売る」ボタンのcustom_idプレフィックス
// （"order:<long|short>:<symbol>"）。internal/serverのhandleOrderButtonが
// このプレフィックスで処理を振り分け、押した人自身の注文フローに入る
// （/priceの応答・市場ティッカーの常設ボタンの両方で共通して使う）。
const CustomIDOrderButton = "order"

// EncodeCustomID はボタン・モーダルのcustom_idのエンコード規約
// （"プレフィックス:パラメータ..."）を組み立てる。
func EncodeCustomID(parts ...string) string {
	return strings.Join(parts, ":")
}
