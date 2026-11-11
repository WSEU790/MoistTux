// Command nexus-release creates the signing key and signed update manifests
// used by the NEXUS self-updater. It runs in CI; the private key is supplied
// through the NEXUS_UPDATE_SIGNING_KEY secret and never committed.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/WSEU790/MoistTux/internal/core/update"
)

func fail(f string, a ...any) {
	fmt.Fprintf(os.Stderr, "nexus-release: "+f+"\n", a...)
	os.Exit(1)
}

func main() {
	if len(os.Args) < 2 {
		fail("usage: nexus-release keygen|manifest|verify …")
	}
	switch os.Args[1] {
	case "keygen":
		fs := flag.NewFlagSet("keygen", flag.ExitOnError)
		out := fs.String("out", "", "file to write the PRIVATE key to (0600)")
		pubOut := fs.String("pub", "", "file to write the public key to")
		_ = fs.Parse(os.Args[2:])
		if *out == "" || *pubOut == "" {
			fail("keygen needs -out and -pub")
		}
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			fail("%v", err)
		}
		if err := os.WriteFile(*out, []byte(base64.StdEncoding.EncodeToString(priv)+"\n"), 0o600); err != nil {
			fail("%v", err)
		}
		if err := os.WriteFile(*pubOut, []byte(base64.StdEncoding.EncodeToString(pub)+"\n"), 0o644); err != nil {
			fail("%v", err)
		}
		fmt.Println("wrote private key to", *out, "and public key to", *pubOut)
	case "manifest":
		fs := flag.NewFlagSet("manifest", flag.ExitOnError)
		version := fs.String("version", "", "release version, e.g. 0.3.17")
		commit := fs.String("commit", "", "git commit")
		notesFile := fs.String("notes-file", "", "release notes (Markdown)")
		installer := fs.String("installer", "", "Windows installer path")
		pubFile := fs.String("pub", "", "public key file to verify the signature against")
		outDir := fs.String("out", ".", "output directory")
		_ = fs.Parse(os.Args[2:])
		key := os.Getenv("NEXUS_UPDATE_SIGNING_KEY")
		if key == "" {
			fail("NEXUS_UPDATE_SIGNING_KEY is not set; add the release signing key as a repository secret")
		}
		if *version == "" || *installer == "" {
			fail("manifest needs -version and -installer")
		}
		f, err := os.Open(*installer)
		if err != nil {
			fail("%v", err)
		}
		h := sha256.New()
		n, err := io.Copy(h, f)
		f.Close()
		if err != nil {
			fail("%v", err)
		}
		notes := ""
		if *notesFile != "" {
			b, _ := os.ReadFile(*notesFile)
			notes = strings.TrimSpace(string(b))
			if len(notes) > 8000 {
				notes = notes[:8000] + "\n…"
			}
		}
		m := update.Manifest{Product: "NEXUS", Version: *version, Released: time.Now().UTC().Truncate(time.Second), Commit: *commit, Notes: notes,
			Files: map[string]update.Asset{"windows-amd64": {Name: filepath.Base(*installer), SHA256: hex.EncodeToString(h.Sum(nil)), Size: n}}}
		mb, _ := json.MarshalIndent(m, "", "  ")
		sig, err := update.Sign(key, mb)
		if err != nil {
			fail("%v", err)
		}
		if *pubFile != "" {
			pub, _ := os.ReadFile(*pubFile)
			if err := update.Verify(string(pub), mb, []byte(sig)); err != nil {
				fail("the signing key does not match %s: %v", *pubFile, err)
			}
		}
		if err := os.WriteFile(filepath.Join(*outDir, update.ManifestName), mb, 0o644); err != nil {
			fail("%v", err)
		}
		if err := os.WriteFile(filepath.Join(*outDir, update.SignatureName), []byte(sig+"\n"), 0o644); err != nil {
			fail("%v", err)
		}
		fmt.Printf("signed manifest for %s (%s, %d bytes)\n", *version, filepath.Base(*installer), n)
	case "verify":
		fs := flag.NewFlagSet("verify", flag.ExitOnError)
		pubFile := fs.String("pub", "", "public key file")
		dir := fs.String("dir", ".", "directory with the manifest and signature")
		_ = fs.Parse(os.Args[2:])
		pub, err := os.ReadFile(*pubFile)
		if err != nil {
			fail("%v", err)
		}
		mb, _ := os.ReadFile(filepath.Join(*dir, update.ManifestName))
		sb, _ := os.ReadFile(filepath.Join(*dir, update.SignatureName))
		if err := update.Verify(string(pub), mb, sb); err != nil {
			fail("%v", err)
		}
		fmt.Println("signature OK")
	default:
		fail("unknown command %q", os.Args[1])
	}
}
