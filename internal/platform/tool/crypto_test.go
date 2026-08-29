package tool

import "testing"

func TestCipherRoundTripAndRejectsTampering(t *testing.T) {
	cipher, err := NewCipher([]byte("test-tool-credential-key-at-least-32-chars"))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	ciphertext, err := cipher.Encrypt("secret-token")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	plain, err := cipher.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if plain != "secret-token" {
		t.Fatalf("Decrypt = %q, want secret-token", plain)
	}

	ciphertext[len(ciphertext)-1] ^= 1
	if _, err := cipher.Decrypt(ciphertext); err == nil {
		t.Fatal("tampered ciphertext must be rejected")
	}
}
