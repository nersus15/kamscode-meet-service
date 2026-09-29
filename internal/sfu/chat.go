package sfu

import (
	"errors"
	"time"
)

// Chat E2EE: sfu hanya mengarahkan amplop terenkripsi ke penerima yang ada di room.
// Isi pesan tidak pernah terbaca di sini.

const (
	MaxChatRecipients = 24
	chatRate          = 5  // pesan per detik
	chatBurst         = 10 // lonjakan maksimum
)

var (
	ErrChatNoRecipients = errors.New("no recipients in room")
	ErrChatRateLimited  = errors.New("sending messages too fast")
	ErrChatNotInRoom    = errors.New("sender is not in the room")
)

type ChatMessage struct {
	ID        string
	To        []string
	Broadcast bool
	Envelopes map[string]string // participant ID -> amplop (ciphertext)
}

// ChatDelivery: satu pesan untuk satu peserta. Envelope kosong = gema ke pengirim.
type ChatDelivery struct {
	ID        string
	From      string
	To        []string // penerima yang benar-benar menerima
	Broadcast bool
	TS        time.Time
	Envelope  string
}

// SendChat mengirim amplop ke tiap penerima yang ada di room (tiap penerima hanya
// menerima amplopnya sendiri), lalu gema ke pengirim. Penerima tak dikenal diabaikan.
func (p *Participant) SendChat(msg ChatMessage) error {
	r := p.Room
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.participants[p.ID] != p {
		return ErrChatNotInRoom
	}
	if !p.chat.allow(time.Now()) {
		return ErrChatRateLimited
	}

	var targets []*Participant
	var to []string
	for _, id := range msg.To {
		o, ok := r.participants[id]
		if !ok || o == p || msg.Envelopes[id] == "" {
			continue
		}
		targets = append(targets, o)
		to = append(to, id)
	}
	if len(targets) == 0 {
		return ErrChatNoRecipients
	}

	d := ChatDelivery{ID: msg.ID, From: p.ID, To: to, Broadcast: msg.Broadcast, TS: time.Now()}
	for _, o := range targets {
		if o.out.Chat != nil {
			od := d
			od.Envelope = msg.Envelopes[o.ID]
			o.out.Chat(od)
		}
	}
	if p.out.Chat != nil {
		p.out.Chat(d)
	}
	return nil
}

// rateLimiter: token bucket sederhana; dijaga Room.mu.
type rateLimiter struct {
	tokens float64
	last   time.Time
}

func (l *rateLimiter) allow(now time.Time) bool {
	if l.last.IsZero() {
		l.tokens = chatBurst
	} else {
		l.tokens = min(chatBurst, l.tokens+now.Sub(l.last).Seconds()*chatRate)
	}
	l.last = now
	if l.tokens < 1 {
		return false
	}
	l.tokens--
	return true
}
