package dman

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
)

func hashFile(filename string, hasher hash.Hash) error {
	f, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("hash file '%s': %w", filename, err)
	}
	defer f.Close()

	if _, err = io.Copy(hasher, f); err != nil {
		return fmt.Errorf("copy to hasher: %w", err)
	}
	return nil
}

func getHash(f string) (string, error) {
	hasher := sha256.New()
	if err := hashFile(f, hasher); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}
