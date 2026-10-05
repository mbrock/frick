package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"tailscale.com/tsnet"
)

// ServeCmd exposes only reads and unsigned EUR SEPA creation, never the full CLI.
type ServeCmd struct {
	Hostname string `default:"frick" help:"Dedicated tsnet identity."`
	StateDir string `required:"" help:"Private directory for tsnet state."`
	Listen   string `default:":8087" help:"Tailnet HTTP listener."`
	Local    bool   `help:"Use a loopback-only listener for local development."`
}

func restrictedScopes(token string) error {
	claims := jwt.MapClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(token, claims); err != nil {
		return fmt.Errorf("invalid bank authorization")
	}
	raw, ok := claims["scope"].([]any)
	if !ok {
		return fmt.Errorf("bank credential has no scope list")
	}
	allowed := map[string]bool{"accounts": true, "transactions": true, "createTransaction": true, "camt053": true}
	seen := map[string]bool{}
	for _, v := range raw {
		s, ok := v.(string)
		if !ok || !allowed[s] {
			return fmt.Errorf("bank credential is not restricted to unsigned service permissions")
		}
		seen[s] = true
	}
	for _, s := range []string{"accounts", "transactions", "createTransaction"} {
		if !seen[s] {
			return fmt.Errorf("bank credential lacks required permissions")
		}
	}
	return nil
}

func (cmd *ServeCmd) Run(ctx *Context) error {
	secret := os.Getenv("FRICK_RELAY_TOKEN")
	if len(secret) < 32 {
		return fmt.Errorf("FRICK_RELAY_TOKEN must contain at least 32 characters")
	}
	c, err := ctx.AuthClient()
	if err != nil {
		return fmt.Errorf("cannot initialize bank client; check restricted credentials")
	}
	c.HTTP = &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if err := restrictedScopes(c.JWT); err != nil {
		return err
	}
	var mu sync.Mutex
	factory := func(reqCtx context.Context) (*Client, error) {
		mu.Lock()
		defer mu.Unlock()
		expiry, e := decodeJWTExp(c.JWT)
		if e != nil || time.Until(expiry) < 30*time.Second {
			if e = c.Authorize(reqCtx); e != nil {
				return nil, e
			}
			if e = restrictedScopes(c.JWT); e != nil {
				return nil, e
			}
			_ = c.saveCache()
		}
		clone := *c
		return &clone, nil
	}
	var listener net.Listener
	if cmd.Local {
		host, _, e := net.SplitHostPort(cmd.Listen)
		if e != nil || !(host == "127.0.0.1" || host == "::1") {
			return fmt.Errorf("local listener must use a loopback address")
		}
		listener, err = net.Listen("tcp", cmd.Listen)
	} else {
		node := &tsnet.Server{Hostname: cmd.Hostname, Dir: cmd.StateDir, AuthKey: os.Getenv("TS_AUTHKEY"), Logf: func(string, ...any) {}, UserLogf: func(string, ...any) {}}
		defer node.Close()
		listener, err = node.Listen("tcp", cmd.Listen)
	}
	if err != nil {
		return fmt.Errorf("cannot start relay listener")
	}
	server := &http.Server{Handler: relayHandler(factory, secret), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 16384}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	log.Print("frick relay ready; read and unsigned-create routes only")
	err = server.Serve(listener)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func relayHandler(factory func(context.Context) (*Client, error), secret string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		reject := func(status int, message string) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
		}
		actual := sha256.Sum256([]byte(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")))
		expected := sha256.Sum256([]byte(secret))
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || subtle.ConstantTimeCompare(actual[:], expected[:]) != 1 {
			reject(401, "Authentication required")
			return
		}
		// Routing is an allowlist, not a general bank proxy. Reject before authorization.
		order := regexp.MustCompile(`^/orders/[0-9]+$`).MatchString(r.URL.Path)
		if !(r.Method == "GET" && (r.URL.Path == "/accounts" || r.URL.Path == "/transactions" || r.URL.Path == "/health" || order) || r.Method == "POST" && r.URL.Path == "/payments") {
			reject(404, "Not found")
			return
		}
		if r.URL.Path == "/health" {
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
			return
		}
		client, err := factory(r.Context())
		if err != nil {
			reject(502, "Bank authorization unavailable")
			return
		}
		var result any
		switch {
		case r.URL.Path == "/accounts":
			result, err = client.Accounts(r.Context())
		case r.URL.Path == "/transactions":
			var accounts *AccountsResp
			accounts, err = client.Accounts(r.Context())
			if err != nil {
				break
			}
			if accounts.MoreResults {
				reject(502, "Account list incomplete")
				return
			}
			account := findRelayAccount(accounts.Accounts, r.URL.Query().Get("account"))
			if account == nil {
				reject(404, "Account not found")
				return
			}
			offset := r.URL.Query().Get("offset")
			if offset == "" {
				offset = "0"
			}
			n, e := strconv.Atoi(offset)
			if e != nil || n < 0 {
				reject(400, "Invalid page offset")
				return
			}
			filters := map[string]string{"maxResults": "100", "firstPosition": strconv.Itoa(n), "order": "desc"}
			if status := r.URL.Query().Get("status"); status != "" {
				if status != "PREPARED" {
					reject(400, "Invalid status filter")
					return
				}
				filters["status"] = status
			}
			result, err = client.Transactions(r.Context(), account.CustomerID(), account.AccountPath(), filters)
		case order:
			var out TransactionsResp
			err = client.Get(r.Context(), "/v2/transactions/"+url.PathEscape(strings.TrimPrefix(r.URL.Path, "/orders/")), &out)
			result = out
		case r.URL.Path == "/payments":
			if r.Header.Get("Content-Type") != "application/json" {
				reject(415, "Use application/json")
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 16384)
			var payment relayPayment
			decoder := json.NewDecoder(r.Body)
			decoder.DisallowUnknownFields()
			if decoder.Decode(&payment) != nil {
				reject(400, "Invalid payment representation")
				return
			}
			var extra any
			if decoder.Decode(&extra) != io.EOF {
				reject(400, "Invalid payment representation")
				return
			}
			var accounts *AccountsResp
			accounts, err = client.Accounts(r.Context())
			if err != nil {
				break
			}
			if accounts.MoreResults {
				reject(502, "Account list incomplete")
				return
			}
			if e := payment.validate(accounts.Accounts); e != nil {
				reject(400, e.Error())
				return
			}
			tx := CreateTx{CustomID: payment.CustomID, Type: "SEPA", Currency: "EUR", Amount: payment.Amount, Reference: payment.Reference, Debitor: TxParty{IBAN: payment.Debitor.IBAN}, Creditor: TxParty{Name: payment.Creditor.Name, IBAN: payment.Creditor.IBAN}}
			result, err = client.CreateTransactions(r.Context(), []CreateTx{tx}, false)
		}
		if err != nil {
			reject(502, "Bank request failed; check pending orders before retrying creation")
			return
		}
		if r.Method == "POST" {
			w.WriteHeader(201)
		}
		_ = json.NewEncoder(w).Encode(result)
	})
}

type relayPayment struct {
	CustomID  string  `json:"customId"`
	Type      string  `json:"type"`
	Amount    float64 `json:"amount"`
	Currency  string  `json:"currency"`
	Reference string  `json:"reference"`
	Debitor   struct {
		IBAN string `json:"iban"`
	} `json:"debitor"`
	Creditor struct {
		Name string `json:"name"`
		IBAN string `json:"iban"`
	} `json:"creditor"`
}

func normalizeRelayIBAN(s string) string { return strings.ToUpper(strings.Join(strings.Fields(s), "")) }
func validRelayIBAN(s string) bool {
	s = normalizeRelayIBAN(s)
	if !regexp.MustCompile(`^[A-Z]{2}[0-9]{2}[A-Z0-9]{11,30}$`).MatchString(s) {
		return false
	}
	remainder := 0
	for _, c := range s[4:] + s[:4] {
		digits := string(c)
		if c >= 'A' && c <= 'Z' {
			digits = strconv.Itoa(int(c-'A') + 10)
		}
		for _, d := range digits {
			remainder = (remainder*10 + int(d-'0')) % 97
		}
	}
	return remainder == 1
}
func findRelayAccount(accounts []Account, iban string) *Account {
	for _, a := range accounts {
		if normalizeRelayIBAN(a.IBAN) == normalizeRelayIBAN(iban) {
			return &a
		}
	}
	return nil
}
func (p *relayPayment) validate(accounts []Account) error {
	p.Debitor.IBAN = normalizeRelayIBAN(p.Debitor.IBAN)
	p.Creditor.IBAN = normalizeRelayIBAN(p.Creditor.IBAN)
	if p.Type != "SEPA" || p.Currency != "EUR" {
		return fmt.Errorf("Only EUR SEPA payments are supported")
	}
	account := findRelayAccount(accounts, p.Debitor.IBAN)
	if account == nil || account.Currency != "EUR" {
		return fmt.Errorf("Select your EUR account")
	}
	if !validRelayIBAN(p.Debitor.IBAN) || !validRelayIBAN(p.Creditor.IBAN) {
		return fmt.Errorf("Invalid IBAN")
	}
	if math.IsNaN(p.Amount) || math.IsInf(p.Amount, 0) || p.Amount <= 0 || p.Amount >= 1e9 || math.Abs(p.Amount*100-math.Round(p.Amount*100)) > 0.00001 {
		return fmt.Errorf("Invalid EUR amount")
	}
	if !regexp.MustCompile(`^frick-worker-[a-f0-9-]{36}$`).MatchString(p.CustomID) {
		return fmt.Errorf("Invalid request ID")
	}
	p.Creditor.Name = strings.TrimSpace(p.Creditor.Name)
	if p.Creditor.Name == "" || len([]rune(p.Creditor.Name)) > 140 || len([]rune(p.Reference)) > 140 {
		return fmt.Errorf("Invalid recipient or reference")
	}
	return nil
}
