package exness

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func tickContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func tickTestClient(t *testing.T, handler func(context.Context, *websocket.Conn, string)) *Client {
	t.Helper()
	return streamTestClient(t, "ticks", handler)
}

func streamTestClient(t *testing.T, endpoint string, handler func(context.Context, *websocket.Conn, string)) *Client {
	t.Helper()
	ctx := tickContext(t)
	var mu sync.Mutex
	var requests []*http.Request
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := r.Clone(r.Context())
		request.Body = nil
		mu.Lock()
		requests = append(requests, request)
		mu.Unlock()
		if r.URL.Path == "/v1/trading/access-point" {
			_ = json.NewEncoder(w).Encode(map[string]string{"access_point": "https://" + r.Host, "account_id": "123456"})
			return
		}
		if r.URL.Path != "/v1/server-events/accounts/123456/ws/"+endpoint {
			t.Errorf("wrong stream path %s", r.URL.Path)
			return
		}
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		} // Cancellation may race subscription transmission.
		var payload struct {
			ID        string `json:"id"`
			Subscribe struct {
				Event       string   `json:"event"`
				Instruments []string `json:"instruments"`
			} `json:"subscribe"`
		}
		validSubscription := json.Unmarshal(data, &payload) == nil && payload.ID != ""
		if endpoint == "ticks" {
			validSubscription = validSubscription && payload.Subscribe.Event == "ticks" && len(payload.Subscribe.Instruments) == 1 && payload.Subscribe.Instruments[0] == "EURUSD"
		} else {
			validSubscription = validSubscription && payload.Subscribe.Event == "transactions" && len(payload.Subscribe.Instruments) == 0
		}
		if !validSubscription {
			t.Error("invalid tick subscription")
			return
		}
		handler(ctx, conn, payload.ID)
	}))
	t.Cleanup(server.Close)
	c := tlsClient(t, server, time.Second)
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		for _, request := range requests {
			verifyRequest(t, request)
		}
	})
	return c
}

func writeTick(ctx context.Context, conn *websocket.Conn) error {
	return conn.Write(ctx, websocket.MessageText, []byte(`{"instrument":"EURUSD","bid":1.08321,"ask":1.08331,"timestamp":"2026-11-19T10:00:00.123Z"}`))
}

func TestTickStreamHandshakePartialDataAndEarlyBreak(t *testing.T) {
	closed := make(chan struct{})
	c := tickTestClient(t, func(ctx context.Context, conn *websocket.Conn, _ string) {
		if err := writeTick(ctx, conn); err != nil {
			t.Error(err)
			return
		}
		if err := conn.Write(ctx, websocket.MessageText, []byte(`{"instrument":"EURUSD","bid":1.2}`)); err != nil {
			return
		}
		<-conn.CloseRead(ctx).Done()
		close(closed)
	})
	count := 0
	for event, err := range c.StreamTicks(tickContext(t), []string{"EURUSD"}, TickStreamOptions{}) {
		if err != nil {
			t.Fatal(err)
		}
		if event.Tick == nil || event.Gap != nil {
			t.Fatal("wrong event envelope")
		}
		count++
		if count == 1 && (event.Tick.Bid == nil || *event.Tick.Bid != 1.08321 || event.Tick.Timestamp == nil) {
			t.Fatal("invalid first tick")
		}
		if count == 2 {
			if event.Tick.Ask != nil || event.Tick.Timestamp != nil {
				t.Fatal("missing fields invented")
			}
			break
		}
	}
	if count != 2 {
		t.Fatal("missing ticks")
	}
	awaitSignal(t, closed)
}

func TestTickStreamReconnectBudgetAndGap(t *testing.T) {
	var mu sync.Mutex
	ids := make(map[string]bool)
	c := tickTestClient(t, func(ctx context.Context, conn *websocket.Conn, id string) {
		mu.Lock()
		ids[id] = true
		mu.Unlock()
		_ = writeTick(ctx, conn)
		_ = conn.CloseNow()
	})
	options := TickStreamOptions{MaxReconnects: 2, ReconnectDelay: time.Millisecond, MaxReconnectDelay: 2 * time.Millisecond}
	ticks, gaps, failures := 0, 0, 0
	for event, err := range c.StreamTicks(tickContext(t), []string{"EURUSD"}, options) {
		if err != nil {
			var transport *TransportError
			if !errors.As(err, &transport) {
				t.Fatalf("unexpected error: %v", err)
			}
			failures++
			continue
		}
		if event.Tick != nil {
			ticks++
			continue
		}
		gaps++
		if event.Gap == nil || event.Gap.ReconnectAttempt != gaps || event.Gap.CloseCode == nil || *event.Gap.CloseCode != 1006 {
			t.Fatal("wrong gap")
		}
		if event.Gap.Delay != time.Duration(gaps)*time.Millisecond {
			t.Fatal("wrong backoff")
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if ticks != 3 || gaps != 2 || failures != 1 || len(ids) != 3 {
		t.Fatalf("ticks=%d gaps=%d errors=%d ids=%d", ticks, gaps, failures, len(ids))
	}
}

func TestTickStreamClosePolicy(t *testing.T) {
	for _, code := range []websocket.StatusCode{1000, 1008, 1009, 1013} {
		t.Run(code.String(), func(t *testing.T) {
			var calls atomic.Int32
			c := tickTestClient(t, func(ctx context.Context, conn *websocket.Conn, _ string) {
				calls.Add(1)
				_ = conn.Close(code, "private server reason")
			})
			failures := 0
			for _, err := range c.StreamTicks(tickContext(t), []string{"EURUSD"}, TickStreamOptions{MaxReconnects: 3}) {
				failures++
				var closed *StreamClosed
				if !errors.As(err, &closed) || closed.Code != int(code) || strings.Contains(err.Error(), "private") {
					t.Fatalf("close error: %v", err)
				}
			}
			expected := 1
			if code == 1000 {
				expected = 0
			}
			if failures != expected || calls.Load() != 1 {
				t.Fatal("terminal close retried or lost")
			}
		})
	}
}

func TestTickStreamProtocolAndSubscriptionErrors(t *testing.T) {
	for _, body := range []string{`[]`, `{}`, `bad JSON`, `{"instrument":"XAUUSD"}`, `{"instrument":"EURUSD","bid":"1.2"}`, `{"instrument":"EURUSD","bid":1e999}`, `subscription-error`} {
		t.Run(body, func(t *testing.T) {
			var calls atomic.Int32
			c := tickTestClient(t, func(ctx context.Context, conn *websocket.Conn, id string) {
				calls.Add(1)
				message := []byte(body)
				if body == "subscription-error" {
					message, _ = json.Marshal(map[string]any{"id": id, "code": 3000, "error_message": "private details"})
				}
				_ = conn.Write(ctx, websocket.MessageText, message)
				<-conn.CloseRead(ctx).Done()
			})
			failures := 0
			for _, err := range c.StreamTicks(tickContext(t), []string{"EURUSD"}, TickStreamOptions{MaxReconnects: 2}) {
				failures++
				var protocol *ProtocolError
				var api *WebSocketAPIError
				if !errors.As(err, &protocol) && !errors.As(err, &api) {
					t.Fatalf("wrong protocol error: %v", err)
				}
				if strings.Contains(err.Error(), "private") {
					t.Fatal("error exposed message")
				}
			}
			if failures != 1 || calls.Load() != 1 {
				t.Fatal("protocol failure retried")
			}
		})
	}
}

func TestTickHandshakeFailureAndNoRedirect(t *testing.T) {
	for _, status := range []int{307, 401, 429, 503, 101} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls, forwarded atomic.Int32
			target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded.Add(1) }))
			defer target.Close()
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/trading/access-point" {
					_ = json.NewEncoder(w).Encode(map[string]string{"access_point": "https://" + r.Host})
					return
				}
				calls.Add(1)
				w.Header().Set("Location", target.URL)
				w.Header().Set("Retry-After", "10")
				w.WriteHeader(status)
			}))
			defer server.Close()
			c := tlsClient(t, server, time.Second)
			for _, err := range c.StreamTicks(tickContext(t), []string{"EURUSD"}, TickStreamOptions{MaxReconnects: 2}) {
				if status == 101 {
					var protocol *ProtocolError
					if !errors.As(err, &protocol) {
						t.Fatalf("invalid handshake: %v", err)
					}
					continue
				}
				var api *APIError
				if !errors.As(err, &api) || api.StatusCode != status || api.RetryAfter != "10" {
					t.Fatalf("wrong handshake error %v", err)
				}
			}
			if calls.Load() != 1 || forwarded.Load() != 0 {
				t.Fatal("handshake retried or credentials forwarded")
			}
		})
	}
}

func TestTickCancellationAndClientClose(t *testing.T) {
	for _, mode := range []string{"cancel-read", "close-read", "close-paused", "cancel-backoff", "break-gap"} {
		t.Run(mode, func(t *testing.T) {
			entered, closed := make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			c := tickTestClient(t, func(ctx context.Context, conn *websocket.Conn, _ string) {
				calls.Add(1)
				close(entered)
				if mode == "cancel-backoff" || mode == "break-gap" {
					_ = conn.CloseNow()
					close(closed)
					return
				}
				if mode == "close-paused" {
					_ = writeTick(ctx, conn)
				}
				<-conn.CloseRead(ctx).Done()
				close(closed)
			})
			ctx, cancel := context.WithCancel(tickContext(t))
			defer cancel()
			result := make(chan error, 1)
			go func() {
				for event, err := range c.StreamTicks(ctx, []string{"EURUSD"}, TickStreamOptions{MaxReconnects: 2, ReconnectDelay: 30 * time.Second}) {
					if err != nil {
						result <- err
						return
					}
					if event.Gap != nil {
						if mode == "break-gap" {
							break
						}
						cancel()
					}
					if event.Tick != nil && mode == "close-paused" {
						c.Close()
						awaitSignal(t, closed)
					}
				}
				result <- nil
			}()
			awaitSignal(t, entered)
			if mode == "cancel-read" {
				cancel()
			}
			if mode == "close-read" {
				c.Close()
			}
			err := awaitError(t, result)
			if mode == "break-gap" {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation: %v", err)
			}
			awaitSignal(t, closed)
			if calls.Load() != 1 {
				t.Fatal("canceled stream reconnected")
			}
		})
	}
}

func TestTickMessageLimitAndSlowConsumer(t *testing.T) {
	t.Run("limit", func(t *testing.T) {
		c := tickTestClient(t, func(ctx context.Context, conn *websocket.Conn, _ string) {
			_ = conn.Write(ctx, websocket.MessageText, []byte(strings.Repeat("x", 2048)))
			<-conn.CloseRead(ctx).Done()
		})
		for _, err := range c.StreamTicks(tickContext(t), []string{"EURUSD"}, TickStreamOptions{MaxMessageBytes: 1024, MaxReconnects: 2}) {
			var closed *StreamClosed
			if !errors.As(err, &closed) || closed.Code != 1009 {
				t.Fatalf("size limit: %v", err)
			}
		}
	})
	t.Run("slow", func(t *testing.T) {
		sent := make(chan struct{})
		c := tickTestClient(t, func(ctx context.Context, conn *websocket.Conn, _ string) {
			conn.CloseRead(ctx) // Read control frames while Ping waits for the matching pong.
			if err := conn.Ping(ctx); err != nil {
				t.Error(err)
				return
			}
			for i := range 50 {
				body, _ := json.Marshal(map[string]any{"instrument": "EURUSD", "bid": i})
				if conn.Write(ctx, websocket.MessageText, body) != nil {
					return
				}
			}
			close(sent)
			_ = conn.Close(1000, "")
		})
		count := 0
		for event, err := range c.StreamTicks(tickContext(t), []string{"EURUSD"}, TickStreamOptions{}) {
			if err != nil {
				t.Fatal(err)
			}
			if event.Tick == nil || event.Tick.Bid == nil || *event.Tick.Bid != float64(count) {
				t.Fatal("tick order changed")
			}
			if count == 0 {
				awaitSignal(t, sent)
			}
			count++
		}
		if count != 50 {
			t.Fatal("ticks were lost")
		}
	})
}

func TestTickOptionsBeforeNetwork(t *testing.T) {
	c := makeClient(t, func(r *http.Request) (*http.Response, error) {
		t.Error("invalid input made request")
		return nil, errors.New("unexpected request")
	})
	for _, options := range []TickStreamOptions{{MaxReconnects: -1}, {MaxReconnects: 1001}, {ReconnectDelay: -1}, {OpenTimeout: -1}, {MaxReconnectDelay: -1}, {ReconnectDelay: time.Hour}, {MaxMessageBytes: -1}, {MaxMessageBytes: 17 << 20}} {
		count := 0
		for _, err := range c.StreamTicks(tickContext(t), []string{"EURUSD"}, options) {
			count++
			if err == nil {
				t.Fatal("invalid option accepted")
			}
		}
		if count != 1 {
			t.Fatal("expected one error")
		}
	}
	for _, names := range [][]string{nil, {""}, {"EURUSD", "EURUSD"}, {"bad/name"}} {
		for _, err := range c.StreamTicks(tickContext(t), names, TickStreamOptions{}) {
			if err == nil {
				t.Fatal("invalid instruments accepted")
			}
		}
	}
	for _, err := range c.StreamTicks(nil, []string{"EURUSD"}, TickStreamOptions{}) {
		if err == nil {
			t.Fatal("nil context accepted")
		}
	}
	c.Close()
	for _, err := range c.StreamTicks(context.Background(), []string{"EURUSD"}, TickStreamOptions{}) {
		if err == nil {
			t.Fatal("closed client accepted")
		}
	}
}

func TestTickUntrustedCertificateIsTerminal(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("untrusted TLS endpoint received a request")
	}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	defer server.Close()
	transport := &http.Transport{} // Deliberately do not trust the test certificate.
	defer transport.CloseIdleConnections()
	var calls int
	c := makeClient(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "gateway.example" {
			body, _ := json.Marshal(map[string]string{"access_point": server.URL})
			return response(200, string(body)), nil
		}
		calls++
		return transport.RoundTrip(r)
	})
	failures := 0
	for event, err := range c.StreamTicks(tickContext(t), []string{"EURUSD"}, TickStreamOptions{MaxReconnects: 2}) {
		var verification *tls.CertificateVerificationError
		if event.Gap != nil || !errors.As(err, &verification) {
			t.Fatalf("expected terminal certificate error, got %v", err)
		}
		failures++
	}
	if calls != 1 || failures != 1 {
		t.Fatal("certificate failure retried or lost")
	}
}

func TestTickOpenTimeout(t *testing.T) {
	t.Run("handshake", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/trading/access-point" {
				_ = json.NewEncoder(w).Encode(map[string]string{"access_point": "https://" + r.Host})
				return
			}
			<-r.Context().Done()
		}))
		defer server.Close()
		c := tlsClient(t, server, time.Second)
		failures := 0
		for _, err := range c.StreamTicks(tickContext(t), []string{"EURUSD"}, TickStreamOptions{OpenTimeout: 100 * time.Millisecond}) {
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("expected handshake deadline, got %v", err)
			}
			failures++
		}
		if failures != 1 {
			t.Fatal("missing handshake timeout")
		}
	})
	t.Run("does-not-limit-stream", func(t *testing.T) {
		c := tickTestClient(t, func(ctx context.Context, conn *websocket.Conn, _ string) {
			select {
			case <-time.After(500 * time.Millisecond):
			case <-ctx.Done():
				return
			}
			_ = writeTick(ctx, conn)
			_ = conn.Close(1000, "")
		})
		count := 0
		for event, err := range c.StreamTicks(tickContext(t), []string{"EURUSD"}, TickStreamOptions{OpenTimeout: 300 * time.Millisecond}) {
			if err != nil || event.Tick == nil {
				t.Fatalf("stream inherited handshake deadline: %v", err)
			}
			count++
		}
		if count != 1 {
			t.Fatal("missing delayed tick")
		}
	})
}

func TestTickDialRetryAndSubscriptionLimit(t *testing.T) {
	for _, mode := range []string{"dial", "subscription-limit", "discovery", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			calls, discovery, gaps, failures := 0, 0, 0, 0
			c := makeClient(t, func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "gateway.example" {
					discovery++
					if mode == "discovery" {
						return nil, io.ErrUnexpectedEOF
					}
					return discoveryResponse(), nil
				}
				calls++
				return nil, io.ErrUnexpectedEOF
			})
			options := TickStreamOptions{MaxReconnects: 1, ReconnectDelay: time.Millisecond}
			if mode == "subscription-limit" {
				options.MaxMessageBytes = 1
			}
			ctx, cancel := context.WithCancel(tickContext(t))
			defer cancel()
			if mode == "canceled" {
				cancel()
			}
			for event, err := range c.StreamTicks(ctx, []string{"EURUSD"}, options) {
				if err != nil {
					failures++
					continue
				}
				if event.Gap == nil || event.Gap.CloseCode != nil || event.Gap.ReconnectAttempt != 1 {
					t.Fatal("invalid pre-connection gap")
				}
				gaps++
			}
			wantCalls, wantGaps, wantDiscovery := 0, 0, 1
			if mode == "dial" {
				wantCalls, wantGaps = 2, 1
			}
			if mode == "canceled" {
				wantDiscovery = 0
			}
			if calls != wantCalls || gaps != wantGaps || discovery != wantDiscovery || failures != 1 {
				t.Fatalf("calls=%d gaps=%d discovery=%d errors=%d", calls, gaps, discovery, failures)
			}
		})
	}
}

func TestTickHeartbeat(t *testing.T) {
	for _, respond := range []bool{true, false} {
		name := "missing-pong"
		if respond {
			name = "pong"
		}
		t.Run(name, func(t *testing.T) {
			ctx := tickContext(t)
			pinged := make(chan struct{}, 1)
			closed := make(chan struct{})
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OnPingReceived: func(context.Context, []byte) bool {
					select {
					case pinged <- struct{}{}:
					default:
					}
					return respond
				}})
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.CloseNow()
				<-conn.CloseRead(ctx).Done()
				close(closed)
			}))
			defer server.Close()
			conn, _, err := websocket.Dial(ctx, server.URL, &websocket.DialOptions{HTTPClient: server.Client()})
			if err != nil {
				t.Fatal(err)
			}
			defer conn.CloseNow()
			readCtx := conn.CloseRead(ctx)
			stop := tickHeartbeat(ctx, conn, 10*time.Millisecond, 100*time.Millisecond)
			defer stop()
			awaitSignal(t, pinged)
			if respond {
				// Repeated pings prove pong processing keeps the connection alive.
				awaitSignal(t, pinged)
				if readCtx.Err() != nil {
					t.Fatal("heartbeat closed a responsive connection")
				}
				stop()
				_ = conn.CloseNow()
			}
			awaitSignal(t, closed)
			awaitSignal(t, readCtx.Done())
		})
	}
}
