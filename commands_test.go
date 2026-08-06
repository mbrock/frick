package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNormalizeIBAN(t *testing.T) {
	tests := []struct {
		input string
		want  string
		ok    bool
	}{
		{"DE89 3704 0044 0532 0130 00", "DE89370400440532013000", true},
		{"gb82 west 1234 5698 7654 32", "GB82WEST12345698765432", true},
		{"ieva lange", "", false},
		{"DE89370400440532013001", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, ok := normalizeIBAN(tt.input)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("normalizeIBAN(%q) = %q, %v; want %q, %v", tt.input, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestSameRecipientName(t *testing.T) {
	if !sameRecipientName("  Ieva   Lange ", "ieva lange") {
		t.Fatal("expected names to match ignoring case and whitespace")
	}
	if sameRecipientName("Ieva Lange", "Ieva Langa") {
		t.Fatal("expected different names not to match")
	}
}

func TestResolveToIBANFromHistory(t *testing.T) {
	var transactionQueries int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/accounts":
			_ = json.NewEncoder(w).Encode(AccountsResp{Accounts: []Account{{
				Account: "1234567/001.000.001", Customer: "1234567 Example", IBAN: "LI21 0881 1000 0001 2345 6",
			}}})
		case "/v2/accounts/1234567/001.000.001/transactions":
			transactionQueries++
			q := r.URL.Query()
			if q.Get("searchName") != "ieva lange" || q.Get("status") != "BOOKED" || q.Get("order") != "desc" {
				t.Errorf("unexpected transaction query: %s", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(TransactionsResp{Transactions: []Transaction{
				{BookingDate: "2026-07-01", Creditor: TxParty{Name: "Someone Else", IBAN: "DE89370400440532013000"}},
				{BookingDate: "2026-06-01", Creditor: TxParty{Name: "IEVA  LANGE", IBAN: "GB82 WEST 1234 5698 7654 32"}},
				{BookingDate: "2025-01-01", Creditor: TxParty{Name: "Ieva Lange", IBAN: "DE89370400440532013000"}},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	c := &Client{BaseURL: server.URL, HTTP: server.Client()}
	got, err := resolveToIBAN(context.Background(), c, "ieva lange")
	if err != nil {
		t.Fatal(err)
	}
	if got != "GB82WEST12345698765432" {
		t.Fatalf("got %q", got)
	}
	if transactionQueries != 1 {
		t.Fatalf("made %d transaction queries; want 1", transactionQueries)
	}
}

func TestResolveToIBANDirectDoesNotFetchHistory(t *testing.T) {
	c := &Client{BaseURL: "http://invalid.example"}
	got, err := resolveToIBAN(context.Background(), c, "DE89 3704 0044 0532 0130 00")
	if err != nil {
		t.Fatal(err)
	}
	if got != "DE89370400440532013000" {
		t.Fatalf("got %q", got)
	}
}
