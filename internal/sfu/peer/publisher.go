package peer

import "github.com/pion/webrtc/v4"

type OnTrackFunc func(remote *webrtc.TrackRemote, receiver *webrtc.RTPReceiver)

func NewPublisher(api *webrtc.API, cfg webrtc.Configuration, participantID string, cb Callbacks, onTrack OnTrackFunc) (*Peer, error) {
	p, err := newPeer(api, cfg, participantID, RolePublisher, cb)
	if err != nil {
		return nil, err
	}

	log := p.log()
	p.PC.OnTrack(func(remote *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		log.Info("track received",
			"kind", remote.Kind().String(),
			"track_id", remote.ID(),
			"ssrc", uint32(remote.SSRC()),
			"codec", remote.Codec().MimeType,
			"payload_type", uint8(remote.PayloadType()),
			"rid", remote.RID(),
		)
		if onTrack != nil {
			onTrack(remote, receiver)
		}
	})

	return p, nil
}
