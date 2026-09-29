package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/nersus15/kamscode-meet-v2/internal/api"
	"github.com/nersus15/kamscode-meet-v2/internal/auth"
	"github.com/nersus15/kamscode-meet-v2/internal/db"
	"github.com/nersus15/kamscode-meet-v2/internal/room"
	"github.com/nersus15/kamscode-meet-v2/internal/token"
	"github.com/nersus15/kamscode-meet-v2/internal/user"
)

type config struct {
	addr         string
	databaseURL  string
	privateKey   string
	sfuURL       string
	cookieSecure bool
	joinTTL      time.Duration
	sessionTTL   time.Duration
	logLevel     slog.Level
}

func loadConfig() (config, error) {
	cfg := config{
		addr:        env("API_ADDR", ":8081"),
		databaseURL: os.Getenv("DATABASE_URL"),
		privateKey:  os.Getenv("JOIN_TOKEN_PRIVATE_KEY"),
		sfuURL:      env("SFU_PUBLIC_URL", "ws://localhost:8080/ws"),
		joinTTL:     60 * time.Second,
		sessionTTL:  30 * 24 * time.Hour,
	}
	if cfg.databaseURL == "" {
		return cfg, errors.New("DATABASE_URL is required")
	}
	if cfg.privateKey == "" {
		return cfg, errors.New("JOIN_TOKEN_PRIVATE_KEY is required (generate with: go run ./apps/api -genkey)")
	}
	secure, err := strconv.ParseBool(env("COOKIE_SECURE", "false"))
	if err != nil {
		return cfg, fmt.Errorf("COOKIE_SECURE: %w", err)
	}
	cfg.cookieSecure = secure
	if err := cfg.logLevel.UnmarshalText([]byte(env("LOG_LEVEL", "info"))); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func main() {
	genkey := flag.Bool("genkey", false, "print a new Ed25519 key pair for join tokens and exit")
	flag.Parse()
	if *genkey {
		priv, pub, err := token.GenerateKeys()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("JOIN_TOKEN_PRIVATE_KEY=%s   # api saja\nJOIN_TOKEN_PUBLIC_KEY=%s    # sfu\n", priv, pub)
		return
	}

	if err := run(); err != nil {
		slog.Error("api", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.logLevel})))

	key, err := token.ParsePrivateKey(cfg.privateKey)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.Open(ctx, cfg.databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}

	srv := api.NewServer(
		api.Config{SFUURL: cfg.sfuURL, CookieSecure: cfg.cookieSecure},
		user.NewStore(pool),
		auth.NewSessionStore(pool, cfg.sessionTTL),
		room.NewStore(pool),
		token.NewSigner(key, cfg.joinTTL),
	)
	httpServer := &http.Server{
		Addr:              cfg.addr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errc := make(chan error, 1)
	go func() {
		slog.Info("api listening", "addr", cfg.addr, "sfu", cfg.sfuURL)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return httpServer.Shutdown(shutdownCtx)
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}
