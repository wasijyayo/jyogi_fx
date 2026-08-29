// Command app はサブコマンド分岐のみを行うエントリポイント。
// ロジックは internal/server, internal/game 側に置く。
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"fxgame/backend/internal/discord"
	"fxgame/backend/internal/game"
	"fxgame/backend/internal/server"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: app <serve|register-commands>")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "serve":
		if err := runServe(); err != nil {
			log.Fatal(err)
		}
	case "register-commands":
		if err := runRegisterCommands(); err != nil {
			log.Fatal(err)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", os.Args[1])
		os.Exit(1)
	}
}

func runServe() error {
	// Cloud Run はリッスンポートを PORT で渡してくる規約。
	// ローカルでは ADDR (":8080" 等) で上書きできるようにしておく。
	// ADDR の有無を「ローカル開発かどうか」の判定にも流用し、Cookie の Secure 属性を切り替える
	// （ローカルは http://localhost なので Secure Cookie がブラウザに保存されない）。
	addr := os.Getenv("ADDR")
	secureCookies := addr == ""
	if addr == "" {
		port := os.Getenv("PORT")
		if port == "" {
			port = "8080"
		}
		addr = ":" + port
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("LOCAL_DATABASE_URL")
	}
	if dsn == "" {
		return fmt.Errorf("DATABASE_URL (or LOCAL_DATABASE_URL) is not set")
	}

	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		return fmt.Errorf("connect to db: %w", err)
	}
	defer pool.Close()

	oauth := discord.OAuthConfig{
		ClientID:     os.Getenv("DISCORD_CLIENT_ID"),
		ClientSecret: os.Getenv("DISCORD_CLIENT_SECRET"),
		RedirectURI:  os.Getenv("DISCORD_REDIRECT_URI"),
	}
	authSvc := game.NewAuthService(pool, oauth, game.RealClock{})

	// Discord の署名検証用公開鍵。
	// 設定漏れに起動時点で気づけるよう、未設定・不正のどちらも起動失敗にする。
	// リクエスト時に初めて分かる形にすると、/health は 200 のままデプロイが成功したように
	// 見えてしまい、Bot だけが静かに死んでいる状態を見逃す。
	discordPublicKey, err := server.ParseDiscordPublicKey(os.Getenv("DISCORD_PUBLIC_KEY"))
	if err != nil {
		return fmt.Errorf("DISCORD_PUBLIC_KEY: %w", err)
	}

	// /internal/tick の保護用共有シークレット（design.md §4）。
	// 未設定のまま起動を通すと「シークレット未設定=誰でも叩ける」という
	// 最悪の状態になりうるため、DISCORD_PUBLIC_KEY と同じくフェイルファストにする。
	tickSharedSecret := os.Getenv("TICK_SHARED_SECRET")
	if tickSharedSecret == "" {
		return fmt.Errorf("TICK_SHARED_SECRET is not set")
	}

	// 市場ティッカー（design.md §6.4、#43 NOTIFY-1）用のBotトークンと投稿先チャンネル。
	// register-commandsサブコマンドと違い、serve中は毎tickでこれを使うため
	// ここでも同じくフェイルファストにする（未設定のまま動かすとtickのたびに
	// エラーログが出続けるだけの静かな壊れ方になるため）。
	discordBotToken := os.Getenv("DISCORD_BOT_TOKEN")
	if discordBotToken == "" {
		return fmt.Errorf("DISCORD_BOT_TOKEN is not set")
	}
	tickerChannelID := os.Getenv("DISCORD_TICKER_CHANNEL_ID")
	if tickerChannelID == "" {
		return fmt.Errorf("DISCORD_TICKER_CHANNEL_ID is not set")
	}

	// 自動通知（design.md §6.7〜6.9、#44 NOTIFY-2）用の投稿先チャンネル。
	// #ティッカーとは別チャンネル（design.md §6.11「3チャンネル構成」）。
	notifyChannelID := os.Getenv("DISCORD_NOTIFY_CHANNEL_ID")
	if notifyChannelID == "" {
		return fmt.Errorf("DISCORD_NOTIFY_CHANNEL_ID is not set")
	}
	notifySvc := game.NewNotifyService(discord.MessagesConfig{
		BotToken: discordBotToken,
	}, notifyChannelID)

	// 大口取引通知（design.md §6.7 MVP必須）の閾値（%の価格インパクト）。
	// design.mdに定義が無かったためユーザーに確認して決定した値（デフォルト2%。
	// デプロイなしで調整できるよう環境変数で上書き可能にする。CLAIM_BASE_AMOUNTと
	// 同じ方針）。
	largeTradeThresholdPercent, err := decimalEnv("LARGE_TRADE_IMPACT_PERCENT", "2")
	if err != nil {
		return fmt.Errorf("LARGE_TRADE_IMPACT_PERCENT: %w", err)
	}

	// 利益確定通知（#82）の閾値（pips）。design.mdに定義が無く、ユーザーからの
	// 追加要望（デフォルト200pips）で実装した値。LARGE_TRADE_IMPACT_PERCENTと同じく
	// デプロイなしで調整できるよう環境変数で上書き可能にする。
	profitPipsThreshold, err := decimalEnv("PROFIT_PIPS_THRESHOLD", "200")
	if err != nil {
		return fmt.Errorf("PROFIT_PIPS_THRESHOLD: %w", err)
	}

	// 「人生の勝者」ロールの自動付与（design.md §6.10、#84）。DISCORD_GUILD_ID・
	// DISCORD_LIFE_WINNER_ROLE_IDはどちらか未設定なら機能自体を無効化する
	// （LifeWinnerService.GrantIfEligibleのガード。DISCORD_BOT_TOKEN等と違い
	// フェイルファストにしない。ロールをDiscord側で作成する前でもデプロイできるように
	// するため）。閾値（pips）はユーザーとの確認で決定したデフォルト10000。
	discordGuildID := os.Getenv("DISCORD_GUILD_ID")
	lifeWinnerRoleID := os.Getenv("DISCORD_LIFE_WINNER_ROLE_ID")
	lifetimePipsThreshold, err := decimalEnv("LIFETIME_PIPS_THRESHOLD", "10000")
	if err != nil {
		return fmt.Errorf("LIFETIME_PIPS_THRESHOLD: %w", err)
	}
	lifeWinnerSvc := game.NewLifeWinnerService(pool, discord.MessagesConfig{
		BotToken: discordBotToken,
	}, notifySvc, discordGuildID, lifeWinnerRoleID, lifetimePipsThreshold)

	// GAME_ALWAYS_OPEN=true で開発環境の「取引時間が常に開いている」モードを有効化する
	// （CLAUDE.md §5.1）。本番では未設定のままにすること。
	sessionSvc := game.NewSessionService(pool, game.RealClock{}, game.SessionConfig{
		AlwaysOpen: os.Getenv("GAME_ALWAYS_OPEN") == "true",
	})
	tradeSvc := game.NewTradeService(pool, game.RealClock{}, sessionSvc, notifySvc, largeTradeThresholdPercent, profitPipsThreshold, lifeWinnerSvc)
	liquidationSvc := game.NewLiquidationService(pool, game.RealClock{}, tradeSvc)

	// CLAIM_BASE_AMOUNT・CLAIM_MEDIAN_BUFF_MULTIPLIER はデプロイなしで調整できる
	// ようにする値（design.md §7.2・issue #39完了条件）。バフ倍率1.5倍は確定値
	// （#15）だがissue側の要求どおり環境変数での上書きも許可する。
	claimBaseAmount, err := decimalEnv("CLAIM_BASE_AMOUNT", "100")
	if err != nil {
		return fmt.Errorf("CLAIM_BASE_AMOUNT: %w", err)
	}
	claimBuffMultiplier, err := decimalEnv("CLAIM_MEDIAN_BUFF_MULTIPLIER", "1.5")
	if err != nil {
		return fmt.Errorf("CLAIM_MEDIAN_BUFF_MULTIPLIER: %w", err)
	}
	claimSvc := game.NewClaimService(pool, game.RealClock{}, game.ClaimConfig{
		BaseAmount:     claimBaseAmount,
		BuffMultiplier: claimBuffMultiplier,
	})

	tickerSvc := game.NewTickerService(pool, game.RealClock{}, discord.MessagesConfig{
		BotToken: discordBotToken,
	}, tickerChannelID)

	rankingSvc := game.NewRankingService(pool, game.RealClock{})
	tickSvc := game.NewTickService(pool, game.RealClock{}, sessionSvc, liquidationSvc, claimSvc, tickerSvc, notifySvc, rankingSvc)

	profileSvc := game.NewProfileService(pool, game.RealClock{}, rankingSvc)
	quoteSvc := game.NewQuoteService(pool, game.RealClock{})

	mux := server.NewMux(server.Config{
		Auth:             authSvc,
		Tick:             tickSvc,
		Ranking:          rankingSvc,
		Profile:          profileSvc,
		Quote:            quoteSvc,
		Trade:            tradeSvc,
		Claim:            claimSvc,
		SecureCookies:    secureCookies,
		DiscordPublicKey: discordPublicKey,
		TickSharedSecret: tickSharedSecret,
	})
	log.Printf("listening on %s", addr)
	return http.ListenAndServe(addr, mux)
}

// decimalEnv は環境変数を decimal.Decimal として読む。未設定なら def を使う
// （CLAUDE.md §5.2: floatを金額・倍率に使わないため strconv.ParseFloat は使わない）。
func decimalEnv(key, def string) (decimal.Decimal, error) {
	v := os.Getenv(key)
	if v == "" {
		v = def
	}
	return decimal.NewFromString(v)
}

// runRegisterCommands は Discord にスラッシュコマンド定義を登録する（#29）。
//
// コードを書くだけではコマンドは Discord のチャット欄に表示されない。
// このサブコマンドを手動で実行して初めて反映される。
//
// DISCORD_GUILD_ID を設定するとギルド限定登録になり、即座に反映される（開発向け）。
// 未設定ならグローバル登録になり、反映まで最大1時間かかる（本番向け）。
func runRegisterCommands() error {
	botToken := os.Getenv("DISCORD_BOT_TOKEN")
	if botToken == "" {
		return fmt.Errorf("DISCORD_BOT_TOKEN is not set")
	}

	appID := os.Getenv("DISCORD_CLIENT_ID")
	if appID == "" {
		return fmt.Errorf("DISCORD_CLIENT_ID is not set")
	}

	guildID := os.Getenv("DISCORD_GUILD_ID")

	cfg := discord.RegisterCommandsConfig{
		BotToken:      botToken,
		ApplicationID: appID,
		GuildID:       guildID,
	}
	if err := discord.RegisterCommands(context.Background(), cfg, discord.Commands); err != nil {
		return fmt.Errorf("register commands: %w", err)
	}

	scope := "global（反映まで最大1時間）"
	if guildID != "" {
		scope = "guild " + guildID + "（即時反映）"
	}
	log.Printf("registered %d commands (%s)", len(discord.Commands), scope)
	return nil
}
