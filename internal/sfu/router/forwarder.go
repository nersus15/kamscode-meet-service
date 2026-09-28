package router

import (
	"log/slog"
	"sync"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
)

const pliInterval = 500 * time.Millisecond

// Forwarder meneruskan RTP dari satu track publisher ke satu TrackLocalStaticRTP
// yang dipakai bersama oleh semua subscriber (setiap AddTrack = satu binding di Pion).
type Forwarder struct {
	remote    *webrtc.TrackRemote
	local     *webrtc.TrackLocalStaticRTP
	writeRTCP func([]rtcp.Packet) error // ke Publisher PC, untuk PLI
	log       *slog.Logger

	mu      sync.Mutex
	lastPLI time.Time
}

// trackID dan streamID muncul di SDP subscriber sebagai a=msid:<streamID> <trackID>.
func NewForwarder(remote *webrtc.TrackRemote, trackID, streamID string,
	writeRTCP func([]rtcp.Packet) error, log *slog.Logger) (*Forwarder, error) {
	local, err := webrtc.NewTrackLocalStaticRTP(remote.Codec().RTPCodecCapability, trackID, streamID)
	if err != nil {
		return nil, err
	}
	return &Forwarder{
		remote:    remote,
		local:     local,
		writeRTCP: writeRTCP,
		log:       log.With("track", trackID, "ssrc", uint32(remote.SSRC())),
	}, nil
}

func (f *Forwarder) Local() *webrtc.TrackLocalStaticRTP {
	return f.local
}

// Run memblok sampai track publisher berakhir (PC publisher ditutup).
func (f *Forwarder) Run() {
	var count uint64
	for {
		pkt, _, err := f.remote.ReadRTP()
		if err != nil {
			f.log.Info("forwarder stopped", "packets", count, "err", err)
			return
		}
		count++
		if count == 1 {
			f.log.Info("forwarding started", "pt", pkt.PayloadType)
		}

		// ID header extension dinegosiasikan per PC; ID dari publisher tidak berlaku di subscriber.
		// Interceptor Pion di sisi subscriber menambahkan extension-nya sendiri (mis. transport-cc).
		pkt.Extension = false
		pkt.Extensions = nil

		// SSRC dan payload type ditulis ulang Pion per binding. Error satu subscriber
		// tidak menghentikan pengiriman ke subscriber lain.
		if err := f.local.WriteRTP(pkt); err != nil {
			f.log.Debug("write rtp", "err", err)
		}
	}
}

// RequestKeyframe mengirim PLI ke publisher, dibatasi satu kali per pliInterval.
func (f *Forwarder) RequestKeyframe() {
	if f.remote.Kind() != webrtc.RTPCodecTypeVideo {
		return
	}

	f.mu.Lock()
	if time.Since(f.lastPLI) < pliInterval {
		f.mu.Unlock()
		return
	}
	f.lastPLI = time.Now()
	f.mu.Unlock()

	err := f.writeRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(f.remote.SSRC())}})
	if err != nil {
		f.log.Debug("write pli", "err", err)
		return
	}
	f.log.Debug("pli sent to publisher")
}

// ReadSenderRTCP membaca RTCP dari satu subscriber (lewat RTPSender miliknya).
// Membaca juga wajib agar interceptor (mis. NACK responder) bekerja.
// Berhenti saat sender dihentikan (RemoveTrack atau PC subscriber ditutup).
func (f *Forwarder) ReadSenderRTCP(sender *webrtc.RTPSender) {
	for {
		pkts, _, err := sender.ReadRTCP()
		if err != nil {
			return
		}
		for _, p := range pkts {
			switch p.(type) {
			case *rtcp.PictureLossIndication, *rtcp.FullIntraRequest:
				f.RequestKeyframe()
			}
		}
	}
}
