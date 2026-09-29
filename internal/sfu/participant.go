package sfu

import (
	"log/slog"

	"github.com/nersus15/kamscode-meet-v2/internal/sfu/peer"
)

type Participant struct {
	ID         string
	Name       string
	Room       *Room
	Publisher  *peer.Peer
	Subscriber *peer.Peer
	Tracks     map[string]MediaTrack

	out Outbound

	// Dijaga Room.mu.
	sources  map[string]Source // mid -> source dari offer publisher
	mic, cam bool              // status yang dilaporkan browser (media-state)
	bundle   string            // bundle kunci E2EE (buram)
	chat     rateLimiter
}

// infoLocked: dipanggil sambil memegang Room.mu.
func (p *Participant) infoLocked() ParticipantInfo {
	return ParticipantInfo{ID: p.ID, Name: p.Name, Mic: p.mic, Cam: p.cam, Bundle: p.bundle}
}

// SetMediaState menyimpan status mic/kamera lalu mengabarkannya ke peserta lain.
func (p *Participant) SetMediaState(mic, cam bool) {
	r := p.Room
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.participants[p.ID] != p {
		return // sudah keluar
	}
	p.mic, p.cam = mic, cam
	info := p.infoLocked()
	r.notifyLocked(p, func(out Outbound) {
		if out.PeerMediaState != nil {
			out.PeerMediaState(info)
		}
	})
}

// SetKeyBundle menyimpan bundle kunci E2EE dan meneruskannya ke peserta lain.
// Peserta yang join belakangan menerimanya lewat roster.
func (p *Participant) SetKeyBundle(bundle string) {
	r := p.Room
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.participants[p.ID] != p {
		return
	}
	p.bundle = bundle
	r.notifyLocked(p, func(out Outbound) {
		if out.PeerKey != nil {
			out.PeerKey(p.ID, bundle)
		}
	})
}

// SetTrackSources dipanggil signaling sebelum Answer, agar OnTrack tahu source tiap mid.
func (p *Participant) SetTrackSources(sources map[string]Source) {
	p.Room.mu.Lock()
	defer p.Room.mu.Unlock()
	for mid, src := range sources {
		p.sources[mid] = src
	}
}

// Peer memilih PeerConnection berdasarkan role dari pesan signaling.
func (p *Participant) Peer(role peer.Role) *peer.Peer {
	switch role {
	case peer.RolePublisher:
		return p.Publisher
	case peer.RoleSubscriber:
		return p.Subscriber
	}
	return nil
}

// evict: sesi lama digantikan. Beri tahu signaling (tutup WS) lalu tutup PC. Aman untuk nil.
func (p *Participant) evict() {
	if p == nil {
		return
	}
	if p.out.Replaced != nil {
		p.out.Replaced()
	}
	p.close()
}

func (p *Participant) close() {
	if p.Publisher != nil {
		if err := p.Publisher.Close(); err != nil {
			slog.Warn("close publisher", "participant", p.ID, "err", err)
		}
	}
	if p.Subscriber != nil {
		if err := p.Subscriber.Close(); err != nil {
			slog.Warn("close subscriber", "participant", p.ID, "err", err)
		}
	}
}
