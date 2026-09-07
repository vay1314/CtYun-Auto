package security

import "testing"

func TestFernetRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	token, err := EncryptFernet("敏感密码", key)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecryptFernet(token, key)
	if err != nil {
		t.Fatal(err)
	}
	if got != "敏感密码" {
		t.Fatalf("decrypt=%q", got)
	}
}
func TestPasswordHash(t *testing.T) {
	encoded := HashPassword("correct horse battery staple")
	if !VerifyPassword(encoded, "correct horse battery staple") {
		t.Fatal("valid password rejected")
	}
	if VerifyPassword(encoded, "wrong") {
		t.Fatal("wrong password accepted")
	}
}
