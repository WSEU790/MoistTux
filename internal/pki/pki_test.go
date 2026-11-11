package pki

import (
	"strings"
	"testing"
	"time"
)

func TestEnrollmentCodeRoundTrip(t *testing.T) {
	id, err := NewIdentity("nexus-agent:test", []string{"localhost", "127.0.0.1"}, time.Hour, true, false)
	if err != nil {
		t.Fatal(err)
	}
	fp, err := id.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	code, err := NewEnrollmentCode(fp)
	if err != nil {
		t.Fatal(err)
	}
	s := code.String()
	if !strings.HasPrefix(s, "NX1-") {
		t.Fatalf("unexpected code format %q", s)
	}
	// Operators paste codes with stray whitespace and mixed case.
	parsed, err := ParseEnrollmentCode("  " + strings.ToLower(s[:20]) + "\n" + s[20:] + " ")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.TokenHash() != code.TokenHash() {
		t.Fatal("token changed in round trip")
	}
	if !parsed.MatchesFingerprint(fp) {
		t.Fatal("code should match its own agent fingerprint")
	}
	other, _ := NewIdentity("nexus-agent:other", nil, time.Hour, true, false)
	ofp, _ := other.Fingerprint()
	if parsed.MatchesFingerprint(ofp) {
		t.Fatal("code must not match a different agent certificate")
	}
}

func TestParseEnrollmentCodeRejectsGarbage(t *testing.T) {
	for _, c := range []string{"", "NX1", "NX2-AAAA-BBBB", "NX1-!!!!-BBBB", "NX1-AAAA"} {
		if _, err := ParseEnrollmentCode(c); err == nil {
			t.Errorf("expected error for %q", c)
		}
	}
}

func TestFingerprintsEqual(t *testing.T) {
	if !FingerprintsEqual("ABcd", "abCD") {
		t.Fatal("comparison must be case-insensitive")
	}
	if FingerprintsEqual("", "") {
		t.Fatal("empty fingerprints must never match")
	}
	if FingerprintsEqual("abcd", "abce") {
		t.Fatal("different fingerprints matched")
	}
}
