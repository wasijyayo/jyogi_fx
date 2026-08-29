package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"fxgame/backend/internal/game"
	"fxgame/backend/internal/server"
)

// TestTickEndpoint は #35 の「エンドポイントの保護は必須」（design.md §4）を確認する。
//
// セッション外の固定時刻を注入することで、game.TickService.Tick が早期returnする
// パス（DBに一切触れない）を使い、DB接続なしでも大部分を検証できるようにしている。
// ローカル Postgres が無い環境でも「保護」自体は確認できるが、200 が返ることまで
// 見るために接続を試み、繋がらなければそのケースだけスキップする。
func TestTickEndpoint(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://app:app@localhost:5432/fxgame?sslmode=disable"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(func() { pool.Close() })

	dbReachable := pool.Ping(ctx) == nil

	// 安全策: offSession の時刻計算を間違えてセッション中と判定されてしまった場合でも
	// （実際に#35の開発中に一度このミスを踏んだ）、その日の分の後始末を必ず行う。
	if dbReachable {
		t.Cleanup(func() {
			cctx := context.Background()
			// events も game_sessions への外部キーを持つため（#40 EVENT-1で追加）、
			// 先に消しておく（#55と同じ理由。本来この安全策が発動すること自体
			// 想定していないが、発動時に外部キー制約で後始末が失敗しないようにする）。
			_, _ = pool.Exec(cctx, `DELETE FROM events WHERE session_id IN
				(SELECT id FROM game_sessions WHERE date = '2026-01-02')`)
			_, _ = pool.Exec(cctx, `DELETE FROM price_ticks WHERE session_id IN
				(SELECT id FROM game_sessions WHERE date = '2026-01-02')`)
			_, _ = pool.Exec(cctx, `DELETE FROM game_sessions WHERE date = '2026-01-02'`)
		})
	}

	const secret = "test-tick-secret"
	sessionSvc := game.NewSessionService(pool, game.RealClock{}, game.SessionConfig{})
	tradeSvc := game.NewTradeService(pool, game.RealClock{}, sessionSvc, nil, decimal.Zero, decimal.Zero, nil)
	liquidationSvc := game.NewLiquidationService(pool, game.RealClock{}, tradeSvc)
	claimSvc := game.NewClaimService(pool, game.RealClock{}, game.ClaimConfig{
		BaseAmount:     decimal.NewFromInt(100),
		BuffMultiplier: decimal.NewFromFloat(1.5),
	})
	tickSvc := game.NewTickService(pool, game.RealClock{}, sessionSvc, liquidationSvc, claimSvc, nil, nil, nil)

	// 深夜0時JST（セッション外）を固定で返すクロック。Tick はセッション外なら
	// DBに触れず早期returnするため、この時刻なら pool が未接続でも 200 になる。
	// セッションは JST 12:00〜13:00 = UTC 3:00〜4:00 なので、UTC時刻を使う際は
	// 混同しないよう注意（UTC 3:00をそのまま使うとJST正午でセッション中になる）。
	offSession := time.Date(2026, 1, 1, 15, 0, 0, 0, time.UTC) // = JST 2026-01-02 00:00
	mux := server.NewMux(server.Config{
		Tick:             tickSvc,
		TickSharedSecret: secret,
		Clock:            fixedClock{now: offSession},
	})

	t.Run("Authorizationヘッダが無いと401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/internal/tick", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
	})

	t.Run("シークレットが誤っていると401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/internal/tick", nil)
		req.Header.Set("Authorization", "Bearer wrong-secret")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
	})

	t.Run("GETは405", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/internal/tick", nil)
		req.Header.Set("Authorization", "Bearer "+secret)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
		}
	})

	t.Run("正しいシークレットでTickが呼ばれる", func(t *testing.T) {
		if !dbReachable {
			t.Skipf("ローカル Postgres に接続できないためスキップ（docker compose up -d を実行してください）")
		}

		req := httptest.NewRequest(http.MethodPost, "/internal/tick", nil)
		req.Header.Set("Authorization", "Bearer "+secret)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
		}
	})
}
