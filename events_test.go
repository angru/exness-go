package exness

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Keep typed public adapters exercised while sharing integration scenarios.
func testServerStream(c *Client, ctx context.Context, kind string, names []string, options ServerStreamOptions) iter.Seq2[any, error] {
	return func(yield func(any, error) bool) {
		switch kind {
		case "account_state":
			for event, err := range c.StreamAccountState(ctx, options) {
				var value any = event.AccountState
				if event.Gap != nil {
					value = event.Gap
				}
				if !yield(value, err) {
					return
				}
			}
		case "instruments", "all":
			// StreamInstruments must copy its input before the iterator starts.
			stream := c.StreamInstruments(ctx, names, options)
			names[0] = "XAUUSD"
			for event, err := range stream {
				var value any = event.Instrument
				if event.Gap != nil {
					value = event.Gap
				}
				if !yield(value, err) {
					return
				}
			}
		case "hmr":
			stream := c.StreamHMR(ctx, names, options)
			names[0] = "XAUUSD"
			for event, err := range stream {
				var value any = event.Snapshot
				if event.Update != nil {
					value = event.Update
				}
				if event.Gap != nil {
					value = event.Gap
				}
				if !yield(value, err) {
					return
				}
			}
		}
	}
}

func serverFixture(kind string) string {
	return map[string]string{"account_state": "account_state_event", "instruments": "instrument_new", "all": "instrument_new", "hmr": "hmr_snapshot"}[kind]
}

func TestServerSubscriptionsRecoveryAndFixtures(t *testing.T) {
	for _, kind := range []string{"account_state", "instruments", "all", "hmr"} {
		t.Run(kind, func(t *testing.T) {
			event, names := kind, []string{"EURUSD"}
			if kind == "all" {
				event, names = "instruments", []string{"all"}
			}
			if kind == "account_state" {
				names = nil
			}
			fixture := kind + "_subscription"
			if kind == "all" {
				fixture = "instruments_all_subscription"
			}
			data, err := eventSubscription(event, names)("synthetic-events-1")
			if err != nil {
				t.Fatal(err)
			}
			var got, want any
			_ = json.Unmarshal(data, &got)
			_ = json.Unmarshal(readFixture(t, fixture), &want)
			a, _ := json.Marshal(got)
			b, _ := json.Marshal(want)
			if string(a) != string(b) {
				t.Fatal("subscription fixture mismatch")
			}
			var calls atomic.Int32
			ids := make(chan string, 2)
			expectedNames := append([]string(nil), names...)
			c := subscriptionTestClient(t, "events", event, expectedNames, func(ctx context.Context, conn *websocket.Conn, id string) {
				count := calls.Add(1)
				ids <- id
				_ = conn.Write(ctx, websocket.MessageText, readFixture(t, serverFixture(kind)))
				if count == 1 {
					_ = conn.CloseNow()
					return
				}
				if kind == "hmr" {
					_ = conn.Write(ctx, websocket.MessageText, readFixture(t, "hmr_update"))
					_ = conn.Write(ctx, websocket.MessageText, readFixture(t, "hmr_empty_snapshot"))
				} else if event == "instruments" {
					_ = conn.Write(ctx, websocket.MessageText, readFixture(t, "instrument_upd"))
					_ = conn.Write(ctx, websocket.MessageText, readFixture(t, "instrument_del"))
				}
				_ = conn.Close(1000, "")
			})
			var rows []any
			for value, err := range testServerStream(c, tickContext(t), kind, names, ServerStreamOptions{MaxReconnects: 1, ReconnectDelay: time.Millisecond}) {
				if err != nil {
					t.Fatal(err)
				}
				rows = append(rows, value)
			}
			if calls.Load() != 2 || <-ids == <-ids {
				t.Fatal("reconnect subscription not fresh")
			}
			gap, ok := rows[1].(*ServerStreamGap)
			if !ok || gap.ReconnectAttempt != 1 {
				t.Fatal("missing gap")
			}
			switch kind {
			case "account_state":
				event := rows[2].(*AccountStateEvent)
				if len(rows) != 3 || event.Payload.AccountState.Balance.String() != "12345678901234567890.123456789" {
					t.Fatal("account precision lost")
				}
			case "hmr":
				snapshot := rows[2].(*HmrSnapshot)
				if snapshot.Periods[0].StartTime != "2026-11-19 11:00:00" || snapshot.Periods[1].StartTime != "Mon, 13:00:00" || len(snapshot.Periods[0].Instruments) != 2 {
					t.Fatal("schedule or period instruments changed")
				}
				update := rows[3].(*HmrUpdate)
				if len(update.RemovedPeriodIDs) != 2 || update.RemovedPeriodIDs[1] != "999" || len(rows[4].(*HmrSnapshot).Periods) != 0 {
					t.Fatal("invalid update or empty baseline")
				}
			default:
				if rows[0].(*InstrumentEvent).Action != "new" || rows[3].(*InstrumentEvent).Action != "upd" || rows[4].(*InstrumentEvent).Action != "del" {
					t.Fatal("lost lifecycle action")
				}
			}
		})
	}
}

func TestHMRReconnectRequiresSnapshot(t *testing.T) {
	var calls atomic.Int32
	c := subscriptionTestClient(t, "events", "hmr", []string{"EURUSD"}, func(ctx context.Context, conn *websocket.Conn, _ string) {
		if calls.Add(1) == 1 {
			_ = conn.Write(ctx, websocket.MessageText, readFixture(t, "hmr_snapshot"))
			_ = conn.CloseNow()
		} else {
			_ = conn.Write(ctx, websocket.MessageText, readFixture(t, "hmr_update"))
			<-conn.CloseRead(ctx).Done()
		}
	})
	snapshots, gaps, failures := 0, 0, 0
	for event, err := range c.StreamHMR(tickContext(t), []string{"EURUSD"}, ServerStreamOptions{MaxReconnects: 3, ReconnectDelay: time.Millisecond}) {
		if err != nil {
			var protocol *ProtocolError
			if !errors.As(err, &protocol) || !strings.Contains(err.Error(), "before HMR snapshot") {
				t.Fatal(err)
			}
			failures++
		} else if event.Gap != nil {
			gaps++
		} else if event.Snapshot != nil {
			snapshots++
		}
	}
	if snapshots != 1 || gaps != 1 || failures != 1 || calls.Load() != 2 {
		t.Fatal("snapshot gate not reset")
	}
}

func TestServerStreamCleanup(t *testing.T) {
	for _, kind := range []string{"account_state", "instruments", "hmr"} {
		for _, mode := range []string{"cancel", "close-paused", "break"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				names := []string{"EURUSD"}
				if kind == "account_state" {
					names = nil
				}
				closed := make(chan struct{})
				c := subscriptionTestClient(t, "events", kind, names, func(ctx context.Context, conn *websocket.Conn, _ string) {
					_ = conn.Write(ctx, websocket.MessageText, readFixture(t, serverFixture(kind)))
					<-conn.CloseRead(ctx).Done()
					close(closed)
				})
				ctx, cancel := context.WithCancel(tickContext(t))
				defer cancel()
				failures := 0
				for _, err := range testServerStream(c, ctx, kind, append([]string(nil), names...), ServerStreamOptions{}) {
					if err != nil {
						if !errors.Is(err, context.Canceled) {
							t.Fatal(err)
						}
						failures++
						continue
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
}

func TestServerInvalidSubscriptionBeforeNetwork(t *testing.T) {
	c := makeClient(t, func(*http.Request) (*http.Response, error) {
		t.Error("invalid subscription made request")
		return nil, errors.New("unexpected request")
	})
	for _, names := range [][]string{nil, {""}, {"EURUSD", "EURUSD"}, {"all", "EURUSD"}, {"bad/name"}} {
		for _, kind := range []string{"instruments", "hmr"} {
			failures := 0
			// Avoid the mutation in testServerStream for empty invalid inputs.
			if kind == "instruments" {
				for _, err := range c.StreamInstruments(tickContext(t), names, ServerStreamOptions{}) {
					if err == nil {
						t.Fatal("invalid input accepted")
					}
					failures++
				}
			} else {
				for _, err := range c.StreamHMR(tickContext(t), names, ServerStreamOptions{}) {
					if err == nil {
						t.Fatal("invalid input accepted")
					}
					failures++
				}
			}
			if failures != 1 {
				t.Fatal("expected one input failure")
			}
		}
	}
	for _, err := range c.StreamHMR(tickContext(t), []string{"all"}, ServerStreamOptions{}) {
		if err == nil {
			t.Fatal("HMR wildcard accepted")
		}
	}
}

func TestServerEventValidation(t *testing.T) {
	type decodeFunc func([]byte) (any, error)
	factory := func(kind string) decodeFunc {
		switch kind {
		case "account_state":
			decode := accountStateDecoder("synthetic-events-1")
			return func(data []byte) (any, error) { return decode(data) }
		case "instruments":
			decode := instrumentDecoder([]string{"EURUSD"}, "synthetic-events-1")
			return func(data []byte) (any, error) { return decode(data) }
		default:
			decode := hmrDecoder("synthetic-events-1")
			return func(data []byte) (any, error) { return decode(data) }
		}
	}
	protocolFailure := func(decode decodeFunc, data []byte) {
		t.Helper()
		_, err := decode(data)
		var protocol *ProtocolError
		if !errors.As(err, &protocol) {
			t.Fatalf("expected protocol error, got %v", err)
		}
	}
	for _, kind := range []string{"account_state", "instruments", "hmr"} {
		decode := factory(kind)
		for _, data := range []string{`bad JSON`, `[]`, `null`, `{}`, `{"event_type":null}`, `{"event_type":123}`, `{"event_type":"unexpected"}`, `{"id":"synthetic-events-1","code":3000}`} {
			protocolFailure(decode, []byte(data))
		}
		for _, id := range []string{"synthetic-events-1", "other"} {
			data, _ := json.Marshal(map[string]any{"id": id, "code": 3000, "error_message": "private details"})
			_, err := decode(data)
			if err == nil || strings.Contains(err.Error(), "private details") {
				t.Fatal("missing or unsafe subscription error")
			}
			var api *WebSocketAPIError
			var protocol *ProtocolError
			if id == "other" && !errors.As(err, &protocol) || id != "other" && !errors.As(err, &api) {
				t.Fatal("incorrect error correlation")
			}
		}
	}
	for _, row := range []struct {
		kind, fixture string
		fields        []string
	}{
		{"account_state", "account_state_event", []string{"event_time", "payload"}},
		{"instruments", "instrument_new", []string{"event_time", "payload", "action"}},
		{"hmr", "hmr_snapshot", []string{"periods"}},
		{"hmr", "hmr_update", []string{"upserted_periods", "removed_period_ids"}},
	} {
		for _, field := range row.fields {
			for _, omit := range []bool{true, false} {
				var value map[string]any
				_ = json.Unmarshal(readFixture(t, row.fixture), &value)
				if omit {
					delete(value, field)
				} else {
					value[field] = nil
				}
				data, _ := json.Marshal(value)
				protocolFailure(factory(row.kind), data)
			}
		}
	}
	for _, field := range []string{"period_id", "event_type", "instruments", "leverage", "start_time", "end_time"} {
		for _, omit := range []bool{true, false} {
			var value map[string]any
			_ = json.Unmarshal(readFixture(t, "hmr_snapshot"), &value)
			period := value["periods"].([]any)[0].(map[string]any)
			if omit {
				delete(period, field)
			} else {
				period[field] = nil
			}
			data, _ := json.Marshal(value)
			protocolFailure(factory("hmr"), data)
		}
	}
	for _, row := range []struct {
		field string
		value any
	}{
		{"period_id", "bad"}, {"event_type", "unknown"}, {"instruments", []string{}},
		{"instruments", []string{"bad/name"}}, {"leverage", 10},
	} {
		var value map[string]any
		_ = json.Unmarshal(readFixture(t, "hmr_snapshot"), &value)
		value["periods"].([]any)[0].(map[string]any)[row.field] = row.value
		data, _ := json.Marshal(value)
		protocolFailure(factory("hmr"), data)
	}
	hmr := factory("hmr")
	protocolFailure(hmr, readFixture(t, "hmr_update"))
	if _, err := hmr(readFixture(t, "hmr_empty_snapshot")); err != nil {
		t.Fatal(err)
	}
	if _, err := hmr(readFixture(t, "hmr_update")); err != nil {
		t.Fatal(err)
	}
	protocolFailure(hmr, []byte(`{"event_type":"hmr_update","upserted_periods":[],"removed_period_ids":["bad"]}`))
	if _, err := instrumentDecoder([]string{"all"}, "id")(readFixture(t, "instrument_new")); err != nil {
		t.Fatal(err)
	}
	if _, err := instrumentDecoder([]string{"XAUUSD"}, "id")(readFixture(t, "instrument_new")); err == nil {
		t.Fatal("unsubscribed instrument accepted")
	}
}

func TestServerProtocolFailureIsTerminal(t *testing.T) {
	for _, kind := range []string{"account_state", "instruments", "hmr"} {
		t.Run(kind, func(t *testing.T) {
			names := []string{"EURUSD"}
			if kind == "account_state" {
				names = nil
			}
			var calls atomic.Int32
			c := subscriptionTestClient(t, "events", kind, names, func(ctx context.Context, conn *websocket.Conn, _ string) {
				calls.Add(1)
				_ = conn.Write(ctx, websocket.MessageText, []byte(`{"event_type":"unexpected"}`))
				<-conn.CloseRead(ctx).Done()
			})
			failures := 0
			for _, err := range testServerStream(c, tickContext(t), kind, append([]string(nil), names...), ServerStreamOptions{MaxReconnects: 2}) {
				var protocol *ProtocolError
				if !errors.As(err, &protocol) {
					t.Fatal(err)
				}
				failures++
			}
			if calls.Load() != 1 || failures != 1 {
				t.Fatal("protocol error retried or lost")
			}
		})
	}
}
