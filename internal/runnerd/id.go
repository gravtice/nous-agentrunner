package runnerd

import (
	"crypto/rand"
	"encoding/hex"
)

func newID(prefix string, nbytes int) (string, error) {
	if nbytes <= 0 {
		nbytes = 16
	}
	b := make([]byte, nbytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(b), nil
}
