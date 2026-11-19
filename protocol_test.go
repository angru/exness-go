package exness

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestRequiredResponseData(t *testing.T) {
	ctx := context.Background()
	start := time.Now().Add(-time.Hour)
	cases := []struct {
		name string
		body string
		call func(*Client) error
	}{
		{"account mismatch", `{"account_id":"99","create_date":"2026-01-01T00:00:00Z"}`, func(c *Client) error { _, err := c.GetAccount(ctx); return err }},
		{"account date", `{"account_id":"123456"}`, func(c *Client) error { _, err := c.GetAccount(ctx); return err }},
		{"instrument syntax", `{"instruments":["EUR/USD"]}`, func(c *Client) error { _, err := c.GetInstruments(ctx); return err }},
		{"missing balance", `{"account_state":{"equity":"0","used_margin":"0"}}`, func(c *Client) error { _, err := c.GetSnapshot(ctx); return err }},
		{"numeric balance", `{"account_state":{"balance":1.1,"equity":"0","used_margin":"0"}}`, func(c *Client) error { _, err := c.GetSnapshot(ctx); return err }},
		{"incomplete order", `{"account_state":{"balance":"0","equity":"0","used_margin":"0"},"orders":[{}]}`, func(c *Client) error { _, err := c.GetSnapshot(ctx); return err }},
		{"incomplete position", `{"account_state":{"balance":"0","equity":"0","used_margin":"0"},"positions":[{}]}`, func(c *Client) error { _, err := c.GetSnapshot(ctx); return err }},
		{"missing limits", `{}`, func(c *Client) error { _, err := c.GetLimits(ctx); return err }},
		{"missing orders", `{}`, func(c *Client) error {
			_, err := c.GetOrdersHistory(ctx, OrderHistoryQuery{HistoryQuery: HistoryQuery{From: &start}})
			return err
		}},
		{"invalid orders", `{"items":[{}]}`, func(c *Client) error {
			_, err := c.GetOrdersHistory(ctx, OrderHistoryQuery{HistoryQuery: HistoryQuery{From: &start}})
			return err
		}},
		{"missing deals", `{}`, func(c *Client) error {
			_, err := c.GetDealsHistory(ctx, DealHistoryQuery{HistoryQuery: HistoryQuery{From: &start}})
			return err
		}},
		{"invalid deals", `{"items":[{}]}`, func(c *Client) error {
			_, err := c.GetDealsHistory(ctx, DealHistoryQuery{HistoryQuery: HistoryQuery{From: &start}})
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := makeClient(t, func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "gateway.example" {
					return discoveryResponse(), nil
				}
				return response(200, tc.body), nil
			})
			var protocol *ProtocolError
			if err := tc.call(c); !errors.As(err, &protocol) {
				t.Fatalf("expected ProtocolError: %v", err)
			}
		})
	}
}

func TestIteratorReportsLaterFailure(t *testing.T) {
	calls := 0
	c := makeClient(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "gateway.example" {
			return discoveryResponse(), nil
		}
		calls++
		if calls == 1 {
			return response(200, string(readFixture(t, "deals"))), nil
		}
		return response(503, `{"code":2,"error_message":"SYSTEM_TEMPORARY_UNAVAILABLE"}`), nil
	})
	start := time.Now().Add(-time.Hour)
	rows, failures := 0, 0
	for _, err := range c.IterDealsHistory(context.Background(), DealHistoryQuery{HistoryQuery: HistoryQuery{From: &start}}) {
		if err != nil {
			failures++
		} else {
			rows++
		}
	}
	if rows != 1 || failures != 1 || calls != 2 {
		t.Fatalf("rows=%d failures=%d calls=%d", rows, failures, calls)
	}
}
