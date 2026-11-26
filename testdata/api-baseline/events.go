package exness

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"slices"
	"time"
)

// ServerStreamOptions and ServerStreamGap share the existing WebSocket policy.
type ServerStreamOptions = TickStreamOptions
type ServerStreamGap = TickStreamGap

type AccountStatePayload struct {
	AccountState AccountState `json:"account_state"`
}

type AccountStateEvent struct {
	EventType string              `json:"event_type"`
	EventTime time.Time           `json:"event_time"`
	Payload   AccountStatePayload `json:"payload"`
}

type InstrumentEvent struct {
	EventType string               `json:"event_type"`
	EventTime time.Time            `json:"event_time"`
	Action    string               `json:"action"` // new, upd or del; payload contains full conditions.
	Payload   InstrumentConditions `json:"payload"`
}

// HmrPeriod preserves server schedule strings, including weekly schedules.
// No timezone, recurrence expansion or active-now calculation is inferred.
type HmrPeriod struct {
	PeriodID    string   `json:"period_id"`
	EventType   string   `json:"event_type"` // regular or periodic.
	Instruments []string `json:"instruments"`
	Leverage    string   `json:"leverage"`
	StartTime   string   `json:"start_time"`
	EndTime     string   `json:"end_time"`
}

type HmrSnapshot struct {
	EventType string      `json:"event_type"`
	Periods   []HmrPeriod `json:"periods"`
}

type HmrUpdate struct {
	EventType        string      `json:"event_type"`
	UpsertedPeriods  []HmrPeriod `json:"upserted_periods"`
	RemovedPeriodIDs []string    `json:"removed_period_ids"`
}

// Successful stream events contain exactly one data field or Gap.
type AccountStateStreamEvent struct {
	AccountState *AccountStateEvent
	Gap          *ServerStreamGap
}
type InstrumentStreamEvent struct {
	Instrument *InstrumentEvent
	Gap        *ServerStreamGap
}
type HmrStreamEvent struct {
	Snapshot *HmrSnapshot
	Update   *HmrUpdate
	Gap      *ServerStreamGap
}

// StreamAccountState yields periodic floating state without an initial snapshot.
// A gap invalidates the last observation; a later event is a new observation only.
func (c *Client) StreamAccountState(ctx context.Context, options ServerStreamOptions) iter.Seq2[AccountStateStreamEvent, error] {
	return func(yield func(AccountStateStreamEvent, error) bool) {
		for message, err := range streamMessages(c, ctx, "events", options, eventSubscription("account_state", nil), accountStateDecoder) {
			if !yield(AccountStateStreamEvent{message.Value, message.Gap}, err) {
				return
			}
		}
	}
}

// StreamInstruments accepts explicit names or exactly ["all"]. No initial snapshot
// is promised. Lists are copied; callers reconcile stale caches after gaps.
func (c *Client) StreamInstruments(ctx context.Context, instruments []string, options ServerStreamOptions) iter.Seq2[InstrumentStreamEvent, error] {
	names := slices.Clone(instruments)
	factory := func(id string) func([]byte) (*InstrumentEvent, error) { return instrumentDecoder(names, id) }
	return func(yield func(InstrumentStreamEvent, error) bool) {
		for message, err := range streamMessages(c, ctx, "events", options, eventSubscription("instruments", names), factory) {
			if !yield(InstrumentStreamEvent{message.Value, message.Gap}, err) {
				return
			}
		}
	}
}

// StreamHMR requires explicit instruments (no "all") and a fresh snapshot before
// updates on every connection. Replace cached periods on every snapshot, even empty.
func (c *Client) StreamHMR(ctx context.Context, instruments []string, options ServerStreamOptions) iter.Seq2[HmrStreamEvent, error] {
	names := slices.Clone(instruments)
	return func(yield func(HmrStreamEvent, error) bool) {
		for message, err := range streamMessages(c, ctx, "events", options, eventSubscription("hmr", names), hmrDecoder) {
			event := HmrStreamEvent{Gap: message.Gap}
			if message.Value != nil {
				event = *message.Value
			}
			if !yield(event, err) {
				return
			}
		}
	}
}

func eventSubscription(event string, names []string) func(string) ([]byte, error) {
	return func(id string) ([]byte, error) {
		if event != "account_state" {
			if _, _, err := tickSubscription(names, id); err != nil {
				return nil, err
			}
			if slices.Contains(names, "all") && (event != "instruments" || len(names) != 1) {
				return nil, errors.New("all is allowed only as the sole instruments subscription name")
			}
		}
		subscribe := struct {
			Event       string   `json:"event"`
			Instruments []string `json:"instruments,omitempty"`
		}{event, names}
		return json.Marshal(struct {
			ID        string `json:"id"`
			Subscribe any    `json:"subscribe"`
		}{id, subscribe})
	}
}

func invalidServerEvent() error { return &ProtocolError{"Invalid server event message"} }

// requiredObject checks presence/null separately from Go's zero-value decoding.
func requiredObject(data []byte, required ...string) (map[string]json.RawMessage, bool) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return nil, false
	}
	for _, key := range required {
		value, present := fields[key]
		if !present || string(value) == "null" {
			return nil, false
		}
	}
	return fields, true
}

func serverDecoder[T any](id string, parse func([]byte, string, map[string]json.RawMessage) (*T, error)) func([]byte) (*T, error) {
	return func(data []byte) (*T, error) {
		fields, ok := requiredObject(data)
		if !ok {
			return nil, invalidServerEvent()
		}
		rawKind, hasKind := fields["event_type"]
		if !hasKind {
			var failure struct {
				ID      *string `json:"id"`
				Code    *int    `json:"code"`
				Message *string `json:"error_message"`
			}
			if json.Unmarshal(data, &failure) != nil || failure.ID == nil || failure.Code == nil || failure.Message == nil {
				return nil, invalidServerEvent()
			}
			if *failure.ID != id {
				return nil, &ProtocolError{"WebSocket error returned an unknown request id"}
			}
			return nil, &WebSocketAPIError{*failure.ID, *failure.Code, *failure.Message}
		}
		var kind string
		if json.Unmarshal(rawKind, &kind) != nil || kind == "" {
			return nil, invalidServerEvent()
		}
		return parse(data, kind, fields)
	}
}

func accountStateDecoder(id string) func([]byte) (*AccountStateEvent, error) {
	return serverDecoder(id, func(data []byte, kind string, fields map[string]json.RawMessage) (*AccountStateEvent, error) {
		_, ok := requiredObject(data, "event_time", "payload")
		var event AccountStateEvent
		if kind != "account_state_event" || !ok || json.Unmarshal(data, &event) != nil || !validState(event.Payload.AccountState) {
			return nil, invalidServerEvent()
		}
		return &event, nil
	})
}

func instrumentDecoder(names []string, id string) func([]byte) (*InstrumentEvent, error) {
	return serverDecoder(id, func(data []byte, kind string, fields map[string]json.RawMessage) (*InstrumentEvent, error) {
		_, ok := requiredObject(data, "event_time", "payload", "action")
		var event InstrumentEvent
		if kind != "instrument_event" || !ok || json.Unmarshal(data, &event) != nil || !instrumentPattern.MatchString(event.Payload.Instrument) || !slices.Contains([]string{"new", "upd", "del"}, event.Action) {
			return nil, invalidServerEvent()
		}
		if !(len(names) == 1 && names[0] == "all") && !slices.Contains(names, event.Payload.Instrument) {
			return nil, &ProtocolError{"Instrument event returned an unsubscribed instrument"}
		}
		return &event, nil
	})
}

func validHmrPeriods(raw json.RawMessage, periods []HmrPeriod) bool {
	var values []json.RawMessage
	if json.Unmarshal(raw, &values) != nil || values == nil {
		return false
	}
	for i, period := range periods {
		if _, ok := requiredObject(values[i], "period_id", "event_type", "instruments", "leverage", "start_time", "end_time"); !ok {
			return false
		}
		if !validTradingID(period.PeriodID) || !slices.Contains([]string{"regular", "periodic"}, period.EventType) || len(period.Instruments) == 0 {
			return false
		}
		for _, name := range period.Instruments {
			if !instrumentPattern.MatchString(name) {
				return false
			}
		}
	}
	return true
}

func hmrDecoder(id string) func([]byte) (*HmrStreamEvent, error) {
	haveSnapshot := false
	return serverDecoder(id, func(data []byte, kind string, fields map[string]json.RawMessage) (*HmrStreamEvent, error) {
		switch kind {
		case "hmr_snapshot":
			var snapshot HmrSnapshot
			if json.Unmarshal(data, &snapshot) != nil || !validHmrPeriods(fields["periods"], snapshot.Periods) {
				return nil, invalidServerEvent()
			}
			haveSnapshot = true
			return &HmrStreamEvent{Snapshot: &snapshot}, nil
		case "hmr_update":
			var update HmrUpdate
			if json.Unmarshal(data, &update) != nil || !validHmrPeriods(fields["upserted_periods"], update.UpsertedPeriods) || update.RemovedPeriodIDs == nil {
				return nil, invalidServerEvent()
			}
			for _, id := range update.RemovedPeriodIDs {
				if !validTradingID(id) {
					return nil, invalidServerEvent()
				}
			}
			if !haveSnapshot {
				return nil, &ProtocolError{"HMR update received before HMR snapshot"}
			}
			return &HmrStreamEvent{Update: &update}, nil
		default:
			return nil, invalidServerEvent()
		}
	})
}
