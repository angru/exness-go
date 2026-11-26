package exness

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestRESTNestedOverflowIsProtocolError(t *testing.T) {
	var cases []responseCase
	if err := json.Unmarshal(readFixture(t, "response-validation"), &cases); err != nil {
		t.Fatal(err)
	}
	var body []byte
	for _, tc := range cases {
		if tc.Name == "id_3_overflow" {
			body = responseCaseBytes(t, tc)
		}
	}
	if body == nil {
		t.Fatal("missing regression case")
	}
	calls := 0
	client := makeClient(t, func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host == "gateway.example" {
			return discoveryResponse(), nil
		}
		return response(200, string(body)), nil
	})
	_, err := client.GetSnapshot(context.Background())
	var protocol *ProtocolError
	if !errors.As(err, &protocol) || calls != 2 {
		t.Fatalf("invalid response escaped validation: %v", err)
	}
}

type responseCase struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Fixture string `json:"fixture"`
	Path    string `json:"path"`
	Action  string `json:"action"`
	Value   any    `json:"value"`
	Valid   bool   `json:"valid"`
}

func responseCaseBytes(t *testing.T, tc responseCase) []byte {
	t.Helper()
	var value any
	if err := json.Unmarshal(readFixture(t, tc.Fixture), &value); err != nil {
		t.Fatal(err)
	}
	if tc.Action != "none" {
		parts := strings.Split(tc.Path, ".")
		parent := value
		for _, part := range parts[:len(parts)-1] {
			switch current := parent.(type) {
			case map[string]any:
				parent = current[part]
			case []any:
				i, _ := strconv.Atoi(part)
				parent = current[i]
			default:
				t.Fatal("invalid corpus path")
			}
		}
		last := parts[len(parts)-1]
		switch current := parent.(type) {
		case map[string]any:
			if tc.Action == "delete" {
				delete(current, last)
			} else {
				current[last] = tc.Value
			}
		case []any:
			i, _ := strconv.Atoi(last)
			current[i] = tc.Value
		default:
			t.Fatal("invalid corpus target")
		}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func decodeResponseCase(t *testing.T, kind string, data []byte) error {
	t.Helper()
	switch kind {
	case "ack":
		var value AckResponse
		return json.Unmarshal(data, &value)
	case "status":
		var value OperationStatus
		return json.Unmarshal(data, &value)
	case "snapshot":
		var value TradingSnapshot
		return json.Unmarshal(data, &value)
	case "transaction":
		decode := tradingDecoder("test")
		if _, err := decode(readFixture(t, "trading_snapshot")); err != nil {
			t.Fatal(err)
		}
		_, err := decode(data)
		return err
	case "hmr_snapshot", "hmr_update":
		decode := hmrDecoder("test")
		if _, err := decode(readFixture(t, "hmr_empty_snapshot")); err != nil {
			t.Fatal(err)
		}
		_, err := decode(data)
		return err
	default:
		t.Fatal("unknown response case kind")
		return nil
	}
}

func TestSharedResponseValidation(t *testing.T) {
	var cases []responseCase
	if err := json.Unmarshal(readFixture(t, "response-validation"), &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			err := decodeResponseCase(t, tc.Kind, responseCaseBytes(t, tc))
			if tc.Valid {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				var protocol *ProtocolError
				if !errors.As(err, &protocol) {
					t.Fatalf("expected protocol failure: %v", err)
				}
			}
		})
	}
}
