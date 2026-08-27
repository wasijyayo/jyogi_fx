// Package game はゲームのルールを担当するサービス層（CLAUDE.md §3）。
// Web / Discord / tick のどの入口から呼ばれても同じ関数を通す。
package game

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"fxgame/backend/internal/db"
	"fxgame/backend/internal/discord"
)

// sessionTTL は Cookie セッションの有効期限。
// ゲーム内の「1日1時間」の取引セッション（game_sessions）とは別物。
// ゲームパラメータではなく実装上のデフォルト値なので design-decision の対象外。
const sessionTTL = 30 * 24 * time.Hour

// Session はログイン成功時にハンドラへ返す最小限の情報。
// Cookie に載せるのは ID のみ（CLAUDE.md §5.4）。
type Session struct {
	ID        string
	UserID    string
	ExpiresAt time.Time
}

// AuthService は Discord OAuth2 ログインとセッション発行を担当する。
type AuthService struct {
	pool  *pgxpool.Pool
	oauth discord.OAuthConfig
	clock Clock
}

func NewAuthService(pool *pgxpool.Pool, oauth discord.OAuthConfig, clock Clock) *AuthService {
	return &AuthService{pool: pool, oauth: oauth, clock: clock}
}

// LoginURL は「Discordでログイン」の遷移先URLと、CSRF対策用の state を返す。
// state はハンドラ側で短命な Cookie に入れておき、コールバック時に照合する
// （インメモリのセッションストア禁止（CLAUDE.md §5.4）だが、これはログイン開始から
// 数分で終わる CSRF トークンでありユーザーセッションではないため Cookie に直接載せてよい）。
func (s *AuthService) LoginURL() (authURL, state string, err error) {
	state, err = randomToken(16)
	if err != nil {
		return "", "", fmt.Errorf("generate state: %w", err)
	}
	return s.oauth.AuthCodeURL(state), state, nil
}

// HandleCallback は Discord からの認可コードを検証し、
// 初回ログインならユーザー行を作成し、Cookie セッションを発行する。
func (s *AuthService) HandleCallback(ctx context.Context, code string) (Session, error) {
	accessToken, err := s.oauth.Exchange(ctx, code)
	if err != nil {
		return Session{}, fmt.Errorf("exchange code: %w", err)
	}

	du, err := s.oauth.FetchUser(ctx, accessToken)
	if err != nil {
		return Session{}, fmt.Errorf("fetch discord user: %w", err)
	}

	sessionID, err := randomToken(32)
	if err != nil {
		return Session{}, fmt.Errorf("generate session id: %w", err)
	}
	expiresAt := s.clock.Now().Add(sessionTTL)

	// ユーザー作成とセッション発行はまとめて1トランザクションにする
	// （dev-guide.md §3: 複数のDB操作は必ずトランザクションで囲む）。
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Session{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	q := db.New(tx)

	if _, err := q.UpsertUser(ctx, db.UpsertUserParams{
		DiscordID:   du.ID,
		DisplayName: du.Username,
	}); err != nil {
		return Session{}, fmt.Errorf("upsert user: %w", err)
	}

	if err := q.CreateSession(ctx, db.CreateSessionParams{
		ID:        sessionID,
		UserID:    du.ID,
		ExpiresAt: pgtype.Timestamptz{Time: expiresAt, Valid: true},
	}); err != nil {
		return Session{}, fmt.Errorf("create session: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Session{}, fmt.Errorf("commit tx: %w", err)
	}

	return Session{ID: sessionID, UserID: du.ID, ExpiresAt: expiresAt}, nil
}

func randomToken(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
