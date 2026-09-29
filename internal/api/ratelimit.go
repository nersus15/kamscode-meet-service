package api

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// limiter: token bucket per kunci (IP atau email), di memori per instance api.
// Cukup untuk menahan brute force password; dengan beberapa replika, batasnya per replika.
type limiter struct {
	rate  float64 // token per detik
	burst float64

	mu      sync.Mutex
	buckets map[string]*bucket
	sweep   time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newLimiter(perMinute, burst int) *limiter {
	return &limiter{rate: float64(perMinute) / 60, burst: float64(burst), buckets: map[string]*bucket{}}
}

func (l *limiter) allow(key string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	// Buang bucket yang sudah penuh kembali, agar map tidak tumbuh tanpa batas.
	if now.Sub(l.sweep) > time.Minute {
		for k, b := range l.buckets {
			if now.Sub(b.last).Seconds()*l.rate >= l.burst {
				delete(l.buckets, k)
			}
		}
		l.sweep = now
	}

	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	b.tokens = min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// clientIP: header proxy (mis. CF-Connecting-IP) hanya dipercaya jika dikonfigurasi,
// karena klien bisa mengisi header itu sendiri bila api diakses langsung.
func clientIP(r *http.Request, header string) string {
	if header != "" {
		if v := r.Header.Get(header); v != "" {
			// X-Forwarded-For: "klien, proxy1, proxy2" -> ambil yang pertama.
			return strings.TrimSpace(strings.Split(v, ",")[0])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
