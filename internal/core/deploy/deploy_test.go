package deploy

import (
	"bytes"
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"

	"github.com/WSEU790/MoistTux/internal/core/events"
	"github.com/WSEU790/MoistTux/internal/core/secrets"
	"github.com/WSEU790/MoistTux/internal/core/store"
)

func TestSavedLoginIsSealedAndRoundTrips(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "nexus.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	prot, err := secrets.NewFileKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	srv := &store.Server{Name: "web-01", Address: "10.0.0.5", Port: 7443, AgentFingerprint: "ab", MonitoringEnabled: true}
	if err := st.CreateServer(ctx, srv); err != nil {
		t.Fatal(err)
	}
	m := NewManager(nil, st, events.New(), prot, log.New(io.Discard, "", 0))
	const pw = "Very-Secret-Passw0rd-7f3a"
	req := Request{Host: "10.0.0.5", SSHPort: 22, User: "deploy", Password: pw, SudoPass: pw, HostKey: "SHA256:" + string(bytes.Repeat([]byte("A"), 43))}
	c, err := m.sealFor(req)
	if err != nil {
		t.Fatal(err)
	}
	j := &Job{toSave: c, Facts: map[string]string{}}
	m.persistSaved(j, srv.ID)
	if j.Remembered != "saved" {
		t.Fatalf("not saved: %s", j.Remembered)
	}

	// Metadata is available, the secret never appears in plaintext on disk.
	meta, err := m.Saved(ctx, srv.ID)
	if err != nil || meta.User != "deploy" || meta.AuthKind != "password" {
		t.Fatalf("metadata: %+v %v", meta, err)
	}
	_ = st.Close()
	for _, f := range []string{"nexus.db", "nexus.db-wal"} {
		if b, err := os.ReadFile(filepath.Join(dir, f)); err == nil && bytes.Contains(b, []byte(pw)) {
			t.Fatalf("plaintext password found in %s", f)
		}
	}
	st, _ = store.Open(dbPath)
	defer st.Close()
	m = NewManager(nil, st, events.New(), prot, log.New(io.Discard, "", 0))

	// A later upgrade can use it without the operator typing anything.
	up := Request{Mode: "upgrade", ServerID: srv.ID, UseSaved: true}
	if err := m.loadSaved(ctx, &up); err != nil {
		t.Fatal(err)
	}
	if up.Password != pw || up.SudoPass != pw || up.User != "deploy" || up.Host != "10.0.0.5" || up.HostKey != req.HostKey {
		t.Fatalf("round trip lost data: %+v", up)
	}

	// A different key (another Windows account / machine) cannot open it.
	other, _ := secrets.NewFileKey(t.TempDir())
	m2 := NewManager(nil, st, events.New(), other, log.New(io.Discard, "", 0))
	if err := m2.loadSaved(ctx, &Request{ServerID: srv.ID}); err == nil {
		t.Fatal("saved login opened with the wrong key")
	}

	// Forget removes it; deleting the server would cascade as well.
	if ok, _ := m.Forget(ctx, srv.ID, "test"); !ok {
		t.Fatal("forget reported nothing deleted")
	}
	if _, err := m.Saved(ctx, srv.ID); err == nil {
		t.Fatal("login still present after forget")
	}
	if _, err := m.Start(Request{Mode: "upgrade", ServerID: srv.ID, UseSaved: true}); err == nil {
		t.Fatal("upgrade with a forgotten login was accepted")
	}
}
