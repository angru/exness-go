package exness

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const writeKey = "trade-test-0001"

func openRequest(t *testing.T) OpenPositionRequest {
	return OpenPositionRequest{Instrument: "EURUSD", Side: TradeBuy, Volume: requestDecimal(t, "0.01")}
}

type writeCase struct {
	Name, Method, Resource, Key string
	Body                        json.RawMessage
	Query                       map[string]string
}

func submitCase(t *testing.T, c *Client, test writeCase) (*AckResponse, error) {
	t.Helper()
	ctx := context.Background()
	switch test.Name {
	case "open":
		var r OpenPositionRequest
		if err := json.Unmarshal(test.Body, &r); err != nil {
			t.Fatal(err)
		}
		return c.OpenPosition(ctx, r, test.Key)
	case "limit":
		var r LimitOrderRequest
		if err := json.Unmarshal(test.Body, &r); err != nil {
			t.Fatal(err)
		}
		return c.PlacePendingOrder(ctx, r, test.Key)
	case "stop":
		var r StopOrderRequest
		if err := json.Unmarshal(test.Body, &r); err != nil {
			t.Fatal(err)
		}
		return c.PlacePendingOrder(ctx, r, test.Key)
	case "sltp":
		var r SltpOrderRequest
		if err := json.Unmarshal(test.Body, &r); err != nil {
			t.Fatal(err)
		}
		return c.PlacePendingOrder(ctx, r, test.Key)
	case "modify":
		var fields struct {
			TakeProfitPrice Decimal `json:"take_profit_price"`
			Comment         string  `json:"comment"`
		}
		if err := json.Unmarshal(test.Body, &fields); err != nil {
			t.Fatal(err)
		}
		return c.ModifyOrder(ctx, "000456", ModifyOrderRequest{StopLossPrice: ClearPrice(), TakeProfitPrice: SetPrice(fields.TakeProfitPrice), Comment: &fields.Comment}, test.Key)
	case "cancel":
		return c.CancelPendingOrder(ctx, "000456", test.Key)
	default:
		r := ClosePositionRequest{}
		if value, ok := test.Query["volume"]; ok {
			d := requestDecimal(t, value)
			r.Volume = &d
		}
		if value, ok := test.Query["price"]; ok {
			d := requestDecimal(t, value)
			r.Price = &d
		}
		if value, ok := test.Query["deviation"]; ok {
			d := requestDecimal(t, value)
			r.Deviation = &d
		}
		if value, ok := test.Query["close_by_position_id"]; ok {
			r.CloseByPositionID = &value
		}
		return c.ClosePosition(ctx, "000789", r, test.Key)
	}
}

func TestAllWritesOnTLSWire(t *testing.T) {
	var cases []writeCase
	if err := json.Unmarshal(readFixture(t, "write-cases"), &cases); err != nil {
		t.Fatal(err)
	}
	type observed struct {
		method, path string
		query        url.Values
		headers      http.Header
		body         []byte
	}
	requests := make(chan observed, len(cases)+1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		requests <- observed{r.Method, r.URL.RequestURI(), r.URL.Query(), r.Header.Clone(), body}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "GET" {
			_ = json.NewEncoder(w).Encode(map[string]string{"access_point": "https://" + r.Host})
			return
		}
		w.WriteHeader(202)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "accepted", "operation_id": "000789", "client_request_id": r.Header.Get("EXN-IDEMPOTENCY-KEY")})
	}))
	defer server.Close()
	c := tlsClient(t, server, 2*time.Second)
	for _, test := range cases {
		ack, err := submitCase(t, c, test)
		if err != nil {
			t.Fatal(test.Name, err)
		}
		if ack.Status != "accepted" || ack.OperationID == nil || *ack.OperationID != "000789" || ack.ClientRequestID == nil || *ack.ClientRequestID != test.Key {
			t.Fatal("ACK correlation lost")
		}
	}
	if len(requests) != len(cases)+1 {
		t.Fatal("unexpected request count")
	}
	if (<-requests).method != "GET" {
		t.Fatal("missing discovery")
	}
	pub, _ := hex.DecodeString(testPublic)
	for _, test := range cases {
		wire := <-requests
		u, err := url.ParseRequestURI(wire.path)
		if err != nil {
			t.Fatal(err)
		}
		if wire.method != test.Method || u.Path != "/v1/trading/accounts/123456/"+test.Resource {
			t.Fatal("incorrect method or path")
		}
		gotQuery := make(map[string]string)
		for key := range wire.query {
			gotQuery[key] = wire.query.Get(key)
		}
		if !reflect.DeepEqual(gotQuery, test.Query) {
			t.Fatalf("query mismatch: %v", gotQuery)
		}
		var got, want any
		if len(wire.body) != 0 {
			if err := json.Unmarshal(wire.body, &got); err != nil {
				t.Fatal(err)
			}
		}
		if err := json.Unmarshal(test.Body, &want); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("body mismatch for %s", test.Name)
		}
		payload, _ := base64.RawURLEncoding.DecodeString(wire.headers.Get("EXN-DATA"))
		sig, _ := base64.RawURLEncoding.DecodeString(wire.headers.Get("EXN-SIGN"))
		if !ed25519.Verify(pub, payload, sig) {
			t.Fatal("invalid signature")
		}
		var meta signedPayload
		if err := json.Unmarshal(payload, &meta); err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(wire.body)
		if meta.Method != wire.method || meta.Path != wire.path || meta.BodyHash != base64.RawURLEncoding.EncodeToString(hash[:]) || meta.IdempotencyKey != test.Key {
			t.Fatal("signature mismatch with wire bytes")
		}
	}
}

func TestWriteFailuresAreSingleAttempt(t *testing.T) {
	for _, mode := range []string{"400", "401", "403", "404", "409", "422", "429", "408", "500", "503", "307", "200", "bad_ack", "wrong_key", "oversize", "transport", "missing_id"} {
		t.Run(mode, func(t *testing.T) {
			writes := 0
			c := makeClient(t, func(r *http.Request) (*http.Response, error) {
				if r.Method == "GET" {
					return discoveryResponse(), nil
				}
				writes++
				if r.GetBody != nil {
					t.Fatal("mutation must not expose a replay body")
				}
				switch mode {
				case "transport":
					return nil, errors.New("private transport details")
				case "bad_ack":
					return response(202, `{"status":"confirmed"}`), nil
				case "wrong_key":
					return response(202, `{"status":"accepted","client_request_id":"other-key"}`), nil
				case "oversize":
					return response(202, strings.Repeat("x", (16<<20)+1)), nil
				case "missing_id":
					return response(202, `{"status":"accepted"}`), nil
				}
				status, _ := strconv.Atoi(mode)
				result := response(status, `{"code":3000,"error_message":"untrusted"}`)
				result.Header.Set("Retry-After", "7")
				result.Header.Set("Location", "https://other.example")
				return result, nil
			})
			ack, err := c.OpenPosition(context.Background(), openRequest(t), writeKey)
			if writes != 1 {
				t.Fatal("write retried or skipped")
			}
			if mode == "missing_id" {
				if err != nil || ack.OperationID != nil {
					t.Fatal("missing identity fabricated", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected failure")
			}
			var unknown *UnknownOutcomeError
			var api *APIError
			status, _ := strconv.Atoi(mode)
			if status >= 400 && status < 500 && status != 408 {
				if errors.As(err, &unknown) || !errors.As(err, &api) || api.StatusCode != status || api.RetryAfter != "7" {
					t.Fatalf("wrong rejection type: %v", err)
				}
			} else {
				if !errors.As(err, &unknown) || unknown.Method != "POST" || unknown.Path != "/v1/trading/accounts/123456/positions" || unknown.AccountID != "123456" || unknown.IdempotencyKey != writeKey {
					t.Fatalf("unknown outcome identity lost: %v", err)
				}
				if strings.Contains(err.Error(), writeKey) || strings.Contains(err.Error(), "private") {
					t.Fatal("unsafe error text")
				}
			}
		})
	}
}

func TestWritePreflightDoesNotSubmit(t *testing.T) {
	calls := 0
	c := makeClient(t, func(r *http.Request) (*http.Response, error) { calls++; return nil, errors.New("discovery failed") })
	ctx := context.Background()
	zero := requestDecimal(t, "0")
	badID := "../2"
	var nilLimit *LimitOrderRequest
	invalid := []func() error{
		func() error { _, e := c.OpenPosition(ctx, openRequest(t), "bad"); return e },
		func() error { _, e := c.OpenPosition(ctx, OpenPositionRequest{}, writeKey); return e },
		func() error { _, e := c.PlacePendingOrder(ctx, nil, writeKey); return e },
		func() error { _, e := c.PlacePendingOrder(ctx, nilLimit, writeKey); return e },
		func() error { _, e := c.PlacePendingOrder(ctx, LimitOrderRequest{}, writeKey); return e },
		func() error {
			_, e := c.ModifyOrder(ctx, badID, ModifyOrderRequest{StopLossPrice: ClearPrice()}, writeKey)
			return e
		},
		func() error { _, e := c.ModifyOrder(ctx, "1", ModifyOrderRequest{}, writeKey); return e },
		func() error { _, e := c.CancelPendingOrder(ctx, badID, writeKey); return e },
		func() error { _, e := c.ClosePosition(ctx, badID, ClosePositionRequest{}, writeKey); return e },
		func() error {
			_, e := c.ClosePosition(ctx, "1", ClosePositionRequest{Volume: &zero}, writeKey)
			return e
		},
	}
	for _, call := range invalid {
		if err := call(); err == nil {
			t.Fatal("invalid input accepted")
		}
	}
	if calls != 0 {
		t.Fatal("validation performed network I/O")
	}
	_, err := c.OpenPosition(ctx, openRequest(t), writeKey)
	var transport *TransportError
	var unknown *UnknownOutcomeError
	if calls != 1 || !errors.As(err, &transport) || errors.As(err, &unknown) {
		t.Fatal("discovery is not submission")
	}
	c.Close()
	if _, err := c.OpenPosition(ctx, openRequest(t), writeKey); err == nil {
		t.Fatal("closed client admitted write")
	}
}

func TestPatchAndCloseShapeValidation(t *testing.T) {
	zero, negative := requestDecimal(t, "0"), requestDecimal(t, "-1")
	badID, goodID, comment, badComment := "18446744073709551616", "2", "only comment", string([]byte{255})
	for _, r := range []ModifyOrderRequest{{}, {Comment: &comment}, {Price: &zero}, {StopLossPrice: SetPrice(negative)}, {StopLossPrice: ClearPrice(), Comment: &badComment}} {
		if _, err := r.MarshalJSON(); err == nil {
			t.Fatal("invalid PATCH accepted")
		}
	}
	b, err := (ModifyOrderRequest{StopLossPrice: ClearPrice()}).MarshalJSON()
	if err != nil || string(b) != `{"stop_loss_price":null}` {
		t.Fatal("clear was omitted", err)
	}
	price := requestDecimal(t, "1.23")
	b, err = (ModifyOrderRequest{Price: &price}).MarshalJSON()
	if err != nil || string(b) != `{"price":"1.23"}` {
		t.Fatal("omitted SLTP sent", err)
	}
	for _, r := range []SltpOrderRequest{{PositionID: "1"}, {PositionID: badID, StopLossPrice: &price}, {PositionID: "1", StopLossPrice: &zero}, {PositionID: "1", StopLossPrice: &price, Comment: &badComment}} {
		if _, err := r.MarshalJSON(); err == nil {
			t.Fatal("invalid SLTP accepted")
		}
	}
	minusZero := requestDecimal(t, "-0")
	for _, r := range []ClosePositionRequest{{Volume: &zero}, {Volume: &negative}, {Volume: &Decimal{}}, {Volume: &minusZero, CloseByPositionID: &goodID}, {Price: &zero}, {Deviation: &Decimal{}}, {CloseByPositionID: &badID}} {
		if _, err := r.Query(); err == nil {
			t.Fatal("invalid close accepted")
		}
	}
}

func TestWriteCancellationAndLostAckOnTLS(t *testing.T) {
	for _, mode := range []string{"cancel", "cancel_body", "timeout", "truncated", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var writes atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" {
					_ = json.NewEncoder(w).Encode(map[string]string{"access_point": "https://" + r.Host})
					return
				}
				writes.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				switch mode {
				case "truncated":
					w.Header().Set("Content-Length", "100")
					w.WriteHeader(202)
					_, _ = w.Write([]byte("{"))
					return
				case "redirect":
					w.Header().Set("Location", "https://"+r.Host+"/redirect-target")
					w.WriteHeader(307)
					return
				case "cancel_body":
					w.Header().Set("Content-Length", "100")
					w.WriteHeader(202)
					w.(http.Flusher).Flush()
				}
				close(entered)
				select {
				case <-release:
				case <-r.Context().Done():
				}
			}))
			defer server.Close()
			defer close(release)
			timeout := 2 * time.Second
			if mode == "timeout" {
				timeout = 100 * time.Millisecond
			}
			c := tlsClient(t, server, timeout)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancel" || mode == "cancel_body" {
				result := make(chan error, 1)
				request := openRequest(t)
				go func() { _, err := c.OpenPosition(ctx, request, writeKey); result <- err }()
				awaitSignal(t, entered)
				cancel()
				err := awaitError(t, result)
				var unknown *UnknownOutcomeError
				if !errors.As(err, &unknown) || !errors.Is(err, context.Canceled) {
					t.Fatalf("lost cancellation or unknown outcome: %v", err)
				}
			} else {
				_, err := c.OpenPosition(ctx, openRequest(t), writeKey)
				var unknown *UnknownOutcomeError
				if !errors.As(err, &unknown) {
					t.Fatalf("missing unknown outcome: %v", err)
				}
			}
			if writes.Load() != 1 {
				t.Fatal("write repeated")
			}
		})
	}
}

func TestPreCancelledWriteIsNotUnknown(t *testing.T) {
	c := makeClient(t, func(r *http.Request) (*http.Response, error) { t.Fatal("cancelled request sent"); return nil, nil })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.OpenPosition(ctx, openRequest(t), writeKey)
	var unknown *UnknownOutcomeError
	if !errors.Is(err, context.Canceled) || errors.As(err, &unknown) {
		t.Fatalf("wrong preflight cancellation: %v", err)
	}
}
