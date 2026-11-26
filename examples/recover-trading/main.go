// Replay synthetic recovery scenarios without credentials, sockets or trading writes.
// This single-account application example is not a durable state store.
package main

import (
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"slices"
	"strconv"

	exness "github.com/angru/exness-go"
)

//go:embed scenarios.json
var scenarioData []byte

const limit = 256 // Bound the teaching example; exhaustion requires a fresh snapshot.

type recoveryView struct {
	reference    exness.OperationReference
	ready        bool
	account      *exness.AccountState
	orders       map[string]exness.Order
	positions    map[string]exness.Position
	deals        map[string]exness.Deal
	seen         map[[32]byte]bool
	outcome      string
	errorCode    *int
	errorMessage *string
	waitExpired  bool
}

func newView(key string) *recoveryView {
	v := &recoveryView{reference: exness.OperationReference{ClientRequestID: key}, outcome: "unknown"}
	v.invalidate()
	return v
}

func protocol(message string) error { return &exness.ProtocolError{Message: message} }

func entityID(value string) (string, error) {
	number, err := strconv.ParseUint(value, 10, 64)
	if err != nil || len(value) > 20 {
		return "", protocol("Invalid entity ID")
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return "", protocol("Invalid entity ID")
		}
	}
	return strconv.FormatUint(number, 10), nil
}

// Invalidate on gaps and every stream exit, including cancellation. Preserve the
// operation evidence separately from the account baseline.
func (v *recoveryView) invalidate() {
	v.ready = false
	v.account = nil
	v.orders = map[string]exness.Order{}
	v.positions = map[string]exness.Position{}
	v.deals = map[string]exness.Deal{}
	v.seen = map[[32]byte]bool{}
}

func terminal(outcome string) bool {
	return outcome == "confirmed" || outcome == "rejected" || outcome == "failed"
}

func (v *recoveryView) record(outcome string, code *int, message *string) error {
	if terminal(v.outcome) {
		if terminal(outcome) && outcome != v.outcome {
			v.invalidate()
			return protocol("Conflicting terminal observations; reconcile manually")
		}
		if outcome != v.outcome {
			return nil
		} // Late ACK/pending cannot undo terminal evidence.
	}
	v.outcome = outcome
	if code != nil {
		value := *code
		v.errorCode = &value
	}
	if message != nil {
		value := *message
		v.errorMessage = &value
	}
	return nil
}

func (v *recoveryView) acknowledge(ack exness.AckResponse) error {
	candidate, err := exness.OperationReferenceFromAck(ack, v.reference.ClientRequestID)
	if err == nil && candidate.OperationID != "" && v.reference.OperationID != "" {
		left, _ := entityID(candidate.OperationID)
		right, _ := entityID(v.reference.OperationID)
		if left != right {
			err = protocol("Conflicting acknowledgement operation ID")
		}
	}
	if err != nil {
		v.invalidate()
		return err
	}
	if candidate.OperationID == "" {
		candidate.OperationID = v.reference.OperationID
	}
	v.reference = candidate
	return v.record("accepted", nil, nil)
}

func (v *recoveryView) status(status exness.OperationStatus) error {
	matched, err := v.reference.MatchesStatus(status)
	if err == nil && !matched {
		err = protocol("Status does not match the tracked operation")
	}
	if err != nil {
		v.invalidate()
		return err
	}
	var code *int
	if status.ErrorCode != nil {
		value := int(*status.ErrorCode)
		code = &value
	}
	if err = v.record(string(status.Status), code, status.ErrorMessage); err != nil {
		return err
	}
	v.reference.OperationID = status.OperationID
	return nil // Terminal status does not repair the account baseline or fill history.
}

func (v *recoveryView) onStream(event exness.TradingStreamEvent) error {
	if err := v.applyStream(event); err != nil {
		v.invalidate()
		return err
	}
	return nil
}

func (v *recoveryView) applyStream(event exness.TradingStreamEvent) error {
	if event.Gap != nil {
		v.invalidate()
		return nil
	}
	if snapshot := event.Snapshot; snapshot != nil {
		orders := map[string]exness.Order{}
		positions := map[string]exness.Position{}
		for _, order := range snapshot.Orders {
			key, err := entityID(order.OrderID)
			if err != nil {
				return err
			}
			if _, exists := orders[key]; exists || (order.State != "pending_new" && order.State != "placed") {
				return protocol("Invalid active order in snapshot")
			}
			orders[key] = order
		}
		for _, position := range snapshot.Positions {
			key, err := entityID(position.PositionID)
			if err != nil {
				return err
			}
			if _, exists := positions[key]; exists || position.State != "open" {
				return protocol("Invalid open position in snapshot")
			}
			positions[key] = position
		}
		if max(len(orders), len(positions)) > limit {
			return protocol("Example entity limit exceeded")
		}
		v.invalidate()
		v.orders = orders
		v.positions = positions
		account := snapshot.AccountState
		v.account = &account
		v.ready = true
		return nil
	}
	if !v.ready {
		return protocol("A fresh stream snapshot is required before transactions")
	}
	transaction := event.Transaction
	if transaction == nil {
		return protocol("Missing transaction")
	}
	// Exact decoded duplicates within this snapshot epoch only. No event sequence
	// or replay token exists; this cannot repair arbitrary unseen reordering.
	encoded, err := json.Marshal(transaction)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(encoded)
	if v.seen[digest] {
		return nil
	}
	if len(v.seen) >= limit {
		return protocol("Duplicate window exhausted; obtain a fresh snapshot")
	}
	matched, err := v.reference.MatchesEvent(*transaction)
	if err != nil {
		return err
	}
	if matched {
		if transaction.Status != nil {
			outcome := *transaction.Status
			if outcome == "success" {
				outcome = "confirmed"
			}
			if err := v.record(outcome, transaction.ErrorCode, transaction.ErrorMessage); err != nil {
				return err
			}
		}
		if transaction.OperationID != nil {
			v.reference.OperationID = *transaction.OperationID
		}
	}
	if transaction.Status == nil {
		v.invalidate()
		return nil
	} // Conservative example policy.
	payload := transaction.Payload
	if *transaction.Status == "rejected" || *transaction.Status == "failed" {
		if len(payload.Orders)+len(payload.Positions)+len(payload.Deals) > 0 || payload.AccountState != nil {
			return protocol("Unexpected failure payload; reconcile account state")
		}
		v.seen[digest] = true
		return nil
	}
	if *transaction.Status != "success" {
		return protocol("Unknown transaction status")
	}
	orders := cloneMap(v.orders)
	positions := cloneMap(v.positions)
	deals := cloneMap(v.deals)
	for _, order := range payload.Orders {
		key, err := entityID(order.OrderID)
		if err != nil {
			return err
		}
		switch order.State {
		case "pending_new", "placed":
			orders[key] = order
		case "filled", "cancelled", "rejected":
			delete(orders, key)
		default:
			return protocol("Unknown order state")
		}
	}
	for _, position := range payload.Positions {
		key, err := entityID(position.PositionID)
		if err != nil {
			return err
		}
		switch position.State {
		case "open":
			positions[key] = position // Replace remaining volume; never subtract twice.
		case "closed":
			delete(positions, key)
		default:
			return protocol("Unknown position state")
		}
	}
	for _, deal := range payload.Deals {
		key, err := entityID(deal.DealID)
		if err != nil {
			return err
		}
		deals[key] = deal // Observed entities, not an additive ledger.
	}
	if max(len(orders), len(positions), len(deals)) > limit {
		return protocol("Example entity limit exceeded; reconcile account state")
	}
	v.orders = orders
	v.positions = positions
	v.deals = deals
	if payload.AccountState != nil {
		account := payload.AccountState.AccountState
		v.account = &account
	}
	v.seen[digest] = true
	return nil
}

func cloneMap[T any](source map[string]T) map[string]T {
	copy := make(map[string]T, len(source))
	for key, value := range source {
		copy[key] = value
	}
	return copy
}

type operationSummary struct {
	OperationID  *string `json:"operation_id"`
	Outcome      string  `json:"outcome"`
	ErrorCode    *int    `json:"error_code"`
	ErrorMessage *string `json:"error_message"`
	WaitExpired  bool    `json:"wait_expired"`
}

type summary struct {
	Ready           bool              `json:"ready"`
	Account         map[string]string `json:"account"`
	OrderIDs        []string          `json:"order_ids"`
	PositionVolumes map[string]string `json:"position_volumes"`
	ObservedDealIDs []string          `json:"observed_deal_ids"`
	Operation       operationSummary  `json:"operation"`
}

func (v *recoveryView) summary() summary {
	s := summary{Ready: v.ready, OrderIDs: []string{}, PositionVolumes: map[string]string{}, ObservedDealIDs: []string{}, Operation: operationSummary{Outcome: v.outcome, ErrorCode: v.errorCode, ErrorMessage: v.errorMessage, WaitExpired: v.waitExpired}}
	if v.reference.OperationID != "" {
		id := v.reference.OperationID
		s.Operation.OperationID = &id
	}
	if v.account != nil {
		s.Account = map[string]string{"balance": v.account.Balance.String(), "equity": v.account.Equity.String(), "used_margin": v.account.UsedMargin.String()}
	}
	for key := range v.orders {
		s.OrderIDs = append(s.OrderIDs, key)
	}
	slices.Sort(s.OrderIDs)
	for key, value := range v.positions {
		s.PositionVolumes[key] = value.Volume.String()
	}
	for key := range v.deals {
		s.ObservedDealIDs = append(s.ObservedDealIDs, key)
	}
	slices.Sort(s.ObservedDealIDs)
	return s
}

type step struct {
	Label         string  `json:"label"`
	Kind          string  `json:"kind"`
	Message       string  `json:"message"`
	Expected      summary `json:"expected"`
	ExpectedError bool    `json:"expected_error"`
}
type scenario struct {
	Name  string `json:"name"`
	Key   string `json:"key"`
	Steps []step `json:"steps"`
}
type suite struct {
	Messages  map[string]json.RawMessage `json:"messages"`
	Scenarios []scenario                 `json:"scenarios"`
}
type row struct {
	Scenario string  `json:"scenario"`
	Step     string  `json:"step"`
	Rejected bool    `json:"rejected"`
	View     summary `json:"view"`
}

func applyStep(v *recoveryView, s step, messages map[string]json.RawMessage) error {
	data := messages[s.Message]
	switch s.Kind {
	case "gap":
		return v.onStream(exness.TradingStreamEvent{Gap: &exness.TradingStreamGap{}})
	case "end":
		v.invalidate()
		return nil
	case "timeout":
		v.waitExpired = true
		return nil // Simulated wait expiry preserves evidence.
	case "ack":
		var ack exness.AckResponse
		if err := json.Unmarshal(data, &ack); err != nil {
			return err
		}
		return v.acknowledge(ack)
	case "status":
		var status exness.OperationStatus
		if err := json.Unmarshal(data, &status); err != nil {
			return err
		}
		return v.status(status)
	case "stream":
		var envelope struct {
			EventType string          `json:"event_type"`
			Payload   json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(data, &envelope); err != nil {
			return err
		}
		switch envelope.EventType {
		case "trading_state_snapshot":
			var snapshot exness.TradingSnapshot
			if err := json.Unmarshal(envelope.Payload, &snapshot); err != nil {
				return err
			}
			return v.onStream(exness.TradingStreamEvent{Snapshot: &snapshot})
		case "transaction_event":
			var event exness.TransactionEvent
			if err := json.Unmarshal(data, &event); err != nil {
				return err
			}
			return v.onStream(exness.TradingStreamEvent{Transaction: &event})
		default:
			return errors.New("unknown scenario stream message")
		}
	default:
		return errors.New("unknown scenario action")
	}
}

func replay(data []byte) ([]row, error) {
	var scenarios suite
	if err := json.Unmarshal(data, &scenarios); err != nil {
		return nil, err
	}
	rows := []row{}
	for _, scenario := range scenarios.Scenarios {
		view := newView(scenario.Key)
		for _, step := range scenario.Steps {
			err := applyStep(view, step, scenarios.Messages)
			var protocol *exness.ProtocolError
			rejected := errors.As(err, &protocol)
			if err != nil && !rejected {
				return nil, err
			}
			actual := view.summary()
			if rejected != step.ExpectedError || !reflect.DeepEqual(actual, step.Expected) {
				return nil, fmt.Errorf("unexpected recovery result: %s/%s", scenario.Name, step.Label)
			}
			rows = append(rows, row{scenario.Name, step.Label, rejected, actual})
		}
	}
	return rows, nil
}

func run(writer io.Writer) error {
	rows, err := replay(scenarioData)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(writer)
	for _, row := range rows {
		if err := encoder.Encode(row); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	if err := run(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
