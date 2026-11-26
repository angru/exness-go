package exness

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
)

// This optional report is test tooling, not a public SDK API or runtime dependency.
func TestParityReport(t *testing.T) {
	var cases []responseCase
	if err := json.Unmarshal(readFixture(t, "response-validation"), &cases); err != nil {
		t.Fatal(err)
	}
	actual := map[string]string{}
	for _, tc := range cases {
		outcome := "accepted"
		if err := decodeResponseCase(t, tc.Kind, responseCaseBytes(t, tc)); err != nil {
			var protocol *ProtocolError
			if !errors.As(err, &protocol) {
				t.Fatalf("%s: unexpected decoder error: %v", tc.Name, err)
			}
			outcome = "protocol_error"
		}
		if _, duplicate := actual[tc.Name]; duplicate {
			t.Fatalf("duplicate case %s", tc.Name)
		}
		if (outcome == "accepted") != tc.Valid {
			t.Fatalf("%s: unexpected outcome %s", tc.Name, outcome)
		}
		actual[tc.Name] = outcome
	}
	if path := os.Getenv("EXNESS_PARITY_REPORT"); path != "" {
		data, err := json.Marshal(actual)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
