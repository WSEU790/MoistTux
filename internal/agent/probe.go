//go:build linux

package agent

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/WSEU790/MoistTux/internal/protocol"
)

// Probe performs an active health check from the server's own vantage point.
func Probe(ctx context.Context, req protocol.ProbeRequest) protocol.ProbeResult {
	timeout := time.Duration(req.TimeoutSec) * time.Second
	if timeout <= 0 || timeout > 30*time.Second {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	res := protocol.ProbeResult{}
	finish := func() protocol.ProbeResult {
		res.LatencyMs = float64(time.Since(start).Microseconds()) / 1000
		return res
	}
	certInfo := func(cs *tls.ConnectionState) {
		if cs == nil || len(cs.PeerCertificates) == 0 {
			return
		}
		leaf := cs.PeerCertificates[0]
		exp := leaf.NotAfter.UTC()
		days := int(time.Until(exp).Hours() / 24)
		res.CertExpiry, res.CertDays = &exp, &days
		res.CertSubject, res.CertIssuer = leaf.Subject.String(), leaf.Issuer.String()
	}
	switch req.Kind {
	case "tcp":
		if _, _, err := net.SplitHostPort(req.Target); err != nil {
			res.Error = "target must be host:port"
			return res
		}
		var d net.Dialer
		c, err := d.DialContext(ctx, "tcp", req.Target)
		if err != nil {
			res.Error = err.Error()
			return finish()
		}
		c.Close()
		res.OK = true
		return finish()
	case "tls":
		host, _, err := net.SplitHostPort(req.Target)
		if err != nil {
			res.Error = "target must be host:port"
			return res
		}
		d := tls.Dialer{Config: &tls.Config{ServerName: host, InsecureSkipVerify: req.InsecureVerify}} //nolint:gosec // explicit operator choice
		c, err := d.DialContext(ctx, "tcp", req.Target)
		if err != nil {
			res.Error = err.Error()
			return finish()
		}
		cs := c.(*tls.Conn).ConnectionState()
		certInfo(&cs)
		c.Close()
		res.OK = true
		return finish()
	case "http":
		u, err := url.Parse(req.Target)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			res.Error = "target must be an http(s) URL"
			return res
		}
		tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: req.InsecureVerify}, DisableKeepAlives: true} //nolint:gosec
		hc := &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		hr, _ := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
		hr.Header.Set("User-Agent", "nexus-agent-probe/1")
		resp, err := hc.Do(hr)
		if err != nil {
			res.Error = err.Error()
			return finish()
		}
		resp.Body.Close()
		res.Status = resp.StatusCode
		certInfo(resp.TLS)
		want := req.ExpectStatus
		if want == 0 {
			res.OK = resp.StatusCode < 400
		} else {
			res.OK = resp.StatusCode == want
		}
		if !res.OK {
			res.Error = fmt.Sprintf("unexpected status %d", resp.StatusCode)
		}
		return finish()
	}
	res.Error = "kind must be tcp, tls or http, got " + strings.TrimSpace(req.Kind)
	return res
}
