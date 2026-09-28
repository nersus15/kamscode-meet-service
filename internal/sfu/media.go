package sfu

import (
	"github.com/pion/webrtc/v4"

	"github.com/nersus15/kamscode-meet-v2/internal/sfu/router"
)

type Source string

const (
	SourceMicrophone  Source = "microphone"
	SourceCamera      Source = "camera"
	SourceScreen      Source = "screen"
	SourceScreenAudio Source = "screen_audio"
)

func ParseSource(s string) (Source, bool) {
	switch src := Source(s); src {
	case SourceMicrophone, SourceCamera, SourceScreen, SourceScreenAudio:
		return src, true
	}
	return "", false
}

// streamID yang dilihat subscriber: kamera+mic dalam satu stream, screen dalam stream terpisah.
func (s Source) streamID(participantID string) string {
	if s == SourceScreen || s == SourceScreenAudio {
		return participantID + ":screen"
	}
	return participantID
}

type MediaTrack struct {
	Source    Source
	Remote    *webrtc.TrackRemote
	Receiver  *webrtc.RTPReceiver
	Forwarder *router.Forwarder
}

func newMediaTrack(src Source, remote *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) MediaTrack {
	return MediaTrack{Source: src, Remote: remote, Receiver: receiver}
}
