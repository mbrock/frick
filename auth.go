package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type authorizeReq struct {
	Key        string `json:"key"`
	ValidUntil string `json:"validUntil,omitempty"`
}

type authorizeResp struct {
	Token string `json:"token"`
}

// Authorize performs POST /v2/authorize and stores the JWT on the client.
// validUntil defaults to one hour from now; override via Client.AuthTTL.
func (c *Client) Authorize(ctx context.Context) error {
	ttl := c.AuthTTL
	if ttl == 0 {
		ttl = time.Hour
	}
	body, err := json.Marshal(authorizeReq{
		Key:        c.APIKey,
		ValidUntil: time.Now().Add(ttl).Format("2006-01-02T15:04"),
	})
	if err != nil {
		return err
	}
	sig, err := signRSASHA512(c.Key, body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/v2/authorize", nil)
	if err != nil {
		return err
	}
	req.Body = newJSONBody(body)
	req.ContentLength = int64(len(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("algorithm", "rsa-sha512")
	req.Header.Set("Signature", sig)

	raw, status, err := c.doRaw(req)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("authorize: status %d: %s", status, string(raw))
	}
	var r authorizeResp
	if err := json.Unmarshal(raw, &r); err != nil {
		return fmt.Errorf("parse: %w; body=%s", err, string(raw))
	}
	c.JWT = r.Token
	return nil
}

// ---------- JWT cache ----------

type jwtCache struct {
	JWT     string    `json:"jwt"`
	Exp     time.Time `json:"exp"`
	KeyHash string    `json:"keyHash"` // fingerprint of API key
	Base    string    `json:"baseURL"` // guard against swapping prod/sandbox
}

// DefaultJWTCachePath returns $XDG_CACHE_HOME/frickgo/jwt.json
// (or ~/.cache/frickgo/jwt.json).
func DefaultJWTCachePath() string {
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, "frickgo", "jwt.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "frickgo", "jwt.json")
}

func keyFingerprint(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:8])
}

// decodeJWTExp reads the `exp` claim from a JWT's payload. We don't verify
// the signature — Bank Frick signs with their private key and we don't have
// the public half; the token is opaque to us.
func decodeJWTExp(token string) (time.Time, error) {
	parser := jwt.NewParser(jwt.WithoutClaimsValidation())
	claims := jwt.RegisteredClaims{}
	if _, _, err := parser.ParseUnverified(token, &claims); err != nil {
		return time.Time{}, err
	}
	if claims.ExpiresAt == nil {
		return time.Time{}, fmt.Errorf("no exp claim in JWT")
	}
	return claims.ExpiresAt.Time, nil
}

// tryLoadCache reads c.CachePath and, if valid for this key+base and not near
// expiry, populates c.JWT. Returns (true, exp) on hit.
func (c *Client) tryLoadCache() (bool, time.Time) {
	if c.CachePath == "" {
		return false, time.Time{}
	}
	data, err := os.ReadFile(c.CachePath)
	if err != nil {
		return false, time.Time{}
	}
	var cache jwtCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return false, time.Time{}
	}
	if cache.KeyHash != keyFingerprint(c.APIKey) || cache.Base != c.BaseURL {
		return false, time.Time{}
	}
	// Require at least 30s of remaining life.
	if time.Now().After(cache.Exp.Add(-30 * time.Second)) {
		return false, time.Time{}
	}
	c.JWT = cache.JWT
	return true, cache.Exp
}

// saveCache writes the current JWT to c.CachePath with 0600 perms.
func (c *Client) saveCache() error {
	if c.CachePath == "" || c.JWT == "" {
		return nil
	}
	exp, err := decodeJWTExp(c.JWT)
	if err != nil {
		return fmt.Errorf("decode exp: %w", err)
	}
	cache := jwtCache{
		JWT:     c.JWT,
		Exp:     exp,
		KeyHash: keyFingerprint(c.APIKey),
		Base:    c.BaseURL,
	}
	b, _ := json.MarshalIndent(cache, "", "  ")
	if err := os.MkdirAll(filepath.Dir(c.CachePath), 0o700); err != nil {
		return err
	}
	return os.WriteFile(c.CachePath, b, 0o600)
}

// EnsureAuthorized uses a cached JWT if available and fresh, otherwise
// performs a new Authorize() and saves to cache. Returns (exp, fromCache).
func (c *Client) EnsureAuthorized(ctx context.Context) (time.Time, bool, error) {
	if ok, exp := c.tryLoadCache(); ok {
		return exp, true, nil
	}
	if err := c.Authorize(ctx); err != nil {
		return time.Time{}, false, err
	}
	if err := c.saveCache(); err != nil {
		fmt.Fprintf(os.Stderr, "cache write failed: %v\n", err)
	}
	exp, _ := decodeJWTExp(c.JWT)
	return exp, false, nil
}

// ClearCache removes the cached JWT.
func (c *Client) ClearCache() error {
	if c.CachePath == "" {
		return nil
	}
	err := os.Remove(c.CachePath)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
