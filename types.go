package main

import "strings"

// ---------- accounts ----------

// Account is one row from GET /v2/accounts.
type Account struct {
	Account            string  `json:"account"`
	Available          float64 `json:"available"`
	Balance            float64 `json:"balance"`
	BalanceRefCurrency float64 `json:"balanceRefCurrency"`
	CashAccountType    string  `json:"cashAccountType"`
	Category           string  `json:"category"`
	Currency           string  `json:"currency"`
	Customer           string  `json:"customer"`
	IBAN               string  `json:"iban"`
	RefCurrency        string  `json:"refCurrency"`
	Type               string  `json:"type"`
}

// CustomerID returns the leading digits of the Customer field ("0104183 Karl
// Brockman" → "0104183").
func (a Account) CustomerID() string {
	if f := strings.Fields(a.Customer); len(f) > 0 {
		return f[0]
	}
	return a.Customer
}

// AccountPath returns the part after the slash ("0104183/001.000.752" →
// "001.000.752"), suitable for the {account} URL param.
func (a Account) AccountPath() string {
	if i := strings.Index(a.Account, "/"); i >= 0 {
		return a.Account[i+1:]
	}
	return a.Account
}

// AccountsResp is the envelope around GET /v2/accounts.
type AccountsResp struct {
	Accounts    []Account `json:"accounts"`
	MoreResults bool      `json:"moreResults"`
	ResultSet   int       `json:"resultSetSize"`
}

// AccountHistory bundles one account with its transactions for output.
type AccountHistory struct {
	Account      Account       `json:"account"`
	Transactions []Transaction `json:"transactions"`
}

// ---------- transactions ----------

// TxParty is either the debitor or creditor side of a transaction.
type TxParty struct {
	Name            string `json:"name,omitempty"`
	Address         string `json:"address,omitempty"`
	Postalcode      string `json:"postalcode,omitempty"`
	City            string `json:"city,omitempty"`
	Country         string `json:"country,omitempty"`
	IBAN            string `json:"iban,omitempty"`
	VBAN            string `json:"vban,omitempty"`
	BIC             string `json:"bic,omitempty"`
	CreditInstitute string `json:"creditInstitution,omitempty"`
	AccountNumber   string `json:"accountNumber,omitempty"`
}

// Transaction is one booked or pending posting.
type Transaction struct {
	OrderID         int64   `json:"orderId"`
	CustomID        string  `json:"customId"`
	TransactionNr   string  `json:"transactionNr"`
	ServiceType     string  `json:"serviceType"`
	TransactionCode string  `json:"transactionCode"`
	Type            string  `json:"type"`
	State           string  `json:"state"`
	Direction       string  `json:"direction"`
	Amount          float64 `json:"amount"`
	TotalAmount     float64 `json:"totalAmount"`
	Currency        string  `json:"currency"`
	Valuta          string  `json:"valuta"`
	BookingDate     string  `json:"bookingDate"`
	Reference       string  `json:"reference"`
	Debitor         TxParty `json:"debitor"`
	Creditor        TxParty `json:"creditor"`
}

// TransactionsResp is the envelope around paginated transaction lists, and
// also the response body of create/delete/sign endpoints.
type TransactionsResp struct {
	Transactions []Transaction `json:"transactions"`
	MoreResults  bool          `json:"moreResults"`
	ResultSet    int           `json:"resultSetSize"`
}

// CreateTx is the body we send in PUT /v2/transactions. Optional scalars are
// pointers so they can be omitted instead of sent as the zero value.
type CreateTx struct {
	CustomID              string            `json:"customId"`
	Type                  string            `json:"type"`
	Amount                float64           `json:"amount"`
	Currency              string            `json:"currency"`
	Express               *bool             `json:"express,omitempty"`
	Valuta                string            `json:"valuta,omitempty"`
	ValutaIsExecutionDate *bool             `json:"valutaIsExecutionDate,omitempty"`
	Reference             string            `json:"reference,omitempty"`
	PurposeOfPayment      string            `json:"purposeOfPayment,omitempty"`
	Charge                string            `json:"charge,omitempty"`
	Correspondence        *bool             `json:"correspondence,omitempty"`
	OrderingCustomer      *OrderingCustomer `json:"orderingCustomer,omitempty"`
	Debitor               TxParty           `json:"debitor"`
	Creditor              TxParty           `json:"creditor"`
}

// OrderingCustomer is the full address block of the person who initiated the
// payment. Required when correspondence=true, and typically required for
// international and SEPA_INSTANT transfers.
type OrderingCustomer struct {
	Name       string `json:"name,omitempty"`
	Address    string `json:"address,omitempty"`
	Postalcode string `json:"postalcode,omitempty"`
	City       string `json:"city,omitempty"`
	Country    string `json:"country,omitempty"`
}

// RequestTanResp mirrors /v2/requestTan's response.
type RequestTanResp struct {
	ChallengeID string `json:"challengeId"`
	Expires     string `json:"expires"`
}

// ---------- notification rules ----------

// Event type constants for instant-transaction webhooks.
const (
	EventReceived = "INSTANT_TRANSACTION_RECEIVED"
	EventExecuted = "INSTANT_TRANSACTION_EXECUTED"
	EventRejected = "INSTANT_TRANSACTION_REJECTED"
)

type NotifTargets struct {
	WebhookURL string `json:"webhookUrl"`
}

type NotifEvent struct {
	Type string `json:"type"`
}

type NotifCreateRulesReq struct {
	Name           string       `json:"name"`
	AccountNumbers []string     `json:"accountNumbers"`
	Targets        NotifTargets `json:"targets"`
	Events         []NotifEvent `json:"events"`
}

type NotifRule struct {
	ID             string       `json:"id"`
	Status         string       `json:"status"` // ACTIVE / INACTIVE
	Name           string       `json:"name"`
	AccountNumber  string       `json:"accountNumber"`
	Targets        NotifTargets `json:"targets"`
	Events         []NotifEvent `json:"events"`
	CreatedBy      string       `json:"createdBy,omitempty"`
	CreatedAt      string       `json:"createdAt,omitempty"`
	LastModifiedBy string       `json:"lastModifiedBy,omitempty"`
	LastModifiedAt string       `json:"lastModifiedAt,omitempty"`
}

type NotifRulesList struct {
	Rules      []NotifRule `json:"rules"`
	Pagination struct {
		TotalCount int  `json:"totalCount"`
		PageIndex  int  `json:"pageIndex"`
		PageSize   int  `json:"pageSize"`
		HasMore    bool `json:"hasMore"`
	} `json:"pagination"`
}

type NotifCreatedRules struct {
	Rules []NotifRule `json:"rules"`
}

// Ptr returns a pointer to v. Useful for optional fields in request bodies
// where the distinction between "false" and "omitted" matters.
func Ptr[T any](v T) *T { return &v }
