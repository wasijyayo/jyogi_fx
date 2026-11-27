package server

import (
	"strings"

	"fxgame/backend/internal/discord"
)

// handleHelpCommand は /help（遊び方の案内。ユーザーからの追加要望）。
//
// 固定の案内文を返すだけで、ゲームのルールを判定するロジックは持たない
// （CLAUDE.md §3「server 層にロジックを書かない」）。通貨名は currencies テーブルの
// 行で増減するため、案内文に具体的な通貨コードは書かない（CLAUDE.md §5.3）。
// 配布額などデプロイなしで変わる値（環境変数で調整できるもの）も、
// 文面と実際の値がずれないよう数字では書かない。
//
// 案内は本人が読むためのものなので、チャンネルを流さないよう ephemeral にする。
func handleHelpCommand(cfg Config) interactionResponseData {
	resp := interactionResponseData{
		Embeds: []discordEmbed{{
			Title: "📘 遊び方",
			Description: "みんなの売買で値段が動く FX ゲームです。" +
				"買いが集まれば上がり、売りが集まれば下がります。",
			Color: embedColorNeutral,
			Fields: []discordEmbedField{
				{
					Name:  "🕛 取引時間",
					Value: "毎日 **12:00〜13:00**（日本時間）だけ取引できます。\n12:59 からは新しい注文はできませんが、決済はできます。",
				},
				{
					Name: "🔰 はじめかた",
					Value: strings.Join([]string{
						"1. `/claim` でその日の資金を受け取る（1 日 1 回。資産が少ない人は多めにもらえます）",
						"2. `/price 通貨コード` で値段を見て「買う」「売る」ボタンで注文",
						"3. `/positions` で含み損益を確認し、決済ボタンで利益を確定",
					}, "\n"),
				},
				{
					Name: "📏 ルール",
					Value: strings.Join([]string{
						"・最初の資金は 1000。レバレッジは最大 10 倍",
						"・売買のたびに少し手数料がかかります",
						"・ポジションは翌日に持ち越せます。取引時間外はロスカットされず、翌日の開始時にまとめて判定されます",
						"・含み損が証拠金の半分に達すると、そのポジションは強制決済されます",
					}, "\n"),
				},
				{
					Name: "💬 コマンド",
					Value: strings.Join([]string{
						"`/rank` 資金ランキング",
						"`/today` 今日の増減ランキング",
						"`/pips` 生涯獲得 pips ランキング",
						"`/balance` 自分の残高",
						"`/profile` 自分や他の人の戦績",
						"`/price` 通貨の値段と注文ボタン",
						"`/positions` 保有ポジションと決済ボタン",
						"`/claim` その日の資金を受け取る",
						"`/help` この案内",
					}, "\n"),
				},
			},
		}},
		Flags: messageFlagEphemeral,
	}

	// PUBLIC_BASE_URL が設定されていれば Web 画面へのリンクボタンを付ける
	// （/price の「詳細チャートを見る」と同じく、未設定なら付けない）。
	if cfg.PublicBaseURL != "" {
		base := strings.TrimRight(cfg.PublicBaseURL, "/")
		resp.Components = []discord.ActionRow{
			discord.NewActionRow(
				discord.Button{Type: 2, Style: discord.ButtonStyleLink, Label: "チャートを見る", URL: base + "/"},
				discord.Button{Type: 2, Style: discord.ButtonStyleLink, Label: "過去チャートモードで遊ぶ", URL: base + "/replay"},
			),
		}
	}
	return resp
}
