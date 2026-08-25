// Command app はサブコマンド分岐のみを行うエントリポイント。
// ロジックは internal/server, internal/game 側に置く。
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

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
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}

	mux := server.NewMux()
	log.Printf("listening on %s", addr)
	return http.ListenAndServe(addr, mux)
}
