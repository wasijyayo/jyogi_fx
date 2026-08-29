package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"fxgame/backend/internal/discord"
	"fxgame/backend/internal/game"
)

// Discord Embed の色（10進数のRGB）。design.md §6.5「Embedの色: /positionsを含み益なら
// 緑・含み損なら赤に」と同じ考え方を、残高・総資産の初期資金比較にも適用する。
const (
	embedColorProfit  = 0x2ECC71 // 緑
	embedColorLoss    = 0xE74C3C // 赤
	embedColorNeutral = 0x5865F2 // Discordブランドカラー。ランキング等、勝敗の概念が無いものに使う
)

// initialFunds は design.md §7.0 の確定値（初期資金1000）。含み益/含み損の色分けの
// 基準にのみ使う（実際の初期資金付与は #39 と同じく users.balance の DEFAULT 列）。
var initialFunds = decimal.NewFromInt(1000)

// handleSlashCommand は type:2（スラッシュコマンド）の Interaction を実行する
// （#41 CMD-1・#42 CMD-2）。CLAUDE.md §4「4つの入口はすべて同じサービス層を呼ぶ」に
// 従い、ここでは internal/game のサービス層を呼んで結果を Embed に整形するだけで
// ロジックは書かない。
func handleSlashCommand(ctx context.Context, cfg Config, req interactionRequest, now time.Time) interactionResponseData {
	if req.Data == nil {
		return unimplementedCommandResponse("")
	}

	switch req.Data.Name {
	case discord.CommandBalance:
		return handleBalanceCommand(ctx, cfg, req)
	case discord.CommandRank:
		return handleRankCommand(ctx, cfg, now)
	case discord.CommandToday:
		return handleTodayCommand(ctx, cfg, now)
	case discord.CommandProfile:
		return handleProfileCommand(ctx, cfg, req, now)
	default:
		return unimplementedCommandResponse(req.Data.Name)
	}
}

func unimplementedCommandResponse(commandName string) interactionResponseData {
	log.Printf("discord interaction: unhandled type %d (command=%q)", int(interactionApplicationCommand), commandName)
	return interactionResponseData{
		Content: "このコマンドはまだ実装されていません。",
		Flags:   messageFlagEphemeral,
	}
}

// errorCommandResponse は本人にだけ見えるエラーメッセージを返す。
// エラー時にランキング等をチャンネル全体に見せる理由が無いため常にephemeralにする。
func errorCommandResponse(message string) interactionResponseData {
	return interactionResponseData{Content: message, Flags: messageFlagEphemeral}
}

// handleBalanceCommand は /balance（design.md §6.6「自分の資金」）。
// 本人にしか見えないメッセージにする（残高は他人に見せる情報ではないため。
// 「他人の戦績を見せる」ための/profileとはあえて公開範囲を分けている）。
func handleBalanceCommand(ctx context.Context, cfg Config, req interactionRequest) interactionResponseData {
	userID, ok := interactionUserID(req)
	if !ok {
		return errorCommandResponse("ユーザーを特定できませんでした。")
	}

	b, err := cfg.Profile.Balance(ctx, userID)
	if err != nil {
		if errors.Is(err, game.ErrUserNotFound) {
			return errorCommandResponse("ユーザー登録がまだのようです。先にWebでログインしてください。")
		}
		log.Printf("balance command: %v", err)
		return errorCommandResponse("エラーが発生しました。")
	}

	return interactionResponseData{
		Embeds: []discordEmbed{{
			Title: "💰 残高",
			Fields: []discordEmbedField{
				{Name: b.DisplayName, Value: formatAmount(b.Balance)},
			},
			Color: colorForAmount(b.Balance),
		}},
		Flags: messageFlagEphemeral,
	}
}

// handleRankCommand は /rank（総資産ランキング。design.md §6.6・§7.7）。
// 見せ合うためのコマンドなので公開メッセージにする（ephemeralにしない）。
func handleRankCommand(ctx context.Context, cfg Config, now time.Time) interactionResponseData {
	entries, err := cfg.Ranking.RankByTotalAssets(ctx, now)
	if err != nil {
		log.Printf("rank command: %v", err)
		return errorCommandResponse("エラーが発生しました。")
	}

	var b strings.Builder
	if len(entries) == 0 {
		b.WriteString("登録者がまだいません。")
	}
	for i, e := range entries {
		fmt.Fprintf(&b, "%d. <@%s> — %s\n", i+1, e.UserID, formatAmount(e.TotalAssets))
	}

	return interactionResponseData{
		Embeds: []discordEmbed{{
			Title:       "🏆 資金ランキング",
			Description: b.String(),
			Color:       embedColorNeutral,
		}},
	}
}

// handleTodayCommand は /today（当日の増減ランキング。design.md §6.6・§7.7）。
func handleTodayCommand(ctx context.Context, cfg Config, now time.Time) interactionResponseData {
	entries, err := cfg.Ranking.RankByTodayChange(ctx, now)
	if err != nil {
		if errors.Is(err, game.ErrNoSessionToday) {
			return errorCommandResponse("本日のセッションはまだ開始していません。")
		}
		log.Printf("today command: %v", err)
		return errorCommandResponse("エラーが発生しました。")
	}

	var b strings.Builder
	if len(entries) == 0 {
		b.WriteString("対象者がまだいません。")
	}
	for i, e := range entries {
		sign := ""
		if e.ChangePercent.IsPositive() {
			sign = "+"
		}
		fmt.Fprintf(&b, "%d. <@%s> — %s%s%% (%s)\n",
			i+1, e.UserID, sign, e.ChangePercent.StringFixed(2), formatAmount(e.TotalAssets))
	}

	return interactionResponseData{
		Embeds: []discordEmbed{{
			Title:       "📈 本日の増減ランキング",
			Description: b.String(),
			Color:       embedColorNeutral,
		}},
	}
}

// handleProfileCommand は /profile [@ユーザー]（design.md §6.6）。
// 引数省略時は呼び出した本人。「他人の戦績が見えることが最大の燃料」（issue #41）
// という狙いのため公開メッセージにする（/balanceと違いephemeralにしない）。
func handleProfileCommand(ctx context.Context, cfg Config, req interactionRequest, now time.Time) interactionResponseData {
	targetID, ok := interactionUserID(req)
	if !ok {
		return errorCommandResponse("ユーザーを特定できませんでした。")
	}
	if req.Data != nil {
		for _, opt := range req.Data.Options {
			if opt.Name == "user" {
				targetID = opt.Value
			}
		}
	}

	p, err := cfg.Profile.Profile(ctx, now, targetID)
	if err != nil {
		if errors.Is(err, game.ErrUserNotFound) {
			return errorCommandResponse("そのユーザーはまだ登録されていません。")
		}
		log.Printf("profile command: %v", err)
		return errorCommandResponse("エラーが発生しました。")
	}

	return interactionResponseData{
		Embeds: []discordEmbed{{
			Title: "👤 " + p.DisplayName,
			Fields: []discordEmbedField{
				{Name: "残高", Value: formatAmount(p.Balance), Inline: true},
				{Name: "総資産", Value: formatAmount(p.TotalAssets), Inline: true},
				{Name: "順位", Value: fmt.Sprintf("%d位 / %d人中", p.Rank, p.RankOutOf), Inline: true},
			},
			Color: colorForAmount(p.TotalAssets),
		}},
	}
}

// formatAmount は金額を表示用に2桁固定でフォーマットする。
func formatAmount(d decimal.Decimal) string {
	return d.StringFixed(2)
}

// colorForAmount は初期資金（1000。design.md §7.0）以上なら緑、未満なら赤を返す。
func colorForAmount(amount decimal.Decimal) int {
	if amount.GreaterThanOrEqual(initialFunds) {
		return embedColorProfit
	}
	return embedColorLoss
}
