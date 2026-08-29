package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"fxgame/backend/internal/discord"
	"fxgame/backend/internal/game"
)

// customID のプレフィックス（#42）。ボタン・モーダルの custom_id は
// "プレフィックス:パラメータ..." の形でエンコードする（encodeCustomID/decodeCustomID）。
const (
	customIDOrderButton  = "order"         // order:<long|short>:<symbol>        価格Embedのボタン→モーダルを開く
	customIDOrderSubmit  = "order_submit"  // order_submit:<long|short>:<symbol> モーダル送信→発注
	customIDClose        = "close"         // close:<positionID>                 決済ボタン→確認を出す
	customIDCloseConfirm = "close_confirm" // close_confirm:<positionID>         確認の「はい」→実際に決済
	customIDCloseCancel  = "close_cancel"  // close_cancel                       確認の「いいえ」
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
	case discord.CommandPrice:
		return handlePriceCommand(ctx, cfg, req, now)
	case discord.CommandPositions:
		return handlePositionsCommand(ctx, cfg, req, now)
	case discord.CommandClaim:
		return handleClaimCommand(ctx, cfg, req, now)
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

// handlePriceCommand は /price <通貨>（design.md §6.3・§6.4・§6.6）。
// 現在価格・変動率・スパークラインに加え、「買う」「売る」ボタンを付ける
// （§6.3「/price USD → 現在価格 + 「買う」「売る」ボタン → 数量をモーダル入力」）。
// 誰でも見られる市況情報なので公開メッセージにする（ephemeralにしない）。
// ボタンは押した人自身の注文フローに入る（/priceを実行した人に限定しない）。
func handlePriceCommand(ctx context.Context, cfg Config, req interactionRequest, now time.Time) interactionResponseData {
	symbol := strings.ToUpper(strings.TrimSpace(commandOptionValue(req.Data, "currency")))
	if symbol == "" {
		return errorCommandResponse("通貨コードを指定してください。")
	}

	quote, err := cfg.Quote.Quote(ctx, now, symbol)
	if err != nil {
		if errors.Is(err, game.ErrCurrencyNotFound) {
			return errorCommandResponse(fmt.Sprintf("通貨 %s が見つかりません。", symbol))
		}
		log.Printf("price command: %v", err)
		return errorCommandResponse("エラーが発生しました。")
	}

	arrow := "―"
	switch {
	case quote.ChangePercent.IsPositive():
		arrow = "▲"
	case quote.ChangePercent.IsNegative():
		arrow = "▼"
	}
	description := fmt.Sprintf("%s  %s%s%%", formatAmount(quote.CurrentPrice), arrow, quote.ChangePercent.Abs().StringFixed(2))
	if quote.Sparkline != "" {
		description += "  " + quote.Sparkline
	}

	return interactionResponseData{
		Embeds: []discordEmbed{{
			Title:       "💹 " + quote.Symbol,
			Description: description,
			Color:       colorForChangePercent(quote.ChangePercent),
		}},
		Components: []discordActionRow{
			newActionRow(
				discordButton{Type: 2, Style: buttonStyleSuccess, Label: "買う", CustomID: encodeCustomID(customIDOrderButton, string(game.SideLong), quote.Symbol)},
				discordButton{Type: 2, Style: buttonStyleDanger, Label: "売る", CustomID: encodeCustomID(customIDOrderButton, string(game.SideShort), quote.Symbol)},
			),
		},
	}
}

// maxPositionsShown はDiscordの1メッセージあたりEmbed数上限（10）に合わせる。
// 20人規模のゲームで1人がこれを超えて保有することは想定していないが、
// メッセージ送信自体が失敗しないための安全策。
const maxPositionsShown = 10

// handlePositionsCommand は /positions（design.md §6.3・§6.6「保有ポジションと
// 含み損益 + 各行に決済ボタン」）。残高同様に本人以外に見せる情報ではないため
// ephemeralにする。
func handlePositionsCommand(ctx context.Context, cfg Config, req interactionRequest, now time.Time) interactionResponseData {
	userID, ok := interactionUserID(req)
	if !ok {
		return errorCommandResponse("ユーザーを特定できませんでした。")
	}

	positions, err := cfg.Trade.ListOpenPositions(ctx, now, userID)
	if err != nil {
		log.Printf("positions command: %v", err)
		return errorCommandResponse("エラーが発生しました。")
	}
	if len(positions) == 0 {
		return interactionResponseData{Content: "保有ポジションはありません。", Flags: messageFlagEphemeral}
	}

	shown := positions
	truncated := false
	if len(shown) > maxPositionsShown {
		shown = shown[:maxPositionsShown]
		truncated = true
	}

	// design.md §6.5「Embedの色: /positionsを含み益なら緑・含み損なら赤に」は
	// Embed単位の色のため、行ごとに色分けするには1ポジション1Embedにする
	// （Discordは1メッセージにEmbedを複数付けられる）。決済ボタンも同様に
	// 1ポジション1Action Rowにして対応づける。
	embeds := make([]discordEmbed, 0, len(shown))
	rows := make([]discordActionRow, 0, len(shown))
	for _, p := range shown {
		sideLabel := "ロング"
		if game.Side(p.Position.Side) == game.SideShort {
			sideLabel = "ショート"
		}
		embeds = append(embeds, discordEmbed{
			Title: fmt.Sprintf("%s %s x%s", p.CurrencySymbol, sideLabel, p.Position.Size.String()),
			Description: fmt.Sprintf("建値 %s → 現在値 %s\n含み損益: %s",
				formatAmount(p.Position.EntryPrice), formatAmount(p.CurrentPrice), formatAmount(p.UnrealizedPnL)),
			Color: colorForPnL(p.UnrealizedPnL),
		})
		rows = append(rows, newActionRow(discordButton{
			Type:     2,
			Style:    buttonStyleDanger,
			Label:    "決済（" + p.CurrencySymbol + "）",
			CustomID: encodeCustomID(customIDClose, strconv.FormatInt(p.Position.ID, 10)),
		}))
	}

	content := ""
	if truncated {
		content = fmt.Sprintf("保有ポジションが多いため直近%d件のみ表示しています。", maxPositionsShown)
	}

	return interactionResponseData{
		Content:    content,
		Embeds:     embeds,
		Components: rows,
		Flags:      messageFlagEphemeral,
	}
}

// handleClaimCommand は /claim（design.md §7.2。#39のClaimServiceを呼ぶだけで
// ロジックは持たない。CLAUDE.md §4）。受取額は個人の懐事情なのでephemeralにする。
func handleClaimCommand(ctx context.Context, cfg Config, req interactionRequest, now time.Time) interactionResponseData {
	userID, ok := interactionUserID(req)
	if !ok {
		return errorCommandResponse("ユーザーを特定できませんでした。")
	}

	result, err := cfg.Claim.Claim(ctx, now, userID)
	if err != nil {
		switch {
		case errors.Is(err, game.ErrClaimNotAvailable):
			return errorCommandResponse("まだ本日のclaimは利用できません。セッション開始をお待ちください。")
		case errors.Is(err, game.ErrAlreadyClaimed):
			return errorCommandResponse("本日分はすでに受け取り済みです。")
		case errors.Is(err, game.ErrUserNotFound):
			return errorCommandResponse("ユーザー登録がまだのようです。先にWebでログインしてください。")
		default:
			log.Printf("claim command: %v", err)
			return errorCommandResponse("エラーが発生しました。")
		}
	}

	buffNote := ""
	if result.Buffed {
		buffNote = "（資産下位バフ適用）"
	}
	return interactionResponseData{
		Embeds: []discordEmbed{{
			Title: "🎁 資金を受け取りました",
			Fields: []discordEmbedField{
				{Name: "受取額", Value: formatAmount(result.Amount) + buffNote},
				{Name: "残高", Value: formatAmount(result.NewBalance)},
			},
			Color: embedColorProfit,
		}},
		Flags: messageFlagEphemeral,
	}
}

// handleMessageComponent は type:3（ボタン等）の Interaction を処理する（#42）。
// custom_id のプレフィックスで通常メッセージ応答（interactionResponse）か
// モーダルを開く応答（interactionModalResponse）かが変わるため、
// handleSlashCommand とは違い応答全体（any）を組み立てて返す。
func handleMessageComponent(ctx context.Context, cfg Config, req interactionRequest, now time.Time) any {
	customID := ""
	if req.Data != nil {
		customID = req.Data.CustomID
	}
	parts := decodeCustomID(customID)
	if len(parts) == 0 {
		return channelMessage(errorCommandResponse("不明な操作です。"))
	}

	switch parts[0] {
	case customIDOrderButton:
		return handleOrderButton(parts)
	case customIDClose:
		return channelMessage(handleCloseButton(parts))
	case customIDCloseConfirm:
		return channelMessage(handleCloseConfirmButton(ctx, cfg, req, now, parts))
	case customIDCloseCancel:
		return channelMessage(errorCommandResponse("キャンセルしました。"))
	default:
		return channelMessage(errorCommandResponse("不明な操作です。"))
	}
}

// channelMessage は interactionResponseData を通常のメッセージ応答（type:4）に包む。
func channelMessage(data interactionResponseData) interactionResponse {
	return interactionResponse{Type: callbackChannelMessage, Data: &data}
}

// handleOrderButton は /price の「買う」「売る」ボタン押下を処理する。
// 数量（・レバレッジ）はモーダルで入力させる（design.md §6.3）ため、
// ここでは発注せずモーダルを開く応答を返すだけ。
func handleOrderButton(parts []string) any {
	if len(parts) != 3 {
		return channelMessage(errorCommandResponse("不明な操作です。"))
	}
	side, symbol := parts[1], parts[2]
	label := "買う"
	if game.Side(side) == game.SideShort {
		label = "売る"
	}

	return interactionModalResponse{
		Type: callbackModal,
		Data: &interactionModalData{
			CustomID: encodeCustomID(customIDOrderSubmit, side, symbol),
			Title:    fmt.Sprintf("%sを%s", symbol, label),
			Components: []discordTextInputRow{
				newTextInputRow(discordTextInput{
					Type: 4, CustomID: "size", Label: "数量", Style: textInputStyleShort, Required: true,
				}),
				newTextInputRow(discordTextInput{
					Type: 4, CustomID: "leverage", Label: "レバレッジ（省略時は1）",
					Style: textInputStyleShort, Required: false, Placeholder: "1〜10",
				}),
			},
		},
	}
}

// handleCloseButton は /positions の「決済」ボタン押下を処理する。
// いきなり決済せず確認を挟む（design.md §6.6「確認 → 決済完了」）。
func handleCloseButton(parts []string) interactionResponseData {
	if len(parts) != 2 {
		return errorCommandResponse("不明な操作です。")
	}
	positionID := parts[1]
	return interactionResponseData{
		Content: "本当にこのポジションを決済しますか？",
		Components: []discordActionRow{
			newActionRow(
				discordButton{Type: 2, Style: buttonStyleDanger, Label: "はい、決済する", CustomID: encodeCustomID(customIDCloseConfirm, positionID)},
				discordButton{Type: 2, Style: buttonStyleSecondary, Label: "いいえ", CustomID: customIDCloseCancel},
			),
		},
		Flags: messageFlagEphemeral,
	}
}

// handleCloseConfirmButton は確認の「はい」押下を処理し、実際に決済する
// （#42完了条件「/positions → 決済ボタン → 実際に決済され残高が変わること」）。
// ClosePositionはUserIDでもポジションを絞り込む（trade.goのGetPositionForUpdate
// コメント参照）ため、押した人と違う持ち主のポジションは自然にErrPositionNotFound
// になる（custom_idを細工されても他人のポジションは決済できない）。
func handleCloseConfirmButton(ctx context.Context, cfg Config, req interactionRequest, now time.Time, parts []string) interactionResponseData {
	if len(parts) != 2 {
		return errorCommandResponse("不明な操作です。")
	}
	positionID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return errorCommandResponse("不明な操作です。")
	}
	userID, ok := interactionUserID(req)
	if !ok {
		return errorCommandResponse("ユーザーを特定できませんでした。")
	}

	result, err := cfg.Trade.ClosePosition(ctx, now, game.ClosePositionParams{UserID: userID, PositionID: positionID})
	if err != nil {
		switch {
		case errors.Is(err, game.ErrPositionNotFound):
			return errorCommandResponse("そのポジションは見つかりません。")
		case errors.Is(err, game.ErrPositionAlreadyClosed):
			return errorCommandResponse("そのポジションはすでに決済済みです。")
		default:
			log.Printf("close position: %v", err)
			return errorCommandResponse("エラーが発生しました。")
		}
	}

	pnl := decimal.Zero
	if result.Position.Pnl.Valid {
		pnl = result.Position.Pnl.Decimal
	}
	return interactionResponseData{
		Embeds: []discordEmbed{{
			Title: "✅ 決済しました",
			Fields: []discordEmbedField{
				{Name: "損益", Value: formatAmount(pnl)},
				{Name: "残高", Value: formatAmount(result.NewBalance)},
			},
			Color: colorForPnL(pnl),
		}},
		Flags: messageFlagEphemeral,
	}
}

// handleModalSubmit は type:5（モーダル送信）の Interaction を処理する（#42）。
// 現状 /price のボタンから開く発注モーダルのみが対象。
func handleModalSubmit(ctx context.Context, cfg Config, req interactionRequest, now time.Time) interactionResponseData {
	customID := ""
	if req.Data != nil {
		customID = req.Data.CustomID
	}
	parts := decodeCustomID(customID)
	if len(parts) != 3 || parts[0] != customIDOrderSubmit {
		return errorCommandResponse("不明な操作です。")
	}
	side, symbol := parts[1], parts[2]

	sizeStr, _ := interactionModalValue(req.Data, "size")
	size, err := decimal.NewFromString(strings.TrimSpace(sizeStr))
	if err != nil || !size.IsPositive() {
		return errorCommandResponse("数量は正の数で入力してください。")
	}

	leverage := decimal.NewFromInt(1)
	if leverageStr, ok := interactionModalValue(req.Data, "leverage"); ok && strings.TrimSpace(leverageStr) != "" {
		leverage, err = decimal.NewFromString(strings.TrimSpace(leverageStr))
		if err != nil || !leverage.IsPositive() {
			return errorCommandResponse("レバレッジは正の数で入力してください。")
		}
	}

	userID, ok := interactionUserID(req)
	if !ok {
		return errorCommandResponse("ユーザーを特定できませんでした。")
	}

	result, err := cfg.Trade.PlaceOrder(ctx, now, game.PlaceOrderParams{
		UserID:         userID,
		CurrencySymbol: symbol,
		Side:           game.Side(side),
		Size:           size,
		Leverage:       leverage,
	})
	if err != nil {
		switch {
		case errors.Is(err, game.ErrInsufficientBalance):
			return errorCommandResponse("残高が不足しています。")
		case errors.Is(err, game.ErrLeverageExceedsMax):
			return errorCommandResponse("レバレッジが上限を超えています。")
		case errors.Is(err, game.ErrNewOrdersClosed):
			return errorCommandResponse("現在は新規注文を受け付けていません（取引時間外、または終了間際です）。")
		case errors.Is(err, game.ErrCurrencyNotFound):
			return errorCommandResponse("その通貨は見つかりません。")
		case errors.Is(err, game.ErrUserNotFound):
			return errorCommandResponse("ユーザー登録がまだのようです。先にWebでログインしてください。")
		default:
			log.Printf("place order: %v", err)
			return errorCommandResponse("エラーが発生しました。")
		}
	}

	sideLabel := "買い"
	if game.Side(side) == game.SideShort {
		sideLabel = "売り"
	}
	return interactionResponseData{
		Embeds: []discordEmbed{{
			Title: "✅ 注文が成立しました",
			Fields: []discordEmbedField{
				{Name: "内容", Value: fmt.Sprintf("%s %s x%s @ %s", symbol, sideLabel, size.String(), formatAmount(result.Position.EntryPrice))},
				{Name: "残高", Value: formatAmount(result.NewBalance)},
			},
			Color: embedColorNeutral,
		}},
		Flags: messageFlagEphemeral,
	}
}

// commandOptionValue はスラッシュコマンドのoptionsからnameで値を引く。
func commandOptionValue(data *interactionCommandData, name string) string {
	if data == nil {
		return ""
	}
	for _, opt := range data.Options {
		if opt.Name == name {
			return opt.Value
		}
	}
	return ""
}

// encodeCustomID/decodeCustomID はボタン・モーダルのcustom_idのエンコード規約
// （"プレフィックス:パラメータ..."）。通貨コード・ポジションIDのいずれも
// ":"を含みえないため単純な分割で安全に往復できる。
func encodeCustomID(parts ...string) string {
	return strings.Join(parts, ":")
}

func decodeCustomID(customID string) []string {
	if customID == "" {
		return nil
	}
	return strings.Split(customID, ":")
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

// colorForPnL は損益0以上なら緑、負なら赤を返す（design.md §6.5「Embedの色」）。
// colorForAmountとは基準点が違う（初期資金1000ではなく0）ため分けている。
func colorForPnL(pnl decimal.Decimal) int {
	if pnl.IsNegative() {
		return embedColorLoss
	}
	return embedColorProfit
}

// colorForChangePercent は/priceの変動率表示用。上昇=緑・下落=赤・変化なし=中立。
func colorForChangePercent(changePercent decimal.Decimal) int {
	switch {
	case changePercent.IsPositive():
		return embedColorProfit
	case changePercent.IsNegative():
		return embedColorLoss
	default:
		return embedColorNeutral
	}
}
