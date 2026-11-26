package consumer

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	exness "github.com/angru/exness-go"
)

type syntheticAPI struct {
	t        *testing.T
	writes   []string
	reads    int
	key      string
	failNext bool
	closed   int
}

type observedBody struct {
	io.Reader
	close func()
}

func (b *observedBody) Close() error { b.close(); return nil }

func (api *syntheticAPI) RoundTrip(r *http.Request) (*http.Response, error) {
	api.t.Helper()
	if r.URL.Host != "consumer.invalid" || r.Header.Get("EXN-SIGN") == "" {
		api.t.Fatal("unexpected destination or unsigned request")
	}
	status := http.StatusOK
	var body any
	switch {
	case r.URL.Path == "/v1/trading/access-point":
		body = map[string]string{"access_point": "https://consumer.invalid"}
	case r.Method == "GET":
		if r.URL.Path != "/v1/trading/accounts/123456/operations/789" {
			api.t.Fatal("unexpected read")
		}
		api.reads++
		state := "confirmed"
		if api.reads == 1 {
			state = "pending"
		}
		body = map[string]string{"operation_id": "789", "client_request_id": api.key, "status": state, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z"}
	default:
		api.key = r.Header.Get("EXN-IDEMPOTENCY-KEY")
		parts := strings.Split(r.URL.Path, "/")
		api.writes = append(api.writes, r.Method+" "+parts[len(parts)-1])
		if r.Method == "PATCH" {
			data, err := io.ReadAll(r.Body)
			if err != nil || string(data) != `{"stop_loss_price":null}` {
				api.t.Fatal("PATCH lost explicit null")
			}
		}
		if r.Method == "DELETE" && strings.HasSuffix(r.URL.Path, "/789") && r.URL.Query().Get("volume") != "0.01" {
			api.t.Fatal("close lost exact volume")
		}
		status = http.StatusAccepted
		body = map[string]string{"status": "accepted", "operation_id": "789", "client_request_id": api.key}
		if api.failNext {
			status = 503
			body = map[string]int{"code": 5000}
		}
	}
	data, err := json.Marshal(body)
	if err != nil {
		api.t.Fatal(err)
	}
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: &observedBody{Reader: strings.NewReader(string(data)), close: func() { api.closed++ }}}, nil
}

func clientFor(t *testing.T, transport http.RoundTripper) *exness.Client {
	t.Helper()
	key := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	credentials, err := exness.NewCredentials("consumer-synthetic", hex.EncodeToString(key))
	if err != nil {
		t.Fatal(err)
	}
	client, err := exness.NewClient(exness.Config{Credentials: credentials, AccountID: "123456", DiscoveryURL: "https://consumer.invalid", Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	return client
}

func decimal(t *testing.T, value string) exness.Decimal {
	t.Helper()
	d, err := exness.NewDecimal(value)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func accepted(t *testing.T, ack *exness.AckResponse, err error) {
	t.Helper()
	if err != nil || ack == nil || ack.Status != "accepted" {
		t.Fatalf("invalid ACK: %v", err)
	}
}

func TestSubmissionsAndWait(t *testing.T) {
	api := &syntheticAPI{t: t}
	client := clientFor(t, api)
	ctx := context.Background()
	volume, price := decimal(t, "0.01"), decimal(t, "1.1")
	ack, err := client.OpenPosition(ctx, exness.OpenPositionRequest{Instrument: "EURUSD", Side: exness.TradeBuy, Volume: volume}, "consumer-open-0001")
	accepted(t, ack, err)
	reference, err := exness.OperationReferenceFromAck(*ack, "consumer-open-0001")
	if err != nil {
		t.Fatal(err)
	}
	status, err := client.WaitOperation(ctx, reference, exness.OperationWaitOptions{Timeout: time.Second, PollInterval: time.Millisecond})
	if err != nil || status.Status != "confirmed" || !status.IsTerminal() {
		t.Fatalf("wait: %v", err)
	}
	if matched, err := reference.MatchesStatus(*status); err != nil || !matched {
		t.Fatal("status correlation failed")
	}
	var event exness.TransactionEvent
	if err := json.Unmarshal([]byte(`{"event_type":"transaction_event","operation_id":"789","client_request_id":"consumer-open-0001","status":"success","payload":{}}`), &event); err != nil {
		t.Fatal(err)
	}
	if matched, err := reference.MatchesEvent(event); err != nil || !matched {
		t.Fatal("event correlation failed")
	}
	timeout := &exness.OperationWaitTimeoutError{Reference: reference, LastStatus: status, Attempts: 2}
	if !errors.Is(timeout, context.DeadlineExceeded) || timeout.LastStatus != status {
		t.Fatal("timeout identity lost")
	}
	orders := []exness.PendingOrderRequest{
		exness.LimitOrderRequest{Instrument: "EURUSD", Side: exness.TradeBuy, Volume: volume, Price: price},
		exness.StopOrderRequest{Instrument: "EURUSD", Side: exness.TradeSell, Volume: volume, Price: price},
		exness.SltpOrderRequest{PositionID: "789", StopLossPrice: &price},
	}
	for i, request := range orders {
		ack, err = client.PlacePendingOrder(ctx, request, fmt.Sprintf("consumer-order-%d", i))
		accepted(t, ack, err)
	}
	// All three PATCH states remain usable by an external caller.
	var omitted exness.PriceChange
	_ = omitted
	if _, err := json.Marshal(exness.ModifyOrderRequest{TakeProfitPrice: exness.SetPrice(price)}); err != nil {
		t.Fatal(err)
	}
	ack, err = client.ModifyOrder(ctx, "456", exness.ModifyOrderRequest{StopLossPrice: exness.ClearPrice()}, "consumer-modify-0001")
	accepted(t, ack, err)
	ack, err = client.CancelPendingOrder(ctx, "456", "consumer-cancel-0001")
	accepted(t, ack, err)
	ack, err = client.ClosePosition(ctx, "789", exness.ClosePositionRequest{Volume: &volume}, "consumer-close-0001")
	accepted(t, ack, err)
	api.failNext = true
	_, err = client.CancelPendingOrder(ctx, "456", "consumer-unknown-0001")
	var unknown *exness.UnknownOutcomeError
	var httpError *exness.APIError
	if !errors.As(err, &unknown) || !errors.As(err, &httpError) || unknown.IdempotencyKey != "consumer-unknown-0001" || httpError.StatusCode != 503 {
		t.Fatalf("unknown outcome: %v", err)
	}
	expected := []string{"POST positions", "POST orders", "POST orders", "POST orders", "PATCH 456", "DELETE 456", "DELETE 789", "DELETE 456"}
	if !reflect.DeepEqual(api.writes, expected) || api.reads != 2 || api.closed != 11 {
		t.Fatalf("unexpected requests or leaked bodies: writes=%v reads=%d closed=%d", api.writes, api.reads, api.closed)
	}
}

func canceled[T any](t *testing.T, stream iter.Seq2[T, error]) {
	t.Helper()
	count := 0
	for _, err := range stream {
		var transport *exness.TransportError
		if !errors.As(err, &transport) || !errors.Is(err, context.Canceled) {
			t.Fatalf("stream cancellation: %v", err)
		}
		count++
	}
	if count != 1 {
		t.Fatal("expected one terminal cancellation")
	}
}

func TestPublicStreams(t *testing.T) {
	api := &syntheticAPI{t: t}
	client := clientFor(t, api)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	options := exness.StreamOptions{}
	var _ exness.TickStreamOptions = options
	var _ exness.TradingStreamOptions = options
	var _ exness.ServerStreamOptions = options
	var _ exness.TickStreamGap = exness.StreamGap{}
	var _ exness.TradingStreamGap = exness.StreamGap{}
	var _ exness.ServerStreamGap = exness.StreamGap{}
	canceled(t, client.StreamTicks(ctx, []string{"EURUSD"}, options))
	canceled(t, client.StreamTrading(ctx, options))
	canceled(t, client.StreamAccountState(ctx, options))
	canceled(t, client.StreamInstruments(ctx, []string{"all"}, options))
	canceled(t, client.StreamHMR(ctx, []string{"EURUSD"}, options))
	if api.closed != 0 {
		t.Fatal("cancelled streams performed HTTP requests")
	}
	// These public errors and event DTOs must remain constructible outside the SDK.
	var _ error = &exness.ProtocolError{}
	var _ error = &exness.WebSocketAPIError{}
	var _ error = &exness.StreamClosed{}
	_ = []any{exness.HmrSnapshot{}, exness.HmrUpdate{}, exness.AccountStateEvent{}, exness.InstrumentEvent{}, exness.TradingSnapshot{}}
}
