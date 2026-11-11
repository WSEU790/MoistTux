// Package agentclient is the core's authenticated client for nexus-agent.
// Every connection pins the agent's certificate fingerprint and presents the
// core's own client certificate.
package agentclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/WSEU790/MoistTux/internal/pki"
	"github.com/WSEU790/MoistTux/internal/protocol"
)

// Error is returned for non-2xx agent responses.
type Error struct {
	Status int
	Code   string
	Msg    string
}

func (e *Error) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("agent returned %d (%s): %s", e.Status, e.Code, e.Msg)
	}
	return fmt.Sprintf("agent returned %d: %s", e.Status, e.Msg)
}

// ErrFingerprintMismatch means the server presented an unexpected certificate.
var ErrFingerprintMismatch = errors.New("agent certificate fingerprint does not match the pinned identity — possible interception or the agent was reinstalled")

type Client struct {
	base string
	hc   *http.Client

	mu      sync.RWMutex
	pinned  []string // accepted fingerprints (current, and pending during rotation)
	lastFP  string
}

// New creates a client for host:port accepting only the given fingerprints.
func New(address string, port int, clientCert tls.Certificate, fingerprints ...string) *Client {
	c := &Client{base: "https://" + net.JoinHostPort(address, strconv.Itoa(port)), pinned: fingerprints}
	cfg := pki.PinnedClientTLS(&clientCert, func(fp string) error {
		c.mu.Lock()
		defer c.mu.Unlock()
		for _, p := range c.pinned {
			if p != "" && pki.FingerprintsEqual(p, fp) {
				c.lastFP = fp
				return nil
			}
		}
		return ErrFingerprintMismatch
	})
	tr := &http.Transport{
		TLSClientConfig:     cfg,
		DialContext:         (&net.Dialer{Timeout: 8 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout: 8 * time.Second,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     90 * time.Second,
		ForceAttemptHTTP2:   true,
	}
	c.hc = &http.Client{Transport: tr}
	return c
}

// SetPinned replaces the accepted fingerprints and drops idle connections.
func (c *Client) SetPinned(fps ...string) {
	c.mu.Lock()
	c.pinned = fps
	c.mu.Unlock()
	c.hc.Transport.(*http.Transport).CloseIdleConnections()
}

// LastFingerprint is the fingerprint seen on the most recent handshake.
func (c *Client) LastFingerprint() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastFP
}

func (c *Client) Close() { c.hc.Transport.(*http.Transport).CloseIdleConnections() }

func (c *Client) do(ctx context.Context, method, path string, q url.Values, body, out any) error {
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("User-Agent", "nexus-core/1")
	resp, err := c.hc.Do(req)
	if err != nil {
		if errors.Is(err, ErrFingerprintMismatch) || strings.Contains(err.Error(), ErrFingerprintMismatch.Error()) {
			return ErrFingerprintMismatch
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var pe protocol.Error
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		if json.Unmarshal(b, &pe) != nil || pe.Error == "" {
			pe.Error = strings.TrimSpace(string(b))
		}
		return &Error{Status: resp.StatusCode, Code: pe.Code, Msg: pe.Error}
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if raw, ok := out.(*json.RawMessage); ok {
		b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		if err != nil {
			return err
		}
		*raw = b
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(out)
}

func (c *Client) Get(ctx context.Context, path string, q url.Values, out any) error {
	return c.do(ctx, http.MethodGet, path, q, nil, out)
}

func (c *Client) Post(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, http.MethodPost, path, nil, body, out)
}

// ---------------------------------------------------------------- typed helpers

func (c *Client) Health(ctx context.Context) (*protocol.Health, error) {
	var h protocol.Health
	return &h, c.Get(ctx, "/v1/health", nil, &h)
}

func (c *Client) Info(ctx context.Context) (*protocol.SystemInfo, error) {
	var i protocol.SystemInfo
	return &i, c.Get(ctx, "/v1/info", nil, &i)
}

func (c *Client) Metrics(ctx context.Context, since uint64) (*protocol.MetricsBatch, error) {
	var b protocol.MetricsBatch
	return &b, c.Get(ctx, "/v1/metrics", url.Values{"since": {strconv.FormatUint(since, 10)}}, &b)
}

// ---------------------------------------------------------------- enrollment

// EnrollResult carries what the core must persist after enrollment.
type EnrollResult struct {
	Response    protocol.EnrollResponse
	Fingerprint string
}

// Enroll connects to an agent that has never seen this core. The handshake is
// accepted only if the agent's certificate matches the fingerprint embedded in
// the enrollment code, so no trust-on-first-use window exists.
func Enroll(ctx context.Context, address string, port int, code string, clientCert tls.Certificate, clientCertPEM []byte, label string) (*EnrollResult, error) {
	ec, err := pki.ParseEnrollmentCode(code)
	if err != nil {
		return nil, err
	}
	var seen string
	cfg := pki.PinnedClientTLS(&clientCert, func(fp string) error {
		seen = fp
		if !ec.MatchesFingerprint(fp) {
			return ErrFingerprintMismatch
		}
		return nil
	})
	hc := &http.Client{Transport: &http.Transport{TLSClientConfig: cfg, TLSHandshakeTimeout: 10 * time.Second}, Timeout: 30 * time.Second}
	defer hc.CloseIdleConnections()
	body, _ := json.Marshal(protocol.EnrollRequest{Token: b32(ec.Token), ClientCert: string(clientCertPEM), Label: label})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+net.JoinHostPort(address, strconv.Itoa(port))+"/v1/enroll", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		if seen != "" && !ec.MatchesFingerprint(seen) {
			return nil, fmt.Errorf("%w (presented %s)", ErrFingerprintMismatch, pki.FormatFingerprint(seen)[:23]+"…")
		}
		return nil, fmt.Errorf("could not reach agent at %s:%d: %w", address, port, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		var pe protocol.Error
		_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&pe)
		return nil, &Error{Status: resp.StatusCode, Code: pe.Code, Msg: pe.Error}
	}
	var er protocol.EnrollResponse
	if err := json.NewDecoder(resp.Body).Decode(&er); err != nil {
		return nil, err
	}
	if !pki.FingerprintsEqual(er.Fingerprint, seen) {
		return nil, errors.New("agent reported a fingerprint different from its TLS certificate")
	}
	return &EnrollResult{Response: er, Fingerprint: seen}, nil
}

func b32(b []byte) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
	var out strings.Builder
	var buf uint64
	bits := 0
	for _, c := range b {
		buf = buf<<8 | uint64(c)
		bits += 8
		for bits >= 5 {
			out.WriteByte(alphabet[(buf>>(bits-5))&31])
			bits -= 5
		}
	}
	if bits > 0 {
		out.WriteByte(alphabet[(buf<<(5-bits))&31])
	}
	return out.String()
}
