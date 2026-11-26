package exness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func ptr(value string) *string { return &value }

func TestSharedOperationCorrelation(t *testing.T) {
	var cases []struct {
		Name      string `json:"name"`
		Reference struct {
			OperationID     string `json:"operation_id"`
			ClientRequestID string `json:"client_request_id"`
		} `json:"reference"`
		Candidate TransactionEvent `json:"candidate"`
		Outcome   string           `json:"outcome"`
	}
	if err := json.Unmarshal(readFixture(t, "operation-correlation-cases"), &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			ref := OperationReference{tc.Reference.OperationID, tc.Reference.ClientRequestID}
			check := func(matched bool, err error) {
				t.Helper()
				var protocol *ProtocolError
				if tc.Outcome == "conflict" {
					if !errors.As(err, &protocol) {
						t.Fatalf("expected conflict: %v", err)
					}
				} else if err != nil || matched != (tc.Outcome == "match") {
					t.Fatalf("match=%v err=%v", matched, err)
				}
			}
			check(ref.MatchesEvent(tc.Candidate))
			if tc.Candidate.Status != nil {
				t.Fatal("identity must not invent a terminal status")
			}
			if tc.Candidate.OperationID != nil {
				check(ref.MatchesStatus(OperationStatus{OperationID: *tc.Candidate.OperationID, ClientRequestID: tc.Candidate.ClientRequestID}))
			}
		})
	}
}

func TestOperationReferenceFromAck(t *testing.T) {
	var ack AckResponse
	if err := json.Unmarshal(readFixture(t, "ack_accepted"), &ack); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"", "trade-test-0001"} {
		ref, err := OperationReferenceFromAck(ack, key)
		if err != nil || ref.OperationID != "000789" || ref.ClientRequestID != "trade-test-0001" {
			t.Fatalf("bad identity: %v", err)
		}
		if strings.Contains(fmt.Sprintf("%v %#v", ref, ref), "trade-test") {
			t.Fatal("identity leaked")
		}
	}
	if _, err := OperationReferenceFromAck(ack, "other-key"); err == nil {
		t.Fatal("accepted conflicting echo")
	}
	for _, value := range []AckResponse{{}, {Status: "accepted"}, {Status: "accepted", OperationID: ptr("invalid")}, {Status: "accepted", OperationID: ptr("789"), ClientRequestID: ptr("")}} {
		if _, err := OperationReferenceFromAck(value, ""); err == nil {
			t.Fatal("accepted invalid acknowledgement identity")
		}
	}
	ref, err := OperationReferenceFromAck(AckResponse{Status: "accepted"}, "saved-key")
	if err != nil || ref.OperationID != "" || ref.ClientRequestID != "saved-key" {
		t.Fatalf("lost fallback key: %v", err)
	}
	for _, ref := range []OperationReference{{}, {OperationID: "18446744073709551616"}, {ClientRequestID: "tiny"}} {
		if _, err := ref.MatchesEvent(TransactionEvent{}); err == nil {
			t.Fatal("accepted invalid reference")
		}
	}
	if _, err := ref.MatchesEvent(TransactionEvent{OperationID: ptr("18446744073709551616")}); err == nil {
		t.Fatal("accepted invalid event ID")
	}
}

func TestWaitPendingThenTerminal(t *testing.T) {
	for _, terminal := range []string{"confirmed", "rejected", "failed"} {
		t.Run(terminal, func(t *testing.T) {
			reads := 0
			c := makeClient(t, func(r *http.Request) (*http.Response, error) {
				verifyRequest(t, r)
				if r.URL.Host == "gateway.example" {
					return discoveryResponse(), nil
				}
				if !strings.HasSuffix(r.URL.Path, "/operations/000789") {
					t.Fatal("wrong operation path")
				}
				reads++
				name := "pending"
				if reads > 1 {
					name = terminal
				}
				return response(200, string(readFixture(t, "operation_"+name))), nil
			})
			result, err := c.WaitOperation(context.Background(), OperationReference{"000789", "synthetic-operation-1"}, OperationWaitOptions{Timeout: time.Second, PollInterval: time.Millisecond})
			var expected OperationStatus
			if decodeErr := json.Unmarshal(readFixture(t, "operation_"+terminal), &expected); decodeErr != nil {
				t.Fatal(decodeErr)
			}
			if err != nil || !reflect.DeepEqual(result, &expected) || reads != 2 {
				t.Fatalf("lost terminal details: %v reads=%d", err, reads)
			}
		})
	}
}

func TestWaitPendingDeadline(t *testing.T) {
	reads := 0
	c := makeClient(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "gateway.example" {
			return discoveryResponse(), nil
		}
		reads++
		return response(200, string(readFixture(t, "operation_pending"))), nil
	})
	ref := OperationReference{"789", "synthetic-operation-1"}
	_, err := c.WaitOperation(context.Background(), ref, OperationWaitOptions{Timeout: 200 * time.Millisecond, PollInterval: time.Minute})
	var timeout *OperationWaitTimeoutError
	if !errors.As(err, &timeout) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wrong error: %v", err)
	}
	if timeout.Reference != ref || timeout.LastStatus == nil || timeout.LastStatus.Status != OperationPending || timeout.Attempts != 1 || reads != 1 {
		t.Fatal("lost identity or polled again")
	}
	if strings.Contains(err.Error(), "synthetic") {
		t.Fatal("identity leaked")
	}
}

func TestWaitReadErrorsStop(t *testing.T) {
	for _, failure := range []string{"404", "429", "transport", "key", "id"} {
		t.Run(failure, func(t *testing.T) {
			reads := 0
			c := makeClient(t, func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "gateway.example" {
					return discoveryResponse(), nil
				}
				reads++
				switch failure {
				case "404":
					return response(404, `{"code":4000}`), nil
				case "429":
					return response(429, `{"code":4000}`), nil
				case "transport":
					return nil, errors.New("synthetic transport")
				}
				body := string(readFixture(t, "operation_pending"))
				if failure == "key" {
					body = strings.ReplaceAll(body, "synthetic-operation-1", "different-key")
				} else {
					body = strings.ReplaceAll(body, `"789"`, `"790"`)
				}
				return response(200, body), nil
			})
			_, err := c.WaitOperation(context.Background(), OperationReference{"789", "synthetic-operation-1"}, OperationWaitOptions{})
			var api *APIError
			var transport *TransportError
			var protocol *ProtocolError
			valid := errors.As(err, &protocol)
			if failure == "404" || failure == "429" {
				valid = errors.As(err, &api)
			}
			if failure == "transport" {
				valid = errors.As(err, &transport)
			}
			if !valid || reads != 1 {
				t.Fatalf("wrong error or retry: %v reads=%d", err, reads)
			}
		})
	}
}

func TestWaitInvalidAndAlreadyCanceled(t *testing.T) {
	c := makeClient(t, func(r *http.Request) (*http.Response, error) { t.Fatal("unexpected request"); return nil, nil })
	for _, tc := range []struct {
		ref     OperationReference
		options OperationWaitOptions
	}{
		{OperationReference{}, OperationWaitOptions{}},
		{OperationReference{ClientRequestID: "saved-key"}, OperationWaitOptions{}},
		{OperationReference{OperationID: "789"}, OperationWaitOptions{Timeout: -1}},
		{OperationReference{OperationID: "789"}, OperationWaitOptions{PollInterval: -1}},
	} {
		if _, err := c.WaitOperation(context.Background(), tc.ref, tc.options); err == nil {
			t.Fatal("invalid wait accepted")
		}
	}
	ref := OperationReference{OperationID: "789"}
	if _, err := c.WaitOperation(nil, ref, OperationWaitOptions{}); err == nil {
		t.Fatal("nil context accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.WaitOperation(ctx, ref, OperationWaitOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
	ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	_, err := c.WaitOperation(ctx, ref, OperationWaitOptions{})
	var timeout *OperationWaitTimeoutError
	if !errors.As(err, &timeout) || timeout.Attempts != 0 || timeout.LastStatus != nil {
		t.Fatalf("lost expired context: %v", err)
	}
}

func TestWaitRealTLSDeadlineAndCancellation(t *testing.T) {
	for _, phase := range []string{"discovery", "headers", "body"} {
		for _, cancelCaller := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/cancel=%v", phase, cancelCaller), func(t *testing.T) {
				entered, closed := make(chan struct{}), make(chan struct{})
				server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, err := io.ReadAll(r.Body)
					if err != nil || len(body) != 0 {
						t.Error("unexpected GET body")
						return
					}
					// Server requests use NoBody; the shared client verifier expects nil.
					r.Body = nil
					verifyRequest(t, r)
					if strings.HasSuffix(r.URL.Path, "access-point") && phase != "discovery" {
						fmt.Fprintf(w, `{"access_point":"https://%s","account_id":"123456"}`, r.Host)
						return
					}
					if phase == "body" {
						w.Header().Set("Content-Length", "100")
						w.WriteHeader(200)
						w.(http.Flusher).Flush()
					}
					close(entered)
					<-r.Context().Done()
					close(closed)
				}))
				defer server.Close()
				c := tlsClient(t, server, 5*time.Second)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				timeout := 500 * time.Millisecond
				if cancelCaller {
					timeout = 5 * time.Second
				}
				result := make(chan error, 1)
				go func() {
					_, err := c.WaitOperation(ctx, OperationReference{OperationID: "789"}, OperationWaitOptions{Timeout: timeout})
					result <- err
				}()
				awaitSignal(t, entered)
				if cancelCaller {
					cancel()
				}
				err := awaitError(t, result)
				if cancelCaller {
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("lost cancel: %v", err)
					}
				} else {
					var expired *OperationWaitTimeoutError
					if !errors.As(err, &expired) || expired.LastStatus != nil || expired.Attempts != 1 {
						t.Fatalf("lost wait timeout: %v", err)
					}
				}
				awaitSignal(t, closed)
			})
		}
	}
}

func TestWaitDiscoveryGateDeadline(t *testing.T) {
	c := makeClient(t, func(r *http.Request) (*http.Response, error) { t.Fatal("unexpected request"); return nil, nil })
	c.discoveryGate <- struct{}{}
	defer func() { <-c.discoveryGate }()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := c.WaitOperation(ctx, OperationReference{OperationID: "789"}, OperationWaitOptions{})
	var timeout *OperationWaitTimeoutError
	if !errors.As(err, &timeout) || timeout.LastStatus != nil {
		t.Fatalf("gate wait did not expire: %v", err)
	}
}
