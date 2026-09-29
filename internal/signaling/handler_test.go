package signaling

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/nersus15/kamscode-meet-v2/internal/protocol"
	"github.com/nersus15/kamscode-meet-v2/internal/sfu"
	"github.com/nersus15/kamscode-meet-v2/internal/token"
)

func newTestServer(t *testing.T) (*httptest.Server, *token.Signer) {
	t.Helper()
	privB64, pubB64, err := token.GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	priv, _ := token.ParsePrivateKey(privB64)
	pub, _ := token.ParsePublicKey(pubB64)

	srv, err := sfu.NewServer(sfu.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	hs := httptest.NewServer(NewHandler(srv, token.NewVerifier(pub), nil))
	t.Cleanup(hs.Close)
	return hs, token.NewSigner(priv, time.Minute)
}

func wsURL(hs *httptest.Server, tok string) string {
	return "ws" + strings.TrimPrefix(hs.URL, "http") + "/?token=" + tok
}

func TestJoinWithToken(t *testing.T) {
	hs, signer := newTestServer(t)
	tok, _ := signer.Sign("room-a", "user-1.device-1", "Budi", token.RoleHost)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, wsURL(hs, tok), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()

	_, data, err := ws.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var m protocol.Message
	var joined protocol.JoinedPayload
	if err := json.Unmarshal(data, &m); err != nil || m.Decode(&joined) != nil {
		t.Fatalf("bad message: %s", data)
	}
	if m.Type != protocol.TypeJoined || joined.RoomID != "room-a" || joined.ParticipantID != "user-1.device-1" {
		t.Fatalf("unexpected joined: %s", data)
	}
}

func TestJoinRejected(t *testing.T) {
	hs, signer := newTestServer(t)
	_, otherSigner := newTestServer(t)

	used, _ := signer.Sign("room-a", "user-1.device-1", "Budi", token.RoleParticipant)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, wsURL(hs, used), nil)
	if err != nil {
		t.Fatal(err)
	}
	ws.CloseNow()

	foreign, _ := otherSigner.Sign("room-a", "user-2.device-1", "Ani", token.RoleParticipant)
	colon, _ := signer.Sign("room-a", "user:3", "Eve", token.RoleParticipant)

	cases := map[string]struct {
		tok  string
		code int
	}{
		"missing":     {"", http.StatusUnauthorized},
		"garbage":     {"not-a-jwt", http.StatusUnauthorized},
		"replayed":    {used, http.StatusUnauthorized},
		"foreign key": {foreign, http.StatusUnauthorized},
		"colon in id": {colon, http.StatusBadRequest},
	}
	for name, tc := range cases {
		_, resp, err := websocket.Dial(ctx, wsURL(hs, tc.tok), nil)
		if err == nil {
			t.Errorf("%s: dial must fail", name)
			continue
		}
		if resp == nil || resp.StatusCode != tc.code {
			t.Errorf("%s: want HTTP %d, got %v", name, tc.code, resp)
		}
	}
}

// readUntil membaca pesan sampai menemukan tipe t (pesan lain, mis. offer/candidate, dilewati).
func readUntil(t *testing.T, ctx context.Context, ws *websocket.Conn, want protocol.Type) protocol.Message {
	t.Helper()
	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			t.Fatalf("waiting for %s: %v", want, err)
		}
		var m protocol.Message
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatal(err)
		}
		if m.Type == want {
			return m
		}
	}
}

func dialAs(t *testing.T, ctx context.Context, hs *httptest.Server, signer *token.Signer, pid, name string) *websocket.Conn {
	t.Helper()
	tok, _ := signer.Sign("room-a", pid, name, token.RoleParticipant)
	ws, _, err := websocket.Dial(ctx, wsURL(hs, tok), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.CloseNow() })
	return ws
}

func TestRoster(t *testing.T) {
	hs, signer := newTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	alice := dialAs(t, ctx, hs, signer, "u-alice.dev", "Alice")
	var aj protocol.JoinedPayload
	readUntil(t, ctx, alice, protocol.TypeJoined).Decode(&aj)
	if len(aj.Participants) != 0 {
		t.Fatalf("alice should join an empty room, got %+v", aj.Participants)
	}

	bob := dialAs(t, ctx, hs, signer, "u-bob.dev", "Bob")
	var bj protocol.JoinedPayload
	readUntil(t, ctx, bob, protocol.TypeJoined).Decode(&bj)
	if len(bj.Participants) != 1 || bj.Participants[0].ID != "u-alice.dev" || bj.Participants[0].Name != "Alice" {
		t.Fatalf("bob's roster should contain alice, got %+v", bj.Participants)
	}

	var pj protocol.ParticipantInfo
	readUntil(t, ctx, alice, protocol.TypeParticipantJoined).Decode(&pj)
	if pj.ID != "u-bob.dev" || pj.Name != "Bob" {
		t.Fatalf("alice should be told about bob, got %+v", pj)
	}

	m, _ := protocol.New(protocol.TypeMediaState, protocol.MediaStatePayload{Mic: false, Cam: true})
	data, _ := json.Marshal(m)
	bob.Write(ctx, websocket.MessageText, data)
	var ms protocol.MediaStatePayload
	readUntil(t, ctx, alice, protocol.TypeMediaState).Decode(&ms)
	if ms.ID != "u-bob.dev" || ms.Mic || !ms.Cam {
		t.Fatalf("unexpected media-state: %+v", ms)
	}

	leave, _ := json.Marshal(protocol.Message{Type: protocol.TypeLeave})
	bob.Write(ctx, websocket.MessageText, leave)
	var pl protocol.ParticipantLeftPayload
	readUntil(t, ctx, alice, protocol.TypeParticipantLeft).Decode(&pl)
	if pl.ID != "u-bob.dev" {
		t.Fatalf("unexpected participant-left: %+v", pl)
	}
}

// Join ulang dengan participant ID yang sama (reconnect dari perangkat yang sama)
// menggantikan sesi lama; sesi lama ditutup dengan kode 4001 dan tidak bisa mengeluarkan sesi baru.
func TestRejoinReplacesOldSession(t *testing.T) {
	hs, signer := newTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	bob := dialAs(t, ctx, hs, signer, "u-bob.dev", "Bob")
	readUntil(t, ctx, bob, protocol.TypeJoined)

	old := dialAs(t, ctx, hs, signer, "u-alice.dev", "Alice")
	readUntil(t, ctx, old, protocol.TypeJoined)
	readUntil(t, ctx, bob, protocol.TypeParticipantJoined)

	fresh := dialAs(t, ctx, hs, signer, "u-alice.dev", "Alice")
	var fj protocol.JoinedPayload
	readUntil(t, ctx, fresh, protocol.TypeJoined).Decode(&fj)
	if len(fj.Participants) != 1 || fj.Participants[0].ID != "u-bob.dev" {
		t.Fatalf("fresh session roster should contain only bob, got %+v", fj.Participants)
	}

	// Sesi lama ditutup dengan 4001.
	for {
		if _, _, err := old.Read(ctx); err != nil {
			if code := websocket.CloseStatus(err); code != closeReplaced {
				t.Fatalf("old session: want close %d, got %v", closeReplaced, err)
			}
			break
		}
	}

	// Bob melihat alice keluar lalu masuk lagi — tepat sekali.
	readUntil(t, ctx, bob, protocol.TypeParticipantLeft)
	readUntil(t, ctx, bob, protocol.TypeParticipantJoined)

	// Sesi baru tetap hidup setelah sesi lama tertutup: media-state darinya sampai ke bob.
	m, _ := protocol.New(protocol.TypeMediaState, protocol.MediaStatePayload{Mic: false, Cam: false})
	data, _ := json.Marshal(m)
	fresh.Write(ctx, websocket.MessageText, data)
	var ms protocol.MediaStatePayload
	msg := readUntil(t, ctx, bob, protocol.TypeMediaState)
	msg.Decode(&ms)
	if ms.ID != "u-alice.dev" || ms.Mic || ms.Cam {
		t.Fatalf("unexpected media-state from fresh session: %+v", ms)
	}
}
