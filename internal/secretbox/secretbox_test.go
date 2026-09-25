package secretbox

import (
	"crypto/rand"
	"encoding/base64"
	"testing"
)

func randomKEK(t *testing.T) string {
	t.Helper()
	b := make([]byte, keySize)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func TestSealOpen_RoundTrip(t *testing.T) {
	box, err := NewBox(randomKEK(t))
	if err != nil {
		t.Fatalf("NewBox: %v", err)
	}

	const secret = "sk-real-upstream-provider-key-abc123"
	sealed, err := box.Seal(secret)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if string(sealed.Ciphertext) == secret {
		t.Fatal("ciphertext must not equal plaintext")
	}

	got, err := box.Open(sealed)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got != secret {
		t.Errorf("Open() = %q, want %q", got, secret)
	}
}

func TestOpen_WrongKEKFails(t *testing.T) {
	box1, _ := NewBox(randomKEK(t))
	box2, _ := NewBox(randomKEK(t))

	sealed, err := box1.Seal("top-secret")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if _, err := box2.Open(sealed); err == nil {
		t.Fatal("expected error opening with wrong KEK, got nil")
	}
}

func TestOpen_TamperedCiphertextFails(t *testing.T) {
	box, _ := NewBox(randomKEK(t))
	sealed, err := box.Seal("top-secret")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	sealed.Ciphertext[len(sealed.Ciphertext)-1] ^= 0xFF // 翻转最后一个字节

	if _, err := box.Open(sealed); err == nil {
		t.Fatal("expected error opening tampered ciphertext, got nil")
	}
}

func TestNewBox_RejectsWrongLengthKEK(t *testing.T) {
	if _, err := NewBox(base64.StdEncoding.EncodeToString([]byte("too-short"))); err != ErrInvalidKEK {
		t.Errorf("error = %v, want ErrInvalidKEK", err)
	}
}

func TestSeal_ProducesDistinctCiphertextEachTime(t *testing.T) {
	box, _ := NewBox(randomKEK(t))
	a, err := box.Seal("same-plaintext")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	b, err := box.Seal("same-plaintext")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if string(a.Ciphertext) == string(b.Ciphertext) {
		t.Error("expected different ciphertexts for repeated Seal calls (random nonce/DEK)")
	}
}
