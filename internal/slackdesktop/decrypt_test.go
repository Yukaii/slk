package slackdesktop

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"testing"

	"golang.org/x/crypto/pbkdf2"
	"crypto/sha1"
)

func cbcEncrypt(t *testing.T, plaintext, password []byte, rounds int) []byte {
	t.Helper()
	dk := pbkdf2.Key(password, []byte("saltysalt"), rounds, 16, sha1.New)
	block, _ := aes.NewCipher(dk)
	iv := bytes.Repeat([]byte{' '}, 16)
	pad := 16 - len(plaintext)%16
	padded := append(plaintext, bytes.Repeat([]byte{byte(pad)}, pad)...)
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, padded)
	return out
}

func TestDecryptCBC(t *testing.T) {
	pw := []byte("peanuts")
	want := []byte("xoxd-secret-value")
	enc := cbcEncrypt(t, want, pw, 1)
	got, err := decryptCBC(enc, pw, 1)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRemoveDomainHashPrefix(t *testing.T) {
	// 32-byte prefix + payload
	prefix := slackDomainHashPrefixes[0]
	in := append(append([]byte{}, prefix...), []byte("xoxd-abc")...)
	if got := removeDomainHashPrefix(in); string(got) != "xoxd-abc" {
		t.Errorf("got %q", got)
	}
}
