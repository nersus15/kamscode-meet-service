package peer

import (
	"fmt"
	"log/slog"

	"github.com/pion/webrtc/v4"
)

type Role string

const (
	RolePublisher  Role = "publisher"
	RoleSubscriber Role = "subscriber"
)

type Peer struct {
	PC            *webrtc.PeerConnection
	ParticipantID string
	Role          Role
}

type Callbacks struct {
	OnICECandidate func(webrtc.ICECandidateInit)
	OnStateChange  func(webrtc.PeerConnectionState)
}

func newPeer(api *webrtc.API, cfg webrtc.Configuration, participantID string, role Role, cb Callbacks) (*Peer, error) {
	pc, err := api.NewPeerConnection(cfg)
	if err != nil {
		return nil, fmt.Errorf("new peer connection: %w", err)
	}

	p := &Peer{PC: pc, ParticipantID: participantID, Role: role}
	p.registerHandlers(cb)
	return p, nil
}

func (p *Peer) log() *slog.Logger {
	return slog.With("participant", p.ParticipantID, "role", p.Role)
}

func (p *Peer) registerHandlers(cb Callbacks) {
	log := p.log()

	p.PC.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			log.Debug("ice gathering complete")
			return
		}
		log.Debug("local candidate", "candidate", c.String())
		if cb.OnICECandidate != nil {
			cb.OnICECandidate(c.ToJSON())
		}
	})

	p.PC.OnSignalingStateChange(func(s webrtc.SignalingState) {
		log.Debug("signaling state", "state", s.String())
	})

	p.PC.OnICEConnectionStateChange(func(s webrtc.ICEConnectionState) {
		log.Info("ice connection state", "state", s.String())
	})

	p.PC.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		log.Info("peer connection state", "state", s.String())
		if cb.OnStateChange != nil {
			cb.OnStateChange(s)
		}
	})
}

func (p *Peer) Answer(offerSDP string) (string, error) {
	offer := webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offerSDP}
	if err := p.PC.SetRemoteDescription(offer); err != nil {
		return "", fmt.Errorf("set remote offer: %w", err)
	}

	answer, err := p.PC.CreateAnswer(nil)
	if err != nil {
		return "", fmt.Errorf("create answer: %w", err)
	}

	if err := p.PC.SetLocalDescription(answer); err != nil {
		return "", fmt.Errorf("set local answer: %w", err)
	}

	return answer.SDP, nil
}

// Offer dipakai saat SFU menjadi offerer (subscriber).
func (p *Peer) Offer() (string, error) {
	offer, err := p.PC.CreateOffer(nil)
	if err != nil {
		return "", fmt.Errorf("create offer: %w", err)
	}

	if err := p.PC.SetLocalDescription(offer); err != nil {
		return "", fmt.Errorf("set local offer: %w", err)
	}

	return offer.SDP, nil
}

func (p *Peer) SetAnswer(answerSDP string) error {
	answer := webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: answerSDP}
	if err := p.PC.SetRemoteDescription(answer); err != nil {
		return fmt.Errorf("set remote answer: %w", err)
	}
	return nil
}

func (p *Peer) AddICECandidate(c webrtc.ICECandidateInit) error {
	return p.PC.AddICECandidate(c)
}

func (p *Peer) Close() error {
	return p.PC.Close()
}
