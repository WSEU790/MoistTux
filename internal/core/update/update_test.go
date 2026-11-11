package update

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.3.10", "0.3.9", 1}, {"v0.3.2", "0.3.2", 0}, {"0.3.2", "0.3.2-dev", 1}, {"0.4", "0.3.99", 1},
		{"1.0.0", "1.0.0.1", -1}, {"0.3.2-dirty", "0.3.3", -1},
	}
	for _, c := range cases {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%s,%s)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

type fakeGitHub struct {
	srv       *httptest.Server
	tag       string
	manifest  []byte
	sig       string
	installer []byte
	sizeLie   int64
}

func newFake(t *testing.T, priv ed25519.PrivateKey, version, tag string, installer []byte, tamper func(*Manifest)) *fakeGitHub {
	f := &fakeGitHub{tag: tag, installer: installer}
	sum := sha256.Sum256(installer)
	m := Manifest{Product: "NEXUS", Version: version, Released: time.Now().UTC(), Notes: "test release",
		Files: map[string]Asset{Platform(): {Name: "NEXUS-Setup-" + version + ".exe", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(installer))}}}
	f.manifest, _ = json.Marshal(m)
	f.sig = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, f.manifest))
	if tamper != nil {
		tamper(&m) // modify after signing: the signature must no longer verify
		f.manifest, _ = json.Marshal(m)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/"+Repo+"/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		size := int64(len(f.installer))
		if f.sizeLie != 0 {
			size = f.sizeLie
		}
		fmt.Fprintf(w, `{"tag_name":%q,"html_url":"https://example/rel","assets":[{"name":%q,"browser_download_url":%q,"size":%d},{"name":%q,"browser_download_url":%q,"size":1},{"name":%q,"browser_download_url":%q,"size":%d}]}`,
			f.tag, ManifestName, f.srv.URL+"/m", len(f.manifest), SignatureName, f.srv.URL+"/s", "NEXUS-Setup-"+version+".exe", f.srv.URL+"/i", size)
	})
	mux.HandleFunc("/m", func(w http.ResponseWriter, r *http.Request) { w.Write(f.manifest) })
	mux.HandleFunc("/s", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(f.sig)) })
	mux.HandleFunc("/i", func(w http.ResponseWriter, r *http.Request) { w.Write(f.installer) })
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func keys(t *testing.T) (string, ed25519.PrivateKey) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(pub), priv
}

func TestSignedUpdateDownloadsAndVerifies(t *testing.T) {
	pub, priv := keys(t)
	dir := t.TempDir()
	inst := []byte("MZ fake installer bytes for test")
	f := newFake(t, priv, "0.3.12", "v0.3.12", inst, nil)
	u := New("0.3.11", dir, nil, nil)
	u.SetAPIForTest(f.srv.URL, pub)
	if err := u.CheckAndDownload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s := u.Status(); s.State != "ready" || s.Latest != "0.3.12" {
		t.Fatalf("status %+v", s)
	}
	p, err := TakePending(dir, "0.3.11")
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p.Path); string(b) != string(inst) {
		t.Fatal("installer content differs")
	}
	// Once the new version runs, the pending update is discarded.
	if _, err := TakePending(dir, "0.3.12"); err == nil {
		t.Fatal("pending update offered again after install")
	}
	if _, err := os.Stat(filepath.Join(dir, "updates")); !os.IsNotExist(err) {
		t.Fatal("updates directory not cleaned up")
	}
}

func TestRejections(t *testing.T) {
	pub, priv := keys(t)
	otherPub, _ := keys(t)
	inst := []byte("installer")
	cases := []struct {
		name    string
		current string
		pub     string
		fake    func() *fakeGitHub
		state   string
	}{
		{"tampered manifest", "0.3.1", pub, func() *fakeGitHub {
			return newFake(t, priv, "0.3.2", "v0.3.2", inst, func(m *Manifest) { m.Notes = "evil" })
		}, "error"},
		{"signed by another key", "0.3.1", otherPub, func() *fakeGitHub { return newFake(t, priv, "0.3.2", "v0.3.2", inst, nil) }, "error"},
		{"tag mismatch", "0.3.1", pub, func() *fakeGitHub { return newFake(t, priv, "0.3.9", "v0.3.2", inst, nil) }, "error"},
		{"not newer (no downgrade)", "0.3.5", pub, func() *fakeGitHub { return newFake(t, priv, "0.3.4", "v0.3.4", inst, nil) }, "up_to_date"},
		{"size lie", "0.3.1", pub, func() *fakeGitHub {
			f := newFake(t, priv, "0.3.2", "v0.3.2", inst, nil)
			f.sizeLie = 99
			return f
		}, "error"},
		{"swapped installer", "0.3.1", pub, func() *fakeGitHub {
			f := newFake(t, priv, "0.3.2", "v0.3.2", inst, nil)
			f.installer = []byte("evilevile") // same length, different bytes
			return f
		}, "error"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			f := c.fake()
			u := New(c.current, dir, nil, nil)
			u.SetAPIForTest(f.srv.URL, c.pub)
			_ = u.CheckAndDownload(context.Background())
			if s := u.Status(); s.State != c.state {
				t.Fatalf("state %s (%s), want %s", s.State, s.Error, c.state)
			}
			if _, err := ReadPending(dir); err == nil {
				t.Fatal("a rejected update was marked pending")
			}
		})
	}
}

func TestDevBuildsDisabled(t *testing.T) {
	u := New("0.3.0-dev", t.TempDir(), nil, nil)
	if u.Status().Enabled {
		t.Fatal("dev build without a key must not update")
	}
}
