package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNoSession = errors.New("session not found or expired")

// SessionStore: token acak dikirim ke browser (cookie httpOnly), DB hanya menyimpan SHA-256-nya.
type SessionStore struct {
	db  *pgxpool.Pool
	ttl time.Duration
}

func NewSessionStore(db *pgxpool.Pool, ttl time.Duration) *SessionStore {
	return &SessionStore{db: db, ttl: ttl}
}

func (s *SessionStore) Create(ctx context.Context, userID string) (token string, expires time.Time, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, err
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	expires = time.Now().Add(s.ttl)
	_, err = s.db.Exec(ctx,
		`INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`,
		hashToken(token), userID, expires,
	)
	return token, expires, err
}

func (s *SessionStore) UserID(ctx context.Context, token string) (string, error) {
	var userID string
	err := s.db.QueryRow(ctx,
		`SELECT user_id FROM sessions WHERE token_hash = $1 AND expires_at > now()`,
		hashToken(token),
	).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNoSession
	}
	return userID, err
}

func (s *SessionStore) Delete(ctx context.Context, token string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, hashToken(token))
	return err
}

func hashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}
