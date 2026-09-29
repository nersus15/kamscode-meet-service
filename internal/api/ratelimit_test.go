package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLimiterBurstThenBlocks(t *testing.T) {
	l := newLimiter(5, 3)
	for i := range 3 {
		if !l.allow("1.2.3.4") {
			t.Fatalf("attempt %d should pass", i+1)
		}
	}
	if l.allow("1.2.3.4") {
		t.Fatal("4th attempt should be limited")
	}
	if !l.allow("5.6.7.8") {
		t.Fatal("other clients have their own bucket")
	}
}

func TestLimitAuthReturns429(t *testing.T) {
	s := &Server{authByIP: newLimiter(1, 2)}
	h := s.limitAuth(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	codes := []int{}
	for range 3 {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
		req.RemoteAddr = "10.0.0.1:5555"
		h(rec, req)
		codes = append(codes, rec.Code)
	}
	if codes[0] != 200 || codes[1] != 200 || codes[2] != http.StatusTooManyRequests {
		t.Fatalf("unexpected codes %v", codes)
	}
}

func TestClientIP(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:5555"
	req.Header.Set("CF-Connecting-IP", "203.0.113.7")
	req.Header.Set("X-Forwarded-For", "198.51.100.1, 10.0.0.9")

	if got := clientIP(req, ""); got != "10.0.0.1" {
		t.Fatalf("without trusted header must use RemoteAddr, got %s", got)
	}
	if got := clientIP(req, "CF-Connecting-IP"); got != "203.0.113.7" {
		t.Fatalf("got %s", got)
	}
	if got := clientIP(req, "X-Forwarded-For"); got != "198.51.100.1" {
		t.Fatalf("got %s", got)
	}
}
