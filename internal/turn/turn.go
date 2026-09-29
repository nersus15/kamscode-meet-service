// Package turn menyediakan daftar ICE server (STUN/TURN) untuk browser (lewat api) dan SFU.
//
// Dua sumber, dipilih lewat konfigurasi:
//   - Cloudflare Realtime TURN: kredensial sementara dari API Cloudflare, di-cache dan
//     diperbarui sebelum kedaluwarsa (dipakai saat SFU di belakang CGNAT, opsi B).
//   - Statis: daftar URL STUN/TURN dari env (mis. STUN saja, atau coturn sendiri).
package turn

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Server satu entri ICE server; bentuk JSON-nya sama dengan RTCIceServer di browser.
type Server struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

type Config struct {
	// Statis. URL dipisah koma, mis. "stun:stun.cloudflare.com:3478".
	StaticURLs []string
	Username   string // untuk URL turn: statis (opsional)
	Credential string

	// Cloudflare Realtime TURN (jika KeyID diisi, sumber statis diabaikan).
	CloudflareKeyID    string
	CloudflareAPIToken string
	TTL                time.Duration // umur kredensial; default 24 jam
}

type Provider struct {
	cfg      Config
	client   *http.Client
	endpoint string // diganti di test

	mu      sync.Mutex
	cached  []Server
	expires time.Time
}

func New(cfg Config) *Provider {
	if cfg.TTL == 0 {
		cfg.TTL = 24 * time.Hour
	}
	return &Provider{
		cfg:      cfg,
		client:   &http.Client{Timeout: 10 * time.Second},
		endpoint: "https://rtc.live.cloudflare.com/v1/turn/keys/%s/credentials/generate-ice-servers",
	}
}

func (p *Provider) Cloudflare() bool { return p.cfg.CloudflareKeyID != "" }

// ICEServers mengembalikan daftar ICE server. Untuk Cloudflare, kredensial diperbarui jika sisa
// umurnya kurang dari 1/4 TTL; jika API gagal, kredensial lama (bila masih berlaku) tetap dipakai.
func (p *Provider) ICEServers(ctx context.Context) []Server {
	if !p.Cloudflare() {
		if len(p.cfg.StaticURLs) == 0 {
			return nil
		}
		return []Server{{URLs: p.cfg.StaticURLs, Username: p.cfg.Username, Credential: p.cfg.Credential}}
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cached != nil && time.Until(p.expires) > p.cfg.TTL/4 {
		return p.cached
	}
	servers, err := p.fetchCloudflare(ctx)
	if err != nil {
		slog.Warn("cloudflare turn credentials", "err", err)
		if time.Now().Before(p.expires) {
			return p.cached
		}
		return []Server{{URLs: []string{"stun:stun.cloudflare.com:3478"}}} // tanpa TURN, hanya STUN
	}
	p.cached = servers
	p.expires = time.Now().Add(p.cfg.TTL)
	return servers
}

func (p *Provider) fetchCloudflare(ctx context.Context) ([]Server, error) {
	url := fmt.Sprintf(p.endpoint, p.cfg.CloudflareKeyID)
	body, _ := json.Marshal(map[string]int{"ttl": int(p.cfg.TTL.Seconds())})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.cfg.CloudflareAPIToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("status %s", resp.Status)
	}

	// iceServers bisa berupa array atau satu objek, tergantung versi API.
	var out struct {
		ICEServers json.RawMessage `json:"iceServers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	var servers []Server
	if err := json.Unmarshal(out.ICEServers, &servers); err != nil {
		var one Server
		if err := json.Unmarshal(out.ICEServers, &one); err != nil {
			return nil, fmt.Errorf("decode iceServers: %w", err)
		}
		servers = []Server{one}
	}
	for i := range servers {
		servers[i].URLs = dropPort53(servers[i].URLs)
	}
	if len(servers) == 0 {
		return nil, errors.New("empty iceServers")
	}
	return servers, nil
}

// Port 53 diblokir sebagian browser dan hanya memperlambat ICE; Cloudflare menyarankan dibuang.
func dropPort53(urls []string) []string {
	out := urls[:0]
	for _, u := range urls {
		if !strings.Contains(u, ":53?") && !strings.HasSuffix(u, ":53") {
			out = append(out, u)
		}
	}
	return out
}

// FromEnv membaca konfigurasi yang sama untuk api dan sfu:
//
//	CF_TURN_KEY_ID, CF_TURN_API_TOKEN   Cloudflare Realtime TURN (opsi B)
//	ICE_URLS                            daftar statis dipisah koma (default fallback: SFU_STUN_URLS)
//	ICE_USERNAME, ICE_CREDENTIAL        kredensial untuk URL turn: statis
func FromEnv(getenv func(string) string) Config {
	urls := getenv("ICE_URLS")
	if urls == "" {
		urls = getenv("SFU_STUN_URLS")
	}
	var list []string
	for _, u := range strings.Split(urls, ",") {
		if u = strings.TrimSpace(u); u != "" {
			list = append(list, u)
		}
	}
	return Config{
		StaticURLs:         list,
		Username:           getenv("ICE_USERNAME"),
		Credential:         getenv("ICE_CREDENTIAL"),
		CloudflareKeyID:    getenv("CF_TURN_KEY_ID"),
		CloudflareAPIToken: getenv("CF_TURN_API_TOKEN"),
	}
}
