package turn

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestStatic(t *testing.T) {
	p := New(Config{StaticURLs: []string{"stun:stun.example.org:3478"}})
	got := p.ICEServers(context.Background())
	want := []Server{{URLs: []string{"stun:stun.example.org:3478"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
	if New(Config{}).ICEServers(context.Background()) != nil {
		t.Fatal("no urls should give no servers")
	}
}

func TestDropPort53(t *testing.T) {
	got := dropPort53([]string{
		"turn:turn.cloudflare.com:3478?transport=udp",
		"turn:turn.cloudflare.com:53?transport=udp",
		"turns:turn.cloudflare.com:443?transport=tcp",
		"stun:stun.cloudflare.com:53",
	})
	want := []string{"turn:turn.cloudflare.com:3478?transport=udp", "turns:turn.cloudflare.com:443?transport=tcp"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestCloudflare(t *testing.T) {
	calls := 0
	fail := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer tok" || r.Method != http.MethodPost || !strings.Contains(r.URL.Path, "/key-1/") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write([]byte(`{"iceServers":[{"urls":["stun:stun.cloudflare.com:3478","stun:stun.cloudflare.com:53"]},` +
			`{"urls":["turn:turn.cloudflare.com:3478?transport=udp","turns:turn.cloudflare.com:443?transport=tcp"],"username":"u","credential":"c"}]}`))
	}))
	defer srv.Close()

	p := New(Config{CloudflareKeyID: "key-1", CloudflareAPIToken: "tok"})
	p.endpoint = srv.URL + "/v1/turn/keys/%s/credentials/generate-ice-servers"
	ctx := context.Background()

	got := p.ICEServers(ctx)
	if len(got) != 2 || got[1].Username != "u" || len(got[0].URLs) != 1 {
		t.Fatalf("unexpected servers: %+v", got)
	}
	p.ICEServers(ctx)
	if calls != 1 {
		t.Fatalf("credentials should be cached, api called %d times", calls)
	}

	// Kredensial hampir kedaluwarsa + API gagal -> kredensial lama tetap dipakai.
	p.expires = time.Now().Add(time.Minute)
	fail = true
	if again := p.ICEServers(ctx); !reflect.DeepEqual(again, got) {
		t.Fatalf("should fall back to cached credentials, got %+v", again)
	}
}

func TestCloudflareSingleObject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"iceServers":{"urls":["turn:turn.cloudflare.com:3478"],"username":"u","credential":"c"}}`))
	}))
	defer srv.Close()
	p := New(Config{CloudflareKeyID: "k", CloudflareAPIToken: "t"})
	p.endpoint = srv.URL + "/%s"
	if got := p.ICEServers(context.Background()); len(got) != 1 || got[0].Credential != "c" {
		t.Fatalf("unexpected servers: %+v", got)
	}
}
