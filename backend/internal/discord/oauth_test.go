package discord_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"fxgame/backend/internal/discord"
)

func TestAuthCodeURL(t *testing.T) {
	c := discord.OAuthConfig{
		ClientID:    "client-123",
		RedirectURI: "https://example.test/callback",
	}

	got, err := url.Parse(c.AuthCodeURL("the-state"))
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}

	q := got.Query()
	for k, want := range map[string]string{
		"client_id":     "client-123",
		"redirect_uri":  "https://example.test/callback",
		"response_type": "code",
		"scope":         "identify",
		"state":         "the-state",
	} {
		if q.Get(k) != want {
			t.Errorf("query[%q] = %q, want %q", k, q.Get(k), want)
		}
	}
}

func TestExchangeAndFetchUser(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		if r.PostForm.Get("code") != "the-code" {
			t.Errorf("code = %q, want the-code", r.PostForm.Get("code"))
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "the-token"})
	})
	mux.HandleFunc("GET /user", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer the-token" {
			t.Errorf("Authorization = %q, want Bearer the-token", got)
		}
		_ = json.NewEncoder(w).Encode(discord.User{ID: "42", Username: "wasijyayo"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := discord.OAuthConfig{
		ClientID:     "client-123",
		ClientSecret: "secret",
		RedirectURI:  "https://example.test/callback",
		TokenURL:     srv.URL + "/token",
		UserURL:      srv.URL + "/user",
	}

	token, err := c.Exchange(context.Background(), "the-code")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if token != "the-token" {
		t.Fatalf("token = %q, want the-token", token)
	}

	u, err := c.FetchUser(context.Background(), token)
	if err != nil {
		t.Fatalf("FetchUser: %v", err)
	}
	if u.ID != "42" || u.Username != "wasijyayo" {
		t.Errorf("user = %+v, want {ID:42 Username:wasijyayo}", u)
	}
}
