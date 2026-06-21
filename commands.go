package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ---------- info / auth / logout ----------

type InfoCmd struct{}

func (cmd *InfoCmd) Run(ctx *Context) error {
	c := &Client{BaseURL: ctx.base()}
	var info map[string]any
	if err := c.Get(ctx, "/v2/info", &info); err != nil {
		return err
	}
	return ctx.emit(info, func() {
		for k, v := range info {
			fmt.Printf("%s: %v\n", k, v)
		}
	})
}

type AuthCmd struct{}

func (cmd *AuthCmd) Run(ctx *Context) error {
	c, err := ctx.AuthClient()
	if err != nil {
		return err
	}
	exp, err := decodeJWTExp(c.JWT)
	if err != nil {
		return err
	}
	fmt.Printf("JWT expires %s (%s from now)\n",
		exp.Format(time.RFC3339), time.Until(exp).Round(time.Second))
	return nil
}

type LogoutCmd struct{}

func (cmd *LogoutCmd) Run(ctx *Context) error {
	c := &Client{CachePath: ctx.Globals.Cache}
	return c.ClearCache()
}

// ---------- balance / transactions / history ----------

type BalanceCmd struct{}

func (cmd *BalanceCmd) Run(ctx *Context) error {
	c, err := ctx.AuthClient()
	if err != nil {
		return err
	}
	r, err := c.Accounts(ctx)
	if err != nil {
		return err
	}
	return ctx.emit(r, func() {
		var total float64
		var ref string
		for _, a := range r.Accounts {
			fmt.Printf("%-26s  %14s %s  %-20s  %s\n",
				formatIBAN(a.IBAN), formatAmount(a.Balance), a.Currency,
				a.Type, a.Category)
			total += a.BalanceRefCurrency
			ref = a.RefCurrency
		}
		fmt.Printf("%-26s  %14s %s\n", "TOTAL", formatAmount(total), ref)
	})
}

type TxCmd struct {
	From   string `help:"Start date YYYY-MM-DD (default: 30 days ago)."`
	To     string `help:"End date YYYY-MM-DD."`
	Status string `default:"BOOKED" help:"Status filter."`
	N      int    `short:"n" default:"50" help:"Max results per account."`
}

func (cmd *TxCmd) Run(ctx *Context) error {
	c, err := ctx.AuthClient()
	if err != nil {
		return err
	}
	from := cmd.From
	if from == "" {
		from = time.Now().AddDate(0, 0, -30).Format("2006-01-02")
	}
	r, err := c.Accounts(ctx)
	if err != nil {
		return err
	}
	all := map[string]*TransactionsResp{}
	for _, a := range r.Accounts {
		filters := map[string]string{
			"fromDate":   from,
			"maxResults": strconv.Itoa(cmd.N),
			"order":      "desc",
		}
		if cmd.Status != "" {
			filters["status"] = cmd.Status
		}
		if cmd.To != "" {
			filters["toDate"] = cmd.To
		}
		txs, err := c.Transactions(ctx, a.CustomerID(), a.AccountPath(), filters)
		if err != nil {
			log.Printf("transactions for %s: %v", a.IBAN, err)
			continue
		}
		all[a.IBAN] = txs
	}
	return ctx.emit(all, func() {
		for _, a := range r.Accounts {
			txs := all[a.IBAN]
			if txs == nil {
				continue
			}
			fmt.Printf("\n%s (%s %s) — %d transactions%s\n",
				a.Category, a.Currency, a.IBAN,
				len(txs.Transactions), moreIndicator(txs.MoreResults))
			fmt.Println(strings.Repeat("─", 100))
			for _, t := range txs.Transactions {
				printTx(t)
			}
		}
	})
}

type HistoryCmd struct {
	From    string `default:"2000-01-01" help:"Start date YYYY-MM-DD."`
	To      string `help:"End date YYYY-MM-DD (default: today)."`
	Status  string `default:"BOOKED" help:"Status filter (empty = all)."`
	Account string `help:"Restrict to one IBAN."`
	CSV     bool   `help:"CSV output."`
	Desc    bool   `help:"Newest first (default: chronological)."`
}

func (cmd *HistoryCmd) Run(ctx *Context) error {
	c, err := ctx.AuthClient()
	if err != nil {
		return err
	}
	accts, err := c.Accounts(ctx)
	if err != nil {
		return err
	}
	order := "asc"
	if cmd.Desc {
		order = "desc"
	}
	var results []AccountHistory
	for _, a := range accts.Accounts {
		if cmd.Account != "" && a.IBAN != strings.ReplaceAll(cmd.Account, " ", "") {
			continue
		}
		filters := map[string]string{
			"fromDate": cmd.From,
			"order":    order,
		}
		if cmd.Status != "" {
			filters["status"] = cmd.Status
		}
		if cmd.To != "" {
			filters["toDate"] = cmd.To
		}
		txs, err := paginatedFetch(ctx, c, a.CustomerID(), a.AccountPath(), filters,
			func(page, n int) {
				if !ctx.Globals.JSON && !cmd.CSV {
					fmt.Fprintf(os.Stderr, "  %s  page %d: %d rows\n", a.IBAN, page, n)
				}
			})
		if err != nil {
			log.Printf("%s: %v", a.IBAN, err)
			continue
		}
		results = append(results, AccountHistory{Account: a, Transactions: txs})
	}
	if cmd.CSV {
		return writeHistoryCSV(os.Stdout, results)
	}
	return ctx.emit(results, func() { printHistory(results) })
}

// paginatedFetch keeps calling /v2/accounts/.../transactions with advancing
// firstPosition until moreResults=false.
func paginatedFetch(ctx context.Context, c *Client, customer, account string, filters map[string]string, onPage func(int, int)) ([]Transaction, error) {
	var all []Transaction
	offset := 0
	page := 0
	for {
		f := make(map[string]string, len(filters)+2)
		for k, v := range filters {
			f[k] = v
		}
		f["firstPosition"] = strconv.Itoa(offset)
		f["maxResults"] = "2500"
		resp, err := c.Transactions(ctx, customer, account, f)
		if err != nil {
			return nil, err
		}
		all = append(all, resp.Transactions...)
		page++
		if onPage != nil {
			onPage(page, len(resp.Transactions))
		}
		if !resp.MoreResults || len(resp.Transactions) == 0 {
			break
		}
		offset += len(resp.Transactions)
	}
	return all, nil
}

// ---------- pending / create / delete ----------

type PendingCmd struct{}

func (cmd *PendingCmd) Run(ctx *Context) error {
	c, err := ctx.AuthClient()
	if err != nil {
		return err
	}
	r, err := c.Accounts(ctx)
	if err != nil {
		return err
	}
	all := []Transaction{}
	for _, a := range r.Accounts {
		txs, err := c.Transactions(ctx, a.CustomerID(), a.AccountPath(),
			map[string]string{"status": "PREPARED", "maxResults": "100"})
		if err != nil {
			log.Printf("pending for %s: %v", a.IBAN, err)
			continue
		}
		all = append(all, txs.Transactions...)
	}
	return ctx.emit(all, func() {
		if len(all) == 0 {
			fmt.Println("no pending transactions")
			return
		}
		for _, t := range all {
			fmt.Printf("#%d  %s %s → %-30s  ref=%q  (custom=%s)\n",
				t.OrderID, formatAmount(t.Amount), t.Currency,
				truncRunes(t.Creditor.Name, 30), t.Reference, t.CustomID)
		}
	})
}

type CreateCmd struct {
	From            string  `required:"" env:"FRICK_FROM_IBAN" help:"Debitor IBAN (your account; defaults to $FRICK_FROM_IBAN)."`
	ToIBAN          string  `name:"to-iban" required:"" help:"Creditor IBAN."`
	ToName          string  `name:"to-name" required:"" help:"Creditor name."`
	ToAddress       string  `name:"to-address" help:"Creditor street address."`
	ToPostalcode    string  `name:"to-postalcode" help:"Creditor postal code."`
	ToCity          string  `name:"to-city" help:"Creditor city."`
	ToCountry       string  `name:"to-country" help:"Creditor country."`
	Amount          float64 `required:"" help:"Amount."`
	Currency        string  `default:"EUR" help:"Currency (SEPA_INSTANT: EUR only)."`
	Ref             string  `help:"Reference / payment note."`
	Type            string  `default:"SEPA" enum:"SEPA,BANK_INTERNAL,INTERNAL,FOREIGN,QR_BILL,SEPA_INSTANT" help:"Transaction type."`
	Valuta          string  `help:"Value/execution date YYYY-MM-DD; ignored for SEPA_INSTANT."`
	Express         bool    `help:"Express transfer (SEPA); ignored for SEPA_INSTANT."`
	CustomID        string  `name:"custom-id" help:"Idempotency key (auto-generated UUID by default)."`
	Purpose         string  `help:"purposeOfPayment code (required for USD)."`
	Charge          string  `help:"SHA, OUR, BEN (required for FOREIGN)."`
	Test            bool    `help:"Dry-run: validate without creating."`
	Sign            bool    `help:"Immediately sign without TAN (minimum-latency path)."`
	Correspondence  bool    `help:"Mark as correspondence payment."`
	OrderingName    string  `name:"ordering-name" help:"Ordering customer name."`
	OrderingAddress string  `name:"ordering-address" help:"Ordering customer address."`
	OrderingPostal  string  `name:"ordering-postalcode" help:"Ordering customer postal code."`
	OrderingCity    string  `name:"ordering-city" help:"Ordering customer city."`
	OrderingCountry string  `name:"ordering-country" help:"Ordering customer country."`
}

func (cmd *CreateCmd) Run(ctx *Context) error {
	c, err := ctx.AuthClient()
	if err != nil {
		return err
	}
	if cmd.CustomID == "" {
		cmd.CustomID = "frickgo-" + uuid.NewString()
	}
	if cmd.Type == "SEPA_INSTANT" && cmd.Currency != "EUR" {
		return fmt.Errorf("SEPA_INSTANT only supports EUR")
	}

	creditor := TxParty{
		Name:       cmd.ToName,
		IBAN:       cmd.ToIBAN,
		Address:    cmd.ToAddress,
		Postalcode: cmd.ToPostalcode,
		City:       cmd.ToCity,
		Country:    cmd.ToCountry,
	}
	tx := CreateTx{
		CustomID:  cmd.CustomID,
		Type:      cmd.Type,
		Amount:    cmd.Amount,
		Currency:  cmd.Currency,
		Reference: cmd.Ref,
		Debitor:   TxParty{IBAN: cmd.From},
		Creditor:  creditor,
	}
	if cmd.Correspondence {
		tx.Correspondence = Ptr(true)
	}
	if cmd.OrderingName != "" || cmd.OrderingAddress != "" || cmd.OrderingPostal != "" ||
		cmd.OrderingCity != "" || cmd.OrderingCountry != "" {
		tx.OrderingCustomer = &OrderingCustomer{
			Name:       cmd.OrderingName,
			Address:    cmd.OrderingAddress,
			Postalcode: cmd.OrderingPostal,
			City:       cmd.OrderingCity,
			Country:    cmd.OrderingCountry,
		}
	}
	if cmd.Type != "SEPA_INSTANT" {
		tx.Express = Ptr(cmd.Express)
		tx.Valuta = cmd.Valuta
		tx.ValutaIsExecutionDate = Ptr(true)
		tx.PurposeOfPayment = cmd.Purpose
		tx.Charge = cmd.Charge
	}

	tCreateStart := time.Now()
	resp, err := c.CreateTransactions(ctx, []CreateTx{tx}, cmd.Test)
	if err != nil {
		return err
	}
	tCreated := time.Now()

	if cmd.Sign && !cmd.Test {
		ids := make([]int64, 0, len(resp.Transactions))
		for _, t := range resp.Transactions {
			if t.OrderID > 0 {
				ids = append(ids, t.OrderID)
			}
		}
		if len(ids) > 0 {
			signResp, signErr := c.SignWithoutTan(ctx, ids)
			tSigned := time.Now()
			if signErr != nil {
				fmt.Fprintf(os.Stderr, "⚠ create OK (orderId=%v) but sign-nt failed: %v\n", ids, signErr)
				fmt.Fprintf(os.Stderr, "  leave PREPARED or delete with: apiprobe delete %d\n", ids[0])
				return signErr
			}
			fmt.Fprintf(os.Stderr, "⏱ create=%s sign=%s total=%s\n",
				tCreated.Sub(tCreateStart).Round(time.Millisecond),
				tSigned.Sub(tCreated).Round(time.Millisecond),
				tSigned.Sub(tCreateStart).Round(time.Millisecond))
			resp = signResp
		}
	}

	return ctx.emit(resp, func() {
		if cmd.Test {
			fmt.Println("(dry-run) validation OK")
		}
		for _, t := range resp.Transactions {
			fmt.Printf("orderId=%d customId=%s state=%s  %s %s → %s\n",
				t.OrderID, t.CustomID, t.State,
				formatAmount(t.Amount), t.Currency, t.Creditor.Name)
		}
	})
}

type DeleteCmd struct {
	OrderIDs []int64 `arg:"" name:"orderid" help:"Order IDs to delete."`
}

func (cmd *DeleteCmd) Run(ctx *Context) error {
	c, err := ctx.AuthClient()
	if err != nil {
		return err
	}
	resp, err := c.DeleteTransactions(ctx, cmd.OrderIDs)
	if err != nil {
		return err
	}
	return ctx.emit(resp, func() {
		for _, t := range resp.Transactions {
			fmt.Printf("deleted orderId=%d  state=%s\n", t.OrderID, t.State)
		}
	})
}

// ---------- signing (with/without TAN) ----------

type SignNTCmd struct {
	OrderIDs []int64 `arg:"" name:"orderid" help:"Order IDs to sign."`
}

func (cmd *SignNTCmd) Run(ctx *Context) error {
	c, err := ctx.AuthClient()
	if err != nil {
		return err
	}
	resp, err := c.SignWithoutTan(ctx, cmd.OrderIDs)
	if err != nil {
		return err
	}
	return ctx.emit(resp, func() {
		for _, t := range resp.Transactions {
			fmt.Printf("signed orderId=%d  state=%s\n", t.OrderID, t.State)
		}
	})
}

type RequestTANCmd struct {
	Method   string  `default:"PUSH_TAN" enum:"PUSH_TAN,SMS_TAN,SECURITY_TOKEN" help:"TAN delivery method."`
	OrderIDs []int64 `arg:"" name:"orderid" help:"Order IDs to request a TAN challenge for."`
}

func (cmd *RequestTANCmd) Run(ctx *Context) error {
	c, err := ctx.AuthClient()
	if err != nil {
		return err
	}
	resp, err := c.RequestTan(ctx, cmd.OrderIDs, cmd.Method)
	if err != nil {
		return err
	}
	return ctx.emit(resp, func() {
		fmt.Printf("challengeId=%s expires=%s\nTAN sent via %s; complete on your device then run:\n  apiprobe sign --challenge %s --tan ...\n",
			resp.ChallengeID, resp.Expires, cmd.Method, resp.ChallengeID)
	})
}

type SignCmd struct {
	Challenge string `required:"" help:"Challenge ID from request-tan."`
	TAN       string `required:"" help:"TAN code from phone/token."`
}

func (cmd *SignCmd) Run(ctx *Context) error {
	c, err := ctx.AuthClient()
	if err != nil {
		return err
	}
	resp, err := c.SignWithTan(ctx, cmd.Challenge, cmd.TAN)
	if err != nil {
		return err
	}
	return ctx.emit(resp, func() {
		for _, t := range resp.Transactions {
			fmt.Printf("signed orderId=%d  state=%s\n", t.OrderID, t.State)
		}
	})
}

type CancelTANCmd struct {
	Challenge string `required:"" help:"Challenge ID to cancel."`
}

func (cmd *CancelTANCmd) Run(ctx *Context) error {
	c, err := ctx.AuthClient()
	if err != nil {
		return err
	}
	return c.CancelTan(ctx, cmd.Challenge)
}

// ---------- notification rules ----------

type RulesCmd struct {
	Page int `default:"0" help:"Page index (0-based)."`
	Size int `default:"100" help:"Page size."`
}

func (cmd *RulesCmd) Run(ctx *Context) error {
	c, err := ctx.AuthClient()
	if err != nil {
		return err
	}
	r, err := c.NotifListRules(ctx, cmd.Page, cmd.Size)
	if err != nil {
		return err
	}
	return ctx.emit(r, func() {
		if len(r.Rules) == 0 {
			fmt.Println("no notification rules")
			return
		}
		for _, rule := range r.Rules {
			events := make([]string, 0, len(rule.Events))
			for _, e := range rule.Events {
				events = append(events, strings.TrimPrefix(e.Type, "INSTANT_TRANSACTION_"))
			}
			fmt.Printf("%s  %-8s  %s → %-30s  events=[%s]\n",
				rule.ID, rule.Status, rule.AccountNumber,
				truncRunes(rule.Targets.WebhookURL, 30),
				strings.Join(events, ","))
			if rule.Name != "" {
				fmt.Printf("  name: %s\n", rule.Name)
			}
		}
		fmt.Printf("\n%d/%d rules (page %d, size %d)\n",
			len(r.Rules), r.Pagination.TotalCount, r.Pagination.PageIndex, r.Pagination.PageSize)
	})
}

type RuleAddCmd struct {
	Name     string   `required:"" help:"Rule name (3-255 chars)."`
	URL      string   `name:"url" required:"" help:"Webhook URL (https only)."`
	Accounts []string `name:"account" help:"IBAN to cover (repeat; one rule created per account)."`
	Events   []string `name:"event" help:"Event type: received, executed, rejected (repeat)."`
}

// eventAliases maps user-facing event names to the canonical server constants.
var eventAliases = map[string]string{
	"received":    EventReceived,
	"executed":    EventExecuted,
	"rejected":    EventRejected,
	EventReceived: EventReceived,
	EventExecuted: EventExecuted,
	EventRejected: EventRejected,
}

func (cmd *RuleAddCmd) Run(ctx *Context) error {
	c, err := ctx.AuthClient()
	if err != nil {
		return err
	}
	if len(cmd.Accounts) == 0 || len(cmd.Events) == 0 {
		return fmt.Errorf("need at least one --account and one --event")
	}
	evs := make([]NotifEvent, 0, len(cmd.Events))
	for _, e := range cmd.Events {
		full, ok := eventAliases[strings.ToLower(e)]
		if !ok {
			full = strings.ToUpper(e)
		}
		evs = append(evs, NotifEvent{Type: full})
	}
	normAccts := make([]string, 0, len(cmd.Accounts))
	for _, a := range cmd.Accounts {
		normAccts = append(normAccts, strings.ReplaceAll(a, " ", ""))
	}
	resp, err := c.NotifCreateRules(ctx, NotifCreateRulesReq{
		Name:           cmd.Name,
		AccountNumbers: normAccts,
		Targets:        NotifTargets{WebhookURL: cmd.URL},
		Events:         evs,
	})
	if err != nil {
		return err
	}
	return ctx.emit(resp, func() {
		for _, rule := range resp.Rules {
			fmt.Printf("created %s  %s  %s → %s\n",
				rule.ID, rule.Status, rule.AccountNumber, rule.Targets.WebhookURL)
		}
	})
}

type RuleOnCmd struct {
	ID string `arg:"" help:"Rule UUID."`
}

func (cmd *RuleOnCmd) Run(ctx *Context) error {
	c, err := ctx.AuthClient()
	if err != nil {
		return err
	}
	if err := c.NotifActivateRule(ctx, cmd.ID); err != nil {
		return err
	}
	fmt.Printf("activated %s\n", cmd.ID)
	return nil
}

type RuleOffCmd struct {
	ID string `arg:"" help:"Rule UUID."`
}

func (cmd *RuleOffCmd) Run(ctx *Context) error {
	c, err := ctx.AuthClient()
	if err != nil {
		return err
	}
	if err := c.NotifDeactivateRule(ctx, cmd.ID); err != nil {
		return err
	}
	fmt.Printf("deactivated %s\n", cmd.ID)
	return nil
}

// assert at compile time that Context satisfies context.Context (so it can be
// passed to client methods directly).
var _ context.Context = (*Context)(nil)

// emit writes result as JSON (if --json) or runs the human fallback.
func (ctx *Context) emit(v any, human func()) error {
	if ctx.Globals.JSON {
		return json.NewEncoder(os.Stdout).Encode(v)
	}
	human()
	return nil
}
