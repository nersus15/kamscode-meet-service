package protocol

import "encoding/json"

type Type string

const (
	TypeJoined    Type = "joined"
	TypeOffer     Type = "offer"
	TypeAnswer    Type = "answer"
	TypeCandidate Type = "candidate"
	TypeLeave     Type = "leave"
	TypeError     Type = "error"

	// Roster: sfu -> browser.
	TypeParticipantJoined Type = "participant-joined"
	TypeParticipantLeft   Type = "participant-left"
	// Status mic/kamera: browser -> sfu (tanpa id), sfu -> browser lain (dengan id).
	TypeMediaState Type = "media-state"
)

type Role string

const (
	RolePublisher  Role = "publisher"
	RoleSubscriber Role = "subscriber"
)

type Message struct {
	Type    Type            `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type JoinedPayload struct {
	RoomID        string `json:"roomId"`
	ParticipantID string `json:"participantId"`
	// Peserta lain yang sudah ada di room saat join.
	Participants []ParticipantInfo `json:"participants"`
}

type ParticipantInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Mic  bool   `json:"mic"`
	Cam  bool   `json:"cam"`
}

type ParticipantLeftPayload struct {
	ID string `json:"id"`
}

type MediaStatePayload struct {
	ID  string `json:"id,omitempty"`
	Mic bool   `json:"mic"`
	Cam bool   `json:"cam"`
}

type SDPPayload struct {
	Role Role   `json:"role"`
	SDP  string `json:"sdp"`
	// Sources: mid -> source ("camera", "microphone", "screen", "screen_audio").
	// Hanya pada offer publisher; ID track dari browser acak sehingga server butuh petunjuk ini.
	Sources map[string]string `json:"sources,omitempty"`
}

type ICECandidate struct {
	Candidate        string  `json:"candidate"`
	SDPMid           *string `json:"sdpMid,omitempty"`
	SDPMLineIndex    *uint16 `json:"sdpMLineIndex,omitempty"`
	UsernameFragment *string `json:"usernameFragment,omitempty"`
}

type CandidatePayload struct {
	Role      Role         `json:"role"`
	Candidate ICECandidate `json:"candidate"`
}

type ErrorPayload struct {
	Message string `json:"message"`
}

func New(t Type, payload any) (Message, error) {
	if payload == nil {
		return Message{Type: t}, nil
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return Message{}, err
	}
	return Message{Type: t, Payload: raw}, nil
}

func (m Message) Decode(v any) error {
	return json.Unmarshal(m.Payload, v)
}
