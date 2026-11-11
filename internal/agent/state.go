//go:build linux

package agent

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/WSEU790/MoistTux/internal/pki"
)

// TrustedClient is a NEXUS core allowed to call this agent.
type TrustedClient struct {
	Fingerprint string    `json:"fingerprint"`
	Label       string    `json:"label"`
	EnrolledAt  time.Time `json:"enrolled_at"`
	LastSeen    time.Time `json:"last_seen,omitempty"`
}

type pendingToken struct {
	Hash    string    `json:"hash"`
	Expires time.Time `json:"expires"`
}

// State is persisted to <dir>/state.json with mode 0600; the private key lives
// in <dir>/agent.key (0600). The directory itself is 0700, owned by the agent user.
type State struct {
	AgentID     string          `json:"agent_id"`
	CertPEM     string          `json:"cert_pem"`
	NextCertPEM string          `json:"next_cert_pem,omitempty"`
	Trusted     []TrustedClient `json:"trusted"`
	Tokens      []pendingToken  `json:"tokens"`
	CreatedAt   time.Time       `json:"created_at"`

	dir      string
	mu       sync.Mutex
	keyPEM   []byte
	nextKey  []byte
	loadedAt time.Time
}

// refreshLocked reloads trust data written by another process (for example
// `nexus-agent enroll-code` or `revoke --all` while the service is running).
func (s *State) refreshLocked() {
	st, err := os.Stat(stateFile(s.dir))
	if err != nil || !st.ModTime().After(s.loadedAt) {
		return
	}
	data, err := os.ReadFile(stateFile(s.dir))
	if err != nil {
		return
	}
	var disk State
	if json.Unmarshal(data, &disk) != nil {
		return
	}
	s.Trusted, s.Tokens = disk.Trusted, disk.Tokens
	s.loadedAt = st.ModTime()
}

func stateFile(dir string) string { return filepath.Join(dir, "state.json") }
func keyFile(dir string) string   { return filepath.Join(dir, "agent.key") }

// LoadOrCreateState loads the agent state or initialises a new identity.
func LoadOrCreateState(dir string) (*State, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := &State{dir: dir}
	data, err := os.ReadFile(stateFile(dir))
	if err == nil {
		if err := json.Unmarshal(data, s); err != nil {
			return nil, fmt.Errorf("corrupt %s: %w", stateFile(dir), err)
		}
		s.keyPEM, err = os.ReadFile(keyFile(dir))
		if err != nil {
			return nil, fmt.Errorf("agent key missing: %w", err)
		}
		if nk, err := os.ReadFile(keyFile(dir) + ".next"); err == nil {
			s.nextKey = nk
		}
		if st, err := os.Stat(stateFile(dir)); err == nil {
			s.loadedAt = st.ModTime()
		}
		return s, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	idb := make([]byte, 8)
	_, _ = rand.Read(idb)
	s.AgentID = "agt_" + hex.EncodeToString(idb)
	s.CreatedAt = time.Now().UTC()
	id, err := newAgentIdentity()
	if err != nil {
		return nil, err
	}
	s.CertPEM, s.keyPEM = string(id.CertPEM), id.KeyPEM
	return s, s.save()
}

func newAgentIdentity() (*pki.Identity, error) {
	host, _ := os.Hostname()
	hosts := []string{host, "localhost", "127.0.0.1", "::1"}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLinkLocalUnicast() {
				hosts = append(hosts, ipn.IP.String())
			}
		}
	}
	return pki.NewIdentity("nexus-agent:"+host, hosts, 5*365*24*time.Hour, true, false)
}

func (s *State) save() error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := writeAtomic(keyFile(s.dir), s.keyPEM, 0o600); err != nil {
		return err
	}
	if s.nextKey != nil {
		if err := writeAtomic(keyFile(s.dir)+".next", s.nextKey, 0o600); err != nil {
			return err
		}
	} else {
		_ = os.Remove(keyFile(s.dir) + ".next")
	}
	if err := writeAtomic(stateFile(s.dir), data, 0o600); err != nil {
		return err
	}
	if st, err := os.Stat(stateFile(s.dir)); err == nil {
		s.loadedAt = st.ModTime()
	}
	return nil
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *State) Identity() *pki.Identity {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &pki.Identity{CertPEM: []byte(s.CertPEM), KeyPEM: s.keyPEM}
}

func (s *State) Fingerprint() string {
	fp, _ := pki.FingerprintPEM([]byte(s.CertPEM))
	return fp
}

// NewEnrollmentCode creates a single-use code valid for ttl.
func (s *State) NewEnrollmentCode(ttl time.Duration) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshLocked()
	fp, err := pki.FingerprintPEM([]byte(s.CertPEM))
	if err != nil {
		return "", err
	}
	code, err := pki.NewEnrollmentCode(fp)
	if err != nil {
		return "", err
	}
	now := time.Now()
	kept := s.Tokens[:0]
	for _, t := range s.Tokens {
		if t.Expires.After(now) {
			kept = append(kept, t)
		}
	}
	s.Tokens = append(kept, pendingToken{Hash: code.TokenHash(), Expires: now.Add(ttl).UTC()})
	return code.String(), s.save()
}

// ConsumeToken burns a matching unexpired token and pins the client.
func (s *State) ConsumeToken(raw []byte, clientFP, label string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshLocked()
	h := pki.HashToken(raw)
	now := time.Now()
	idx := -1
	for i, t := range s.Tokens {
		if t.Expires.After(now) && pki.FingerprintsEqual(t.Hash, h) {
			idx = i
		}
	}
	if idx < 0 {
		return errors.New("enrollment token invalid, expired or already used")
	}
	s.Tokens = append(s.Tokens[:idx], s.Tokens[idx+1:]...)
	for i, c := range s.Trusted {
		if pki.FingerprintsEqual(c.Fingerprint, clientFP) {
			s.Trusted[i].Label = label
			return s.save()
		}
	}
	s.Trusted = append(s.Trusted, TrustedClient{Fingerprint: clientFP, Label: label, EnrolledAt: now.UTC()})
	return s.save()
}

func (s *State) IsTrusted(fp string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshLocked()
	for _, c := range s.Trusted {
		if pki.FingerprintsEqual(c.Fingerprint, fp) {
			return true
		}
	}
	return false
}

func (s *State) TouchClient(fp string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, c := range s.Trusted {
		if pki.FingerprintsEqual(c.Fingerprint, fp) && time.Since(c.LastSeen) > 5*time.Minute {
			s.Trusted[i].LastSeen = time.Now().UTC()
			_ = s.save()
		}
	}
}

// ReplaceClient swaps a pinned client fingerprint (credential rotation).
func (s *State) ReplaceClient(oldFP, newFP string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, c := range s.Trusted {
		if pki.FingerprintsEqual(c.Fingerprint, oldFP) {
			s.Trusted[i].Fingerprint = newFP
			s.Trusted[i].EnrolledAt = time.Now().UTC()
			return s.save()
		}
	}
	return errors.New("client not enrolled")
}

func (s *State) RemoveClient(fp string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.Trusted[:0]
	for _, c := range s.Trusted {
		if !pki.FingerprintsEqual(c.Fingerprint, fp) {
			kept = append(kept, c)
		}
	}
	s.Trusted = kept
	return s.save()
}

func (s *State) RevokeAll() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Trusted = nil
	s.Tokens = nil
	return s.save()
}

func (s *State) Clients() []TrustedClient {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshLocked()
	return append([]TrustedClient(nil), s.Trusted...)
}

// StageAgentRotation generates the next agent identity. It becomes active on
// the next agent restart or when ActivateRotation is called.
func (s *State) StageAgentRotation() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, err := newAgentIdentity()
	if err != nil {
		return "", err
	}
	s.NextCertPEM, s.nextKey = string(id.CertPEM), id.KeyPEM
	fp, _ := id.Fingerprint()
	return fp, s.save()
}

func (s *State) ActivateRotation() (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.NextCertPEM == "" || s.nextKey == nil {
		return false, nil
	}
	s.CertPEM, s.keyPEM = s.NextCertPEM, s.nextKey
	s.NextCertPEM, s.nextKey = "", nil
	return true, s.save()
}
