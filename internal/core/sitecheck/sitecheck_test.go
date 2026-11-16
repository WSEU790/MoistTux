package sitecheck

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WSEU790/MoistTux/internal/core/events"
	"github.com/WSEU790/MoistTux/internal/core/monitor"
	"github.com/WSEU790/MoistTux/internal/core/store"
)

type nopNotifier struct{}

func (nopNotifier) Notify(context.Context, *store.Notification) {}

func setup(t *testing.T) (*Runner, *store.Store, *store.Server) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv := &store.Server{Name: "web-01", Address: "10.0.0.1", Port: 7443, AgentFingerprint: "ab", Environment: "production", MonitoringEnabled: true}
	if err := st.CreateServer(context.Background(), srv); err != nil {
		t.Fatal(err)
	}
	mon := monitor.New(st, events.New(), nopNotifier{}, monitor.DefaultConfig(), log.New(io.Discard, "", 0))
	return New(st, mon, events.New(), log.New(io.Discard, "", 0)), st, srv
}

func check(t *testing.T, st *store.Store, srv *store.Server, url string, mod func(*store.SiteCheck)) *store.SiteCheck {
	t.Helper()
	c := &store.SiteCheck{ServerID: srv.ID, URL: url, Enabled: true, TimeoutSec: 3}
	if mod != nil {
		mod(c)
	}
	if err := Validate(c); err != nil {
		t.Fatal(err)
	}
	if err := st.PutSiteCheck(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestProbeOutcomes(t *testing.T) {
	_, st, srv := setup(t)
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/err":
			w.WriteHeader(503)
		default:
			io.WriteString(w, "<html>Welcome to the shop</html>")
		}
	}))
	defer ok.Close()
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := "http://" + l.Addr().String()
	l.Close()

	cases := []struct {
		url, text, wantErr string
		wantOK             bool
	}{
		{ok.URL + "/", "Welcome", "", true},
		{ok.URL + "/err", "", "HTTP 503", false},
		{ok.URL + "/", "Checkout", "does not contain", false},
		{dead, "", "connection refused", false},
	}
	for _, c := range cases {
		sc := check(t, st, srv, c.url, func(s *store.SiteCheck) { s.ExpectText = c.text })
		res := Probe(context.Background(), sc)
		if res.OK != c.wantOK || !strings.Contains(res.Error, c.wantErr) {
			t.Errorf("%s: ok=%v err=%q", c.url, res.OK, res.Error)
		}
	}
}

func TestDownAlertNeedsConsecutiveFailuresAndClears(t *testing.T) {
	r, st, srv := setup(t)
	healthy := true
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !healthy {
			w.WriteHeader(502)
		}
	}))
	defer ts.Close()
	c := check(t, st, srv, ts.URL, func(s *store.SiteCheck) { s.FailThreshold = 2 })
	ctx := context.Background()
	firing := func() int {
		a, _ := st.FiringForServer(ctx, srv.ID)
		n := 0
		for _, x := range a {
			if x.Rule == RuleDown {
				n++
			}
		}
		return n
	}
	r.RunOnce(ctx, c)
	healthy = false
	r.RunOnce(ctx, c)
	if firing() != 0 {
		t.Fatal("alert after a single failure")
	}
	r.RunOnce(ctx, c)
	if firing() != 1 {
		t.Fatal("no alert after two consecutive failures")
	}
	healthy = true
	r.RunOnce(ctx, c)
	if firing() != 0 {
		t.Fatal("alert not cleared after recovery")
	}
	sums, _ := r.Summaries(ctx)
	if len(sums) != 1 || sums[0].State != "up" || sums[0].Uptime24h == nil || *sums[0].Uptime24h != 50 {
		t.Fatalf("summary: %+v", sums[0])
	}
}

func TestCertificateExpiry(t *testing.T) {
	r, st, srv := setup(t)
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "127.0.0.1"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(50 * time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	ts.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: k}}}
	ts.StartTLS()
	defer ts.Close()
	c := check(t, st, srv, ts.URL, nil)
	res := r.RunOnce(context.Background(), c)
	// Self-signed: the request fails, but the expiry is still read and reported.
	if res.OK || !strings.Contains(res.Error, "not trusted") || res.CertExpires == nil {
		t.Fatalf("result %+v", res)
	}
	a, _ := st.FiringForServer(context.Background(), srv.ID)
	found := false
	for _, x := range a {
		if x.Rule == RuleCert && x.Severity == "critical" {
			found = true
		}
	}
	if !found {
		t.Fatal("certificate expiring in 2 days did not raise a critical alert")
	}
}

func TestValidate(t *testing.T) {
	for _, bad := range []store.SiteCheck{
		{URL: "ftp://example.com"}, {URL: "example.com"}, {URL: "https://x.test", ExpectStatus: "abc"},
		{URL: "https://x.test", IntervalSec: 5}, {URL: "https://x.test", TimeoutSec: 120},
	} {
		b := bad
		if err := Validate(&b); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	c := store.SiteCheck{URL: "https://shop.example.com/health"}
	if err := Validate(&c); err != nil || c.Name != "shop.example.com" || c.IntervalSec != 60 || c.ExpectStatus != "200-399" {
		t.Fatalf("defaults: %+v %v", c, err)
	}
	if r, err := parseStatus("200, 301-302"); err != nil || len(r) != 2 || r[1] != [2]int{301, 302} {
		t.Fatalf("parse: %v %v", r, err)
	}
}
