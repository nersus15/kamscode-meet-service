package peer

import "github.com/pion/webrtc/v4"

type OnOfferFunc func(sdp string)

// NewSubscriber membuat PC untuk mengirim media peserta lain ke browser. SFU selalu offerer.
func NewSubscriber(api *webrtc.API, cfg webrtc.Configuration, participantID string, cb Callbacks, onOffer OnOfferFunc) (*Peer, error) {
	p, err := newPeer(api, cfg, participantID, RoleSubscriber, cb)
	if err != nil {
		return nil, err
	}

	log := p.log()
	// Dipicu Pion setelah AddTrack/RemoveTrack. Jika masih menunggu answer (have-local-offer),
	// Pion menunda event ini dan memicunya lagi setelah signaling kembali stable.
	p.PC.OnNegotiationNeeded(func() {
		sdp, err := p.Offer()
		if err != nil {
			log.Warn("create offer", "err", err)
			return
		}
		log.Debug("negotiation needed, offer created")
		if onOffer != nil {
			onOffer(sdp)
		}
	})

	return p, nil
}
