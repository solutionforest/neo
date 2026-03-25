package app

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// GenerateValue generates a value based on a spec like "hex:64" or "base64:32".
func GenerateValue(spec string) (string, error) {
	parts := strings.SplitN(spec, ":", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid generate spec: %s", spec)
	}

	encoding := parts[0]
	length, err := strconv.Atoi(parts[1])
	if err != nil {
		return "", fmt.Errorf("invalid generate length: %s", parts[1])
	}

	// Generate random bytes
	numBytes := length
	if encoding == "hex" {
		numBytes = length / 2
	}
	buf := make([]byte, numBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate random: %w", err)
	}

	switch encoding {
	case "hex":
		return hex.EncodeToString(buf), nil
	case "base64":
		return base64.StdEncoding.EncodeToString(buf), nil
	default:
		return "", fmt.Errorf("unknown generate encoding: %s", encoding)
	}
}
