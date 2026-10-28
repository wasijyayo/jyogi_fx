// Package backend は Vite でビルドした SPA（backend/web/）を単一バイナリへ
// 同梱するための embed 定義だけを置く。
//
// embedディレクティブは自分と同じディレクトリ以下しか参照できないため、internal/server
// 配下ではなく web/ と同じ階層（backend/ 直下）に置いている（CLAUDE.md §3 配信形態）。
package backend

import "embed"

// WebFS は backend/web/（Vite の `npm run build` 出力）を丸ごと埋め込んだもの。
// フロントエンド未ビルド時でも `go build` が通るよう backend/web/.gitkeep を置いてある
// （go:embed は対象が0件だとコンパイルエラーになるため）。
//
//go:embed all:web
var WebFS embed.FS
