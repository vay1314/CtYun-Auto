package update

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"
)

func TestVerifySignature(t *testing.T) {
	publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	data := []byte("update manifest")
	signature := ed25519.Sign(privateKey, data)
	if !VerifySignature(publicKey, data, signature) {
		t.Fatal("valid signature was rejected")
	}
	if VerifySignature(publicKey, []byte("tampered"), signature) {
		t.Fatal("tampered payload was accepted")
	}
}

func TestParsePublicKey(t *testing.T) {
	publicKey, _, _ := ed25519.GenerateKey(rand.Reader)
	parsed, err := ParsePublicKey(base64.StdEncoding.EncodeToString(publicKey))
	if err != nil {
		t.Fatal(err)
	}
	if !parsed.Equal(publicKey) {
		t.Fatal("parsed public key does not match")
	}
	if _, err := ParsePublicKey("not-base64"); err == nil {
		t.Fatal("invalid base64 was accepted")
	}
}

func TestDecodeSignature(t *testing.T) {
	if _, err := DecodeSignature(""); err == nil {
		t.Fatal("empty signature was accepted")
	}
}
