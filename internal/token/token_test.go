package token

import (
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func keys(t *testing.T) (*Signer, *Verifier) {
	t.Helper()
	privB64, pubB64, err := GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	priv, err := ParsePrivateKey(privB64)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := ParsePublicKey(pubB64)
	if err != nil {
		t.Fatal(err)
	}
	return NewSigner(priv, time.Minute), NewVerifier(pub)
}

func TestSignVerify(t *testing.T) {
	s, v := keys(t)
	raw, err := s.Sign("room-1", "user-1.dev-1", "Budi", RoleHost)
	if err != nil {
		t.Fatal(err)
	}
	c, err := v.Verify(raw)
	if err != nil {
		t.Fatal(err)
	}
	if c.Room != "room-1" || c.ParticipantID() != "user-1.dev-1" || c.Name != "Budi" || c.Role != RoleHost {
		t.Fatalf("unexpected claims: %+v", c)
	}
}

func TestReplayRejected(t *testing.T) {
	s, v := keys(t)
	raw, _ := s.Sign("r", "p", "n", RoleParticipant)
	if _, err := v.Verify(raw); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Verify(raw); !errors.Is(err, ErrReplayed) {
		t.Fatalf("want ErrReplayed, got %v", err)
	}
}

func TestExpiredRejected(t *testing.T) {
	s, v := keys(t)
	s.ttl = -time.Minute
	raw, _ := s.Sign("r", "p", "n", RoleParticipant)
	if _, err := v.Verify(raw); !errors.Is(err, jwt.ErrTokenExpired) {
		t.Fatalf("want expired, got %v", err)
	}
}

func TestOtherKeyRejected(t *testing.T) {
	s, _ := keys(t)
	_, v := keys(t)
	raw, _ := s.Sign("r", "p", "n", RoleParticipant)
	if _, err := v.Verify(raw); err == nil {
		t.Fatal("token signed by another key must be rejected")
	}
}

// Serangan klasik: token HS256 yang "ditandatangani" dengan public key sebagai secret.
func TestAlgorithmLocked(t *testing.T) {
	_, v := keys(t)
	claims := Claims{Room: "r", RegisteredClaims: jwt.RegisteredClaims{
		Issuer: issuer, Audience: jwt.ClaimStrings{audience}, Subject: "p", ID: "x",
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
	}}
	raw, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(v.key))
	if _, err := v.Verify(raw); err == nil {
		t.Fatal("HS256 token must be rejected")
	}
}
