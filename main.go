// frick: Bank Frick Web API CLI (kong-powered).
//
// Reads FRICK_API_KEY from env or a local config file, loads the RSA private key specified by
// --key-file / $FRICK_KEY_FILE, obtains a JWT via POST /v2/authorize, caches
// it under $XDG_CACHE_HOME/frickgo/jwt.json, and signs every write with
// RSA-SHA512. See docs/frick-api.md.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alecthomas/kong"
)

const (
	prodBase    = "https://olb.bankfrick.li/webapi"
	sandboxBase = "https://olbsandbox.bankfrick.li/webapi"
)

// Globals are the flags shared across every subcommand.
type Globals struct {
	Sandbox bool   `help:"Use the sandbox environment."`
	KeyFile string `name:"key-file" env:"FRICK_KEY_FILE" help:"RSA private key used to sign requests (required for authenticated commands)."`
	Cache   string `default:"${cacheDefault}" help:"JWT cache path (empty string to disable)."`
	NoCache bool   `name:"no-cache" help:"Ignore and overwrite any cached JWT."`
	AuthTTL string `name:"auth-ttl" default:"1h" help:"Requested JWT lifetime."`
	JSON    bool   `help:"Emit structured JSON on stdout."`
}

// CLI is the full command schema.
type CLI struct {
	Globals

	Info       InfoCmd       `cmd:"" help:"GET /v2/info (no auth)."`
	Auth       AuthCmd       `cmd:"" help:"Warm or refresh the JWT cache."`
	Logout     LogoutCmd     `cmd:"" help:"Clear the JWT cache."`
	Balance    BalanceCmd    `cmd:"" help:"List accounts with balances."`
	Tx         TxCmd         `cmd:"" help:"List recent transactions (quick, default 30d)."`
	History    HistoryCmd    `cmd:"" help:"Paginated full account history."`
	Pending    PendingCmd    `cmd:"" help:"List orders awaiting signature."`
	Create     CreateCmd     `cmd:"" help:"Create a pending order (optionally sign immediately)."`
	Delete     DeleteCmd     `cmd:"" help:"Delete pending orders by orderId."`
	SignNT     SignNTCmd     `cmd:"sign-nt" help:"Sign orders without TAN."`
	RequestTAN RequestTANCmd `cmd:"request-tan" help:"Request a TAN challenge (PushTAN/SMS)."`
	Sign       SignCmd       `cmd:"" help:"Submit TAN code for a challenge."`
	CancelTAN  CancelTANCmd  `cmd:"cancel-tan" help:"Cancel an outstanding TAN challenge."`
	Rules      RulesCmd      `cmd:"" help:"List instant-transaction notification rules."`
	RuleAdd    RuleAddCmd    `cmd:"rule-add" help:"Create a notification rule."`
	RuleOn     RuleOnCmd     `cmd:"rule-on" help:"Activate a rule by UUID."`
	RuleOff    RuleOffCmd    `cmd:"rule-off" help:"Deactivate a rule by UUID."`
}

// Context is the per-invocation context passed to each Run method. It embeds
// a context.Context (cancelled on SIGINT) so commands can pass `ctx` straight
// to client methods.
type Context struct {
	context.Context
	Globals *Globals
	client  *Client
}

func (ctx *Context) base() string {
	if ctx.Globals.Sandbox {
		return sandboxBase
	}
	return prodBase
}

// AuthClient returns (and memoizes) an authenticated API client.
func (ctx *Context) AuthClient() (*Client, error) {
	if ctx.client != nil {
		return ctx.client, nil
	}
	apiKey := os.Getenv("FRICK_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("FRICK_API_KEY not set")
	}
	kp := ctx.Globals.KeyFile
	if kp == "" {
		return nil, fmt.Errorf("RSA private key not set; pass --key-file or set FRICK_KEY_FILE")
	}
	key, err := loadRSAKey(kp)
	if err != nil {
		return nil, fmt.Errorf("load key %s: %w", kp, err)
	}
	ttl, err := time.ParseDuration(ctx.Globals.AuthTTL)
	if err != nil {
		return nil, fmt.Errorf("--auth-ttl: %w", err)
	}
	c := &Client{
		BaseURL:   ctx.base(),
		APIKey:    apiKey,
		Key:       key,
		AuthTTL:   ttl,
		CachePath: ctx.Globals.Cache,
	}
	if ctx.Globals.NoCache {
		_ = c.ClearCache()
	}
	if _, _, err := c.EnsureAuthorized(ctx); err != nil {
		return nil, err
	}
	ctx.client = c
	return c, nil
}

func main() {
	var cli CLI
	if len(os.Args) < 2 {
		os.Args = append(os.Args, "--help")
	}
	if _, err := loadDefaultEnv(); err != nil {
		fmt.Fprintf(os.Stderr, "frick: load config: %v\n", err)
		os.Exit(2)
	}
	if err := normalizeCredentialEnv(); err != nil {
		fmt.Fprintf(os.Stderr, "frick: normalize config: %v\n", err)
		os.Exit(2)
	}
	kctx := kong.Parse(&cli,
		kong.Name("frick"),
		kong.Description("Bank Frick Web API CLI. Reads FRICK_API_KEY from env or ~/.config/frick/.env and signs with an RSA key."),
		kong.Vars{"cacheDefault": DefaultJWTCachePath()},
		kong.UsageOnError(),
	)
	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	app := &Context{Context: sigCtx, Globals: &cli.Globals}
	kctx.FatalIfErrorf(kctx.Run(app))
}
