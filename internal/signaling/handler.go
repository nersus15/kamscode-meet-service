package signaling

import (
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/coder/websocket"
	"github.com/pion/webrtc/v4"

	"github.com/nersus15/kamscode-meet-v2/internal/protocol"
	"github.com/nersus15/kamscode-meet-v2/internal/sfu"
	"github.com/nersus15/kamscode-meet-v2/internal/sfu/peer"
)

type Handler struct {
	server         *sfu.Server
	originPatterns []string
}

// originPatterns: origin lain yang diizinkan (mis. "localhost:5173" untuk dev server Vite).
func NewHandler(server *sfu.Server, originPatterns []string) *Handler {
	return &Handler{server: server, originPatterns: originPatterns}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// TEMPORARY (Milestone 1-2): diganti join token di Milestone 3.
	roomID := r.URL.Query().Get("room")
	userID := r.URL.Query().Get("user")
	if roomID == "" || userID == "" {
		http.Error(w, "room and user are required", http.StatusBadRequest)
		return
	}
	// ":" dipakai sebagai pemisah stream screen ("alice:screen").
	if strings.Contains(userID, ":") {
		http.Error(w, "user must not contain ':'", http.StatusBadRequest)
		return
	}

	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: h.originPatterns})
	if err != nil {
		slog.Warn("ws accept", "err", err)
		return
	}

	log := slog.With("room", roomID, "participant", userID)
	s := &session{
		server:  h.server,
		conn:    newConn(ws, log),
		log:     log,
		roomID:  roomID,
		userID:  userID,
		pending: map[peer.Role][]webrtc.ICECandidateInit{},
		ready:   map[peer.Role]bool{},
		offers:  map[peer.Role]string{},
	}
	s.run(r)
}

// session adalah state satu koneksi WS = satu participant.
type session struct {
	server *sfu.Server
	conn   *conn
	log    *slog.Logger
	roomID string
	userID string
	p      *sfu.Participant

	left atomic.Bool

	// Urutan pesan keluar dijaga di bawah mu:
	//   joined -> offer (subscriber) ; SDP (answer/offer) per role -> candidate role itu.
	mu      sync.Mutex
	joined  bool
	offers  map[peer.Role]string // offer yang dibuat sebelum joined terkirim (maks. satu per role)
	ready   map[peer.Role]bool   // SDP role ini sudah terkirim, candidate boleh lewat
	pending map[peer.Role][]webrtc.ICECandidateInit
}

func (s *session) run(r *http.Request) {
	p, err := s.server.Join(s.roomID, s.userID, sfu.Outbound{
		SendCandidate: s.sendCandidate,
		SendOffer:     s.sendOffer,
		Closed: func() {
			// sfu sudah memanggil Leave sendiri; jangan panggil lagi.
			s.left.Store(true)
			s.conn.close()
		},
	})
	if err != nil {
		// Jangan Leave: participant dengan ID ini milik koneksi lain.
		s.log.Warn("join failed", "err", err)
		s.conn.sendError(err.Error())
		s.conn.close()
		return
	}
	s.p = p

	s.mu.Lock()
	s.sendMessage(protocol.TypeJoined, protocol.JoinedPayload{RoomID: s.roomID, ParticipantID: p.ID})
	s.joined = true
	for role, sdp := range s.offers {
		s.sendSDPLocked(protocol.TypeOffer, role, sdp)
	}
	clear(s.offers)
	s.mu.Unlock()

	err = s.conn.readLoop(r.Context(), s.handle)
	s.log.Info("ws closed", "err", err)
	s.leave()
	s.conn.close()
}

// leave memastikan sesi ini memanggil server.Leave paling banyak sekali,
// agar tidak mengeluarkan participant baru dengan ID yang sama.
func (s *session) leave() {
	if s.left.CompareAndSwap(false, true) {
		s.server.Leave(s.roomID, s.userID)
	}
}

func (s *session) handle(m protocol.Message) {
	switch m.Type {
	case protocol.TypeOffer:
		s.handleOffer(m)
	case protocol.TypeAnswer:
		s.handleAnswer(m)
	case protocol.TypeCandidate:
		s.handleCandidate(m)
	case protocol.TypeLeave:
		s.leave()
		s.conn.close()
	default:
		s.conn.sendError("unsupported message type: " + string(m.Type))
	}
}

func (s *session) handleOffer(m protocol.Message) {
	var in protocol.SDPPayload
	if err := m.Decode(&in); err != nil {
		s.conn.sendError("invalid offer payload")
		return
	}
	if in.Role != protocol.RolePublisher {
		s.conn.sendError("offer only supported for publisher")
		return
	}
	role := peer.RolePublisher

	sources := make(map[string]sfu.Source, len(in.Sources))
	for mid, v := range in.Sources {
		src, ok := sfu.ParseSource(v)
		if !ok {
			s.conn.sendError("unknown source: " + v)
			return
		}
		sources[mid] = src
	}
	s.p.SetTrackSources(sources)

	sdp, err := s.p.Peer(role).Answer(in.SDP)
	if err != nil {
		s.log.Warn("answer failed", "err", err)
		s.conn.sendError(err.Error())
		return
	}

	s.mu.Lock()
	s.sendSDPLocked(protocol.TypeAnswer, role, sdp)
	s.mu.Unlock()
}

// Browser menjawab offer SFU pada Subscriber PC.
func (s *session) handleAnswer(m protocol.Message) {
	var in protocol.SDPPayload
	if err := m.Decode(&in); err != nil {
		s.conn.sendError("invalid answer payload")
		return
	}
	if in.Role != protocol.RoleSubscriber {
		s.conn.sendError("answer only supported for subscriber")
		return
	}

	if err := s.p.Subscriber.SetAnswer(in.SDP); err != nil {
		s.log.Warn("set answer failed", "err", err)
		s.conn.sendError(err.Error())
	}
}

// Dipanggil dari goroutine Pion (OnNegotiationNeeded di Subscriber).
func (s *session) sendOffer(role peer.Role, sdp string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.joined {
		s.offers[role] = sdp
		return
	}
	s.sendSDPLocked(protocol.TypeOffer, role, sdp)
}

// sendSDPLocked mengirim SDP lalu candidate yang tertahan untuk role itu.
// Karena memakai lock yang sama dengan sendCandidate, browser selalu menerima
// SDP sebelum candidate role tersebut.
func (s *session) sendSDPLocked(t protocol.Type, role peer.Role, sdp string) {
	s.sendMessage(t, protocol.SDPPayload{Role: toProtocolRole(role), SDP: sdp})
	for _, c := range s.pending[role] {
		s.sendCandidateNow(role, c)
	}
	delete(s.pending, role)
	s.ready[role] = true
}

func (s *session) handleCandidate(m protocol.Message) {
	var in protocol.CandidatePayload
	if err := m.Decode(&in); err != nil {
		s.conn.sendError("invalid candidate payload")
		return
	}
	role, ok := toPeerRole(in.Role)
	if !ok {
		s.conn.sendError("unknown role")
		return
	}
	pr := s.p.Peer(role)
	if pr == nil {
		s.conn.sendError("no peer connection for role " + string(in.Role))
		return
	}

	c := webrtc.ICECandidateInit{
		Candidate:        in.Candidate.Candidate,
		SDPMid:           in.Candidate.SDPMid,
		SDPMLineIndex:    in.Candidate.SDPMLineIndex,
		UsernameFragment: in.Candidate.UsernameFragment,
	}
	if err := pr.AddICECandidate(c); err != nil {
		s.log.Warn("add ice candidate", "role", role, "err", err)
		s.conn.sendError(err.Error())
	}
}

// Dipanggil dari goroutine Pion (OnICECandidate).
func (s *session) sendCandidate(role peer.Role, c webrtc.ICECandidateInit) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ready[role] {
		s.pending[role] = append(s.pending[role], c)
		return
	}
	s.sendCandidateNow(role, c)
}

func (s *session) sendCandidateNow(role peer.Role, c webrtc.ICECandidateInit) {
	s.sendMessage(protocol.TypeCandidate, protocol.CandidatePayload{
		Role: toProtocolRole(role),
		Candidate: protocol.ICECandidate{
			Candidate:        c.Candidate,
			SDPMid:           c.SDPMid,
			SDPMLineIndex:    c.SDPMLineIndex,
			UsernameFragment: c.UsernameFragment,
		},
	})
}

func (s *session) sendMessage(t protocol.Type, payload any) {
	m, err := protocol.New(t, payload)
	if err != nil {
		s.log.Error("encode message", "type", t, "err", err)
		return
	}
	s.conn.send(m)
}

func toPeerRole(r protocol.Role) (peer.Role, bool) {
	switch r {
	case protocol.RolePublisher:
		return peer.RolePublisher, true
	case protocol.RoleSubscriber:
		return peer.RoleSubscriber, true
	}
	return "", false
}

func toProtocolRole(r peer.Role) protocol.Role {
	if r == peer.RoleSubscriber {
		return protocol.RoleSubscriber
	}
	return protocol.RolePublisher
}
