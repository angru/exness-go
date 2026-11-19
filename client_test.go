package exness

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func discoveryResponse() *http.Response {
	return response(200, `{"access_point":"https://account.example","account_id":"123456"}`)
}

func makeClient(t *testing.T, handler roundTripFunc) *Client {
	t.Helper()
	c, err := NewClient(Config{Credentials: testCredentials(t), AccountID: "123456", DiscoveryURL: "https://gateway.example", Transport: handler})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c
}

func verifyRequest(t *testing.T, r *http.Request) {
	t.Helper()
	data, err := base64.RawURLEncoding.DecodeString(r.Header.Get("EXN-DATA"))
	if err != nil {
		t.Fatal(err)
	}
	sig, err := base64.RawURLEncoding.DecodeString(r.Header.Get("EXN-SIGN"))
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := hex.DecodeString(testPublic)
	if !ed25519.Verify(pub, data, sig) {
		t.Fatal("invalid request signature")
	}
	var payload signedPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Path != r.URL.RequestURI() || payload.Method != "GET" || r.Body != nil || payload.BodyHash != "47DEQpj8HBSa-_TImW-5JCeuQeRkm5NMpJWZG3hSuFU" {
		t.Fatal("signature does not match sent request")
	}
}

func TestReadWorkflowsAndCachedDiscovery(t *testing.T) {
	var paths []string
	c := makeClient(t, func(r *http.Request) (*http.Response, error) {
		verifyRequest(t, r)
		paths = append(paths, r.URL.Path)
		if r.URL.Host == "gateway.example" {
			if r.URL.Query().Get("account_id") != "123456" {
				t.Fatal("wrong discovery account")
			}
			return discoveryResponse(), nil
		}
		if r.URL.Host != "account.example" {
			t.Fatal("wrong host")
		}
		name := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		return response(200, string(readFixture(t, name))), nil
	})
	ctx := context.Background()
	account, err := c.GetAccount(ctx)
	if err != nil || account.AccountID != "123456" {
		t.Fatalf("account: %v", err)
	}
	instruments, err := c.GetInstruments(ctx)
	if err != nil || len(instruments.Instruments) != 2 {
		t.Fatalf("instruments: %v", err)
	}
	snapshot, err := c.GetSnapshot(ctx)
	if err != nil || snapshot.AccountState.Balance.String() != "12345678901234567890.123456789" {
		t.Fatalf("snapshot: %v", err)
	}
	limits, err := c.GetLimits(ctx)
	if err != nil || *limits.Limits.Rest.Methods[0].Limits.HistoryRangeDays != 7 {
		t.Fatalf("limits: %v", err)
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	orders, err := c.GetOrdersHistory(ctx, OrderHistoryQuery{HistoryQuery: HistoryQuery{From: &start}})
	if err != nil || orders.Items[0].Order.OrderID != "1002" {
		t.Fatalf("orders: %v", err)
	}
	deals, err := c.GetDealsHistory(ctx, DealHistoryQuery{HistoryQuery: HistoryQuery{From: &start}})
	if err != nil || deals.Items[0].Deal.Profit.Amount.String() != "100.01" {
		t.Fatalf("deals: %v", err)
	}
	want := []string{"/v1/trading/access-point", "/v1/configuration/accounts/123456/account", "/v1/configuration/accounts/123456/instruments", "/v1/trading/accounts/123456/snapshot", "/v1/configuration/accounts/123456/limits", "/v1/history/accounts/123456/orders", "/v1/history/accounts/123456/deals"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths: %v", paths)
	}
}

func TestPaginationBothResources(t *testing.T) {
	for _, kind := range []string{"orders", "deals"} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			c := makeClient(t, func(r *http.Request) (*http.Response, error) {
				verifyRequest(t, r)
				if r.URL.Host == "gateway.example" {
					return discoveryResponse(), nil
				}
				calls++
				if calls == 1 {
					return response(200, string(readFixture(t, kind))), nil
				}
				want := "next-page"
				if kind == "orders" {
					want = "opaque+/= &%?Ж"
				}
				if r.URL.Query().Get("cursor") != want || len(r.URL.Query()) != 2 {
					t.Fatal("cursor changed or filters carried forward")
				}
				return response(200, `{"items":[],"has_more":false}`), nil
			})
			start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			query := HistoryQuery{From: &start, Instrument: "EURUSD", Limit: 20}
			count := 0
			if kind == "orders" {
				for _, err := range c.IterOrdersHistory(context.Background(), OrderHistoryQuery{query, OrderFilled}) {
					if err != nil {
						t.Fatal(err)
					}
					count++
				}
			} else {
				for _, err := range c.IterDealsHistory(context.Background(), DealHistoryQuery{query, DealBalance}) {
					if err != nil {
						t.Fatal(err)
					}
					count++
				}
			}
			if calls != 2 || count != 1 {
				t.Fatalf("calls=%d rows=%d", calls, count)
			}
		})
	}
}

func TestHTTPFailuresNoRedirectOrRetry(t *testing.T) {
	for _, status := range []int{302, 401, 403, 429, 503} {
		calls := 0
		c := makeClient(t, func(r *http.Request) (*http.Response, error) {
			calls++
			rsp := response(status, `{"code":1001,"error_message":"AUTH_INVALID_SIGNATURE"}`)
			rsp.Header.Set("Location", "https://elsewhere.example")
			rsp.Header.Set("Retry-After", "60")
			return rsp, nil
		})
		_, err := c.GetAccount(context.Background())
		var api *APIError
		if !errors.As(err, &api) || api.StatusCode != status || *api.Code != 1001 || api.RetryAfter != "60" || calls != 1 {
			t.Fatalf("HTTP %d: %v; calls=%d", status, err, calls)
		}
		if strings.Contains(api.Error(), "AUTH_INVALID") {
			t.Fatal("untrusted text leaked into error formatting")
		}
	}
}

func TestMalformedResponsesAndDiscovery(t *testing.T) {
	for _, body := range []string{"", "   ", "not json", "[]", "null", "{}", `{"access_point":123}`, `{"access_point":"http://insecure.example"}`, `{"access_point":"https://ap.example","account_id":"99"}`} {
		c := makeClient(t, func(r *http.Request) (*http.Response, error) { return response(200, body), nil })
		_, err := c.GetAccount(context.Background())
		var protocol *ProtocolError
		if !errors.As(err, &protocol) {
			t.Fatalf("expected protocol error for %q: %v", body, err)
		}
	}
	c := makeClient(t, func(r *http.Request) (*http.Response, error) { return response(502, "private body"), nil })
	_, err := c.GetAccount(context.Background())
	var api *APIError
	if !errors.As(err, &api) || api.Code != nil || strings.Contains(err.Error(), "private body") {
		t.Fatal(err)
	}
}

func TestCancellationRefreshAndClose(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := makeClient(t, func(r *http.Request) (*http.Response, error) { return nil, r.Context().Err() })
	_, err := c.GetAccount(ctx)
	var transport *TransportError
	if !errors.As(err, &transport) || !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	calls := 0
	c = makeClient(t, func(r *http.Request) (*http.Response, error) { calls++; return discoveryResponse(), nil })
	for _, refresh := range []bool{false, false, true} {
		if _, err := c.Discover(context.Background(), refresh); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatal("discovery cache failed")
	}
	c.Close()
	c.Close()
	if _, err := c.GetAccount(context.Background()); err == nil {
		t.Fatal("closed client accepted operation")
	}
}

func TestConcurrentDiscoveryIsCached(t *testing.T) {
	var calls atomic.Int32
	c := makeClient(t, func(r *http.Request) (*http.Response, error) { calls.Add(1); return discoveryResponse(), nil })
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			if _, err := c.Discover(context.Background(), false); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("discovery called %d times", calls.Load())
	}
}

func TestConfigurationAndQueryValidation(t *testing.T) {
	for _, id := range []string{"", "0", "-1", "01", "1/2", "18446744073709551616"} {
		_, err := NewClient(Config{Credentials: testCredentials(t), AccountID: id, DiscoveryURL: "https://gateway.example"})
		if err == nil {
			t.Fatalf("invalid account accepted: %q", id)
		}
	}
	for _, host := range []string{"bad", "http://gateway.example", "https://u:p@host", "https://host/path", "https://host?", "https://host#", "https://host:bad", "https://host/%2f"} {
		if _, err := baseURL(host); err == nil {
			t.Fatalf("invalid host accepted: %q", host)
		}
	}
	if _, err := NewClient(Config{}); err == nil {
		t.Fatal("empty config accepted")
	}
	if _, err := NewClient(Config{Credentials: testCredentials(t), AccountID: "1", DiscoveryURL: "https://gateway.example", Timeout: -1}); err == nil {
		t.Fatal("negative timeout accepted")
	}
	c := makeClient(t, func(r *http.Request) (*http.Response, error) {
		t.Fatal("invalid query made a network call")
		return nil, nil
	})
	start := time.Now()
	before := start.Add(-time.Hour)
	cursor := "opaque"
	empty := ""
	for _, query := range []HistoryQuery{
		{}, {From: &start, Limit: -1}, {From: &start, Limit: 201},
		{From: &start, To: &before}, {From: &start, Instrument: "invalid/name"},
		{From: &start, Cursor: &cursor}, {Cursor: &empty},
	} {
		if _, err := c.GetDealsHistory(context.Background(), DealHistoryQuery{HistoryQuery: query}); err == nil {
			t.Fatal("invalid history query accepted")
		}
	}
	if _, err := c.GetOrdersHistory(context.Background(), OrderHistoryQuery{OrderState: "bad"}); err == nil {
		t.Fatal("invalid order filter accepted")
	}
	if _, err := c.GetDealsHistory(context.Background(), DealHistoryQuery{DealType: "bad"}); err == nil {
		t.Fatal("invalid deal filter accepted")
	}
}
