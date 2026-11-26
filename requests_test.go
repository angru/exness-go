package exness

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type mutationVector struct {
	Method    string `json:"method"`
	Path      string `json:"path"`
	Body      string `json:"body_utf8"`
	Key       string `json:"idempotency_key"`
	Timestamp int64  `json:"timestamp_ms"`
	Payload   string `json:"payload_utf8"`
	Data      string `json:"exn_data"`
	Signature string `json:"exn_sign"`
}

func mutationVectors(t *testing.T) []mutationVector {
	t.Helper()
	var fixture struct {
		Vectors []mutationVector `json:"vectors"`
	}
	if err := json.Unmarshal(readFixture(t, "mutation-signing-vectors"), &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture.Vectors
}

func requestDecimal(t *testing.T, value string) Decimal {
	t.Helper()
	d, err := NewDecimal(value)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestSharedMutationSigningVectors(t *testing.T) {
	c := testCredentials(t)
	for _, v := range mutationVectors(t) {
		t.Run(v.Method, func(t *testing.T) {
			h, err := c.signRequest(v.Method, v.Path, []byte(v.Body), v.Key, time.UnixMilli(v.Timestamp))
			if err != nil {
				t.Fatal(err)
			}
			data, _ := base64.RawURLEncoding.DecodeString(h.Get("EXN-DATA"))
			if h.Get("EXN-DATA") != v.Data || h.Get("EXN-SIGN") != v.Signature || string(data) != v.Payload {
				t.Fatal("signature differs from shared OpenSSL mutation vector")
			}
			if h.Get("EXN-IDEMPOTENCY-KEY") != v.Key {
				t.Fatal("idempotency header mismatch")
			}
		})
	}
}

func TestMutationSigningValidationAndFreshTimestamp(t *testing.T) {
	c := testCredentials(t)
	for _, method := range []string{"POST", "PATCH", "DELETE"} {
		for _, key := range []string{"", "abcd", strings.Repeat("a", 129), "has space", "тест-123", "abcde\n", "abcde/"} {
			if _, err := c.signRequest(method, "/test", nil, key, time.Now()); err == nil {
				t.Fatalf("accepted invalid key for %s", method)
			}
		}
	}
	for _, input := range []struct{ method, body, key string }{
		{"GET", "{}", ""}, {"GET", "", "abcde"}, {"post", "", "abcde"}, {"PUT", "", "abcde"},
	} {
		if _, err := c.signRequest(input.method, "/test", []byte(input.body), input.key, time.Now()); err == nil {
			t.Fatal("accepted invalid signing shape")
		}
	}
	for _, key := range []string{"a._~-", strings.Repeat("A", 128)} {
		first, err := c.signRequest("POST", "/test", []byte("{}"), key, time.Unix(1000, 0))
		if err != nil {
			t.Fatal(err)
		}
		second, err := c.signRequest("POST", "/test", []byte("{}"), key, time.Unix(1001, 0))
		if err != nil {
			t.Fatal(err)
		}
		if first.Get("EXN-SIGN") == second.Get("EXN-SIGN") || first.Get("EXN-IDEMPOTENCY-KEY") != key || second.Get("EXN-IDEMPOTENCY-KEY") != key {
			t.Fatal("timestamp or key changed incorrectly")
		}
	}
}

func TestOpeningPositionEncodingAndValidation(t *testing.T) {
	var request OpenPositionRequest
	fixture := readFixture(t, "open_position_request")
	if err := json.Unmarshal(fixture, &request); err != nil {
		t.Fatal(err)
	}
	body, err := request.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var want, got any
	if err := json.Unmarshal(fixture, &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	wantJSON, _ := json.Marshal(want)
	gotJSON, _ := json.Marshal(got)
	if !bytes.Equal(wantJSON, gotJSON) {
		t.Fatal("request changed fixture values")
	}
	minimal := OpenPositionRequest{Instrument: "EURUSD", Side: TradeSell, Volume: requestDecimal(t, "0.00000000000000000001")}
	body, err = json.Marshal(minimal)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"instrument":"EURUSD","side":"sell","volume":"0.00000000000000000001"}` {
		t.Fatalf("unexpected body: %s", body)
	}
	zero, negative := requestDecimal(t, "0"), requestDecimal(t, "-1")
	badComment := string([]byte{0xff})
	for _, alter := range []func(*OpenPositionRequest){
		func(r *OpenPositionRequest) { r.Instrument = "EUR/USD" },
		func(r *OpenPositionRequest) { r.Side = "BUY" },
		func(r *OpenPositionRequest) { r.Volume = Decimal{} },
		func(r *OpenPositionRequest) { r.Volume = zero },
		func(r *OpenPositionRequest) { r.Volume = negative },
		func(r *OpenPositionRequest) { r.Price = &zero },
		func(r *OpenPositionRequest) { r.StopLossPrice = &negative },
		func(r *OpenPositionRequest) { r.TakeProfitPrice = &zero },
		func(r *OpenPositionRequest) { r.Deviation = &Decimal{} },
		func(r *OpenPositionRequest) { r.Comment = &badComment },
	} {
		invalid := minimal
		alter(&invalid)
		if _, err := json.Marshal(invalid); err == nil {
			t.Fatal("invalid opening request serialized")
		}
	}
}

func TestAckAdmissionAndMissingIdentity(t *testing.T) {
	var ack AckResponse
	if err := json.Unmarshal(readFixture(t, "ack_accepted"), &ack); err != nil {
		t.Fatal(err)
	}
	if ack.Status != "accepted" || ack.OperationID == nil || *ack.OperationID != "000789" || ack.ClientRequestID == nil || *ack.ClientRequestID != "trade-test-0001" {
		t.Fatal("lost ACK fields")
	}
	if err := json.Unmarshal(readFixture(t, "ack_without_id"), &ack); err != nil {
		t.Fatal(err)
	}
	if ack.OperationID != nil || ack.ClientRequestID != nil {
		t.Fatal("fabricated operation identity")
	}
	if err := json.Unmarshal([]byte(`{"status":"accepted","operation_id":"18446744073709551615"}`), &ack); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{`{}`, `null`, `[]`, `{"status":"confirmed"}`, `{"status":"accepted","operation_id":1}`, `{"status":"accepted","operation_id":"18446744073709551616"}`, `{"status":"accepted","operation_id":"+1"}`} {
		if err := json.Unmarshal([]byte(invalid), &ack); err == nil {
			t.Fatal("invalid acknowledgement accepted")
		}
	}
}

func TestMutationSigningOnTLSWire(t *testing.T) {
	c := testCredentials(t)
	for _, v := range mutationVectors(t) {
		t.Run(v.Method, func(t *testing.T) {
			body := []byte(v.Body)
			if v.Method == "POST" {
				var model OpenPositionRequest
				if err := json.Unmarshal(readFixture(t, "open_position_request"), &model); err != nil {
					t.Fatal(err)
				}
				var err error
				body, err = model.MarshalJSON()
				if err != nil {
					t.Fatal(err)
				}
			}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				wire, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				payload, err := base64.RawURLEncoding.DecodeString(r.Header.Get("EXN-DATA"))
				if err != nil {
					t.Error(err)
					return
				}
				signature, err := base64.RawURLEncoding.DecodeString(r.Header.Get("EXN-SIGN"))
				if err != nil {
					t.Error(err)
					return
				}
				if !ed25519.Verify(c.privateKey.Public().(ed25519.PublicKey), payload, signature) {
					t.Error("wire signature invalid")
				}
				var metadata signedPayload
				if err := json.Unmarshal(payload, &metadata); err != nil {
					t.Error(err)
					return
				}
				hash := sha256.Sum256(wire)
				if !bytes.Equal(wire, body) || metadata.BodyHash != base64.RawURLEncoding.EncodeToString(hash[:]) {
					t.Error("body differs from signed bytes")
				}
				if metadata.Method != r.Method || r.Method != v.Method || metadata.Path != r.RequestURI || r.RequestURI != v.Path {
					t.Error("method/path differs from signature")
				}
				if metadata.IdempotencyKey != v.Key || r.Header.Get("EXN-IDEMPOTENCY-KEY") != v.Key {
					t.Error("key differs from signature")
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"status":"accepted"}`))
			}))
			defer server.Close()
			request, err := http.NewRequest(v.Method, server.URL+v.Path, bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			request.Header, err = c.signRequest(request.Method, request.URL.RequestURI(), body, v.Key, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Content-Type", "application/json")
			client := server.Client()
			client.Timeout = 2 * time.Second
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			var ack AckResponse
			if err := json.NewDecoder(response.Body).Decode(&ack); err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != http.StatusAccepted || ack.Status != "accepted" {
				t.Fatal("missing admission acknowledgement")
			}
		})
	}
}
