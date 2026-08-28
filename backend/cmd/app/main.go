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

	"fxgame/backend/internal/discord"
	"fxgame/backend/internal/game"
	"fxgame/backend/internal/server"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: app <serve>")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "serve":
		if err := runServe(); err != nil {
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

	mux := server.NewMux(server.Config{
		Auth:             authSvc,
		SecureCookies:    secureCookies,
		DiscordPublicKey: discordPublicKey,
	})
	log.Printf("listening on %s", addr)
	return http.ListenAndServe(addr, mux)
}
