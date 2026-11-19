package exness

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Tick contains market-data doubles, not monetary Decimal values. Optional fields
// remain nil. The stream consumer must check Instrument against its subscription.
type Tick struct {
	Instrument *string    `json:"instrument,omitempty"`
	Bid        *float64   `json:"bid,omitempty"`
	Ask        *float64   `json:"ask,omitempty"`
	Timestamp  *time.Time `json:"timestamp,omitempty"`
}

// WebSocketAPIError reports a subscription rejection. ErrorMessage is untrusted.
type WebSocketAPIError struct {
	RequestID    string
	Code         int
	ErrorMessage string
}

func (e *WebSocketAPIError) Error() string {
	return fmt.Sprintf("Exness WebSocket returned code %d", e.Code)
}

// decodeTick validates the message envelope without treating unknown messages as ticks.
func decodeTick(data []byte, instruments map[string]bool, requestID string) (*Tick, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return nil, &ProtocolError{"Invalid tick stream message"}
	}
	_, hasCode := fields["code"]
	_, hasMessage := fields["error_message"]
	if hasCode || hasMessage {
		var result struct {
			ID      *string `json:"id"`
			Code    *int    `json:"code"`
			Message *string `json:"error_message"`
		}
		if json.Unmarshal(data, &result) != nil || result.ID == nil || result.Code == nil || result.Message == nil {
			return nil, &ProtocolError{"Invalid WebSocket error response"}
		}
		if *result.ID != requestID {
			return nil, &ProtocolError{"WebSocket error returned an unknown request id"}
		}
		return nil, &WebSocketAPIError{*result.ID, *result.Code, *result.Message}
	}
	var tick Tick
	if json.Unmarshal(data, &tick) != nil {
		return nil, &ProtocolError{"Invalid tick stream message"}
	}
	if tick.Instrument == nil || !instruments[*tick.Instrument] {
		return nil, &ProtocolError{"Tick returned a missing or unsubscribed instrument"}
	}
	return &tick, nil
}

func tickSubscription(instruments []string, requestID string) ([]byte, map[string]bool, error) {
	if len(instruments) == 0 {
		return nil, nil, errors.New("provide a nonempty instrument list")
	}
	names := make(map[string]bool, len(instruments))
	for _, name := range instruments {
		if !instrumentPattern.MatchString(name) || names[name] {
			return nil, nil, errors.New("invalid or duplicate instrument")
		}
		names[name] = true
	}
	payload := struct {
		ID        string `json:"id"`
		Subscribe struct {
			Event       string   `json:"event"`
			Instruments []string `json:"instruments"`
		} `json:"subscribe"`
	}{ID: requestID}
	payload.Subscribe.Event = "ticks"
	payload.Subscribe.Instruments = instruments
	data, err := json.Marshal(payload)
	return data, names, err
}
