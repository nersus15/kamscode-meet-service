// Package api: REST untuk akun, sesi login, room, dan penerbitan join token SFU.
package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"time"

	"github.com/nersus15/kamscode-meet-v2/internal/auth"
	"github.com/nersus15/kamscode-meet-v2/internal/room"
	"github.com/nersus15/kamscode-meet-v2/internal/token"
	"github.com/nersus15/kamscode-meet-v2/internal/user"
)

const (
	sessionCookie = "kc_session"
	maxBodyBytes  = 64 << 10
)

type Config struct {
	SFUURL       string // dikirim ke browser bersama join token
	CookieSecure bool   // true di produksi (HTTPS)
}

type Server struct {
	cfg      Config
	users    *user.Store
	sessions *auth.SessionStore
	rooms    *room.Store
	signer   *token.Signer
}

func NewServer(cfg Config, users *user.Store, sessions *auth.SessionStore, rooms *room.Store, signer *token.Signer) *Server {
	return &Server{cfg: cfg, users: users, sessions: sessions, rooms: rooms, signer: signer}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/auth/register", s.register)
	mux.HandleFunc("POST /api/auth/login", s.login)
	mux.HandleFunc("POST /api/auth/logout", s.logout)
	mux.HandleFunc("GET /api/auth/me", s.requireUser(s.me))

	mux.HandleFunc("POST /api/rooms", s.requireUser(s.createRoom))
	mux.HandleFunc("GET /api/rooms/{slug}", s.requireUser(s.getRoom))
	mux.HandleFunc("POST /api/rooms/{slug}/join", s.requireUser(s.joinRoom))

	return requireJSON(mux)
}

// ---------- middleware ----------

// requireUser memuat user dari cookie sesi; tanpa sesi valid -> 401.
func (s *Server) requireUser(next func(http.ResponseWriter, *http.Request, user.User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "not logged in")
			return
		}
		userID, err := s.sessions.UserID(r.Context(), c.Value)
		if err != nil {
			if !errors.Is(err, auth.ErrNoSession) {
				slog.Error("load session", "err", err)
			}
			writeError(w, http.StatusUnauthorized, "not logged in")
			return
		}
		u, err := s.users.ByID(r.Context(), userID)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "not logged in")
			return
		}
		next(w, r, u)
	}
}

// requireJSON: request yang mengubah data wajib Content-Type application/json.
// Form dari situs lain tidak bisa mengirim JSON tanpa preflight CORS, sehingga
// ini (bersama cookie SameSite=Lax) menahan serangan CSRF.
func requireJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if mt != "application/json" {
				writeError(w, http.StatusUnsupportedMediaType, "content-type must be application/json")
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
}

// ---------- helper ----------

func (s *Server) setSessionCookie(w http.ResponseWriter, tok string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    tok,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true, // tidak bisa dibaca JavaScript (tahan XSS)
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("write json", "err", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func internalError(w http.ResponseWriter, what string, err error) {
	slog.Error(what, "err", err)
	writeError(w, http.StatusInternalServerError, "internal error")
}
