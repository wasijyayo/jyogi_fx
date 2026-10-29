// Package game はゲームのルールを担当するサービス層（CLAUDE.md §3）。
// Web / Discord / tick のどの入口から呼ばれても同じ関数を通す。
package game

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"fxgame/backend/internal/db"
	"fxgame/backend/internal/discord"
)

// ErrSessionInvalid はセッションが存在しない・期限切れの場合に返る。
// ハンドラ側はこれを 401 に変換する。
var ErrSessionInvalid = errors.New("session invalid or expired")

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

// User はハンドラ層に返す最小限のユーザー情報。
// server 層が internal/db に直接依存しないための境界（CLAUDE.md §3）。
type User struct {
	DiscordID   string
	DisplayName string
	// Balance は残高。金額は decimal（CLAUDE.md §5.2）。
	Balance decimal.Decimal
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
	defer func() { _ = tx.Rollback(ctx) }()

	q := db.New(tx)

	// Webログイン時は既存ユーザーの display_name を Discord の最新のユーザー名に更新する。
	if err := registerUser(ctx, q, du.ID, du.Username, true); err != nil {
		return Session{}, err
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

// EnsureDiscordUser は Discord Interaction の送信者の users 行が無ければ作成する（#86）。
// これまで users 行は Web の OAuth ログイン（HandleCallback）でしか作られず、Webに
// 一度もログインしていないユーザーは Discord からプレイできなかった。主キーは
// Discord ユーザーIDなので（design.md §6.1）、Interaction のペイロードに入っている
// IDでそのまま作れば後でWebログインしても同じユーザーに繋がる。
// 既存ユーザーには何もしないため何度呼んでも初期資金の二重付与にならない。
func (s *AuthService) EnsureDiscordUser(ctx context.Context, discordID, displayName string) error {
	// display_name の更新は Web ログイン時（HandleCallback）に任せる。
	return registerUser(ctx, db.New(s.pool), discordID, displayName, false)
}

// registerUser は users 行が無ければ作成する。Web ログイン（HandleCallback）と
// Discord Interaction（EnsureDiscordUser）のどちらの入口からもここを通す（CLAUDE.md §4）。
// 初期資金（design.md §7.0）は users.balance の DEFAULT 列で付与される。
// 既存ユーザーの場合、updateDisplayName が true なら display_name だけ更新し、
// false なら何もしない。
func registerUser(ctx context.Context, q *db.Queries, discordID, displayName string, updateDisplayName bool) error {
	if updateDisplayName {
		if _, err := q.UpsertUser(ctx, db.UpsertUserParams{
			DiscordID:   discordID,
			DisplayName: displayName,
		}); err != nil {
			return fmt.Errorf("upsert user: %w", err)
		}
		return nil
	}
	if err := q.InsertUserIfNotExists(ctx, db.InsertUserIfNotExistsParams{
		DiscordID:   discordID,
		DisplayName: displayName,
	}); err != nil {
		return fmt.Errorf("insert user if not exists: %w", err)
	}
	return nil
}

// CurrentUser は Cookie に入っているセッションIDからユーザーを引く。
// GET /api/me など、ログイン必須のエンドポイントから呼ばれる。
func (s *AuthService) CurrentUser(ctx context.Context, sessionID string) (User, error) {
	q := db.New(s.pool)

	session, err := q.GetSession(ctx, sessionID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrSessionInvalid
		}
		return User{}, fmt.Errorf("get session: %w", err)
	}
	if !session.ExpiresAt.Valid || s.clock.Now().After(session.ExpiresAt.Time) {
		return User{}, ErrSessionInvalid
	}

	u, err := q.GetUser(ctx, session.UserID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrSessionInvalid
		}
		return User{}, fmt.Errorf("get user: %w", err)
	}

	return User{DiscordID: u.DiscordID, DisplayName: u.DisplayName, Balance: u.Balance}, nil
}

// Logout はセッション行を削除する（POST /auth/logout。CLAUDE.md §5.4）。
// Cookie の破棄は呼び出し側（server 層）が行う。セッションIDが既に無くても
// エラーにはしない（冪等。ログアウトは何度呼んでも安全）。
func (s *AuthService) Logout(ctx context.Context, sessionID string) error {
	q := db.New(s.pool)
	if err := q.DeleteSession(ctx, sessionID); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

func randomToken(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
