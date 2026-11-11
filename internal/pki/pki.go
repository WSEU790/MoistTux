// Package pki provides the key, certificate and fingerprint primitives used for
// mutual certificate pinning between the NEXUS core and its Linux agents.
package pki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base32"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"strings"
	"time"
)

// Identity is a private key plus a self-signed certificate.
type Identity struct {
	CertPEM []byte
	KeyPEM  []byte
}

// NewIdentity creates an ECDSA P-256 key and a self-signed certificate.
// hosts may contain DNS names and IP addresses; they become SANs.
func NewIdentity(commonName string, hosts []string, validFor time.Duration, serverAuth, clientAuth bool) (*Identity, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: commonName, Organization: []string{"NEXUS"}},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.Add(validFor),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	if serverAuth {
		tpl.ExtKeyUsage = append(tpl.ExtKeyUsage, x509.ExtKeyUsageServerAuth)
	}
	if clientAuth {
		tpl.ExtKeyUsage = append(tpl.ExtKeyUsage, x509.ExtKeyUsageClientAuth)
	}
	for _, h := range hosts {
		if h == "" {
			continue
		}
		if ip := net.ParseIP(h); ip != nil {
			tpl.IPAddresses = append(tpl.IPAddresses, ip)
		} else {
			tpl.DNSNames = append(tpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	return &Identity{
		CertPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		KeyPEM:  pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
	}, nil
}

// TLSCertificate converts the identity into a tls.Certificate.
func (id *Identity) TLSCertificate() (tls.Certificate, error) {
	return tls.X509KeyPair(id.CertPEM, id.KeyPEM)
}

// Fingerprint returns the hex SHA-256 fingerprint of the identity's certificate.
func (id *Identity) Fingerprint() (string, error) {
	return FingerprintPEM(id.CertPEM)
}

// NotAfter returns the certificate expiry.
func (id *Identity) NotAfter() (time.Time, error) {
	c, err := ParseCertPEM(id.CertPEM)
	if err != nil {
		return time.Time{}, err
	}
	return c.NotAfter, nil
}

// ParseCertPEM parses the first certificate block of a PEM document.
func ParseCertPEM(certPEM []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("pki: no certificate PEM block")
	}
	return x509.ParseCertificate(block.Bytes)
}

// FingerprintDER returns the lower-case hex SHA-256 of a DER certificate.
func FingerprintDER(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// FingerprintPEM returns the lower-case hex SHA-256 of a PEM certificate.
func FingerprintPEM(certPEM []byte) (string, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return "", errors.New("pki: no certificate PEM block")
	}
	if _, err := x509.ParseCertificate(block.Bytes); err != nil {
		return "", err
	}
	return FingerprintDER(block.Bytes), nil
}

// FingerprintsEqual compares two hex fingerprints in constant time.
func FingerprintsEqual(a, b string) bool {
	a, b = strings.ToLower(a), strings.ToLower(b)
	if len(a) != len(b) || len(a) == 0 {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// FormatFingerprint renders a fingerprint as colon-separated byte pairs for display.
func FormatFingerprint(fp string) string {
	var b strings.Builder
	for i := 0; i+2 <= len(fp); i += 2 {
		if i > 0 {
			b.WriteByte(':')
		}
		b.WriteString(strings.ToUpper(fp[i : i+2]))
	}
	return b.String()
}

// ----------------------------------------------------------------------------
// Enrollment codes
// ----------------------------------------------------------------------------

const (
	enrollPrefix   = "NX1"
	tokenBytes     = 16
	fpPrefixBytes  = 16
	EnrollTokenTTL = 24 * time.Hour
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// EnrollmentCode is the decoded form of the string an operator copies from the
// agent into NEXUS. It binds a one-time secret to the agent's certificate.
type EnrollmentCode struct {
	Token    []byte // raw secret, 16 bytes
	FPPrefix []byte // first 16 bytes of the agent certificate SHA-256
}

// NewEnrollmentCode creates a fresh random token bound to the agent fingerprint.
func NewEnrollmentCode(agentFingerprintHex string) (*EnrollmentCode, error) {
	fp, err := hex.DecodeString(agentFingerprintHex)
	if err != nil || len(fp) < fpPrefixBytes {
		return nil, errors.New("pki: invalid agent fingerprint")
	}
	tok := make([]byte, tokenBytes)
	if _, err := rand.Read(tok); err != nil {
		return nil, err
	}
	return &EnrollmentCode{Token: tok, FPPrefix: fp[:fpPrefixBytes]}, nil
}

// String renders the code as NX1-<TOKEN>-<FP>, grouped for readability.
func (c *EnrollmentCode) String() string {
	return enrollPrefix + "-" + b32.EncodeToString(c.Token) + "-" + b32.EncodeToString(c.FPPrefix)
}

// TokenHash returns the hex SHA-256 of the token; agents store only this.
func (c *EnrollmentCode) TokenHash() string {
	sum := sha256.Sum256(c.Token)
	return hex.EncodeToString(sum[:])
}

// ParseEnrollmentCode parses a code, tolerating whitespace and lower case.
func ParseEnrollmentCode(s string) (*EnrollmentCode, error) {
	s = strings.ToUpper(strings.Join(strings.Fields(s), ""))
	parts := strings.Split(s, "-")
	if len(parts) != 3 || parts[0] != enrollPrefix {
		return nil, errors.New("enrollment code must look like NX1-XXXX-XXXX")
	}
	tok, err := b32.DecodeString(parts[1])
	if err != nil || len(tok) != tokenBytes {
		return nil, errors.New("enrollment code token is malformed")
	}
	fp, err := b32.DecodeString(parts[2])
	if err != nil || len(fp) != fpPrefixBytes {
		return nil, errors.New("enrollment code fingerprint is malformed")
	}
	return &EnrollmentCode{Token: tok, FPPrefix: fp}, nil
}

// MatchesFingerprint reports whether the full agent fingerprint starts with the
// prefix carried in the code.
func (c *EnrollmentCode) MatchesFingerprint(fullHex string) bool {
	full, err := hex.DecodeString(fullHex)
	if err != nil || len(full) < fpPrefixBytes {
		return false
	}
	return subtle.ConstantTimeCompare(full[:fpPrefixBytes], c.FPPrefix) == 1
}

// HashToken hashes a raw token for comparison against a stored hash.
func HashToken(tok []byte) string {
	sum := sha256.Sum256(tok)
	return hex.EncodeToString(sum[:])
}

// PinnedClientTLS returns a client TLS config presenting clientCert and
// accepting only a server certificate whose fingerprint satisfies accept.
func PinnedClientTLS(clientCert *tls.Certificate, accept func(fp string) error) *tls.Config {
	cfg := &tls.Config{
		MinVersion: tls.VersionTLS13,
		// Chain verification is replaced by fingerprint pinning below.
		InsecureSkipVerify: true, //nolint:gosec // pinned in VerifyConnection
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("pki: server presented no certificate")
			}
			leaf := cs.PeerCertificates[0]
			now := time.Now()
			if now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
				return fmt.Errorf("pki: server certificate not valid at %s", now.Format(time.RFC3339))
			}
			return accept(FingerprintDER(leaf.Raw))
		},
	}
	if clientCert != nil {
		cfg.Certificates = []tls.Certificate{*clientCert}
	}
	return cfg
}
