package ssh

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"

	gossh "golang.org/x/crypto/ssh"
)

// NeoKeyPath returns the path to neo's private key (~/.neo/neo_ed25519).
func NeoKeyPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".neo", "neo_ed25519")
}

// NeoKeyPubPath returns the path to neo's public key (~/.neo/neo_ed25519.pub).
func NeoKeyPubPath() string {
	return NeoKeyPath() + ".pub"
}

// NeoKeyExists returns true if neo's SSH key pair exists.
func NeoKeyExists() bool {
	_, err := os.Stat(NeoKeyPath())
	return err == nil
}

// GenerateNeoKey creates a new ed25519 key pair at ~/.neo/neo_ed25519.
// Returns the public key string. No-op if the key already exists.
func GenerateNeoKey() (string, error) {
	if NeoKeyExists() {
		pub, err := os.ReadFile(NeoKeyPubPath())
		if err != nil {
			return "", fmt.Errorf("read existing neo key: %w", err)
		}
		return string(pub), nil
	}

	// Generate ed25519 key pair
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", fmt.Errorf("generate key: %w", err)
	}

	// Marshal private key to PEM
	privBytes, err := gossh.MarshalPrivateKey(privKey, "neo managed key")
	if err != nil {
		return "", fmt.Errorf("marshal private key: %w", err)
	}
	privPEM := pem.EncodeToMemory(privBytes)

	// Marshal public key to authorized_keys format
	sshPub, err := gossh.NewPublicKey(pubKey)
	if err != nil {
		return "", fmt.Errorf("marshal public key: %w", err)
	}
	pubLine := string(gossh.MarshalAuthorizedKey(sshPub))

	// Ensure directory exists
	dir := filepath.Dir(NeoKeyPath())
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("create key directory: %w", err)
	}

	// Write private key
	if err := os.WriteFile(NeoKeyPath(), privPEM, 0600); err != nil {
		return "", fmt.Errorf("write private key: %w", err)
	}

	// Write public key
	if err := os.WriteFile(NeoKeyPubPath(), []byte(pubLine), 0644); err != nil {
		return "", fmt.Errorf("write public key: %w", err)
	}

	return pubLine, nil
}

// LoadNeoKey reads neo's public key file and returns the contents.
func LoadNeoKey() (string, error) {
	data, err := os.ReadFile(NeoKeyPubPath())
	if err != nil {
		return "", err
	}
	return string(data), nil
}
