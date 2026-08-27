package server

import (
	"log"
	"net/http"
)

// CLAUDE.md §5.4: Cookie属性は HttpOnly / Secure / SameSite=Lax。
// Secure はローカル開発（http://localhost）では有効化できないため cfg.SecureCookies で切り替える。
const (
	stateCookieName   = "oauth_state"
	sessionCookieName = "session"
)

func registerAuthRoutes(mux *http.ServeMux, cfg Config) {
	mux.HandleFunc("GET /auth/discord/login", handleDiscordLogin(cfg))
	mux.HandleFunc("GET /auth/discord/callback", handleDiscordCallback(cfg))
}

// handleDiscordLogin は「Discordでログイン」の入口。認可URLへリダイレクトする。
func handleDiscordLogin(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authURL, state, err := cfg.Auth.LoginURL()
		if err != nil {
			log.Printf("discord login: failed to build auth url: %v", err)
			http.Error(w, "failed to start login", http.StatusInternalServerError)
			return
		}

		// state はCSRF対策用の使い捨てトークン。ユーザーセッションではないため
		// DBに置かず短命なCookieに直接載せる（CLAUDE.md §5.4はログインセッションの話）。
		http.SetCookie(w, &http.Cookie{
			Name:     stateCookieName,
			Value:    state,
			Path:     "/",
			MaxAge:   300,
			HttpOnly: true,
			Secure:   cfg.SecureCookies,
			SameSite: http.SameSiteLaxMode,
		})
		http.Redirect(w, r, authURL, http.StatusFound)
	}
}

// handleDiscordCallback は Discord からのコールバックを受け、Cookie セッションを発行する。
func handleDiscordCallback(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		state := r.URL.Query().Get("state")
		if code == "" || state == "" {
			http.Error(w, "missing code or state", http.StatusBadRequest)
			return
		}

		stateCookie, err := r.Cookie(stateCookieName)
		if err != nil || stateCookie.Value == "" || stateCookie.Value != state {
			http.Error(w, "invalid state", http.StatusBadRequest)
			return
		}
		clearCookie(w, stateCookieName, cfg.SecureCookies)

		session, err := cfg.Auth.HandleCallback(r.Context(), code)
		if err != nil {
			log.Printf("discord oauth callback failed: %v", err)
			http.Error(w, "login failed", http.StatusBadGateway)
			return
		}

		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookieName,
			Value:    session.ID,
			Path:     "/",
			Expires:  session.ExpiresAt,
			HttpOnly: true,
			Secure:   cfg.SecureCookies,
			SameSite: http.SameSiteLaxMode,
		})
		http.Redirect(w, r, "/", http.StatusFound)
	}
}

func clearCookie(w http.ResponseWriter, name string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}
