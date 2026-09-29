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
	// Replaced dipanggil saat sesi ini digantikan join baru dengan participant ID yang sama.
	Replaced func()

	// Roster. Dipanggil sambil memegang Room.mu agar urutan join/leave konsisten
	// untuk semua peserta, jadi implementasi tidak boleh memblok atau memanggil balik sfu.
	PeerJoined     func(ParticipantInfo)
	PeerLeft       func(participantID string)
	PeerMediaState func(ParticipantInfo)
}

// ParticipantInfo: data seorang peserta yang dilihat peserta lain.
type ParticipantInfo struct {
	ID   string
	Name string
	Mic  bool
	Cam  bool
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

// Join mengembalikan participant baru beserta daftar peserta lain yang sudah ada.
// Jika participant ID yang sama masih ada (reconnect dari perangkat yang sama),
// sesi lama dikeluarkan dan digantikan.
func (s *Server) Join(roomID, participantID, name string, out Outbound) (*Participant, []ParticipantInfo, error) {
	s.mu.Lock()
	r, ok := s.rooms[roomID]
	if !ok {
		r = newRoom(roomID, s)
		s.rooms[roomID] = r
		slog.Info("room created", "room", roomID)
	}

	old := r.takeParticipant(participantID)
	p, others, err := r.addParticipant(participantID, name, out)
	if err != nil {
		if r.isEmpty() {
			delete(s.rooms, roomID)
		}
		s.mu.Unlock()
		old.evict()
		return nil, nil, err
	}
	s.mu.Unlock()

	// Di luar lock: menutup PC lama dan AddTrack memanggil Pion.
	old.evict()
	r.subscribeExisting(p)
	return p, others, nil
}

// Leave idempotent: aman dipanggil dari WS close, pesan leave, maupun PC failed.
// Membandingkan pointer, bukan ID: sesi lama yang sudah digantikan tidak bisa
// mengeluarkan sesi baru dengan ID yang sama. Mengembalikan true jika p benar-benar dikeluarkan.
func (s *Server) Leave(p *Participant) bool {
	s.mu.Lock()
	r := p.Room
	if s.rooms[r.ID] != r || !r.removeParticipant(p) {
		s.mu.Unlock()
		return false
	}
	if r.isEmpty() {
		delete(s.rooms, r.ID)
		slog.Info("room closed", "room", r.ID)
	}
	s.mu.Unlock()

	// Close di luar lock: Pion bisa memicu callback yang kembali ke Leave.
	p.close()
	return true
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
