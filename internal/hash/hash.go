package hash

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

// GetHash returns the SHA-256 hex digest of the file at path f.

func GetHash(f string) (string, error) {
	file, err := os.Open(f)
	if err != nil {
		return "", fmt.Errorf("hash file '%s': %w", f, err)
	}
	defer func() { _ = file.Close() }()

	h := sha256.New()
	if _, err = io.Copy(h, file); err != nil {
		return "", fmt.Errorf("hash file '%s': %w", f, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
