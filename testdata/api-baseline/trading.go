package exness

import (
	"context"
	"encoding/json"
	"iter"
	"slices"
	"time"
)

// TradingStreamOptions shares the tick transport's bounded reconnect policy.
type TradingStreamOptions = TickStreamOptions

// TradingStreamGap invalidates cached trading state; wait for a new snapshot.
type TradingStreamGap = TickStreamGap

// TransactionAccountState includes leverage, required by transaction payloads.
type TransactionAccountState struct {
	AccountState
	Leverage string `json:"leverage"`
}

// TransactionPayload holds changed entities, never a complete account snapshot.
type TransactionPayload struct {
	Orders       []Order                  `json:"orders,omitempty"`
	Positions    []Position               `json:"positions,omitempty"`
	Deals        []Deal                   `json:"deals,omitempty"`
	AccountState *TransactionAccountState `json:"account_state,omitempty"`
}

// TransactionEvent includes successful changes and rejected/failed results.
// ErrorMessage is untrusted server data. OperationID may be absent, e.g. deposits.
type TransactionEvent struct {
	EventType       string             `json:"event_type"`
	Payload         TransactionPayload `json:"payload"`
	OperationID     *string            `json:"operation_id,omitempty"`
	ClientRequestID *string            `json:"client_request_id,omitempty"`
	Status          *string            `json:"status,omitempty"`
	ErrorCode       *int               `json:"error_code,omitempty"`
	ErrorMessage    *string            `json:"error_message,omitempty"`
	EventTime       *time.Time         `json:"event_time,omitempty"`
}

// TradingStreamEvent contains exactly one snapshot, transaction or gap on success.
// Replace cached state on Snapshot; invalidate it on Gap and at stream termination.
type TradingStreamEvent struct {
	Snapshot    *TradingSnapshot
	Transaction *TransactionEvent
	Gap         *TradingStreamGap
}

// StreamTrading subscribes to transactions. A snapshot is required before any
// transaction on every connection. It does not merge deltas, replay or deduplicate.
func (c *Client) StreamTrading(ctx context.Context, options TradingStreamOptions) iter.Seq2[TradingStreamEvent, error] {
	return func(yield func(TradingStreamEvent, error) bool) {
		for message, err := range streamMessages(c, ctx, "events", options, tradingSubscription, tradingDecoder) {
			event := TradingStreamEvent{Gap: message.Gap}
			if message.Value != nil {
				event = *message.Value
			}
			if !yield(event, err) {
				return
			}
		}
	}
}

func tradingSubscription(id string) ([]byte, error) {
	return json.Marshal(struct {
		ID        string            `json:"id"`
		Subscribe map[string]string `json:"subscribe"`
	}{id, map[string]string{"event": "transactions"}})
}

func validState(state AccountState) bool {
	return state.Balance.text != "" && state.Equity.text != "" && state.UsedMargin.text != ""
}

func validPosition(position Position) bool {
	return validTradingID(position.PositionID) && !position.CreateTime.IsZero() &&
		slices.Contains([]string{"buy", "sell"}, position.Direction) && slices.Contains([]string{"open", "closed"}, position.State) && instrumentPattern.MatchString(position.Instrument) && position.Volume.text != ""
}

func tradingDecoder(requestID string) func([]byte) (*TradingStreamEvent, error) {
	haveSnapshot := false
	return func(data []byte) (*TradingStreamEvent, error) {
		var envelope struct {
			EventType string          `json:"event_type"`
			Payload   json.RawMessage `json:"payload"`
		}
		invalid := func() (*TradingStreamEvent, error) { return nil, &ProtocolError{"Invalid transactions stream message"} }
		if json.Unmarshal(data, &envelope) != nil {
			return invalid()
		}
		if envelope.EventType == "" {
			var failure struct {
				ID      *string `json:"id"`
				Code    *int    `json:"code"`
				Message *string `json:"error_message"`
			}
			if json.Unmarshal(data, &failure) != nil || failure.ID == nil || failure.Code == nil || failure.Message == nil {
				return invalid()
			}
			if *failure.ID != requestID {
				return nil, &ProtocolError{"WebSocket error returned an unknown request id"}
			}
			return nil, &WebSocketAPIError{*failure.ID, *failure.Code, *failure.Message}
		}
		var payload map[string]json.RawMessage
		if json.Unmarshal(envelope.Payload, &payload) != nil || payload == nil {
			return invalid()
		}
		// Explicit null entity arrays are not part of the contract.
		for _, field := range []string{"orders", "positions", "deals", "account_state"} {
			if value, ok := payload[field]; ok && string(value) == "null" {
				return invalid()
			}
		}
		switch envelope.EventType {
		case "trading_state_snapshot":
			var snapshot TradingSnapshot
			if json.Unmarshal(envelope.Payload, &snapshot) != nil || !validState(snapshot.AccountState) {
				return invalid()
			}
			for _, order := range snapshot.Orders {
				if !validOrder(order) {
					return invalid()
				}
			}
			for _, position := range snapshot.Positions {
				if !validPosition(position) {
					return invalid()
				}
			}
			haveSnapshot = true
			return &TradingStreamEvent{Snapshot: &snapshot}, nil
		case "transaction_event":
			var event TransactionEvent
			if json.Unmarshal(data, &event) != nil {
				return invalid()
			}
			if event.Status != nil && *event.Status != "success" && *event.Status != "rejected" && *event.Status != "failed" {
				return invalid()
			}
			if event.OperationID != nil && !validTradingID(*event.OperationID) {
				return invalid()
			}
			for _, order := range event.Payload.Orders {
				if !validOrder(order) {
					return invalid()
				}
			}
			for _, position := range event.Payload.Positions {
				if !validPosition(position) {
					return invalid()
				}
			}
			for _, deal := range event.Payload.Deals {
				if !validDeal(deal) {
					return invalid()
				}
			}
			if state := event.Payload.AccountState; state != nil {
				var fields map[string]json.RawMessage
				_ = json.Unmarshal(payload["account_state"], &fields)
				leverage, present := fields["leverage"]
				if !validState(state.AccountState) || !present || string(leverage) == "null" {
					return invalid()
				}
			}
			if !haveSnapshot {
				return nil, &ProtocolError{"Transaction received before trading snapshot"}
			}
			return &TradingStreamEvent{Transaction: &event}, nil
		default:
			return nil, &ProtocolError{"Unexpected transactions stream event"}
		}
	}
}
