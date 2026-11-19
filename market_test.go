package exness

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCandleHistorySignedQueries(t *testing.T) {
	for _, priceType := range []CandlePriceType{PriceBid, PriceAsk, PriceBoth} {
		for _, useRange := range []bool{false, true} {
			calls := 0
			start := time.Date(2026, 1, 1, 2, 0, 0, 0, time.FixedZone("Nicosia", 2*60*60))
			end := start.Add(time.Hour)
			count := -10
			c := makeClient(t, func(r *http.Request) (*http.Response, error) {
				calls++
				verifyRequest(t, r)
				if r.URL.Host == "gateway.example" {
					return discoveryResponse(), nil
				}
				if r.URL.Path != "/v1/market-data/accounts/123456/candles" {
					t.Fatal("wrong candle path")
				}
				want := map[string][]string{"instrument": {"EURUSD"}, "timeframe": {"H1"}, "from": {"2026-01-01T00:00:00Z"}, "price_type": {string(priceType)}}
				if useRange {
					want["to"] = []string{"2026-01-01T01:00:00Z"}
				} else {
					want["count"] = []string{"-10"}
				}
				if !reflect.DeepEqual(map[string][]string(r.URL.Query()), want) {
					t.Fatalf("query: %v", r.URL.Query())
				}
				return response(200, string(readFixture(t, "candles_"+string(priceType)))), nil
			})
			query := CandleQuery{Instrument: "EURUSD", Timeframe: TimeframeH1, From: start, PriceType: priceType}
			if useRange {
				query.To = &end
			} else {
				query.Count = &count
			}
			result, err := c.GetCandleHistory(context.Background(), query)
			if err != nil {
				t.Fatal(err)
			}
			if calls != 2 || len(result.Candles) != 1 {
				t.Fatal("unexpected request or row count")
			}
			candle := result.Candles[0]
			if candle.Volume == nil || *candle.Volume != 124 {
				t.Fatal("volume not preserved")
			}
			if priceType == PriceBoth {
				if candle.Open != nil || candle.Bid == nil || candle.Ask == nil || *candle.Bid.Open != 1.08321 || *candle.Ask.Open != 1.08331 {
					t.Fatal("both-side candle lost data")
				}
			} else {
				if candle.Open == nil || candle.Bid != nil || candle.Ask != nil {
					t.Fatal("wrong single-side shape")
				}
			}
		}
	}
}

func TestInstrumentConditionsAndPrecision(t *testing.T) {
	c := makeClient(t, func(r *http.Request) (*http.Response, error) {
		verifyRequest(t, r)
		if r.URL.Host == "gateway.example" {
			return discoveryResponse(), nil
		}
		if r.URL.Path != "/v1/configuration/accounts/123456/instruments/EURUSD/conditions" || r.URL.RawQuery != "" {
			t.Fatal("wrong conditions URL")
		}
		return response(200, string(readFixture(t, "conditions"))), nil
	})
	result, err := c.GetInstrumentConditions(context.Background(), "EURUSD")
	if err != nil {
		t.Fatal(err)
	}
	if *result.PointDigits != 5 || result.VolumeStep.String() != "0.01" || result.SwapLong.String() != "-6.35" {
		t.Fatal("precision/conditions mismatch")
	}
	if result.CommissionSettings.CommissionValue.Amount.String() != "3.50" || *result.TradeSessions[0].WeekStartTime != "Mon, 00:00:00" {
		t.Fatal("nested settings mismatch")
	}
	var minimal InstrumentConditions
	if err := json.Unmarshal([]byte(`{"instrument":"EURUSD","volume_min":null}`), &minimal); err != nil {
		t.Fatal(err)
	}
	if minimal.PointDigits != nil || minimal.VolumeMin != nil {
		t.Fatal("missing settings must remain absent")
	}
}

func TestOperationStatesAndIdentity(t *testing.T) {
	for _, state := range []OperationState{OperationPending, OperationConfirmed, OperationRejected, OperationFailed} {
		calls := 0
		c := makeClient(t, func(r *http.Request) (*http.Response, error) {
			calls++
			verifyRequest(t, r)
			if r.URL.Host == "gateway.example" {
				return discoveryResponse(), nil
			}
			if r.URL.Path != "/v1/trading/accounts/123456/operations/00789" {
				t.Fatal("wrong operation path")
			}
			return response(200, string(readFixture(t, "operation_"+string(state)))), nil
		})
		result, err := c.GetOperationStatus(context.Background(), "00789")
		if err != nil {
			t.Fatal(err)
		}
		if calls != 2 || result.Status != state || result.IsTerminal() != (state != OperationPending) {
			t.Fatal("incorrect state handling")
		}
		if (result.ErrorCode != nil) != (state == OperationRejected || state == OperationFailed) {
			t.Fatal("operation error not preserved")
		}
	}
}

func TestMarketParameterValidationBeforeDiscovery(t *testing.T) {
	c := makeClient(t, func(r *http.Request) (*http.Response, error) {
		t.Fatal("invalid arguments made a network call")
		return nil, nil
	})
	ctx := context.Background()
	for _, name := range []string{"", ".", "..", "EUR/USD", "EUR%2FUSD", "A?B", strings.Repeat("a", 12)} {
		if _, err := c.GetInstrumentConditions(ctx, name); err == nil {
			t.Fatalf("accepted instrument %q", name)
		}
	}
	for _, id := range []string{"", "-1", "1/2", "１２", "18446744073709551616", strings.Repeat("0", 21)} {
		if _, err := c.GetOperationStatus(ctx, id); err == nil {
			t.Fatalf("accepted operation ID %q", id)
		}
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	one, zero := 1, 0
	base := CandleQuery{Instrument: "EURUSD", Timeframe: TimeframeH1, From: start, Count: &one}
	cases := []func(*CandleQuery){
		func(q *CandleQuery) { q.Instrument = "EUR/USD" },
		func(q *CandleQuery) { q.Timeframe = "M2" },
		func(q *CandleQuery) { q.PriceType = "last" },
		func(q *CandleQuery) { q.From = time.Time{} },
		func(q *CandleQuery) { q.Count = nil },
		func(q *CandleQuery) { q.To = &end },
		func(q *CandleQuery) { q.Count = nil; q.To = &start },
		func(q *CandleQuery) { q.Count = &zero },
	}
	for _, change := range cases {
		query := base
		change(&query)
		if _, err := c.GetCandleHistory(ctx, query); err == nil {
			t.Fatal("accepted invalid candle query")
		}
	}
}

func TestMalformedMarketResponses(t *testing.T) {
	ctx := context.Background()
	start := time.Now().Add(-time.Hour)
	count := 1
	cases := []struct {
		body string
		call func(*Client) error
	}{
		{`{"instrument":"GBPUSD"}`, func(c *Client) error { _, err := c.GetInstrumentConditions(ctx, "EURUSD"); return err }},
		{`{"instrument":"EURUSD","volume_step":0.01}`, func(c *Client) error { _, err := c.GetInstrumentConditions(ctx, "EURUSD"); return err }},
		{`{"operation_id":"99","status":"pending","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`, func(c *Client) error { _, err := c.GetOperationStatus(ctx, "789"); return err }},
		{`{"operation_id":"789","status":"pending"}`, func(c *Client) error { _, err := c.GetOperationStatus(ctx, "789"); return err }},
		{`{"operation_id":"789","status":"accepted","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`, func(c *Client) error { _, err := c.GetOperationStatus(ctx, "789"); return err }},
		{`{"candles":[{"open":"1.2"}]}`, func(c *Client) error {
			_, err := c.GetCandleHistory(ctx, CandleQuery{Instrument: "EURUSD", Timeframe: TimeframeM1, From: start, Count: &count})
			return err
		}},
	}
	for _, tc := range cases {
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
	}
}

func TestOperationHTTPErrorAndDefaultCandlePrice(t *testing.T) {
	c := makeClient(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "gateway.example" {
			return discoveryResponse(), nil
		}
		if strings.Contains(r.URL.Path, "/operations/") {
			return response(404, `{"code":3014,"error_message":"REQUEST_OPERATION_NOT_FOUND"}`), nil
		}
		if r.URL.Query().Get("price_type") != "bid" {
			t.Fatal("default price type must be bid")
		}
		return response(200, `{"candles":[]}`), nil
	})
	_, err := c.GetOperationStatus(context.Background(), "789")
	var api *APIError
	if !errors.As(err, &api) || *api.Code != 3014 {
		t.Fatal(err)
	}
	count := 1
	result, err := c.GetCandleHistory(context.Background(), CandleQuery{Instrument: "EURUSD", Timeframe: TimeframeM1, From: time.Now(), Count: &count})
	if err != nil || len(result.Candles) != 0 {
		t.Fatalf("empty candles: %v", err)
	}
}

func TestCandleJSONNumberAndOptionalFields(t *testing.T) {
	for _, value := range []string{`"1.2"`, `true`, `NaN`, `Infinity`, `1e400`} {
		var history CandleHistory
		if json.Unmarshal([]byte(`{"candles":[{"open":`+value+`}]}`), &history) == nil {
			t.Fatalf("accepted non-double %s", value)
		}
	}
	var history CandleHistory
	if err := json.Unmarshal([]byte(`{"candles":[{"bid":{"open":0},"volume":0}]}`), &history); err != nil {
		t.Fatal(err)
	}
	if history.Candles[0].Time != nil || *history.Candles[0].Bid.Open != 0 {
		t.Fatal("optional/zero fields confused")
	}
}
