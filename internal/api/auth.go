package api

import (
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"unicode/utf8"

	"github.com/nersus15/kamscode-meet-v2/internal/auth"
	"github.com/nersus15/kamscode-meet-v2/internal/user"
)

// Hash acak untuk login dengan email tak dikenal: waktu respons tetap setara,
// sehingga penyerang tidak bisa menebak email mana yang terdaftar dari timing.
var dummyHash, _ = auth.HashPassword("kamscode-meet-dummy")

type credentials struct {
	Email    string `json:"email"`
	Name     string `json:"name,omitempty"`
	Password string `json:"password"`
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var in credentials
	if err := decode(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	email := normalizeEmail(in.Email)
	name := strings.TrimSpace(in.Name)
	switch {
	case !validEmail(email):
		writeError(w, http.StatusBadRequest, "invalid email")
		return
	case name == "" || utf8.RuneCountInString(name) > 64:
		writeError(w, http.StatusBadRequest, "name must be 1-64 characters")
		return
	case len(in.Password) < 8 || len(in.Password) > 256:
		writeError(w, http.StatusBadRequest, "password must be 8-256 characters")
		return
	}

	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		internalError(w, "hash password", err)
		return
	}
	u, err := s.users.Create(r.Context(), email, name, hash)
	if errors.Is(err, user.ErrEmailTaken) {
		writeError(w, http.StatusConflict, "email already registered")
		return
	}
	if err != nil {
		internalError(w, "create user", err)
		return
	}
	s.startSession(w, r, u)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in credentials
	if err := decode(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	u, hash, err := s.users.ByEmail(r.Context(), normalizeEmail(in.Email))
	if err != nil && !errors.Is(err, user.ErrNotFound) {
		internalError(w, "load user", err)
		return
	}
	if errors.Is(err, user.ErrNotFound) {
		hash = dummyHash
	}
	ok, verr := auth.VerifyPassword(in.Password, hash)
	if err != nil || verr != nil || !ok {
		// Pesan yang sama untuk email tak dikenal dan password salah.
		writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	s.startSession(w, r, u)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if err := s.sessions.Delete(r.Context(), c.Value); err != nil {
			internalError(w, "delete session", err)
			return
		}
	}
	s.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, _ *http.Request, u user.User) {
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, u user.User) {
	tok, expires, err := s.sessions.Create(r.Context(), u.ID)
	if err != nil {
		internalError(w, "create session", err)
		return
	}
	s.setSessionCookie(w, tok, expires)
	writeJSON(w, http.StatusOK, u)
}

func normalizeEmail(e string) string {
	return strings.ToLower(strings.TrimSpace(e))
}

func validEmail(e string) bool {
	if len(e) > 254 {
		return false
	}
	a, err := mail.ParseAddress(e)
	return err == nil && a.Address == e
}
