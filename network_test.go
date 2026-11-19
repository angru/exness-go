package exness

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func tlsClient(t *testing.T, server *httptest.Server, timeout time.Duration) *Client {
	t.Helper()
	c, err := NewClient(Config{Credentials: testCredentials(t), AccountID: "123456", DiscoveryURL: server.URL, Transport: server.Client().Transport, Timeout: timeout})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c
}

func awaitSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(3 * time.Second):
		t.Fatal("server synchronization timed out")
	}
}

func awaitError(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("request did not finish promptly")
		return nil
	}
}

func TestTLSCancellationAndDeadline(t *testing.T) {
	for _, mode := range []string{"cancel", "deadline", "body-timeout"} {
		t.Run(mode, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if mode == "body-timeout" {
					w.Header().Set("Content-Length", "100")
					w.WriteHeader(200)
					w.(http.Flusher).Flush()
				}
				close(entered)
				select {
				case <-release:
				case <-r.Context().Done():
				}
			}))
			defer server.Close()
			defer close(release)
			timeout := 2 * time.Second
			if mode == "body-timeout" {
				timeout = 300 * time.Millisecond
			}
			c := tlsClient(t, server, timeout)
			ctx, cancel := context.WithCancel(context.Background())
			if mode == "deadline" {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 300*time.Millisecond)
			}
			defer cancel()
			result := make(chan error, 1)
			go func() { _, err := c.Discover(ctx, false); result <- err }()
			awaitSignal(t, entered)
			if mode == "cancel" {
				cancel()
			}
			err := awaitError(t, result)
			want := context.DeadlineExceeded
			if mode == "cancel" {
				want = context.Canceled
			}
			var transport *TransportError
			if !errors.As(err, &transport) || !errors.Is(err, want) {
				t.Fatalf("wrong cancellation error: %v", err)
			}
			if strings.Contains(err.Error(), server.URL) {
				t.Fatal("transport error exposes URL")
			}
		})
	}
}

func TestTLSQueuedDiscoveryCanCancelAndClose(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release }))
	defer server.Close()
	c := tlsClient(t, server, 10*time.Second)
	first := make(chan error, 1)
	defer func() { close(release); _ = awaitError(t, first) }()
	go func() { _, err := c.Discover(context.Background(), false); first <- err }()
	awaitSignal(t, entered)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	queued := make(chan error, 1)
	go func() { _, err := c.Discover(ctx, false); queued <- err }()
	// Keep cleanup nonblocking even if the original mutex regression reappears.
	err := awaitError(t, queued)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queued cancellation: %v", err)
	}
	closed := make(chan struct{})
	go func() { c.Close(); close(closed) }()
	awaitSignal(t, closed)
	if _, err := c.Discover(context.Background(), false); err == nil {
		t.Fatal("closed client accepted discovery")
	}
}

func TestCachedDiscoveryDuringRefresh(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	c := makeClient(t, func(r *http.Request) (*http.Response, error) {
		if calls.Add(1) == 2 {
			close(entered)
			<-release
			return response(200, `{"access_point":"https://migrated.example","account_id":"123456"}`), nil
		}
		return discoveryResponse(), nil
	})
	if _, err := c.Discover(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	refresh := make(chan error, 1)
	go func() { _, err := c.Discover(context.Background(), true); refresh <- err }()
	defer func() {
		close(release)
		if err := awaitError(t, refresh); err != nil {
			t.Error(err)
		}
	}()
	awaitSignal(t, entered)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	read := make(chan error, 1)
	go func() {
		host, err := c.Discover(ctx, false)
		if err == nil && host != "https://account.example" {
			err = errors.New("cached host changed before refresh completed")
		}
		read <- err
	}()
	if err := awaitError(t, read); err != nil {
		t.Fatalf("cached read blocked by refresh: %v", err)
	}
	cancel()
	go func() { _, err := c.Discover(ctx, false); read <- err }()
	if err := awaitError(t, read); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled cached call: %v", err)
	}
}

func TestTLSTruncatedResponse(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer connection.Close()
		_, _ = buffer.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 100\r\nConnection: close\r\n\r\n{\"access_point\":")
		_ = buffer.Flush()
	}))
	defer server.Close()
	c := tlsClient(t, server, 2*time.Second)
	_, err := c.Discover(context.Background(), false)
	var transport *TransportError
	if !errors.As(err, &transport) || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated body: %v", err)
	}
}

func TestTLSRedirectDoesNotForwardCredentials(t *testing.T) {
	var hits atomic.Int32
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer target.Close()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	c := tlsClient(t, server, 2*time.Second)
	_, err := c.Discover(context.Background(), false)
	var api *APIError
	if !errors.As(err, &api) || api.StatusCode != 307 || hits.Load() != 0 {
		t.Fatalf("redirect followed or wrong error: %v", err)
	}
}

func TestTLSConnectionReuseAndClose(t *testing.T) {
	var connections atomic.Int32
	closed := make(chan struct{}, 4)
	requests := make(chan *http.Request, 3)
	account := readFixture(t, "account")
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.Clone(r.Context())
		if r.URL.Path == "/v1/trading/access-point" {
			_ = json.NewEncoder(w).Encode(map[string]string{"access_point": "https://" + r.Host, "account_id": "123456"})
		} else {
			_, _ = w.Write(account)
		}
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
		if state == http.StateClosed {
			closed <- struct{}{}
		}
	}
	server.StartTLS()
	defer server.Close()
	c := tlsClient(t, server, 2*time.Second)
	for range 2 {
		if _, err := c.GetAccount(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if connections.Load() != 1 {
		t.Fatalf("expected one reusable connection, got %d", connections.Load())
	}
	for range 3 {
		r := <-requests
		// Server-side GET bodies are http.NoBody, unlike outgoing requests.
		if r.Body != http.NoBody {
			t.Fatal("unexpected request body")
		}
		r.Body = nil
		verifyRequest(t, r)
	}
	c.Close()
	awaitSignal(t, closed)
	if _, err := c.GetAccount(context.Background()); err == nil {
		t.Fatal("closed client accepted a request")
	}
}
