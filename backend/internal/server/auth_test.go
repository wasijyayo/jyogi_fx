package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"fxgame/backend/internal/discord"
	"fxgame/backend/internal/game"
	"fxgame/backend/internal/server"
)

func TestHandleDiscordLogin(t *testing.T) {
	oauth := discord.OAuthConfig{
		ClientID:    "client-123",
		RedirectURI: "http://localhost:8080/auth/discord/callback",
	}
	authSvc := game.NewAuthService(nil, oauth, game.RealClock{})

	mux := server.NewMux(server.Config{Auth: authSvc, SecureCookies: true})

	req := httptest.NewRequest(http.MethodGet, "/auth/discord/login", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusFound)
	}

	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "https://discord.com/api/oauth2/authorize?") {
		t.Errorf("Location = %q, want discord authorize URL", loc)
	}
	if !strings.Contains(loc, "client_id=client-123") {
		t.Errorf("Location missing client_id: %q", loc)
	}

	cookies := rec.Result().Cookies()
	var stateCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "oauth_state" {
			stateCookie = c
		}
	}
	if stateCookie == nil {
		t.Fatal("oauth_state cookie not set")
	}
	if stateCookie.Value == "" {
		t.Error("oauth_state cookie value is empty")
	}
	if !stateCookie.HttpOnly {
		t.Error("oauth_state cookie is not HttpOnly")
	}
	if !stateCookie.Secure {
		t.Error("oauth_state cookie is not Secure")
	}
	if stateCookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("oauth_state cookie SameSite = %v, want Lax", stateCookie.SameSite)
	}
	if !strings.Contains(loc, "state="+stateCookie.Value) {
		t.Errorf("state in redirect URL does not match cookie: %q vs %q", loc, stateCookie.Value)
	}
}

func TestHandleDiscordCallback_MissingParams(t *testing.T) {
	authSvc := game.NewAuthService(nil, discord.OAuthConfig{}, game.RealClock{})
	mux := server.NewMux(server.Config{Auth: authSvc, SecureCookies: true})

	req := httptest.NewRequest(http.MethodGet, "/auth/discord/callback", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleDiscordCallback_StateMismatch(t *testing.T) {
	authSvc := game.NewAuthService(nil, discord.OAuthConfig{}, game.RealClock{})
	mux := server.NewMux(server.Config{Auth: authSvc, SecureCookies: true})

	req := httptest.NewRequest(http.MethodGet, "/auth/discord/callback?code=abc&state=expected", nil)
	req.AddCookie(&http.Cookie{Name: "oauth_state", Value: "different"})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}
