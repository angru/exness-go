package exness

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestSharedTickFixtures(t *testing.T) {
	tick, err := decodeTick(readFixture(t, "tick"), map[string]bool{"EURUSD": true}, "synthetic-ticks-1")
	if err != nil || tick.Bid == nil || *tick.Bid != 1.08321 || tick.Timestamp == nil {
		t.Fatalf("tick fixture: %v", err)
	}
	data, names, err := tickSubscription([]string{"EURUSD"}, "synthetic-ticks-1")
	if err != nil || !names["EURUSD"] {
		t.Fatal(err)
	}
	var actual, expected any
	if json.Unmarshal(data, &actual) != nil || json.Unmarshal(readFixture(t, "tick_subscription"), &expected) != nil || !reflect.DeepEqual(actual, expected) {
		t.Fatal("subscription differs from shared fixture")
	}
	_, err = decodeTick(readFixture(t, "ws_error"), names, "synthetic-ticks-1")
	var api *WebSocketAPIError
	if !errors.As(err, &api) || api.Code != 3000 || strings.Contains(err.Error(), "REQUEST_INVALID") {
		t.Fatalf("error fixture: %v", err)
	}
}

func TestTickValidation(t *testing.T) {
	for _, data := range []string{`[]`, `null`, `{}`, `{"instrument":"XAUUSD"}`, `{"instrument":"EURUSD","bid":"1.2"}`, `{"instrument":"EURUSD","bid":1e999}`, `{"id":"a","code":3000}`, `{"id":"other","code":3000,"error_message":"private"}`, `bad JSON`} {
		if _, err := decodeTick([]byte(data), map[string]bool{"EURUSD": true}, "a"); err == nil {
			t.Fatalf("accepted invalid message %s", data)
		}
	}
	tick, err := decodeTick([]byte(`{"instrument":"EURUSD","bid":1.2}`), map[string]bool{"EURUSD": true}, "a")
	if err != nil || tick.Ask != nil || tick.Timestamp != nil {
		t.Fatal("missing values were invented")
	}
	for _, names := range [][]string{nil, {""}, {"EURUSD", "EURUSD"}, {"invalid/name"}} {
		if _, _, err := tickSubscription(names, "a"); err == nil {
			t.Fatal("invalid subscription accepted")
		}
	}
}
