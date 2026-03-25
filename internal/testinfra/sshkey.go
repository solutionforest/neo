package testinfra

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"

	"golang.org/x/crypto/ssh"
)

// EphemeralKey holds an in-memory SSH keypair.
type EphemeralKey struct {
	PrivateKeyPEM []byte // PEM-encoded private key
	PublicKeySSH  string // OpenSSH authorized_keys format
}

// GenerateEphemeralKey creates a new ed25519 keypair in memory.
func GenerateEphemeralKey() (*EphemeralKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ed25519 key: %w", err)
	}

	// Marshal private key to OpenSSH PEM format
	pemBlock, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		return nil, fmt.Errorf("marshal private key: %w", err)
	}
	privPEM := pem.EncodeToMemory(pemBlock)

	// Marshal public key to authorized_keys format
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, fmt.Errorf("create ssh public key: %w", err)
	}
	pubSSH := string(ssh.MarshalAuthorizedKey(sshPub))

	return &EphemeralKey{
		PrivateKeyPEM: privPEM,
		PublicKeySSH:  pubSSH,
	}, nil
}
