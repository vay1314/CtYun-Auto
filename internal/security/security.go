package security

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
)

func RandomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func LoadOrCreateKey(path string, n int) ([]byte, error) {
	if raw, err := os.ReadFile(path); err == nil {
		if decoded, err := base64.URLEncoding.DecodeString(strings.TrimSpace(string(raw))); err == nil && len(decoded) == n {
			return decoded, nil
		}
	}
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(base64.URLEncoding.EncodeToString(b)), 0600); err != nil {
		return nil, err
	}
	return b, nil
}

// DecryptFernet reads the tokens created by the previous Python deployment.
func DecryptFernet(token string, key []byte) (string, error) {
	raw, err := base64.URLEncoding.DecodeString(token)
	if err != nil || len(raw) < 1+8+16+32 || len(key) != 32 {
		return "", errors.New("凭据密文无效")
	}
	if raw[0] != 0x80 {
		return "", errors.New("不支持的凭据版本")
	}
	signed, sig := raw[:len(raw)-32], raw[len(raw)-32:]
	mac := hmac.New(sha256.New, key[:16])
	mac.Write(signed)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return "", errors.New("凭据签名校验失败")
	}
	block, err := aes.NewCipher(key[16:])
	if err != nil {
		return "", err
	}
	data := append([]byte(nil), raw[25:len(raw)-32]...)
	if len(data) == 0 || len(data)%aes.BlockSize != 0 {
		return "", errors.New("凭据长度无效")
	}
	cipher.NewCBCDecrypter(block, raw[9:25]).CryptBlocks(data, data)
	pad := int(data[len(data)-1])
	if pad < 1 || pad > aes.BlockSize || pad > len(data) {
		return "", errors.New("凭据填充无效")
	}
	for _, v := range data[len(data)-pad:] {
		if int(v) != pad {
			return "", errors.New("凭据填充无效")
		}
	}
	return string(data[:len(data)-pad]), nil
}

func EncryptFernet(value string, key []byte) (string, error) {
	if len(key) != 32 {
		return "", errors.New("凭据密钥长度无效")
	}
	plain := []byte(value)
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	plain = append(plain, bytes.Repeat([]byte{byte(pad)}, pad)...)
	iv := make([]byte, 16)
	if _, err := rand.Read(iv); err != nil {
		return "", err
	}
	block, _ := aes.NewCipher(key[16:])
	encrypted := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(encrypted, plain)
	raw := []byte{0x80}
	ts := make([]byte, 8)
	binary.BigEndian.PutUint64(ts, uint64(time.Now().Unix()))
	raw = append(raw, ts...)
	raw = append(raw, iv...)
	raw = append(raw, encrypted...)
	mac := hmac.New(sha256.New, key[:16])
	mac.Write(raw)
	raw = append(raw, mac.Sum(nil)...)
	return base64.URLEncoding.EncodeToString(raw), nil
}

func HashPassword(password string) string {
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	hash := argon2.IDKey([]byte(password), salt, 3, 65536, 4, 32)
	return fmt.Sprintf("$argon2id$v=19$m=65536,t=3,p=4$%s$%s", base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash))
}

func VerifyPassword(encoded, password string) bool {
	p := strings.Split(encoded, "$")
	if len(p) != 6 || p[1] != "argon2id" {
		return false
	}
	var mem uint32
	var iter uint32
	var par uint8
	if _, err := fmt.Sscanf(p[3], "m=%d,t=%d,p=%d", &mem, &iter, &par); err != nil {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(p[4])
	expected, err2 := base64.RawStdEncoding.DecodeString(p[5])
	if err1 != nil || err2 != nil {
		return false
	}
	actual := argon2.IDKey([]byte(password), salt, iter, mem, par, uint32(len(expected)))
	return hmac.Equal(actual, expected)
}

func SignCookie(key []byte, value string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(value))
	return value + "." + hex.EncodeToString(mac.Sum(nil))
}
func VerifyCookie(key []byte, value string) bool {
	i := strings.LastIndexByte(value, '.')
	if i < 1 {
		return false
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(value[:i]))
	got, e := hex.DecodeString(value[i+1:])
	return e == nil && hmac.Equal(got, mac.Sum(nil))
}
func Int(s string) int { n, _ := strconv.Atoi(s); return n }
