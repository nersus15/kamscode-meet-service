package sfu

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/pion/webrtc/v4"

	"github.com/nersus15/kamscode-meet-v2/internal/sfu/peer"
	"github.com/nersus15/kamscode-meet-v2/internal/sfu/router"
)

type Room struct {
	ID     string
	server *Server

	// subMu menserialkan perubahan langganan (AddTrack/RemoveTrack ke Subscriber).
	// Dipegang selama memanggil Pion, jadi jangan ambil saat memegang Server.mu atau Room.mu.
	// Lock order: subMu -> Room.mu.
	subMu sync.Mutex

	mu           sync.Mutex
	participants map[string]*Participant
}

func newRoom(id string, s *Server) *Room {
	return &Room{
		ID:           id,
		server:       s,
		participants: map[string]*Participant{},
	}
}

// Dipanggil oleh Server.Join (Server.mu sudah dipegang).
// Mengembalikan daftar peserta lain; mendaftar dan mengabari peserta lain terjadi
// di bawah Room.mu yang sama, sehingga tiap pasangan peserta saling tahu tepat sekali.
func (r *Room) addParticipant(participantID, name string, pcConfig webrtc.Configuration, out Outbound) (*Participant, []ParticipantInfo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.participants[participantID]; exists {
		return nil, nil, fmt.Errorf("participant %s already in room %s", participantID, r.ID)
	}

	p := &Participant{
		ID:      participantID,
		Name:    name,
		Room:    r,
		Tracks:  map[string]MediaTrack{},
		out:     out,
		sources: map[string]Source{},
		mic:     true,
		cam:     true,
	}

	// PC failed pada publisher maupun subscriber mengeluarkan participant.
	onState := func(state webrtc.PeerConnectionState) {
		if state != webrtc.PeerConnectionStateFailed {
			return
		}
		// Hanya beri tahu signaling jika p memang dikeluarkan (bukan sesi lama yang sudah diganti).
		if r.server.Leave(p) && out.Closed != nil {
			out.Closed()
		}
	}

	pub, err := peer.NewPublisher(r.server.api, pcConfig, participantID,
		peer.Callbacks{
			OnICECandidate: func(c webrtc.ICECandidateInit) {
				out.SendCandidate(peer.RolePublisher, c)
			},
			OnStateChange: onState,
		},
		func(remote *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
			// Keluar dari goroutine Pion: publishTrack memanggil AddTrack ke PC lain.
			go r.publishTrack(p, remote, receiver)
		},
	)
	if err != nil {
		return nil, nil, err
	}

	sub, err := peer.NewSubscriber(r.server.api, pcConfig, participantID,
		peer.Callbacks{
			OnICECandidate: func(c webrtc.ICECandidateInit) {
				out.SendCandidate(peer.RoleSubscriber, c)
			},
			OnStateChange: onState,
		},
		func(sdp string) {
			if out.SendOffer != nil {
				out.SendOffer(peer.RoleSubscriber, sdp)
			}
		},
	)
	if err != nil {
		if cerr := pub.Close(); cerr != nil {
			slog.Warn("close publisher", "participant", participantID, "err", cerr)
		}
		return nil, nil, err
	}

	p.Publisher = pub
	p.Subscriber = sub

	others := make([]ParticipantInfo, 0, len(r.participants))
	for _, o := range r.participants {
		others = append(others, o.infoLocked())
	}
	info := p.infoLocked()
	r.notifyLocked(nil, func(o Outbound) {
		if o.PeerJoined != nil {
			o.PeerJoined(info)
		}
	})

	r.participants[participantID] = p
	slog.Info("participant joined", "room", r.ID, "participant", participantID, "name", name)
	return p, others, nil
}

// Dipanggil oleh Server.Leave. Tidak menutup PC; itu dilakukan pemanggil di luar lock.
func (r *Room) removeParticipant(p *Participant) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.participants[p.ID] != p {
		return false
	}
	r.removeLocked(p)
	return true
}

// takeParticipant mengeluarkan participant dengan ID tertentu (untuk digantikan).
// Dipanggil oleh Server.Join (Server.mu dipegang). Pemanggil wajib evict() di luar lock.
func (r *Room) takeParticipant(participantID string) *Participant {
	r.mu.Lock()
	defer r.mu.Unlock()

	p, ok := r.participants[participantID]
	if !ok {
		return nil
	}
	r.removeLocked(p)
	slog.Info("participant replaced", "room", r.ID, "participant", participantID)
	return p
}

func (r *Room) removeLocked(p *Participant) {
	delete(r.participants, p.ID)
	r.notifyLocked(nil, func(o Outbound) {
		if o.PeerLeft != nil {
			o.PeerLeft(p.ID)
		}
	})
	slog.Info("participant left", "room", r.ID, "participant", p.ID)
}

// notifyLocked memanggil fn untuk Outbound setiap peserta kecuali except.
// Dipanggil sambil memegang r.mu; callback Outbound tidak memblok (conn.send non-blocking).
func (r *Room) notifyLocked(except *Participant, fn func(Outbound)) {
	for _, o := range r.participants {
		if o != except {
			fn(o.out)
		}
	}
}

func (r *Room) removeAll() []*Participant {
	r.mu.Lock()
	defer r.mu.Unlock()

	all := make([]*Participant, 0, len(r.participants))
	for id, p := range r.participants {
		all = append(all, p)
		delete(r.participants, id)
	}
	return all
}

func (r *Room) isEmpty() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.participants) == 0
}

// subscribeExisting menambahkan semua track peserta lain ke Subscriber p (saat join).
func (r *Room) subscribeExisting(p *Participant) {
	r.subMu.Lock()
	defer r.subMu.Unlock()

	r.mu.Lock()
	if r.participants[p.ID] != p {
		r.mu.Unlock()
		return
	}
	var fwds []*router.Forwarder
	for _, other := range r.participants {
		if other == p {
			continue
		}
		for _, mt := range other.Tracks {
			fwds = append(fwds, mt.Forwarder)
		}
	}
	r.mu.Unlock()

	for _, f := range fwds {
		subscribe(p, f)
	}
}

// publishTrack: track baru dari publisher -> buat Forwarder -> fan-out ke peserta lain.
func (r *Room) publishTrack(p *Participant, remote *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
	mid := midOf(p.Publisher.PC, receiver)
	r.mu.Lock()
	src, ok := p.sources[mid]
	r.mu.Unlock()
	if !ok {
		// Client tanpa peta sources: tebak dari jenis media.
		src = SourceCamera
		if remote.Kind() == webrtc.RTPCodecTypeAudio {
			src = SourceMicrophone
		}
	}

	mt := newMediaTrack(src, remote, receiver)
	key := string(mt.Source)
	log := slog.With("room", r.ID, "participant", p.ID, "source", mt.Source, "mid", mid)

	f, err := router.NewForwarder(remote, key, src.streamID(p.ID), p.Publisher.PC.WriteRTCP, log)
	if err != nil {
		log.Warn("new forwarder", "err", err)
		return
	}
	mt.Forwarder = f

	// RTCP dari publisher (Sender Report) harus dibaca agar diproses interceptor.
	go drainRTCP(receiver)

	r.subMu.Lock()
	defer r.subMu.Unlock()

	r.mu.Lock()
	if r.participants[p.ID] != p {
		r.mu.Unlock()
		return
	}
	if _, exists := p.Tracks[key]; exists {
		// Satu track per source per participant.
		r.mu.Unlock()
		log.Warn("duplicate source, track ignored", "track_id", remote.ID())
		return
	}
	p.Tracks[key] = mt
	others := r.othersLocked(p)
	r.mu.Unlock()

	for _, o := range others {
		subscribe(o, f)
	}

	go r.runForwarder(p, key, mt)
}

// runForwarder memblok selama track hidup, lalu melepas track dari semua subscriber.
func (r *Room) runForwarder(p *Participant, key string, mt MediaTrack) {
	mt.Forwarder.Run()

	r.subMu.Lock()
	defer r.subMu.Unlock()

	r.mu.Lock()
	if cur, ok := p.Tracks[key]; ok && cur.Forwarder == mt.Forwarder {
		delete(p.Tracks, key)
	}
	others := r.othersLocked(p)
	r.mu.Unlock()

	for _, o := range others {
		unsubscribe(o, mt.Forwarder)
	}
}

// othersLocked: semua participant selain p. Harus dipanggil sambil memegang r.mu.
func (r *Room) othersLocked(p *Participant) []*Participant {
	others := make([]*Participant, 0, len(r.participants))
	for _, o := range r.participants {
		if o != p {
			others = append(others, o)
		}
	}
	return others
}

// subscribe dan unsubscribe dipanggil dengan subMu dipegang dan Room.mu dilepas.
// AddTrack/RemoveTrack memicu OnNegotiationNeeded di Subscriber (renegotiation).
func subscribe(p *Participant, f *router.Forwarder) {
	sender, err := p.Subscriber.PC.AddTrack(f.Local())
	if err != nil {
		slog.Debug("add track", "participant", p.ID, "err", err)
		return
	}
	go f.ReadSenderRTCP(sender)
}

func unsubscribe(p *Participant, f *router.Forwarder) {
	for _, s := range p.Subscriber.PC.GetSenders() {
		if s.Track() != f.Local() {
			continue
		}
		if err := p.Subscriber.PC.RemoveTrack(s); err != nil {
			slog.Debug("remove track", "participant", p.ID, "err", err)
		}
	}
}

func midOf(pc *webrtc.PeerConnection, receiver *webrtc.RTPReceiver) string {
	for _, t := range pc.GetTransceivers() {
		if t.Receiver() == receiver {
			return t.Mid()
		}
	}
	return ""
}

func drainRTCP(receiver *webrtc.RTPReceiver) {
	for {
		if _, _, err := receiver.ReadRTCP(); err != nil {
			return
		}
	}
}
