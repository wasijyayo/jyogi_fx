package game

import (
	"context"
	"log"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"fxgame/backend/internal/db"
	"fxgame/backend/internal/discord"
)

// LifeWinnerService は「人生の勝者」ロールの自動付与を担当する（#84。design.md
// §6.10「ロール自動付与」の最初の実装。具体的な発動条件（生涯累計pipsの閾値）は
// design.mdに定義が無く、ユーザーからの追加要望で決定した）。
//
// 生涯累計pips（users.lifetime_pips）はTradeService.ClosePositionが決済のたびに
// 更新する（通常決済・強制ロスカットのどちらもClosePositionを通るため両方が
// 自然に積み上がる。CLAUDE.md §4）。このサービスは「閾値を超えたら付与する」
// 判定と実際のDiscord API呼び出し・祝福通知だけを持つ。
//
// §6.8の「今日の称号」ロール（翌セッション開始時に自動で剥がす）とは別枠で、
// 一度付与したら剥奪しない永続ロール（殿堂入り）として扱う（ユーザーの意向）。
type LifeWinnerService struct {
	pool     *pgxpool.Pool
	messages discord.MessagesConfig
	// notify は初回付与時の祝福通知（#通知チャンネル）の投稿先。nilなら投稿しない
	// （NotifyService.postと同じnilセーフの考え方）。
	notify *NotifyService
	// guildID/roleID はどちらか空文字なら機能自体を無効化する（未設定環境向け。
	// design.md §6.10「ロール自動付与」はDISCORD_GUILD_ID・
	// DISCORD_LIFE_WINNER_ROLE_ID未設定でも既存の決済・通知フローに影響しない）。
	guildID string
	roleID  string
	// threshold はこの値（pips）以上の生涯累計pipsで「人生の勝者」ロールを付与する
	// 閾値。main.goのLIFETIME_PIPS_THRESHOLD環境変数で上書き可能（デフォルト10000。
	// ユーザーとの確認で決定した値）。ゼロ以下なら付与しない。
	threshold decimal.Decimal
}

func NewLifeWinnerService(pool *pgxpool.Pool, messages discord.MessagesConfig, notify *NotifyService, guildID, roleID string, threshold decimal.Decimal) *LifeWinnerService {
	return &LifeWinnerService{
		pool:      pool,
		messages:  messages,
		notify:    notify,
		guildID:   guildID,
		roleID:    roleID,
		threshold: threshold,
	}
}

// GrantIfEligible は lifetimePips が閾値以上で、まだ付与していないユーザーに
// 「人生の勝者」ロールを付与し、#通知チャンネルに祝福メッセージを投稿する。
// TradeService.ClosePositionがtx.Commit後に呼ぶ想定（コミット後のベストエフォート。
// maybeNotifyLargeTrade/maybeNotifyProfitTradeと同じ方針）。
//
// nilレシーバでも安全（NotifyService.postと同じ理由。未設定環境向け）。
//
// 冪等性: Discordのロール付与（PUT /guilds/.../members/.../roles/...）はAPI自体が
// 冪等（既に付与済みでも204が返るだけ）なので、この後のDBフラグ更新が何らかの
// 理由で失敗しても次回の呼び出しで安全に再試行できる（CLAUDE.md §5.5）。
// 一方、閾値到達後にそのユーザーが二度と決済しなければ再試行の機会自体が
// 来ないが、「毎分／毎決済で必ず再試行が走る」前提を置かない設計
// （tick.goの欠損許容・tick_notify.goのclosing_notifiedのケースと同種の
// 既知の限界）としてはこれを許容する。
func (s *LifeWinnerService) GrantIfEligible(ctx context.Context, userID, displayName string, alreadyGranted bool, lifetimePips decimal.Decimal) {
	if s == nil || alreadyGranted || s.guildID == "" || s.roleID == "" || !s.threshold.IsPositive() {
		return
	}
	if lifetimePips.LessThan(s.threshold) {
		return
	}

	if err := discord.AddGuildMemberRole(ctx, s.messages, s.guildID, userID, s.roleID); err != nil {
		log.Printf("life winner role grant: %v", err)
		return
	}

	q := db.New(s.pool)
	if err := q.MarkUserLifeWinnerGranted(ctx, userID); err != nil {
		log.Printf("mark life winner granted: %v", err)
	}

	if err := s.notify.LifeWinner(ctx, displayName, s.threshold.Round(0).IntPart()); err != nil {
		log.Printf("life winner notify: %v", err)
	}
}
