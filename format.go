package main

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

// formatIBAN inserts a space every four characters: "LI..."  →  "LI02 ...".
func formatIBAN(s string) string {
	if len(s) < 4 {
		return s
	}
	var out strings.Builder
	for i, r := range s {
		if i > 0 && i%4 == 0 {
			out.WriteByte(' ')
		}
		out.WriteRune(r)
	}
	return out.String()
}

// formatAmount prints a float with thousands separators and two decimals,
// e.g. 1234567.89 → "1 234 567.89".
func formatAmount(f float64) string {
	sign := ""
	if f < 0 {
		sign = "-"
		f = -f
	}
	intPart := int64(f)
	frac := int64((f - float64(intPart)) * 100)
	if frac < 0 {
		frac = -frac
	}
	ints := strconv.FormatInt(intPart, 10)
	var out strings.Builder
	n := len(ints)
	for i, c := range ints {
		if i > 0 && (n-i)%3 == 0 {
			out.WriteByte(' ')
		}
		out.WriteRune(c)
	}
	return fmt.Sprintf("%s%s.%02d", sign, out.String(), frac)
}

// truncRunes shortens s to n runes, appending "…" if truncated.
func truncRunes(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n-1]) + "…"
}

func moreIndicator(more bool) string {
	if more {
		return "  (more available)"
	}
	return ""
}

func txDate(t Transaction) string {
	if t.BookingDate != "" {
		return t.BookingDate
	}
	return t.Valuta
}

// printTx writes one transaction as a single row.
func printTx(t Transaction) {
	date := txDate(t)
	counterparty := t.Creditor.Name
	if t.Direction == "incoming" && t.Debitor.Name != "" {
		counterparty = t.Debitor.Name
	}
	if counterparty == "" && t.TransactionCode != "" {
		counterparty = t.TransactionCode
	}
	ref := t.Reference
	if len(ref) > 50 {
		ref = ref[:50] + "…"
	}
	state := t.State
	if state == "" {
		state = "-"
	}
	fmt.Printf("%-10s %-9s %14s %s  %-30s  %s\n",
		date, state, formatAmount(t.Amount), t.Currency,
		truncRunes(counterparty, 30), ref)
}

// printHistory writes a per-account section for each result, sorted
// chronologically within each account.
func printHistory(results []AccountHistory) {
	total := 0
	for _, h := range results {
		total += len(h.Transactions)
	}
	fmt.Fprintf(os.Stderr, "\n%d transactions across %d account(s)\n\n", total, len(results))
	for _, h := range results {
		fmt.Printf("\n%s  (%s %s)  — %d transactions\n",
			h.Account.Category, h.Account.Currency, h.Account.IBAN, len(h.Transactions))
		fmt.Println(strings.Repeat("─", 100))
		sort.SliceStable(h.Transactions, func(i, j int) bool {
			return txDate(h.Transactions[i]) < txDate(h.Transactions[j])
		})
		for _, t := range h.Transactions {
			printTx(t)
		}
	}
}

// writeHistoryCSV emits every transaction across all accounts as one CSV.
func writeHistoryCSV(w io.Writer, results []AccountHistory) error {
	cw := csv.NewWriter(w)
	defer cw.Flush()
	if err := cw.Write([]string{
		"iban", "account_currency",
		"booking_date", "valuta", "direction",
		"amount", "currency",
		"counterparty_name", "counterparty_iban",
		"reference", "transaction_code",
		"state", "order_id", "transaction_nr", "custom_id",
	}); err != nil {
		return err
	}
	for _, h := range results {
		for _, t := range h.Transactions {
			cp := t.Creditor
			if t.Direction == "incoming" && t.Debitor.Name != "" {
				cp = t.Debitor
			}
			if err := cw.Write([]string{
				h.Account.IBAN, h.Account.Currency,
				t.BookingDate, t.Valuta, t.Direction,
				strconv.FormatFloat(t.Amount, 'f', 2, 64), t.Currency,
				cp.Name, cp.IBAN,
				t.Reference, t.TransactionCode,
				t.State, strconv.FormatInt(t.OrderID, 10), t.TransactionNr, t.CustomID,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}
