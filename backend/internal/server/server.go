// Package server はこのアプリの HTTP ハンドラ層（① 入口 → ② サービス層を呼ぶだけの層）。
package server

import (
	"net/http"
)

// NewMux は HTTP ルーティングを組み立てる。
// Walking Skeleton の現段階では /health のみ。
func NewMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", handleHealth)
	return mux
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}
