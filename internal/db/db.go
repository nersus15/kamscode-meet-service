// Package db: koneksi PostgreSQL dan migrasi skema (goose, file SQL di-embed ke binary).
package db

import (
	"context"
	"embed"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

//go:embed migrations/*.sql
var migrations embed.FS

func Open(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

// Migrate menjalankan semua migrasi yang belum diterapkan. Dipanggil saat api start.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	// goose butuh *sql.DB; menutupnya tidak menutup pool.
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()

	fsys, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return err
	}
	// Advisory lock Postgres: beberapa replika api yang start bersamaan tidak saling bertabrakan.
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return err
	}
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, fsys, goose.WithSessionLocker(locker))
	if err != nil {
		return fmt.Errorf("goose provider: %w", err)
	}
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}
