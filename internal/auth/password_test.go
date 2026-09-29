package auth

import (
	"strings"
	"testing"
)

func TestPassword(t *testing.T) {
	hash, err := HashPassword("rahasia-panjang")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("unexpected format: %s", hash)
	}
	if ok, _ := VerifyPassword("rahasia-panjang", hash); !ok {
		t.Fatal("correct password rejected")
	}
	if ok, _ := VerifyPassword("salah", hash); ok {
		t.Fatal("wrong password accepted")
	}
	other, _ := HashPassword("rahasia-panjang")
	if other == hash {
		t.Fatal("salt must make hashes differ")
	}
}
