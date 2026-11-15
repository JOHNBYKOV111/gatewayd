package auth

import (
	"strings"
	"testing"
)

func TestGenerateKey_Format(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	if !strings.HasPrefix(key, KeyPrefix) {
		t.Errorf("key %q should start with %q", key, KeyPrefix)
	}
	// "gw-" + hex(32 байта) = 3 + 64 = 67 символов.
	if len(key) != len(KeyPrefix)+64 {
		t.Errorf("key length = %d, want %d", len(key), len(KeyPrefix)+64)
	}
}

func TestGenerateKey_Unique(t *testing.T) {
	seen := make(map[string]struct{}, 100)
	for i := 0; i < 100; i++ {
		key, err := GenerateKey()
		if err != nil {
			t.Fatalf("GenerateKey: %v", err)
		}
		if _, dup := seen[key]; dup {
			t.Fatalf("duplicate key generated: %s", key)
		}
		seen[key] = struct{}{}
	}
}

func TestHashKey_Deterministic(t *testing.T) {
	key := "gw-abcdef1234567890"
	h1 := HashKey(key)
	h2 := HashKey(key)
	if h1 != h2 {
		t.Errorf("HashKey not deterministic: %q != %q", h1, h2)
	}
	// SHA-256 в hex = 64 символа.
	if len(h1) != 64 {
		t.Errorf("hash length = %d, want 64", len(h1))
	}
}

func TestHashKey_DifferentKeys(t *testing.T) {
	h1 := HashKey("gw-aaaa")
	h2 := HashKey("gw-bbbb")
	if h1 == h2 {
		t.Errorf("different keys should have different hashes")
	}
}

func TestPrefixAndLast4(t *testing.T) {
	key := "gw-1234567890abcdef"
	prefix, last4 := PrefixAndLast4(key)
	if prefix != KeyPrefix {
		t.Errorf("prefix = %q, want %q", prefix, KeyPrefix)
	}
	if last4 != "cdef" {
		t.Errorf("last4 = %q, want cdef", last4)
	}
}

func TestIsValidKeyFormat(t *testing.T) {
	cases := []struct {
		key  string
		want bool
	}{
		{"gw-abcdef1234567890", true},
		{"gw-", false},                 // слишком короткий
		{"sk-abcdef1234567890", false}, // неверный префикс
		{"", false},
	}
	for _, tc := range cases {
		if got := IsValidKeyFormat(tc.key); got != tc.want {
			t.Errorf("IsValidKeyFormat(%q) = %v, want %v", tc.key, got, tc.want)
		}
	}
}
