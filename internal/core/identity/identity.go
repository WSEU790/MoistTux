// Package identity manages the core's client certificate used to authenticate
// to agents. The private key is stored sealed by the platform protector.
package identity

import (
	"crypto/tls"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/WSEU790/MoistTux/internal/core/secrets"
	"github.com/WSEU790/MoistTux/internal/pki"
)

type Identity struct {
	CertPEM     []byte
	Cert        tls.Certificate
	Fingerprint string
	NotAfter    time.Time
}

func paths(dir string) (string, string) {
	return filepath.Join(dir, "core.crt"), filepath.Join(dir, "core.key.sealed")
}

// LoadOrCreate returns the core identity, generating it on first run.
func LoadOrCreate(dir string, p secrets.Protector, label string) (*Identity, error) {
	certPath, keyPath := paths(dir)
	certPEM, err1 := os.ReadFile(certPath)
	sealed, err2 := os.ReadFile(keyPath)
	if errors.Is(err1, os.ErrNotExist) && errors.Is(err2, os.ErrNotExist) {
		return create(dir, p, label)
	}
	if err1 != nil {
		return nil, err1
	}
	if err2 != nil {
		return nil, err2
	}
	keyPEM, err := secrets.Open(p, string(sealed))
	if err != nil {
		return nil, fmt.Errorf("cannot unseal core key (was the data directory copied from another user or machine?): %w", err)
	}
	return build(certPEM, keyPEM)
}

func create(dir string, p secrets.Protector, label string) (*Identity, error) {
	id, err := pki.NewIdentity("nexus-core:"+label, nil, 3*365*24*time.Hour, false, true)
	if err != nil {
		return nil, err
	}
	if err := save(dir, p, id.CertPEM, id.KeyPEM); err != nil {
		return nil, err
	}
	return build(id.CertPEM, id.KeyPEM)
}

// Rotate creates a new identity and returns it without persisting; the caller
// persists with Commit after every agent has accepted the new certificate.
func Rotate(label string) (*pki.Identity, error) {
	return pki.NewIdentity("nexus-core:"+label, nil, 3*365*24*time.Hour, false, true)
}

func Commit(dir string, p secrets.Protector, id *pki.Identity) (*Identity, error) {
	if err := save(dir, p, id.CertPEM, id.KeyPEM); err != nil {
		return nil, err
	}
	return build(id.CertPEM, id.KeyPEM)
}

func save(dir string, p secrets.Protector, certPEM, keyPEM []byte) error {
	certPath, keyPath := paths(dir)
	sealed, err := secrets.Seal(p, keyPEM)
	if err != nil {
		return err
	}
	if err := os.WriteFile(keyPath+".tmp", []byte(sealed), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(certPath+".tmp", certPEM, 0o644); err != nil {
		return err
	}
	if err := os.Rename(keyPath+".tmp", keyPath); err != nil {
		return err
	}
	return os.Rename(certPath+".tmp", certPath)
}

func build(certPEM, keyPEM []byte) (*Identity, error) {
	c, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	fp, err := pki.FingerprintPEM(certPEM)
	if err != nil {
		return nil, err
	}
	x, err := pki.ParseCertPEM(certPEM)
	if err != nil {
		return nil, err
	}
	return &Identity{CertPEM: certPEM, Cert: c, Fingerprint: fp, NotAfter: x.NotAfter}, nil
}
