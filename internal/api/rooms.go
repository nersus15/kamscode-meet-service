package api

import (
	"errors"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/nersus15/kamscode-meet-v2/internal/room"
	"github.com/nersus15/kamscode-meet-v2/internal/token"
	"github.com/nersus15/kamscode-meet-v2/internal/turn"
	"github.com/nersus15/kamscode-meet-v2/internal/user"
)

// deviceID dibuat browser (localStorage). Tanpa ":" (pemisah stream screen) dan "." (pemisah pid).
var deviceIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

func (s *Server) createRoom(w http.ResponseWriter, r *http.Request, u user.User) {
	var in struct {
		Name string `json:"name"`
	}
	if err := decode(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || utf8.RuneCountInString(name) > 80 {
		writeError(w, http.StatusBadRequest, "name must be 1-80 characters")
		return
	}
	rm, err := s.rooms.Create(r.Context(), name, u.ID)
	if err != nil {
		internalError(w, "create room", err)
		return
	}
	writeJSON(w, http.StatusCreated, rm)
}

func (s *Server) listRooms(w http.ResponseWriter, r *http.Request, u user.User) {
	rooms, err := s.rooms.ListByOwner(r.Context(), u.ID)
	if err != nil {
		internalError(w, "list rooms", err)
		return
	}
	if rooms == nil {
		rooms = []room.Room{} // JSON [] bukan null
	}
	writeJSON(w, http.StatusOK, rooms)
}

// deleteRoom: hanya pemilik. Room yang dihapus menolak join baru; peserta yang sedang di
// dalam tidak dikeluarkan (SFU tidak mengakses DB), mereka lanjut sampai keluar sendiri.
func (s *Server) deleteRoom(w http.ResponseWriter, r *http.Request, u user.User) {
	rm, ok := s.loadRoom(w, r)
	if !ok {
		return
	}
	if rm.OwnerID != u.ID {
		writeError(w, http.StatusForbidden, "only the room owner can delete it")
		return
	}
	err := s.rooms.Delete(r.Context(), rm.Slug, u.ID)
	if errors.Is(err, room.ErrNotFound) {
		writeError(w, http.StatusNotFound, "room not found") // terhapus di antara dua query
		return
	}
	if err != nil {
		internalError(w, "delete room", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getRoom(w http.ResponseWriter, r *http.Request, _ user.User) {
	rm, ok := s.loadRoom(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, rm)
}

type joinResponse struct {
	Token         string        `json:"token"`
	SFUURL        string        `json:"sfuUrl"`
	ParticipantID string        `json:"participantId"`
	Role          token.Role    `json:"role"`
	Room          room.Room     `json:"room"`
	ICEServers    []turn.Server `json:"iceServers"` // STUN/TURN untuk kedua PeerConnection browser
}

// joinRoom menerbitkan join token sekali pakai (umur pendek) untuk membuka WebSocket ke SFU.
func (s *Server) joinRoom(w http.ResponseWriter, r *http.Request, u user.User) {
	var in struct {
		DeviceID string `json:"deviceId"`
	}
	if err := decode(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if !deviceIDPattern.MatchString(in.DeviceID) {
		writeError(w, http.StatusBadRequest, "invalid deviceId")
		return
	}
	rm, ok := s.loadRoom(w, r)
	if !ok {
		return
	}

	role := token.RoleParticipant
	if rm.OwnerID == u.ID {
		role = token.RoleHost
	}
	// Satu akun di dua perangkat = dua peserta; perangkat yang sama = ID sama (rejoin menggantikan).
	pid := u.ID + "." + in.DeviceID

	tok, err := s.signer.Sign(rm.Slug, pid, u.Name, role)
	if err != nil {
		internalError(w, "sign join token", err)
		return
	}
	writeJSON(w, http.StatusOK, joinResponse{
		Token:         tok,
		SFUURL:        s.cfg.SFUURL,
		ParticipantID: pid,
		Role:          role,
		Room:          rm,
		ICEServers:    s.ice.ICEServers(r.Context()),
	})
}

func (s *Server) loadRoom(w http.ResponseWriter, r *http.Request) (room.Room, bool) {
	rm, err := s.rooms.BySlug(r.Context(), r.PathValue("slug"))
	if errors.Is(err, room.ErrNotFound) {
		writeError(w, http.StatusNotFound, "room not found")
		return room.Room{}, false
	}
	if err != nil {
		internalError(w, "load room", err)
		return room.Room{}, false
	}
	return rm, true
}
