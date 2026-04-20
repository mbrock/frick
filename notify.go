package main

// Notification rules API.
//
// This endpoint group lives at a different host (api.bankfrick.li) from the
// main Web API but uses the same JWT + RSA-SHA512 signing scheme. Signed
// GETs are used for listing, which is why we send a signature over an empty
// body; see Client.signedRequest.

import (
	"context"
	"fmt"
	"strings"
)

// notifBase returns the notification API root that matches c.BaseURL.
func (c *Client) notifBase() string {
	if strings.Contains(c.BaseURL, "olbtest") || strings.Contains(c.BaseURL, "olbsandbox") {
		return "https://api-test.bankfrick.li/onlinebanking/notifications"
	}
	return "https://api.bankfrick.li/onlinebanking/notifications"
}

// NotifListRules fetches one page of notification rules.
func (c *Client) NotifListRules(ctx context.Context, pageIndex, pageSize int) (*NotifRulesList, error) {
	if pageSize == 0 {
		pageSize = 100
	}
	u := fmt.Sprintf("%s/topics/instant-transactions/rules?pageIndex=%d&pageSize=%d",
		c.notifBase(), pageIndex, pageSize)
	var out NotifRulesList
	if err := c.signedRequest(ctx, "GET", u, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// NotifCreateRules creates one rule per account in the request.
func (c *Client) NotifCreateRules(ctx context.Context, req NotifCreateRulesReq) (*NotifCreatedRules, error) {
	u := c.notifBase() + "/topics/instant-transactions/rules"
	var out NotifCreatedRules
	if err := c.signedRequest(ctx, "POST", u, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// NotifActivateRule turns a rule on by UUID.
func (c *Client) NotifActivateRule(ctx context.Context, id string) error {
	u := c.notifBase() + "/topics/instant-transactions/rules/activation"
	return c.signedRequest(ctx, "POST", u, map[string]string{"id": id}, nil)
}

// NotifDeactivateRule turns a rule off by UUID.
func (c *Client) NotifDeactivateRule(ctx context.Context, id string) error {
	u := c.notifBase() + "/topics/instant-transactions/rules/deactivation"
	return c.signedRequest(ctx, "POST", u, map[string]string{"id": id}, nil)
}
