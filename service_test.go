package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"github.com/golang-jwt/jwt/v5"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRelayRefusesSigningAndUnauthenticatedReads(t *testing.T) {
	calls := 0
	h := relayHandler(func(context.Context) (*Client, error) { calls++; return nil, nil }, "test-relay-token")
	for _, path := range []string{"/accounts", "/payments", "/sign", "/v2/signTransactionWithoutTan"} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", path, nil)
		h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatalf("unauthenticated %s: %d", path, w.Code)
		}
	}
	for _, path := range []string{"/sign", "/v2/signTransactionWithoutTan", "/v2/authorize", "/trading"} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", path, nil)
		r.Header.Set("Authorization", "Bearer test-relay-token")
		h.ServeHTTP(w, r)
		if w.Code != 404 {
			t.Fatalf("unexpected route %s: %d", path, w.Code)
		}
	}
	if calls != 0 {
		t.Fatal("bank client used for disallowed requests")
	}
}
func TestRelayRestrictedScopes(t *testing.T) {
	for _, tc := range []struct {
		scopes []string
		ok     bool
	}{{[]string{"accounts", "transactions", "createTransaction", "camt053"}, true}, {[]string{"accounts", "transactions", "createTransaction", "signTransactionWithoutTan"}, false}, {[]string{"accounts", "transactions"}, false}} {
		token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"scope": tc.scopes}).SignedString([]byte("test"))
		if err != nil {
			t.Fatal(err)
		}
		if (restrictedScopes(token) == nil) != tc.ok {
			t.Fatalf("scope check: %v", tc.scopes)
		}
	}
}
func TestRelayCreatesOnlyUnsignedValidatedPayments(t *testing.T) {
	writes := 0
	bank := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v2/accounts" {
			json.NewEncoder(w).Encode(AccountsResp{Accounts: []Account{{IBAN: "DE89370400440532013000", Currency: "EUR"}}})
			return
		}
		if r.Method != "PUT" || r.URL.Path != "/v2/transactions" {
			t.Errorf("unexpected bank call %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
			return
		}
		writes++
		var body struct {
			Transactions []CreateTx `json:"transactions"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if len(body.Transactions) != 1 || body.Transactions[0].Type != "SEPA" || body.Transactions[0].Currency != "EUR" {
			t.Error("unexpected payment")
		}
		if r.Header.Get("Signature") == "" {
			t.Error("unsigned bank request")
		}
		json.NewEncoder(w).Encode(TransactionsResp{Transactions: []Transaction{{OrderID: 100, State: "PREPARED"}}})
	}))
	defer bank.Close()
	key := testRSAKey(t)
	h := relayHandler(func(context.Context) (*Client, error) { return &Client{BaseURL: bank.URL, Key: key, JWT: "mock"}, nil }, "test-relay-token")
	valid := `{"customId":"frick-worker-00000000-0000-4000-8000-000000000001","type":"SEPA","amount":12.34,"currency":"EUR","reference":"Invoice","debitor":{"iban":"DE89370400440532013000"},"creditor":{"name":"Example","iban":"DE12500105170648489890"}}`
	for _, tc := range []struct {
		body   string
		status int
	}{{valid, 201}, {strings.Replace(valid, `"amount":12.34`, `"amount":-1`, 1), 400}, {strings.Replace(valid, `"type":"SEPA"`, `"type":"FOREIGN"`, 1), 400}, {strings.Replace(valid, `"reference":"Invoice"`, `"reference":"Invoice","sign":true`, 1), 400}, {strings.Replace(valid, "DE89370400440532013000", "DE12500105170648489890", 1), 400}} {
		r := httptest.NewRequest("POST", "/payments", strings.NewReader(tc.body))
		r.Header.Set("Authorization", "Bearer test-relay-token")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("status %d, expected %d: %s", w.Code, tc.status, w.Body.String())
		}
	}
	if writes != 1 {
		t.Fatalf("bank writes: %d", writes)
	}
}

func testRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestRelayHistoryFiltersAndBounds(t *testing.T) {
	reads := 0
	bank := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v2/accounts" {
			json.NewEncoder(w).Encode(AccountsResp{Accounts: []Account{{IBAN: "DE89370400440532013000", Currency: "EUR", Customer: "100 Example", Account: "100/001"}}})
			return
		}
		reads++
		q := r.URL.Query()
		if q.Get("maxResults") != "15" || q.Get("firstPosition") != "15" || q.Get("status") != "BOOKED" || q.Get("fromDate") != "2000-01-01" || q.Get("order") != "desc" {
			t.Errorf("incorrect bank history filters: %v", q)
		}
		json.NewEncoder(w).Encode(TransactionsResp{})
	}))
	defer bank.Close()
	h := relayHandler(func(context.Context) (*Client, error) { return &Client{BaseURL: bank.URL, JWT: "mock"}, nil }, "test-relay-token")
	for _, tc := range []struct {
		query  string
		status int
	}{
		{"offset=15&limit=15&status=BOOKED", 200},
		{"limit=101", 400}, {"limit=0", 400}, {"offset=-1", 400}, {"status=BOGUS", 400},
	} {
		r := httptest.NewRequest("GET", "/transactions?account=DE89370400440532013000&"+tc.query, nil)
		r.Header.Set("Authorization", "Bearer test-relay-token")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s: %d, expected %d", tc.query, w.Code, tc.status)
		}
	}
	if reads != 1 {
		t.Fatalf("unexpected transaction reads: %d", reads)
	}
}
