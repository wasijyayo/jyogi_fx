// Package discord は Discord API 呼び出し（OAuth2・通知・ロール付与）を担当する。
// docs/design.md §6.1 のとおり、独自ログイン機構は作らず Discord OAuth2 一本で完結させる。
package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	defaultAuthorizeURL = "https://discord.com/api/oauth2/authorize"
	defaultTokenURL     = "https://discord.com/api/oauth2/token"
	defaultUserURL      = "https://discord.com/api/users/@me"

	// scope は identify のみ（design.md §6.1）。
	// サーバー限定にする場合は guilds を追加しコールバック時に所属を確認する。
	oauthScope = "identify"
)

// OAuthConfig は Discord OAuth2 の認可コードフローに必要な設定。
type OAuthConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string

	// AuthorizeURL / TokenURL / UserURL はテストでのエンドポイント差し替え用。
	// 空文字なら本物の Discord API を使う。
	AuthorizeURL string
	TokenURL     string
	UserURL      string
}

func (c OAuthConfig) authorizeURL() string {
	if c.AuthorizeURL != "" {
		return c.AuthorizeURL
	}
	return defaultAuthorizeURL
}

func (c OAuthConfig) tokenURL() string {
	if c.TokenURL != "" {
		return c.TokenURL
	}
	return defaultTokenURL
}

func (c OAuthConfig) userURL() string {
	if c.UserURL != "" {
		return c.UserURL
	}
	return defaultUserURL
}

// User は /users/@me から返る最小限のフィールド。
type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

// AuthCodeURL は「Discordでログイン」ボタンの遷移先 URL を組み立てる。
// state は CSRF 対策用。呼び出し側が発行し、コールバック時に照合する。
func (c OAuthConfig) AuthCodeURL(state string) string {
	q := url.Values{
		"client_id":     {c.ClientID},
		"redirect_uri":  {c.RedirectURI},
		"response_type": {"code"},
		"scope":         {oauthScope},
		"state":         {state},
	}
	return c.authorizeURL() + "?" + q.Encode()
}

// Exchange は認可コードをアクセストークンに交換する。
func (c OAuthConfig) Exchange(ctx context.Context, code string) (accessToken string, err error) {
	form := url.Values{
		"client_id":     {c.ClientID},
		"client_secret": {c.ClientSecret},
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {c.RedirectURI},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL(), strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("discord token exchange failed: status=%d body=%s", resp.StatusCode, body)
	}

	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	return out.AccessToken, nil
}

// FetchUser はアクセストークンで Discord のユーザー情報を取得する。
func (c OAuthConfig) FetchUser(ctx context.Context, accessToken string) (User, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.userURL(), nil)
	if err != nil {
		return User{}, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return User{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return User{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return User{}, fmt.Errorf("discord fetch user failed: status=%d body=%s", resp.StatusCode, body)
	}

	var u User
	if err := json.Unmarshal(body, &u); err != nil {
		return User{}, fmt.Errorf("decode user response: %w", err)
	}
	return u, nil
}
