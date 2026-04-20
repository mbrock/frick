// Package-main: HTTP transport for the Bank Frick Web API.
//
// Every write and every call against the notification host carries two
// custom headers, `algorithm: rsa-sha512` and `Signature: <base64>`, where
// the signature is over the raw request body (empty bytes for signed GETs).
// This is not HTTP Signatures as defined in RFC 9421; it's Bank Frick's
// bespoke scheme, so we do the signing by hand.
package main

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// defaultHTTPTimeout bounds a single request (TLS handshake + body read).
// Matters because some endpoints occasionally hang in the sandbox.
const defaultHTTPTimeout = 30 * time.Second

// Client is a Bank Frick Web API client. Call EnsureAuthorized before issuing
// any signed request; it populates JWT from cache or by calling Authorize.
type Client struct {
	BaseURL   string
	APIKey    string
	Key       *rsa.PrivateKey
	JWT       string
	HTTP      *http.Client
	AuthTTL   time.Duration // requested JWT lifetime, default 1h
	CachePath string        // if non-empty, EnsureAuthorized reads/writes here
}

func loadRSAKey(path string) (*rsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	raw, err := ssh.ParseRawPrivateKey(data)
	if err != nil {
		return nil, err
	}
	key, ok := raw.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("key is %T, not *rsa.PrivateKey", raw)
	}
	return key, nil
}

func signRSASHA512(key *rsa.PrivateKey, body []byte) (string, error) {
	h := sha512.New()
	h.Write(body)
	sig, err := rsa.SignPKCS1v15(nil, key, crypto.SHA512, h.Sum(nil))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(sig), nil
}

func (c *Client) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: defaultHTTPTimeout}
}

// newJSONBody wraps a JSON byte slice as an io.ReadCloser for http.Request.
func newJSONBody(raw []byte) io.ReadCloser {
	return io.NopCloser(bytes.NewReader(raw))
}

// doRaw executes req and returns the response body, status code, and any
// transport error. Body is always fully consumed and closed.
func (c *Client) doRaw(req *http.Request) ([]byte, int, error) {
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	return raw, resp.StatusCode, err
}

// Get performs an authenticated GET and decodes the JSON response into out.
// path can include a query string; it's joined onto BaseURL as-is.
func (c *Client) Get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	if c.JWT != "" {
		req.Header.Set("Authorization", "Bearer "+c.JWT)
	}
	req.Header.Set("Accept", "application/json")
	raw, status, err := c.doRaw(req)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("GET %s: status %d: %s", path, status, strings.TrimSpace(string(raw)))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// signedRequest marshals body as JSON, signs it with RSA-SHA512, sends method
// to path, and decodes the response JSON into out (both may be nil). path may
// be a relative path (joined onto BaseURL) or an absolute URL (for the
// notification host).
func (c *Client) signedRequest(ctx context.Context, method, path string, body any, out any) error {
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	sig, err := signRSASHA512(c.Key, raw)
	if err != nil {
		return err
	}
	target := path
	if !strings.HasPrefix(path, "http") {
		target = c.BaseURL + path
	}
	req, err := http.NewRequestWithContext(ctx, method, target, nil)
	if err != nil {
		return err
	}
	if len(raw) > 0 {
		req.Body = newJSONBody(raw)
		req.ContentLength = int64(len(raw))
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.JWT)
	req.Header.Set("algorithm", "rsa-sha512")
	req.Header.Set("Signature", sig)

	respBody, status, err := c.doRaw(req)
	if err != nil {
		return err
	}
	switch status {
	case http.StatusOK, http.StatusCreated, http.StatusNoContent:
		if out == nil || len(respBody) == 0 {
			return nil
		}
		return json.Unmarshal(respBody, out)
	default:
		return fmt.Errorf("%s %s: status %d: %s", method, path, status, strings.TrimSpace(string(respBody)))
	}
}

// ---------- account & transaction endpoints ----------

// Accounts fetches /v2/accounts.
func (c *Client) Accounts(ctx context.Context) (*AccountsResp, error) {
	var r AccountsResp
	if err := c.Get(ctx, "/v2/accounts", &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// Transactions fetches /v2/accounts/{customer}/{account}/transactions with
// filters passed as query params (e.g. "status": "BOOKED").
func (c *Client) Transactions(ctx context.Context, customer, account string, filters map[string]string) (*TransactionsResp, error) {
	q := url.Values{}
	for k, v := range filters {
		q.Set(k, v)
	}
	path := fmt.Sprintf("/v2/accounts/%s/%s/transactions", customer, account)
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var r TransactionsResp
	if err := c.Get(ctx, path, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// CreateTransactions issues PUT /v2/transactions. When test=true, the server
// only validates without persisting (and skips customId uniqueness check).
func (c *Client) CreateTransactions(ctx context.Context, txs []CreateTx, test bool) (*TransactionsResp, error) {
	path := "/v2/transactions"
	if test {
		path += "?test=true"
	}
	var out TransactionsResp
	if err := c.signedRequest(ctx, "PUT", path, map[string]any{"transactions": txs}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteTransactions issues DELETE /v2/transactions.
func (c *Client) DeleteTransactions(ctx context.Context, orderIDs []int64) (*TransactionsResp, error) {
	var out TransactionsResp
	if err := c.signedRequest(ctx, "DELETE", "/v2/transactions",
		map[string]any{"orderIds": orderIDs}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SignWithoutTan issues POST /v2/signTransactionWithoutTan. Requires the API
// key to have the `signTransactionWithoutTan` scope.
func (c *Client) SignWithoutTan(ctx context.Context, orderIDs []int64) (*TransactionsResp, error) {
	var out TransactionsResp
	if err := c.signedRequest(ctx, "POST", "/v2/signTransactionWithoutTan",
		map[string]any{"orderIds": orderIDs}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RequestTan issues POST /v2/requestTan. method: SMS_TAN, PUSH_TAN, SECURITY_TOKEN.
func (c *Client) RequestTan(ctx context.Context, orderIDs []int64, method string) (*RequestTanResp, error) {
	var out RequestTanResp
	if err := c.signedRequest(ctx, "POST", "/v2/requestTan",
		map[string]any{"orderIds": orderIDs, "method": method}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SignWithTan issues POST /v2/signTransactionWithTan.
func (c *Client) SignWithTan(ctx context.Context, challengeID, tan string) (*TransactionsResp, error) {
	var out TransactionsResp
	if err := c.signedRequest(ctx, "POST", "/v2/signTransactionWithTan",
		map[string]any{"challengeId": challengeID, "tan": tan}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CancelTan issues DELETE /v2/requestTan.
func (c *Client) CancelTan(ctx context.Context, challengeID string) error {
	return c.signedRequest(ctx, "DELETE", "/v2/requestTan",
		map[string]any{"challengeId": challengeID}, nil)
}
