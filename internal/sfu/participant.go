package sfu

import (
	"log/slog"

	"github.com/nersus15/kamscode-meet-v2/internal/sfu/peer"
)

type Participant struct {
	ID         string
	Room       *Room
	Publisher  *peer.Peer
	Subscriber *peer.Peer
	Tracks     map[string]MediaTrack

	sources map[string]Source // mid -> source dari offer publisher; dijaga Room.mu
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
