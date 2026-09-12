package update

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"strings"
)

// ParsePublicKey decodes a base64 encoded Ed25519 public key.
func ParsePublicKey(raw string) (ed25519.PublicKey, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("更新公钥解码失败: %w", err)
	}
	if len(key) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("更新公钥长度无效")
	}
	return ed25519.PublicKey(key), nil
}

// DecodeSignature decodes a base64 encoded detached signature.
func DecodeSignature(raw string) ([]byte, error) {
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("更新签名解码失败: %w", err)
	}
	if len(sig) != ed25519.SignatureSize {
		return nil, fmt.Errorf("更新签名长度无效")
	}
	return sig, nil
}

// VerifySignature returns true when the signature matches data under publicKey.
func VerifySignature(publicKey ed25519.PublicKey, data, signature []byte) bool {
	if len(publicKey) != ed25519.PublicKeySize {
		return false
	}
	return ed25519.Verify(publicKey, data, signature)
}
