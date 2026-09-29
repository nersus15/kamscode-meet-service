package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/nersus15/kamscode-meet-v2/internal/sfu"
	"github.com/nersus15/kamscode-meet-v2/internal/signaling"
	"github.com/nersus15/kamscode-meet-v2/internal/token"
	"github.com/nersus15/kamscode-meet-v2/internal/turn"
)

type config struct {
	addr           string
	allowedOrigins []string
	publicKey      string
	udpPort        int      // opsi A: satu port UDP untuk semua media (UDP mux)
	publicIPs      []string // opsi A: IP publik yang diumumkan di candidate
	ice            turn.Config
	logLevel       slog.Level
}

func loadConfig() (config, error) {
	cfg := config{
		addr:           env("SFU_ADDR", ":8080"),
		allowedOrigins: splitList(env("SFU_ALLOWED_ORIGINS", "localhost:5173")),
		publicKey:      os.Getenv("JOIN_TOKEN_PUBLIC_KEY"),
		publicIPs:      splitList(env("SFU_PUBLIC_IPS", "")),
		ice:            turn.FromEnv(os.Getenv),
	}
	if p := env("SFU_UDP_PORT", ""); p != "" {
		port, err := strconv.Atoi(p)
		if err != nil || port <= 0 || port > 65535 {
			return cfg, errors.New("SFU_UDP_PORT must be a port number")
		}
		cfg.udpPort = port
	}
	if cfg.publicKey == "" {
		return cfg, errors.New("JOIN_TOKEN_PUBLIC_KEY is required (generate with: go run ./apps/api -genkey)")
	}
	if err := cfg.logLevel.UnmarshalText([]byte(env("LOG_LEVEL", "info"))); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.logLevel})))

	// SFU di belakang CGNAT (opsi B) memakai TURN yang sama dengan browser, supaya punya
	// relay candidate yang pasti bisa dijangkau.
	ice := turn.New(cfg.ice)
	server, err := sfu.NewServer(sfu.Config{
		UDPPort:   cfg.udpPort,
		PublicIPs: cfg.publicIPs,
		ICEServers: func() []webrtc.ICEServer {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var out []webrtc.ICEServer
			for _, s := range ice.ICEServers(ctx) {
				out = append(out, webrtc.ICEServer{URLs: s.URLs, Username: s.Username, Credential: s.Credential})
			}
			return out
		},
	})
	if err != nil {
		slog.Error("new sfu server", "err", err)
		os.Exit(1)
	}

	pub, err := token.ParsePublicKey(cfg.publicKey)
	if err != nil {
		slog.Error("join token key", "err", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.Handle("/ws", signaling.NewHandler(server, token.NewVerifier(pub), cfg.allowedOrigins))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })

	// Koneksi WS sudah di-hijack sehingga tidak ikut ditutup oleh Shutdown.
	// Membatalkan baseCtx membatalkan r.Context() yang dipakai readLoop.
	baseCtx, cancelBase := context.WithCancel(context.Background())
	httpServer := &http.Server{
		Addr:        cfg.addr,
		Handler:     mux,
		BaseContext: func(net.Listener) context.Context { return baseCtx },
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		slog.Info("sfu listening", "addr", cfg.addr, "origins", cfg.allowedOrigins,
			"udp_port", cfg.udpPort, "public_ips", cfg.publicIPs,
			"cloudflare_turn", ice.Cloudflare(), "ice_urls", cfg.ice.StaticURLs)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http server", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		slog.Warn("http shutdown", "err", err)
	}
	cancelBase()
	server.Close()
	slog.Info("stopped")
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

func splitList(s string) []string {
	var out []string
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
