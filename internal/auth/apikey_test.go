package auth

import (
	"strings"
	"testing"
)

func TestGenerateAPIKey_FormatAndUniqueness(t *testing.T) {
	pepper := []byte("test-pepper")

	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		k, err := GenerateAPIKey(pepper)
		if err != nil {
			t.Fatalf("GenerateAPIKey: %v", err)
		}
		if !strings.HasPrefix(k.Raw, KeyPrefix) {
			t.Fatalf("raw key missing prefix: %q", k.Raw)
		}
		if seen[k.Raw] {
			t.Fatalf("duplicate key generated: %q", k.Raw)
		}
		seen[k.Raw] = true

		if len(k.DisplayPrefix) != DisplayPrefixLen {
			t.Fatalf("display prefix length = %d, want %d", len(k.DisplayPrefix), DisplayPrefixLen)
		}
		if !strings.HasPrefix(k.Raw, k.DisplayPrefix) {
			t.Fatalf("display prefix %q is not a prefix of raw key %q", k.DisplayPrefix, k.Raw)
		}
		if len(k.HMAC) != 32 { // sha256 输出 32 字节
			t.Fatalf("hmac length = %d, want 32", len(k.HMAC))
		}
	}
}

func TestGenerateAPIKey_EmptyPepperRejected(t *testing.T) {
	if _, err := GenerateAPIKey(nil); err == nil {
		t.Fatal("expected error for empty pepper, got nil")
	}
}

func TestComputeHMAC_MatchesGeneration(t *testing.T) {
	pepper := []byte("test-pepper")
	k, err := GenerateAPIKey(pepper)
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}

	got, err := ComputeHMAC(pepper, k.Raw)
	if err != nil {
		t.Fatalf("ComputeHMAC: %v", err)
	}
	if !EqualHMAC(got, k.HMAC) {
		t.Fatalf("ComputeHMAC result does not match stored HMAC")
	}
}

func TestComputeHMAC_DifferentPepperMismatches(t *testing.T) {
	k, err := GenerateAPIKey([]byte("pepper-a"))
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	got, err := ComputeHMAC([]byte("pepper-b"), k.Raw)
	if err != nil {
		t.Fatalf("ComputeHMAC: %v", err)
	}
	if EqualHMAC(got, k.HMAC) {
		t.Fatal("expected HMAC mismatch with different pepper")
	}
}

func TestComputeHMAC_RejectsMalformedKey(t *testing.T) {
	cases := []string{"", "not-a-key", "sk-uft-", "sk-wrong-prefix-xxxx"}
	for _, c := range cases {
		if _, err := ComputeHMAC([]byte("pepper"), c); err != ErrInvalidKeyFormat {
			t.Fatalf("ComputeHMAC(%q) error = %v, want ErrInvalidKeyFormat", c, err)
		}
	}
}

func TestBase62Encode_NoPaddingCharacters(t *testing.T) {
	pepper := []byte("test-pepper")
	for i := 0; i < 200; i++ {
		k, err := GenerateAPIKey(pepper)
		if err != nil {
			t.Fatalf("GenerateAPIKey: %v", err)
		}
		body := strings.TrimPrefix(k.Raw, KeyPrefix)
		for _, r := range body {
			if !strings.ContainsRune(base62Alphabet, r) {
				t.Fatalf("unexpected character %q in generated key %q", r, k.Raw)
			}
		}
	}
}
