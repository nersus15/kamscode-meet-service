// Package token: join token untuk SFU. api menandatangani (private key Ed25519),
// sfu hanya memverifikasi (public key), sehingga sfu tidak bisa membuat token.
package token

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	issuer   = "kamscode-meet-api"
	audience = "kamscode-meet-sfu"
)

type Role string

const (
	RoleHost        Role = "host"
	RoleParticipant Role = "participant"
)

// Claims: Subject = participant ID, ID (jti) = ID unik token untuk cegah replay.
type Claims struct {
	Room string `json:"room"`
	Name string `json:"name"`
	Role Role   `json:"role"`
	jwt.RegisteredClaims
}

func (c *Claims) ParticipantID() string { return c.Subject }

type Signer struct {
	key ed25519.PrivateKey
	ttl time.Duration
}

func NewSigner(key ed25519.PrivateKey, ttl time.Duration) *Signer {
	return &Signer{key: key, ttl: ttl}
}

func (s *Signer) Sign(room, participantID, name string, role Role) (string, error) {
	jti := make([]byte, 16)
	if _, err := rand.Read(jti); err != nil {
		return "", err
	}
	now := time.Now()
	claims := Claims{
		Room: room,
		Name: name,
		Role: role,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer,
			Audience:  jwt.ClaimStrings{audience},
			Subject:   participantID,
			ID:        base64.RawURLEncoding.EncodeToString(jti),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.ttl)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims).SignedString(s.key)
}

var ErrReplayed = errors.New("token already used")

// Verifier memeriksa tanda tangan, issuer, audience, exp, dan memastikan token sekali pakai.
type Verifier struct {
	key ed25519.PublicKey

	mu   sync.Mutex
	used map[string]time.Time // jti -> exp
}

func NewVerifier(key ed25519.PublicKey) *Verifier {
	return &Verifier{key: key, used: map[string]time.Time{}}
}

func (v *Verifier) Verify(raw string) (*Claims, error) {
	var claims Claims
	_, err := jwt.ParseWithClaims(raw, &claims,
		func(*jwt.Token) (any, error) { return v.key, nil },
		// Kunci algoritma: token dengan alg lain (mis. "none" atau HS256) ditolak.
		jwt.WithValidMethods([]string{jwt.SigningMethodEdDSA.Alg()}),
		jwt.WithIssuer(issuer),
		jwt.WithAudience(audience),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(5*time.Second),
	)
	if err != nil {
		return nil, err
	}
	if claims.ID == "" || claims.Subject == "" || claims.Room == "" {
		return nil, errors.New("token missing required claims")
	}

	v.mu.Lock()
	defer v.mu.Unlock()
	now := time.Now()
	for jti, exp := range v.used {
		if now.After(exp) {
			delete(v.used, jti)
		}
	}
	if _, ok := v.used[claims.ID]; ok {
		return nil, ErrReplayed
	}
	v.used[claims.ID] = claims.ExpiresAt.Add(5 * time.Second)
	return &claims, nil
}

// Kunci disimpan di env sebagai base64 standar: private = seed 32 byte, public = 32 byte.

func ParsePrivateKey(b64 string) (ed25519.PrivateKey, error) {
	seed, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("decode private key: %w", err)
	}
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("private key must be %d-byte seed, got %d", ed25519.SeedSize, len(seed))
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

func ParsePublicKey(b64 string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("decode public key: %w", err)
	}
	if len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("public key must be %d bytes, got %d", ed25519.PublicKeySize, len(b))
	}
	return ed25519.PublicKey(b), nil
}

// GenerateKeys menghasilkan pasangan kunci dalam format env (dipakai `api -genkey`).
func GenerateKeys() (privateB64, publicB64 string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(priv.Seed()), base64.StdEncoding.EncodeToString(pub), nil
}
