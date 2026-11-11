// Package secrets protects sensitive material at rest (the core's private
// key, deployment credentials, webhook URLs, database passwords).
//
// On Windows it uses DPAPI bound to the current user account. Elsewhere it
// uses AES-256-GCM with a random key stored in a 0600 file next to the data.
// Plaintext secrets are never written to the database.
package secrets

import (
	"encoding/base64"
	"errors"
)

// Protector encrypts and decrypts small secrets.
type Protector interface {
	Protect(plain []byte) ([]byte, error)
	Unprotect(sealed []byte) ([]byte, error)
	Kind() string
}

// Seal returns a printable, prefixed ciphertext suitable for storing in JSON.
func Seal(p Protector, plain []byte) (string, error) {
	c, err := p.Protect(plain)
	if err != nil {
		return "", err
	}
	return "enc:" + p.Kind() + ":" + base64.StdEncoding.EncodeToString(c), nil
}

// Open reverses Seal.
func Open(p Protector, sealed string) ([]byte, error) {
	prefix := "enc:" + p.Kind() + ":"
	if len(sealed) < len(prefix) || sealed[:len(prefix)] != prefix {
		return nil, errors.New("secrets: value was not sealed by this protector")
	}
	c, err := base64.StdEncoding.DecodeString(sealed[len(prefix):])
	if err != nil {
		return nil, err
	}
	return p.Unprotect(c)
}
