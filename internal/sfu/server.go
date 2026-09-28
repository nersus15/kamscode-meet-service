package sfu

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/pion/interceptor"
	"github.com/pion/webrtc/v4"

	"github.com/nersus15/kamscode-meet-v2/internal/sfu/peer"
)

type Config struct {
	ICEServers []webrtc.ICEServer
}

// Outbound adalah jalur sfu -> browser, diisi oleh signaling.
type Outbound struct {
	SendCandidate func(role peer.Role, c webrtc.ICECandidateInit)
	// SendOffer dipanggil saat SFU membuat offer (subscriber, renegotiation).
	SendOffer func(role peer.Role, sdp string)
	// Closed dipanggil saat sfu sendiri yang mengeluarkan participant (mis. PC failed).
	Closed func()
}

// Lock order: Server.mu -> Room.mu. Jangan ambil Server.mu saat memegang Room.mu.
type Server struct {
	api      *webrtc.API
	pcConfig webrtc.Configuration

	mu    sync.Mutex
	rooms map[string]*Room
}

func NewServer(cfg Config) (*Server, error) {
	m := &webrtc.MediaEngine{}
	if err := m.RegisterDefaultCodecs(); err != nil {
		return nil, fmt.Errorf("register codecs: %w", err)
	}

	ir := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(m, ir); err != nil {
		return nil, fmt.Errorf("register interceptors: %w", err)
	}

	api := webrtc.NewAPI(
		webrtc.WithMediaEngine(m),
		webrtc.WithInterceptorRegistry(ir),
	)

	return &Server{
		api:      api,
		pcConfig: webrtc.Configuration{ICEServers: cfg.ICEServers},
		rooms:    map[string]*Room{},
	}, nil
}

func (s *Server) Join(roomID, participantID string, out Outbound) (*Participant, error) {
	s.mu.Lock()
	r, ok := s.rooms[roomID]
	if !ok {
		r = newRoom(roomID, s)
		s.rooms[roomID] = r
		slog.Info("room created", "room", roomID)
	}

	p, err := r.addParticipant(participantID, out)
	if err != nil {
		if r.isEmpty() {
			delete(s.rooms, roomID)
		}
		s.mu.Unlock()
		return nil, err
	}
	s.mu.Unlock()

	// Di luar Server.mu: subscribeExisting memanggil Pion (AddTrack).
	r.subscribeExisting(p)
	return p, nil
}

// Leave idempotent: aman dipanggil dari WS close, pesan leave, maupun PC failed.
func (s *Server) Leave(roomID, participantID string) {
	s.mu.Lock()
	r, ok := s.rooms[roomID]
	if !ok {
		s.mu.Unlock()
		return
	}

	p := r.removeParticipant(participantID)
	if r.isEmpty() {
		delete(s.rooms, roomID)
		slog.Info("room closed", "room", roomID)
	}
	s.mu.Unlock()

	// Close di luar lock: Pion bisa memicu callback yang kembali ke Leave.
	if p != nil {
		p.close()
	}
}

func (s *Server) Close() {
	s.mu.Lock()
	var all []*Participant
	for id, r := range s.rooms {
		all = append(all, r.removeAll()...)
		delete(s.rooms, id)
	}
	s.mu.Unlock()

	for _, p := range all {
		p.close()
	}
}
