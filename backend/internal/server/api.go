package server

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"fxgame/backend/internal/apigen"
	"fxgame/backend/internal/game"
)

// apiServer は api/openapi.yaml から生成された apigen.ServerInterface の実装。
// ロジックは持たず internal/game を呼ぶだけ（CLAUDE.md §3）。
type apiServer struct {
	auth *game.AuthService
}

var _ apigen.ServerInterface = (*apiServer)(nil)

func registerAPIRoutes(mux *http.ServeMux, cfg Config) {
	apigen.HandlerFromMux(&apiServer{auth: cfg.Auth}, mux)
}

// GetMe は GET /api/me。Cookie セッションからユーザーを特定して返す。
func (s *apiServer) GetMe(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		writeAPIError(w, http.StatusUnauthorized, "not logged in")
		return
	}

	user, err := s.auth.CurrentUser(r.Context(), cookie.Value)
	if err != nil {
		if errors.Is(err, game.ErrSessionInvalid) {
			writeAPIError(w, http.StatusUnauthorized, "not logged in")
			return
		}
		log.Printf("get me: %v", err)
		writeAPIError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, apigen.Me{
		DiscordId:   user.DiscordID,
		DisplayName: user.DisplayName,
	})
}

func writeAPIError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, apigen.Error{Message: message})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write json response: %v", err)
	}
}
