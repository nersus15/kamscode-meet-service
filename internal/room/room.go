// Package room: data room di sisi api (tabel rooms). Berbeda dari sfu.Room yang hanya hidup di memori SFU.
package room

import (
	"context"
	"crypto/rand"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("room not found")

type Room struct {
	ID        string    `json:"id"`
	Slug      string    `json:"slug"`
	Name      string    `json:"name"`
	OwnerID   string    `json:"ownerId"`
	CreatedAt time.Time `json:"createdAt"`
}

type Store struct {
	db *pgxpool.Pool
}

func NewStore(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

func (s *Store) Create(ctx context.Context, name, ownerID string) (Room, error) {
	r := Room{Name: name, OwnerID: ownerID}
	// Slug acak bisa bentrok (sangat jarang); coba ulang beberapa kali.
	for range 5 {
		r.Slug = newSlug()
		err := s.db.QueryRow(ctx,
			`INSERT INTO rooms (slug, name, owner_id) VALUES ($1, $2, $3) RETURNING id, created_at`,
			r.Slug, name, ownerID,
		).Scan(&r.ID, &r.CreatedAt)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			continue
		}
		return r, err
	}
	return Room{}, errors.New("could not allocate room slug")
}

func (s *Store) BySlug(ctx context.Context, slug string) (Room, error) {
	var r Room
	err := s.db.QueryRow(ctx,
		`SELECT id, slug, name, owner_id, created_at FROM rooms WHERE slug = $1`, slug,
	).Scan(&r.ID, &r.Slug, &r.Name, &r.OwnerID, &r.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Room{}, ErrNotFound
	}
	return r, err
}

// ListByOwner: room milik user, terbaru dulu.
func (s *Store) ListByOwner(ctx context.Context, ownerID string) ([]Room, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, slug, name, owner_id, created_at FROM rooms
		 WHERE owner_id = $1 ORDER BY created_at DESC LIMIT 100`, ownerID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Room, error) {
		var r Room
		err := row.Scan(&r.ID, &r.Slug, &r.Name, &r.OwnerID, &r.CreatedAt)
		return r, err
	})
}

// Delete menghapus room milik ownerID. Room milik orang lain tidak tersentuh (ErrNotFound).
func (s *Store) Delete(ctx context.Context, slug, ownerID string) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM rooms WHERE slug = $1 AND owner_id = $2`, slug, ownerID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Format xxx-xxxx-xxx; alfabet tanpa huruf/angka yang mudah tertukar (0/o, 1/l/i).
func newSlug() string {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789"
	b := make([]byte, 10)
	_, _ = rand.Read(b)
	out := make([]byte, 0, 12)
	for i, v := range b {
		if i == 3 || i == 7 {
			out = append(out, '-')
		}
		out = append(out, alphabet[int(v)%len(alphabet)])
	}
	return string(out)
}
