package secure

import (
	"regexp"
	"strings"
	"testing"
)

func TestNodeCompatibleSecretFormat(t *testing.T) {
	box, err := New(t.TempDir(), strings.Repeat("01", 32), false)
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := box.Encrypt("private-value")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(encrypted, "v1:") || strings.Contains(encrypted, "private-value") {
		t.Fatalf("unexpected encrypted format: %q", encrypted)
	}
	decrypted, err := box.Decrypt(encrypted)
	if err != nil || decrypted != "private-value" {
		t.Fatalf("decrypt mismatch: %q %v", decrypted, err)
	}
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(box.BlindIndex(" User@Example.com ")) {
		t.Fatal("blind index is not SHA-256 hex")
	}
}

func TestDecryptsNodeAESGCMVector(t *testing.T) {
	box, err := New(t.TempDir(), "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f", false)
	if err != nil {
		t.Fatal(err)
	}
	const encryptedByNode = "v1:ICEiIyQlJicoKSor:lZgSyCaUGSbMGoaNorioZQ==:vFXCFUHrf21oGTbjJJ1IHH7w"
	decrypted, err := box.Decrypt(encryptedByNode)
	if err != nil || decrypted != "node-secret-兼容" {
		t.Fatalf("Node AES-GCM vector mismatch: %q %v", decrypted, err)
	}
	if got := box.BlindIndex(" User@Example.com "); got != "a2338a592a541ed0b0f667e0ab5ea16e52df675a4d0b0b774346c102eb27ddf4" {
		t.Fatalf("Node blind index mismatch: %s", got)
	}
}
