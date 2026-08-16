// Package laravel implements the payload format produced by Laravel's
// `php artisan env:encrypt` command, so neo can read a committed
// .env.encrypted at deploy time without shelling out to PHP.
//
// The format (Illuminate\Encryption\Encrypter) is:
//
//	base64( json{ iv, value, mac, tag } )
//
// where iv and value are base64, mac is the hex HMAC-SHA256 of the *base64
// text* of iv+value (CBC only), and tag holds the AEAD tag (GCM only). The
// encrypted plaintext is PHP-serialized before encryption, so a decrypted
// file arrives wrapped as `s:<byte-length>:"...";`.
package laravel

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const gcmTagSize = 16

// payload mirrors the JSON object Laravel base64-encodes. Field order matches
// PHP's compact('iv', 'value', 'mac', 'tag') so re-encrypting produces
// byte-identical structure.
type payload struct {
	IV    string `json:"iv"`
	Value string `json:"value"`
	MAC   string `json:"mac"`
	Tag   string `json:"tag"`
}

// ParseKey decodes an environment encryption key. `env:encrypt` prints
// generated keys with a "base64:" prefix; a raw 16/32-byte key (what you get
// when you pass your own --key) is accepted as-is.
func ParseKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("empty encryption key")
	}

	if rest, ok := strings.CutPrefix(s, "base64:"); ok {
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(rest))
		if err != nil {
			return nil, fmt.Errorf("decode base64 key: %w", err)
		}
		return checkKeyLen(raw)
	}

	// PHP treats a bare key as raw bytes, so the literal string wins when it is
	// already the right length. Only fall back to base64 decoding otherwise —
	// that covers keys copied without their "base64:" prefix.
	if raw, err := checkKeyLen([]byte(s)); err == nil {
		return raw, nil
	}
	if raw, err := base64.StdEncoding.DecodeString(s); err == nil {
		return checkKeyLen(raw)
	}
	return nil, fmt.Errorf("invalid key: %d bytes (want a 16 or 32 byte key, or a base64: key)", len(s))
}

// GenerateKey returns a new random key in Laravel's "base64:" display format.
// size is the key length in bytes: 16 for AES-128, 32 for AES-256.
func GenerateKey(size int) (string, error) {
	if size != 16 && size != 32 {
		return "", fmt.Errorf("invalid key size %d (want 16 or 32)", size)
	}
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate key: %w", err)
	}
	return "base64:" + base64.StdEncoding.EncodeToString(buf), nil
}

// CipherName returns the Laravel cipher label for a key length, for display.
func CipherName(key []byte) string {
	if len(key) == 16 {
		return "AES-128-CBC"
	}
	return "AES-256-CBC"
}

// Decrypt reverses `php artisan env:encrypt` and returns the plaintext file
// contents. The cipher is inferred from the key length and from whether the
// payload carries an AEAD tag, so all four ciphers Laravel supports
// (aes-128/256-cbc, aes-128/256-gcm) decrypt without extra configuration.
func Decrypt(encoded string, key []byte) (string, error) {
	if _, err := checkKeyLen(key); err != nil {
		return "", err
	}

	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return "", fmt.Errorf("not a Laravel encrypted file (payload is not base64): %w", err)
	}
	var p payload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("not a Laravel encrypted file (payload is not JSON): %w", err)
	}

	iv, err := base64.StdEncoding.DecodeString(p.IV)
	if err != nil {
		return "", fmt.Errorf("decode iv: %w", err)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(p.Value)
	if err != nil {
		return "", fmt.Errorf("decode value: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	var plain []byte
	if p.Tag != "" {
		plain, err = decryptGCM(block, iv, ciphertext, p.Tag)
	} else {
		plain, err = decryptCBC(block, key, iv, ciphertext, p)
	}
	if err != nil {
		return "", err
	}

	return phpUnserializeString(plain), nil
}

// Encrypt produces a payload `php artisan env:decrypt` can read: AES-CBC with
// an HMAC-SHA256 MAC over the base64 text, PHP-serialized plaintext. The
// cipher follows the key length (16 bytes → AES-128-CBC, 32 → AES-256-CBC).
func Encrypt(plaintext string, key []byte) (string, error) {
	if _, err := checkKeyLen(key); err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	iv := make([]byte, block.BlockSize())
	if _, err := rand.Read(iv); err != nil {
		return "", fmt.Errorf("generate iv: %w", err)
	}

	// PHP serialize() of a string, byte length not rune length.
	serialized := fmt.Sprintf("s:%d:\"%s\";", len(plaintext), plaintext)

	padded := padPKCS7([]byte(serialized), block.BlockSize())
	ciphertext := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext, padded)

	ivB64 := base64.StdEncoding.EncodeToString(iv)
	valueB64 := base64.StdEncoding.EncodeToString(ciphertext)

	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(ivB64 + valueB64))

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // Laravel uses JSON_UNESCAPED_SLASHES
	if err := enc.Encode(payload{IV: ivB64, Value: valueB64, MAC: hex.EncodeToString(mac.Sum(nil))}); err != nil {
		return "", fmt.Errorf("encode payload: %w", err)
	}

	return base64.StdEncoding.EncodeToString(bytes.TrimRight(buf.Bytes(), "\n")), nil
}

func decryptCBC(block cipher.Block, key, iv, ciphertext []byte, p payload) ([]byte, error) {
	// Laravel MACs the base64 text of the IV and value, not the raw bytes.
	expected, err := hex.DecodeString(p.MAC)
	if err != nil {
		return nil, errors.New("mac is not valid hex")
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(p.IV + p.Value))
	if !hmac.Equal(mac.Sum(nil), expected) {
		return nil, errors.New("MAC mismatch — wrong key, or the encrypted file was modified")
	}

	if len(iv) != block.BlockSize() {
		return nil, fmt.Errorf("bad iv length %d (want %d)", len(iv), block.BlockSize())
	}
	if len(ciphertext) == 0 || len(ciphertext)%block.BlockSize() != 0 {
		return nil, errors.New("ciphertext length is not a multiple of the block size")
	}

	out := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, ciphertext)
	return stripPKCS7(out, block.BlockSize())
}

func decryptGCM(block cipher.Block, iv, ciphertext []byte, tagB64 string) ([]byte, error) {
	tag, err := base64.StdEncoding.DecodeString(tagB64)
	if err != nil {
		return nil, fmt.Errorf("decode tag: %w", err)
	}
	if len(tag) != gcmTagSize {
		return nil, fmt.Errorf("bad auth tag length %d (want %d)", len(tag), gcmTagSize)
	}
	if len(iv) == 0 {
		return nil, errors.New("empty iv")
	}

	aead, err := cipher.NewGCMWithNonceSize(block, len(iv))
	if err != nil {
		return nil, err
	}
	// PHP keeps the tag in its own field; Go expects it appended to the ciphertext.
	sealed := make([]byte, 0, len(ciphertext)+len(tag))
	sealed = append(sealed, ciphertext...)
	sealed = append(sealed, tag...)

	plain, err := aead.Open(nil, iv, sealed, nil)
	if err != nil {
		return nil, errors.New("authentication failed — wrong key, or the encrypted file was modified")
	}
	return plain, nil
}

func checkKeyLen(k []byte) ([]byte, error) {
	if len(k) == 16 || len(k) == 32 {
		return k, nil
	}
	return nil, fmt.Errorf("invalid key: %d bytes (want 16 or 32)", len(k))
}

func padPKCS7(b []byte, blockSize int) []byte {
	pad := blockSize - len(b)%blockSize
	return append(b, bytes.Repeat([]byte{byte(pad)}, pad)...)
}

func stripPKCS7(b []byte, blockSize int) ([]byte, error) {
	if len(b) == 0 {
		return nil, errors.New("empty plaintext")
	}
	pad := int(b[len(b)-1])
	if pad == 0 || pad > blockSize || pad > len(b) {
		return nil, errors.New("invalid padding — wrong key?")
	}
	for _, c := range b[len(b)-pad:] {
		if int(c) != pad {
			return nil, errors.New("invalid padding — wrong key?")
		}
	}
	return b[:len(b)-pad], nil
}

// phpUnserializeString unwraps PHP's serialize() of a string: `s:<bytes>:"…";`.
// env:encrypt serializes before encrypting; encryptString() does not, so a
// payload that isn't serialized is returned untouched.
func phpUnserializeString(b []byte) string {
	s := string(b)
	if !strings.HasPrefix(s, "s:") || !strings.HasSuffix(s, `";`) {
		return s
	}
	colon := strings.IndexByte(s[2:], ':')
	if colon < 0 {
		return s
	}
	n, err := strconv.Atoi(s[2 : 2+colon])
	if err != nil {
		return s
	}
	body := s[2+colon+1:] // `"…";`
	if len(body) < 3 || body[0] != '"' {
		return s
	}
	inner := body[1 : len(body)-2]
	if len(inner) != n {
		return s
	}
	return inner
}
