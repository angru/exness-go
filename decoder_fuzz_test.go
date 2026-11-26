package exness

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
)

const decoderInputLimit = 8192

func decoderFixtures(t testing.TB) map[string][]byte {
	t.Helper()
	fixtures := map[string][]byte{}
	for _, name := range []string{"ack_accepted", "operation_pending", "snapshot", "trading_snapshot", "transaction_rejected", "hmr_empty_snapshot", "hmr_snapshot", "hmr_update", "tick", "account_state_event", "instrument_new"} {
		fixtures[name] = readFixture(t, name)
	}
	return fixtures
}

func malformedDecoderSeeds(t testing.TB) []string {
	t.Helper()
	var seeds []string
	if err := json.Unmarshal(readFixture(t, "decoder-seeds"), &seeds); err != nil {
		t.Fatal(err)
	}
	return seeds
}

// The boolean reports a decoded delta, allowing an independent baseline oracle.
func websocketDecoder(t testing.TB, mode byte, baseline bool, fixtures map[string][]byte) func([]byte) (bool, error) {
	t.Helper()
	switch mode % 5 {
	case 0:
		decode := tradingDecoder("test")
		if baseline {
			if _, err := decode(fixtures["trading_snapshot"]); err != nil {
				t.Fatal(err)
			}
		}
		return func(data []byte) (bool, error) {
			event, err := decode(data)
			return event != nil && event.Transaction != nil, err
		}
	case 1:
		decode := hmrDecoder("test")
		if baseline {
			if _, err := decode(fixtures["hmr_empty_snapshot"]); err != nil {
				t.Fatal(err)
			}
		}
		return func(data []byte) (bool, error) {
			event, err := decode(data)
			return event != nil && event.Update != nil, err
		}
	case 2:
		decode := accountStateDecoder("test")
		return func(data []byte) (bool, error) { _, err := decode(data); return false, err }
	case 3:
		decode := instrumentDecoder([]string{"EURUSD"}, "test")
		return func(data []byte) (bool, error) { _, err := decode(data); return false, err }
	default:
		return func(data []byte) (bool, error) {
			_, err := decodeTick(data, map[string]bool{"EURUSD": true}, "test")
			return false, err
		}
	}
}

func assertDecoderError(t testing.TB, err error) {
	t.Helper()
	var protocol *ProtocolError
	var api *WebSocketAPIError
	if !errors.As(err, &protocol) && !errors.As(err, &api) {
		t.Fatalf("unexpected decoder error: %v", err)
	}
}

func TestMalformedDecoderSeeds(t *testing.T) {
	fixtures := decoderFixtures(t)
	for mode := byte(0); mode < 5; mode++ {
		for _, seed := range malformedDecoderSeeds(t) {
			_, err := websocketDecoder(t, mode, false, fixtures)([]byte(seed))
			assertDecoderError(t, err)
		}
	}
}

func FuzzWebSocketMessages(f *testing.F) {
	fixtures := decoderFixtures(f)
	for mode := byte(0); mode < 5; mode++ {
		for _, raw := range malformedDecoderSeeds(f) {
			f.Add(mode, false, []byte(raw))
		}
		for _, name := range []string{"trading_snapshot", "transaction_rejected", "hmr_empty_snapshot", "hmr_update", "tick", "account_state_event", "instrument_new"} {
			f.Add(mode, true, fixtures[name])
		}
	}
	f.Fuzz(func(t *testing.T, mode byte, baseline bool, data []byte) {
		if len(data) > decoderInputLimit {
			t.Skip()
		}
		delta, err := websocketDecoder(t, mode, baseline, fixtures)(data)
		if err != nil {
			assertDecoderError(t, err)
			return
		}
		if delta && !baseline {
			t.Fatal("delta bypassed fresh snapshot prerequisite")
		}
	})
}

type observedBody struct {
	io.ReadCloser
	closed bool
}

func (b *observedBody) Close() error { b.closed = true; return b.ReadCloser.Close() }

func FuzzRESTResponses(f *testing.F) {
	fixtures := decoderFixtures(f)
	credentials := testCredentials(f)
	for mode := byte(0); mode < 3; mode++ {
		for _, seed := range malformedDecoderSeeds(f) {
			f.Add(mode, byte(0), []byte(seed))
		}
		for _, name := range []string{"ack_accepted", "operation_pending", "snapshot"} {
			f.Add(mode, byte(0), fixtures[name])
		}
		f.Add(mode, byte(1), []byte(`{"code":4000,"error_message":"SYNTHETIC"}`))
	}
	f.Fuzz(func(t *testing.T, mode, status byte, data []byte) {
		if len(data) > decoderInputLimit {
			t.Skip()
		}
		code := []int{200, 400, 429, 500}[status%4]
		res := response(code, string(data))
		body := &observedBody{ReadCloser: res.Body}
		res.Body = body
		client, err := NewClient(Config{Credentials: credentials, AccountID: "123456", DiscoveryURL: "https://synthetic.invalid", Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return res, nil })})
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()
		var target any
		switch mode % 3 {
		case 0:
			target = &AckResponse{}
		case 1:
			target = &OperationStatus{}
		case 2:
			target = &TradingSnapshot{}
		}
		err = client.get(context.Background(), "https://synthetic.invalid", "/decode", nil, target)
		if !body.closed {
			t.Fatal("response body was not closed")
		}
		if code != 200 {
			var api *APIError
			if !errors.As(err, &api) || api.StatusCode != code {
				t.Fatalf("lost HTTP error: %v", err)
			}
			return
		}
		if err != nil {
			var protocol *ProtocolError
			if !errors.As(err, &protocol) {
				t.Fatalf("unexpected decode failure: %v", err)
			}
		}
	})
}

func checkDecoderSequence(t testing.TB, mode byte, actions []byte, fixtures map[string][]byte) {
	t.Helper()
	mode %= 2
	decode := websocketDecoder(t, mode, false, fixtures)
	ready := false
	snapshot, delta, empty := fixtures["trading_snapshot"], fixtures["transaction_rejected"], []byte(`{"event_type":"trading_state_snapshot","payload":{"account_state":{"balance":"0","equity":"0","used_margin":"0"}}}`)
	malformed := []byte(`{"event_type":"trading_state_snapshot","payload":{}}`)
	if mode == 1 {
		snapshot, delta, empty = fixtures["hmr_snapshot"], fixtures["hmr_update"], fixtures["hmr_empty_snapshot"]
		malformed = []byte(`{"event_type":"hmr_snapshot","periods":[{}]}`)
	}
	for _, action := range actions {
		switch action % 5 {
		case 3:
			decode = websocketDecoder(t, mode, false, fixtures)
			ready = false
		case 0, 4:
			message := snapshot
			if action%5 == 4 {
				message = empty
			}
			if _, err := decode(message); err != nil {
				t.Fatal(err)
			}
			ready = true
		case 2:
			_, err := decode(malformed)
			var protocol *ProtocolError
			if !errors.As(err, &protocol) {
				t.Fatal("accepted failed snapshot")
			}
		case 1:
			isDelta, err := decode(delta)
			if ready {
				if err != nil || !isDelta {
					t.Fatalf("lost delta after snapshot: %v", err)
				}
			} else {
				var protocol *ProtocolError
				if !errors.As(err, &protocol) {
					t.Fatal("delta accepted before snapshot")
				}
			}
		}
	}
}

type decoderSequence struct {
	Name    string `json:"name"`
	Actions []int  `json:"actions"`
}

func decoderSequenceSeeds(t testing.TB) []decoderSequence {
	t.Helper()
	var seeds []decoderSequence
	if err := json.Unmarshal(readFixture(t, "decoder-sequences"), &seeds); err != nil {
		t.Fatal(err)
	}
	return seeds
}
func sequenceBytes(sequence decoderSequence) []byte {
	actions := make([]byte, len(sequence.Actions))
	for i, action := range sequence.Actions {
		actions[i] = byte(action)
	}
	return actions
}

func TestSharedDecoderSequences(t *testing.T) {
	fixtures := decoderFixtures(t)
	for _, sequence := range decoderSequenceSeeds(t) {
		t.Run(sequence.Name, func(t *testing.T) {
			for mode := byte(0); mode < 2; mode++ {
				checkDecoderSequence(t, mode, sequenceBytes(sequence), fixtures)
			}
		})
	}
}

func FuzzDecoderSequences(f *testing.F) {
	fixtures := decoderFixtures(f)
	for _, sequence := range decoderSequenceSeeds(f) {
		for mode := byte(0); mode < 2; mode++ {
			f.Add(mode, sequenceBytes(sequence))
		}
	}
	f.Fuzz(func(t *testing.T, mode byte, actions []byte) {
		if len(actions) > 32 {
			t.Skip()
		}
		checkDecoderSequence(t, mode, actions, fixtures)
	})
}
