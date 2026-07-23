package slackdesktop

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"fmt"

	"golang.org/x/crypto/pbkdf2"
)

// slackDomainHashPrefixes are the Chromium SHA256 domain-hash prefixes that
// precede the decrypted value for slack.com / .slack.com.
var slackDomainHashPrefixes = [][]byte{
	{3, 202, 236, 172, 132, 247, 212, 240, 217, 211, 68, 226, 103, 153, 245, 64, 85, 68, 2, 183, 83, 182, 186, 218, 14, 102, 237, 62, 231, 241, 231, 142},
	{145, 28, 115, 68, 173, 92, 42, 78, 104, 243, 5, 63, 24, 206, 51, 190, 31, 169, 160, 244, 247, 106, 147, 228, 60, 68, 92, 134, 105, 199, 162, 120},
}

func removeDomainHashPrefix(value []byte) []byte {
	for _, p := range slackDomainHashPrefixes {
		if bytes.HasPrefix(value, p) {
			return value[len(p):]
		}
	}
	return value
}

// decryptCBC decrypts a v10/v11 Chromium cookie value (Linux/macOS) using a
// PBKDF2-SHA1 key (salt "saltysalt", 16 bytes) and a 16-space IV. `value`
// must already have its 3-byte version prefix stripped.
func decryptCBC(value, password []byte, rounds int) ([]byte, error) {
	if len(value) == 0 || len(value)%16 != 0 {
		return nil, fmt.Errorf("%w: bad ciphertext length %d", ErrDecryptFailed, len(value))
	}
	dk := pbkdf2.Key(password, []byte("saltysalt"), rounds, 16, sha1.New)
	block, err := aes.NewCipher(dk)
	if err != nil {
		return nil, err
	}
	iv := bytes.Repeat([]byte{' '}, 16)
	out := make([]byte, len(value))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, value)
	// Strip PKCS#7 padding.
	n := int(out[len(out)-1])
	if n <= 0 || n > 16 || n > len(out) {
		return nil, fmt.Errorf("%w: bad padding", ErrDecryptFailed)
	}
	return removeDomainHashPrefix(out[:len(out)-n]), nil
}

// decryptGCM decrypts a v10 Chromium cookie value (Windows) using an
// AES-256-GCM key. `value` must already have its 3-byte version prefix
// stripped; nonce is the first 12 bytes.
func decryptGCM(value, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(value) < 12 {
		return nil, fmt.Errorf("%w: short gcm value", ErrDecryptFailed)
	}
	out, err := gcm.Open(nil, value[:12], value[12:], nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDecryptFailed, err)
	}
	return removeDomainHashPrefix(out), nil
}
