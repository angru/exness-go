package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"testing"

	exness "github.com/angru/exness-go"
)

func TestSharedRecoveryCheckpoints(t *testing.T) {
	var data suite
	if err := json.Unmarshal(scenarioData, &data); err != nil {
		t.Fatal(err)
	}
	for _, current := range data.Scenarios {
		t.Run(current.Name, func(t *testing.T) {
			encoded, err := json.Marshal(suite{Messages: data.Messages, Scenarios: []scenario{current}})
			if err != nil {
				t.Fatal(err)
			}
			rows, err := replay(encoded)
			if err != nil || len(rows) != len(current.Steps) {
				t.Fatalf("recovery checkpoints: %v", err)
			}
		})
	}
}

func TestRunnableExampleOutput(t *testing.T) {
	var output bytes.Buffer
	if err := run(&output); err != nil {
		t.Fatal(err)
	}
	actual := []row{}
	for _, line := range bytes.Split(bytes.TrimSpace(output.Bytes()), []byte("\n")) {
		var value row
		if err := json.Unmarshal(line, &value); err != nil {
			t.Fatal(err)
		}
		actual = append(actual, value)
	}
	expected, err := replay(scenarioData)
	if err != nil || !reflect.DeepEqual(actual, expected) || len(actual) != 49 {
		t.Fatalf("unexpected CLI output: %v", err)
	}
}

func messages(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	var data suite
	if err := json.Unmarshal(scenarioData, &data); err != nil {
		t.Fatal(err)
	}
	return data.Messages
}

func baseline(t *testing.T) *recoveryView {
	t.Helper()
	v := newView("demo-close")
	if err := applyStep(v, step{Kind: "stream", Message: "baseline"}, messages(t)); err != nil {
		t.Fatal(err)
	}
	return v
}

func assertInvalidated(t *testing.T, v *recoveryView, err error) {
	t.Helper()
	var protocol *exness.ProtocolError
	if !errors.As(err, &protocol) || v.ready || v.account != nil || len(v.positions)+len(v.orders)+len(v.deals) > 0 {
		t.Fatalf("invalid view retained: %v", err)
	}
}

func TestDuplicateWindowBoundedAndReset(t *testing.T) {
	v := baseline(t)
	status := "success"
	var event exness.TransactionEvent
	for i := range limit {
		id := strconv.Itoa(i)
		event = exness.TransactionEvent{EventType: "transaction_event", Status: &status, OperationID: &id}
		if err := v.onStream(exness.TradingStreamEvent{Transaction: &event}); err != nil {
			t.Fatal(err)
		}
	}
	if len(v.seen) != limit {
		t.Fatal("unexpected window size")
	}
	if err := v.onStream(exness.TradingStreamEvent{Transaction: &event}); err != nil {
		t.Fatal(err)
	}
	assertInvalidated(t, v, applyStep(v, step{Kind: "stream", Message: "balance"}, messages(t)))
	if len(v.seen) != 0 {
		t.Fatal("window not cleared")
	}
	for _, name := range []string{"baseline", "balance"} {
		if err := applyStep(v, step{Kind: "stream", Message: name}, messages(t)); err != nil {
			t.Fatal(err)
		}
	}
	if !v.ready || len(v.deals) != 1 {
		t.Fatal("new snapshot did not restore baseline")
	}
}

func TestBadSnapshotsInvalidate(t *testing.T) {
	for _, kind := range []string{"duplicate", "closed", "overflow", "oversized"} {
		t.Run(kind, func(t *testing.T) {
			v := baseline(t)
			var wire struct {
				Payload exness.TradingSnapshot `json:"payload"`
			}
			if err := json.Unmarshal(messages(t)["baseline"], &wire); err != nil {
				t.Fatal(err)
			}
			snapshot := wire.Payload
			switch kind {
			case "duplicate":
				p := snapshot.Positions[0]
				p.PositionID = "0002001"
				snapshot.Positions = append(snapshot.Positions, p)
			case "closed":
				snapshot.Positions[0].State = "closed"
			case "overflow":
				snapshot.Positions[0].PositionID = "18446744073709551616"
			case "oversized":
				p := snapshot.Positions[0]
				snapshot.Positions = nil
				for i := range limit + 1 {
					p.PositionID = strconv.Itoa(i)
					snapshot.Positions = append(snapshot.Positions, p)
				}
			}
			assertInvalidated(t, v, v.onStream(exness.TradingStreamEvent{Snapshot: &snapshot}))
		})
	}
}

func TestEntityLimitAndFailurePayload(t *testing.T) {
	for _, failure := range []bool{false, true} {
		v := baseline(t)
		var event exness.TransactionEvent
		if err := json.Unmarshal(messages(t)["balance"], &event); err != nil {
			t.Fatal(err)
		}
		if failure {
			status := "failed"
			event.Status = &status
		} else {
			deal := event.Payload.Deals[0]
			event.Payload.Deals = nil
			for i := range limit + 1 {
				deal.DealID = strconv.Itoa(i)
				event.Payload.Deals = append(event.Payload.Deals, deal)
			}
		}
		assertInvalidated(t, v, v.onStream(exness.TradingStreamEvent{Transaction: &event}))
	}
}

func TestConflictingAckPreservesIdentity(t *testing.T) {
	v := baseline(t)
	if err := applyStep(v, step{Kind: "ack", Message: "ack_close"}, messages(t)); err != nil {
		t.Fatal(err)
	}
	other := "791"
	err := v.acknowledge(exness.AckResponse{Status: "accepted", OperationID: &other})
	assertInvalidated(t, v, err)
	if v.reference.OperationID != "790" || v.outcome != "accepted" {
		t.Fatal("conflict replaced identity")
	}
}
