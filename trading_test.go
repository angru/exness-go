package exness

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestTradingSharedFixtures(t *testing.T) {
	data, err := tradingSubscription("synthetic-transactions-1")
	if err != nil {
		t.Fatal(err)
	}
	var got, want any
	_ = json.Unmarshal(data, &got)
	_ = json.Unmarshal(readFixture(t, "trading_subscription"), &want)
	a, _ := json.Marshal(got)
	b, _ := json.Marshal(want)
	if string(a) != string(b) {
		t.Fatal("subscription contract mismatch")
	}
	decode := tradingDecoder("synthetic-transactions-1")
	event, err := decode(readFixture(t, "trading_snapshot"))
	if err != nil || event.Snapshot == nil || event.Snapshot.AccountState.Balance.String() != "12345678901234567890.123456789" {
		t.Fatalf("snapshot: %v", err)
	}
	for _, name := range []string{"transaction_balance", "transaction_rejected", "transaction_partial_close"} {
		event, err := decode(readFixture(t, name))
		if err != nil || event.Transaction == nil {
			t.Fatalf("%s: %v", name, err)
		}
		tr := event.Transaction
		switch name {
		case "transaction_balance":
			if tr.OperationID != nil || tr.Payload.AccountState == nil || tr.Payload.AccountState.Leverage != "100" || tr.Payload.Deals[0].Type != DealBalance {
				t.Fatal("balance transaction corrupted")
			}
		case "transaction_rejected":
			if tr.Status == nil || *tr.Status != "rejected" || tr.ErrorMessage == nil || len(tr.Payload.Orders) != 0 {
				t.Fatal("rejected result corrupted")
			}
		case "transaction_partial_close":
			if tr.Payload.Positions[0].Volume.String() != "0.005" || tr.Payload.Positions[0].State != "open" {
				t.Fatal("partial close corrupted")
			}
		}
	}
}

func TestTradingReconnectBoundary(t *testing.T) {
	for _, missingSnapshot := range []bool{false, true} {
		name := "fresh-snapshot"
		if missingSnapshot {
			name = "missing-snapshot"
		}
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			ids := make(chan string, 2)
			c := streamTestClient(t, "events", func(ctx context.Context, conn *websocket.Conn, id string) {
				count := calls.Add(1)
				ids <- id
				if count == 1 || !missingSnapshot {
					_ = conn.Write(ctx, websocket.MessageText, readFixture(t, "trading_snapshot"))
				}
				if count == 1 {
					_ = conn.CloseNow()
					return
				}
				_ = conn.Write(ctx, websocket.MessageText, readFixture(t, "transaction_rejected"))
				_ = conn.Close(1000, "")
			})
			var order []string
			for event, err := range c.StreamTrading(tickContext(t), TradingStreamOptions{MaxReconnects: 2, ReconnectDelay: time.Millisecond}) {
				if err != nil {
					var protocol *ProtocolError
					if !missingSnapshot || !errors.As(err, &protocol) {
						t.Fatal(err)
					}
					order = append(order, "error")
				} else if event.Snapshot != nil {
					order = append(order, "snapshot")
				} else if event.Gap != nil {
					order = append(order, "gap")
				} else if event.Transaction != nil {
					order = append(order, "transaction")
				} else {
					t.Fatal("empty successful event")
				}
			}
			expected := "snapshot,gap,snapshot,transaction"
			if missingSnapshot {
				expected = "snapshot,gap,error"
			}
			if strings.Join(order, ",") != expected || calls.Load() != 2 {
				t.Fatalf("wrong stream: %v, calls %d", order, calls.Load())
			}
			if <-ids == <-ids {
				t.Fatal("reused subscription id")
			}
		})
	}
}

func TestTradingCleanup(t *testing.T) {
	for _, mode := range []string{"cancel", "close-paused", "break"} {
		t.Run(mode, func(t *testing.T) {
			closed := make(chan struct{})
			c := streamTestClient(t, "events", func(ctx context.Context, conn *websocket.Conn, _ string) {
				_ = conn.Write(ctx, websocket.MessageText, readFixture(t, "trading_snapshot"))
				<-conn.CloseRead(ctx).Done()
				close(closed)
			})
			ctx, cancel := context.WithCancel(tickContext(t))
			defer cancel()
			failures := 0
			for event, err := range c.StreamTrading(ctx, TradingStreamOptions{}) {
				if err != nil {
					if !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
					failures++
					continue
				}
				if event.Snapshot == nil {
					t.Fatal("missing snapshot")
				}
				if mode == "break" {
					break
				}
				if mode == "cancel" {
					cancel()
				} else {
					c.Close()
					awaitSignal(t, closed)
				}
			}
			awaitSignal(t, closed)
			if mode != "break" && failures != 1 {
				t.Fatal("missing cancellation")
			}
		})
	}
}

func TestTradingValidation(t *testing.T) {
	bad := []string{
		`bad JSON`, `[]`, `null`, `{}`,
		`{"event_type":"account_state_event","payload":{}}`,
		`{"event_type":"transaction_event"}`,
		`{"event_type":"transaction_event","payload":null}`,
		`{"event_type":"transaction_event","payload":[]}`,
		`{"event_type":"trading_state_snapshot","payload":{}}`,
		`{"event_type":"transaction_event","status":"unknown","payload":{}}`,
		`{"event_type":"transaction_event","operation_id":"bad","payload":{}}`,
		`{"event_type":"transaction_event","payload":{"orders":[{}]}}`,
		`{"event_type":"transaction_event","payload":{"positions":[{}]}}`,
		`{"event_type":"transaction_event","payload":{"deals":[{}]}}`,
		`{"event_type":"transaction_event","payload":{"orders":null}}`,
		`{"event_type":"transaction_event","payload":{"account_state":{"balance":"1","equity":"1","used_margin":"0"}}}`,
		`{"event_type":"transaction_event","payload":{"account_state":{"balance":1,"equity":"1","used_margin":"0","leverage":"100"}}}`,
		`{"id":"synthetic-transactions-1","code":3000}`,
	}
	for _, body := range bad {
		t.Run(body, func(t *testing.T) {
			decode := tradingDecoder("synthetic-transactions-1")
			if _, err := decode(readFixture(t, "trading_snapshot")); err != nil {
				t.Fatal(err)
			}
			_, err := decode([]byte(body))
			var protocol *ProtocolError
			if !errors.As(err, &protocol) {
				t.Fatalf("expected protocol error: %v", err)
			}
		})
	}
	decode := tradingDecoder("synthetic-transactions-1")
	if _, err := decode(readFixture(t, "transaction_rejected")); err == nil {
		t.Fatal("transaction accepted before baseline")
	}
	for _, id := range []string{"synthetic-transactions-1", "other"} {
		data, _ := json.Marshal(map[string]any{"id": id, "code": 3000, "error_message": "private details"})
		_, err := decode(data)
		var api *WebSocketAPIError
		var protocol *ProtocolError
		if err == nil || strings.Contains(err.Error(), "private details") {
			t.Fatal("missing or unsafe error")
		}
		if id == "other" && !errors.As(err, &protocol) || id != "other" && !errors.As(err, &api) {
			t.Fatal("incorrect correlation")
		}
	}
	for range 2 {
		if _, err := decode(readFixture(t, "trading_snapshot")); err != nil {
			t.Fatal(err)
		}
	}
	event, err := decode([]byte(`{"event_type":"transaction_event","payload":{}}`))
	if err != nil || event.Transaction.Status != nil || event.Transaction.Payload.AccountState != nil {
		t.Fatal("invented optional fields")
	}
}
