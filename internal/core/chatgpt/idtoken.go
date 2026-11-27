package chatgpt

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"
)

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

type idClaims struct {
	Issuer   string          `json:"iss"`
	Subject  string          `json:"sub"`
	Audience json.RawMessage `json:"aud"`
	Expiry   int64           `json:"exp"`
	Nonce    string          `json:"nonce"`
	Email    string          `json:"email"`
	Name     string          `json:"name"`
}

func (c idClaims) hasAudience(aud string) bool {
	var one string
	if json.Unmarshal(c.Audience, &one) == nil {
		return one == aud
	}
	var many []string
	if json.Unmarshal(c.Audience, &many) == nil {
		for _, a := range many {
			if a == aud {
				return true
			}
		}
	}
	return false
}

func (m *Manager) keys(ctx context.Context, refresh bool) (map[string]jwk, error) {
	m.jwksMu.Lock()
	defer m.jwksMu.Unlock()
	if !refresh && m.jwks != nil && time.Since(m.jwksAt) < time.Hour {
		return m.jwks, nil
	}
	req, err := http.NewRequestWithContext(ctx, "GET", m.ep.JWKS, nil)
	if err != nil {
		return nil, err
	}
	resp, err := m.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot fetch OpenAI's signing keys: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("cannot fetch OpenAI's signing keys: HTTP %d", resp.StatusCode)
	}
	var set struct {
		Keys []jwk `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&set); err != nil {
		return nil, err
	}
	keys := map[string]jwk{}
	for _, k := range set.Keys {
		keys[k.Kid] = k
	}
	m.jwks, m.jwksAt = keys, time.Now()
	return keys, nil
}

func b64(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
}

// verifyIDToken checks the signature (RS256 or ES256) against OpenAI's JWKS
// and the issuer, audience, expiry and nonce.
func (m *Manager) verifyIDToken(ctx context.Context, raw, clientID, nonce string) (*idClaims, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return nil, errors.New("no ID token was returned")
	}
	hb, err := b64(parts[0])
	if err != nil {
		return nil, errors.New("malformed header")
	}
	var hdr struct{ Alg, Kid string }
	if err := json.Unmarshal(hb, &hdr); err != nil {
		return nil, errors.New("malformed header")
	}
	sig, err := b64(parts[2])
	if err != nil {
		return nil, errors.New("malformed signature")
	}
	keys, err := m.keys(ctx, false)
	if err != nil {
		return nil, err
	}
	k, ok := keys[hdr.Kid]
	if !ok { // keys rotate: fetch once more
		if keys, err = m.keys(ctx, true); err != nil {
			return nil, err
		}
		if k, ok = keys[hdr.Kid]; !ok {
			return nil, fmt.Errorf("signed with an unknown key %q", hdr.Kid)
		}
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	switch hdr.Alg {
	case "RS256":
		if k.Kty != "RSA" {
			return nil, errors.New("key type does not match the algorithm")
		}
		nb, err1 := b64(k.N)
		eb, err2 := b64(k.E)
		if err1 != nil || err2 != nil || len(eb) == 0 || len(eb) > 4 {
			return nil, errors.New("malformed RSA key")
		}
		e := 0
		for _, b := range eb {
			e = e<<8 | int(b)
		}
		pub := &rsa.PublicKey{N: new(big.Int).SetBytes(nb), E: e}
		if pub.N.BitLen() < 2048 {
			return nil, errors.New("RSA key too small")
		}
		if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig); err != nil {
			return nil, errors.New("bad signature")
		}
	case "ES256":
		if k.Kty != "EC" || k.Crv != "P-256" || len(sig) != 64 {
			return nil, errors.New("key type does not match the algorithm")
		}
		xb, err1 := b64(k.X)
		yb, err2 := b64(k.Y)
		if err1 != nil || err2 != nil {
			return nil, errors.New("malformed EC key")
		}
		pub := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(xb), Y: new(big.Int).SetBytes(yb)}
		if !ecdsa.Verify(pub, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
			return nil, errors.New("bad signature")
		}
	default:
		return nil, fmt.Errorf("unsupported signing algorithm %q", hdr.Alg)
	}
	pb, err := b64(parts[1])
	if err != nil {
		return nil, errors.New("malformed claims")
	}
	var c idClaims
	if err := json.Unmarshal(pb, &c); err != nil {
		return nil, errors.New("malformed claims")
	}
	switch {
	case c.Issuer != m.ep.Issuer:
		return nil, fmt.Errorf("unexpected issuer %q", c.Issuer)
	case !c.hasAudience(clientID):
		return nil, errors.New("the token was issued to a different app")
	case c.Expiry == 0 || m.now().After(time.Unix(c.Expiry, 0).Add(time.Minute)):
		return nil, errors.New("the token has expired (check this PC's clock)")
	case nonce == "" || c.Nonce != nonce:
		return nil, errors.New("the nonce does not match this sign-in")
	case c.Subject == "":
		return nil, errors.New("the token has no subject")
	}
	return &c, nil
}
