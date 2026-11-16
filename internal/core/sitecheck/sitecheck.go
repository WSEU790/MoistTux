// Package sitecheck runs website and certificate checks from the NEXUS PC —
// what visitors see from outside, as opposed to the agent's view from inside
// the server. Failures and expiring certificates raise alerts through the
// monitor, so they group into the hosting server's incidents and use the
// normal notifications.
package sitecheck

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/WSEU790/MoistTux/internal/core/events"
	"github.com/WSEU790/MoistTux/internal/core/monitor"
	"github.com/WSEU790/MoistTux/internal/core/store"
)

const (
	RuleDown = "site_down"
	RuleSlow = "site_slow"
	RuleCert = "site_certificate"
)

// Summary is a check with its current state, for the UI and the AI.
type Summary struct {
	*store.SiteCheck
	State       string            `json:"state"` // up | down | pending | disabled
	LastResult  *store.SiteResult `json:"last_result,omitempty"`
	Uptime24h   *float64          `json:"uptime_24h,omitempty"`
	Uptime7d    *float64          `json:"uptime_7d,omitempty"`
	AvgMs24h    *float64          `json:"avg_ms_24h,omitempty"`
	CertDays    *int              `json:"cert_days_left,omitempty"`
	CertExpires *time.Time        `json:"cert_expires,omitempty"`
}

type Runner struct {
	st  *store.Store
	mon *monitor.Engine
	bus *events.Bus
	log *log.Logger

	mu       sync.Mutex
	lastRun  map[string]time.Time
	failures map[string]int
	running  map[string]bool
}

func New(st *store.Store, mon *monitor.Engine, bus *events.Bus, logger *log.Logger) *Runner {
	return &Runner{st: st, mon: mon, bus: bus, log: logger, lastRun: map[string]time.Time{}, failures: map[string]int{}, running: map[string]bool{}}
}

// Validate normalises a check before it is saved.
func Validate(c *store.SiteCheck) error {
	c.Name = strings.TrimSpace(c.Name)
	c.URL = strings.TrimSpace(c.URL)
	u, err := url.Parse(c.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("the URL must start with http:// or https:// and include a host")
	}
	if c.Name == "" {
		c.Name = u.Host
	}
	if c.ExpectStatus == "" {
		c.ExpectStatus = "200-399"
	}
	if _, err := parseStatus(c.ExpectStatus); err != nil {
		return err
	}
	if c.IntervalSec == 0 {
		c.IntervalSec = 60
	}
	if c.IntervalSec < 30 || c.IntervalSec > 86400 {
		return errors.New("interval must be between 30 seconds and 24 hours")
	}
	if c.TimeoutSec == 0 {
		c.TimeoutSec = 10
	}
	if c.TimeoutSec < 1 || c.TimeoutSec > 60 {
		return errors.New("timeout must be between 1 and 60 seconds")
	}
	if c.FailThreshold == 0 {
		c.FailThreshold = 2
	}
	if c.FailThreshold < 1 || c.FailThreshold > 10 {
		return errors.New("alert after 1 to 10 consecutive failures")
	}
	if c.CertWarnDays == 0 {
		c.CertWarnDays = 14
	}
	if c.SlowMs < 0 || c.CertWarnDays < 1 || c.CertWarnDays > 90 || len(c.ExpectText) > 500 {
		return errors.New("invalid slow threshold, certificate warning days or expected text")
	}
	return nil
}

// parseStatus accepts "200", "200-399" and comma lists of either.
func parseStatus(spec string) ([][2]int, error) {
	var out [][2]int
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		lo, hi, isRange := strings.Cut(part, "-")
		a, err1 := strconv.Atoi(strings.TrimSpace(lo))
		b := a
		var err2 error
		if isRange {
			b, err2 = strconv.Atoi(strings.TrimSpace(hi))
		}
		if err1 != nil || err2 != nil || a < 100 || b > 599 || a > b {
			return nil, fmt.Errorf("expected status %q: use codes or ranges such as 200-399 or 200,301", spec)
		}
		out = append(out, [2]int{a, b})
	}
	return out, nil
}

// Run checks every due check every few seconds and prunes old results.
func (r *Runner) Run(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	lastPrune := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		checks, err := r.st.ListSiteChecks(ctx)
		if err != nil {
			continue
		}
		now := time.Now()
		for _, c := range checks {
			if !c.Enabled {
				continue
			}
			r.mu.Lock()
			due := !r.running[c.ID] && now.Sub(r.lastRun[c.ID]) >= time.Duration(c.IntervalSec)*time.Second
			if due {
				r.running[c.ID], r.lastRun[c.ID] = true, now
			}
			r.mu.Unlock()
			if due {
				go func(c *store.SiteCheck) {
					defer func() { r.mu.Lock(); r.running[c.ID] = false; r.mu.Unlock() }()
					r.RunOnce(ctx, c)
				}(c)
			}
		}
		if now.Sub(lastPrune) > time.Hour {
			r.st.PruneSiteResults(ctx, now.Add(-30*24*time.Hour))
			lastPrune = now
		}
	}
}

// RunOnce performs one check, records it and updates alerts.
func (r *Runner) RunOnce(ctx context.Context, c *store.SiteCheck) *store.SiteResult {
	res := Probe(ctx, c)
	_ = r.st.AddSiteResult(ctx, res)
	r.evaluate(ctx, c, res)
	r.bus.Publish("sitecheck", map[string]any{"check_id": c.ID, "result": res})
	return res
}

func (r *Runner) evaluate(ctx context.Context, c *store.SiteCheck, res *store.SiteResult) {
	r.mu.Lock()
	if res.OK {
		r.failures[c.ID] = 0
	} else {
		r.failures[c.ID]++
	}
	fails := r.failures[c.ID]
	r.mu.Unlock()
	subject := c.ID
	ev := map[string]any{"url": c.URL, "status": res.Status, "latency_ms": res.LatencyMs, "error": res.Error}
	if res.OK {
		r.mon.Clear(ctx, c.ServerID, RuleDown, subject)
	} else if fails >= c.FailThreshold {
		r.mon.Raise(ctx, c.ServerID, RuleDown, subject, "critical", "Website down: "+c.Name,
			fmt.Sprintf("%s failed %d checks in a row from NEXUS: %s", c.URL, fails, res.Error), ev)
	}
	if c.SlowMs > 0 && res.OK {
		if res.LatencyMs > int64(c.SlowMs) {
			r.mon.Raise(ctx, c.ServerID, RuleSlow, subject, "warning", "Website slow: "+c.Name,
				fmt.Sprintf("%s answered in %d ms (threshold %d ms)", c.URL, res.LatencyMs, c.SlowMs), ev)
		} else {
			r.mon.Clear(ctx, c.ServerID, RuleSlow, subject)
		}
	}
	if res.CertExpires != nil {
		days := int(time.Until(*res.CertExpires).Hours() / 24)
		switch {
		case days <= 3:
			r.mon.Raise(ctx, c.ServerID, RuleCert, subject, "critical", "Certificate expires in "+strconv.Itoa(days)+" days: "+c.Name,
				fmt.Sprintf("The TLS certificate of %s expires on %s.", c.URL, res.CertExpires.Format("2006-01-02")), ev)
		case days <= c.CertWarnDays:
			r.mon.Raise(ctx, c.ServerID, RuleCert, subject, "warning", "Certificate expires in "+strconv.Itoa(days)+" days: "+c.Name,
				fmt.Sprintf("The TLS certificate of %s expires on %s. Check that renewal works (certbot renew --dry-run).", c.URL, res.CertExpires.Format("2006-01-02")), ev)
		default:
			r.mon.Clear(ctx, c.ServerID, RuleCert, subject)
		}
	}
}

// Probe performs one HTTP request with normal certificate verification.
func Probe(ctx context.Context, c *store.SiteCheck) *store.SiteResult {
	res := &store.SiteResult{CheckID: c.ID, Time: time.Now().UTC()}
	ranges, _ := parseStatus(c.ExpectStatus)
	timeout := time.Duration(c.TimeoutSec) * time.Second
	tr := &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: timeout}).DialContext, TLSHandshakeTimeout: timeout, DisableKeepAlives: true}
	hc := &http.Client{Transport: tr, Timeout: timeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("more than 5 redirects")
		}
		return nil
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL, nil)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	req.Header.Set("User-Agent", "NEXUS-website-check/1")
	start := time.Now()
	resp, err := hc.Do(req)
	res.LatencyMs = time.Since(start).Milliseconds()
	if err != nil {
		res.Error = describe(err)
		// Read the certificate anyway so an expired one is reported clearly.
		if u, perr := url.Parse(c.URL); perr == nil && u.Scheme == "https" {
			if exp := peekCert(u, timeout); exp != nil {
				res.CertExpires = exp
			}
		}
		return res
	}
	defer resp.Body.Close()
	res.Status = resp.StatusCode
	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		exp := resp.TLS.PeerCertificates[0].NotAfter.UTC()
		res.CertExpires = &exp
	}
	statusOK := false
	for _, rg := range ranges {
		if resp.StatusCode >= rg[0] && resp.StatusCode <= rg[1] {
			statusOK = true
		}
	}
	if !statusOK {
		res.Error = fmt.Sprintf("HTTP %d (expected %s)", resp.StatusCode, c.ExpectStatus)
		return res
	}
	if c.ExpectText != "" {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		if !strings.Contains(string(body), c.ExpectText) {
			res.Error = fmt.Sprintf("the page does not contain %q", c.ExpectText)
			return res
		}
	}
	res.OK = true
	return res
}

func describe(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	var ce *tls.CertificateVerificationError
	var ui x509.UnknownAuthorityError
	var hn x509.HostnameError
	var inv x509.CertificateInvalidError
	var ne net.Error
	var dnsErr *net.DNSError
	switch {
	case errors.As(err, &inv) && inv.Reason == x509.Expired:
		return "the TLS certificate has expired"
	case errors.As(err, &hn):
		return "the TLS certificate does not match the host name: " + hn.Error()
	case errors.As(err, &ui):
		return "the TLS certificate is not trusted (self-signed or missing intermediate)"
	case errors.As(err, &ce):
		return "TLS certificate problem: " + ce.Error()
	case errors.As(err, &dnsErr):
		return "DNS lookup failed: " + dnsErr.Error()
	case errors.As(err, &ne) && ne.Timeout():
		return "timed out"
	}
	if strings.Contains(err.Error(), "connection refused") {
		return "connection refused (nothing listening, or the server is down)"
	}
	return err.Error()
}

func peekCert(u *url.URL, timeout time.Duration) *time.Time {
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		port = "443"
	}
	d := &net.Dialer{Timeout: timeout}
	conn, err := tls.DialWithDialer(d, "tcp", net.JoinHostPort(host, port), &tls.Config{ServerName: host, InsecureSkipVerify: true}) //nolint:gosec // only reads the expiry date
	if err != nil {
		return nil
	}
	defer conn.Close()
	cs := conn.ConnectionState()
	if len(cs.PeerCertificates) == 0 {
		return nil
	}
	t := cs.PeerCertificates[0].NotAfter.UTC()
	return &t
}

// Summaries returns every check with its state and statistics.
func (r *Runner) Summaries(ctx context.Context) ([]Summary, error) {
	checks, err := r.st.ListSiteChecks(ctx)
	if err != nil {
		return nil, err
	}
	out := []Summary{}
	now := time.Now()
	for _, c := range checks {
		s := Summary{SiteCheck: c, State: "pending"}
		res, _ := r.st.SiteResults(ctx, c.ID, now.Add(-7*24*time.Hour), 100000)
		if len(res) > 0 {
			last := res[len(res)-1]
			s.LastResult = &last
			if last.OK {
				s.State = "up"
			} else {
				s.State = "down"
			}
			s.Uptime7d = uptime(res, time.Time{})
			s.Uptime24h = uptime(res, now.Add(-24*time.Hour))
			var sum, n float64
			for _, x := range res {
				if x.OK && x.Time.After(now.Add(-24*time.Hour)) {
					sum += float64(x.LatencyMs)
					n++
				}
			}
			if n > 0 {
				v := float64(int(sum/n*10)) / 10
				s.AvgMs24h = &v
			}
			for i := len(res) - 1; i >= 0; i-- {
				if res[i].CertExpires != nil {
					d := int(time.Until(*res[i].CertExpires).Hours() / 24)
					s.CertDays, s.CertExpires = &d, res[i].CertExpires
					break
				}
			}
		}
		if !c.Enabled {
			s.State = "disabled"
		}
		out = append(out, s)
	}
	return out, nil
}

func uptime(res []store.SiteResult, since time.Time) *float64 {
	var ok, n float64
	for _, r := range res {
		if r.Time.Before(since) {
			continue
		}
		n++
		if r.OK {
			ok++
		}
	}
	if n == 0 {
		return nil
	}
	v := float64(int(ok/n*10000)) / 100
	return &v
}
