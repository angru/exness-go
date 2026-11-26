package exness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

const lifecycleRounds, lifecycleWorkers = 6, 6

func assertNoStreamSessions(t *testing.T, c *Client) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.streamSessions) != 0 {
		t.Fatalf("leaked %d stream sessions", len(c.streamSessions))
	}
}

func TestLifecycleConcurrentRESTCancellation(t *testing.T) {
	for cycle := range lifecycleRounds {
		t.Run(fmt.Sprint(cycle), func(t *testing.T) {
			entered, closed := make(chan struct{}, lifecycleWorkers), make(chan struct{}, lifecycleWorkers+1)
			release := make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			var opened, requests atomic.Int32
			body := readFixture(t, "account")
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.URL.Path == "/v1/trading/access-point" {
					_ = json.NewEncoder(w).Encode(map[string]string{"access_point": "https://" + r.Host, "account_id": "123456"})
					return
				}
				w.Header().Set("Content-Length", fmt.Sprint(len(body)))
				w.WriteHeader(200)
				w.(http.Flusher).Flush()
				entered <- struct{}{}
				select {
				case <-release:
					_, _ = w.Write(body)
				case <-r.Context().Done():
				}
			}))
			server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
				if state == http.StateNew {
					opened.Add(1)
				}
				if state == http.StateClosed {
					closed <- struct{}{}
				}
			}
			server.StartTLS()
			defer server.Close()
			defer unblock()
			c := tlsClient(t, server, 5*time.Second)
			defer c.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := c.Discover(ctx, false); err != nil {
				t.Fatal(err)
			}
			var cancels []context.CancelFunc
			var results []chan error
			var workers sync.WaitGroup
			defer func() { cancel(); unblock(); workers.Wait() }()
			for range lifecycleWorkers {
				requestCtx, stop := context.WithCancel(ctx)
				cancels = append(cancels, stop)
				result := make(chan error, 1)
				results = append(results, result)
				workers.Go(func() {
					defer stop()
					account, err := c.GetAccount(requestCtx)
					if err == nil && account.AccountID != "123456" {
						err = errors.New("wrong account")
					}
					result <- err
				})
			}
			for range lifecycleWorkers {
				awaitSignal(t, entered)
			}
			for i := range lifecycleWorkers / 2 {
				cancels[i]()
				if err := awaitError(t, results[i]); !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation: %v", err)
				}
			}
			unblock()
			for _, result := range results[lifecycleWorkers/2:] {
				if err := awaitError(t, result); err != nil {
					t.Fatal(err)
				}
			}
			workers.Wait()
			c.Close()
			for range opened.Load() {
				awaitSignal(t, closed)
			}
			if opened.Load() != lifecycleWorkers || requests.Load() != lifecycleWorkers+1 {
				t.Fatalf("opened=%d requests=%d", opened.Load(), requests.Load())
			}
		})
	}
}

func TestLifecycleReconnectBudgetAndCleanup(t *testing.T) {
	closed := make(chan struct{}, lifecycleRounds*3)
	c := tickTestClient(t, func(ctx context.Context, conn *websocket.Conn, _ string) {
		defer func() { closed <- struct{}{} }()
		if err := writeTick(ctx, conn); err != nil {
			t.Error(err)
		}
		_ = conn.CloseNow()
	})
	for range lifecycleRounds {
		ticks, gaps, failures := 0, 0, 0
		for event, err := range c.StreamTicks(tickContext(t), []string{"EURUSD"}, StreamOptions{MaxReconnects: 2, ReconnectDelay: time.Millisecond}) {
			if err != nil {
				var transport *TransportError
				if !errors.As(err, &transport) {
					t.Fatal(err)
				}
				failures++
			} else if event.Tick != nil {
				ticks++
			} else {
				gaps++
				if event.Gap == nil || event.Gap.ReconnectAttempt != gaps {
					t.Fatal("incorrect gap")
				}
			}
		}
		if ticks != 3 || gaps != 2 || failures != 1 {
			t.Fatalf("ticks=%d gaps=%d failures=%d", ticks, gaps, failures)
		}
		for range 3 {
			awaitSignal(t, closed)
		}
		assertNoStreamSessions(t, c)
	}
}

func TestLifecycleConcurrentStreamShutdown(t *testing.T) {
	for cycle := range lifecycleRounds {
		t.Run(fmt.Sprint(cycle), func(t *testing.T) {
			ready, closed := make(chan struct{}, lifecycleWorkers), make(chan struct{}, lifecycleWorkers)
			c := tickTestClient(t, func(ctx context.Context, conn *websocket.Conn, _ string) {
				defer func() { closed <- struct{}{} }()
				if err := writeTick(ctx, conn); err != nil {
					t.Error(err)
					return
				}
				<-conn.CloseRead(ctx).Done()
			})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var workers sync.WaitGroup
			defer func() { cancel(); c.Close(); workers.Wait() }()
			var cancels []context.CancelFunc
			var results []chan error
			for range lifecycleWorkers {
				streamCtx, stop := context.WithCancel(ctx)
				cancels = append(cancels, stop)
				result := make(chan error, 1)
				results = append(results, result)
				workers.Go(func() {
					defer stop()
					var outcome error
					for event, err := range c.StreamTicks(streamCtx, []string{"EURUSD"}, StreamOptions{}) {
						if err != nil {
							outcome = err
							break
						}
						if event.Tick != nil {
							ready <- struct{}{}
						}
					}
					result <- outcome
				})
			}
			for range lifecycleWorkers {
				awaitSignal(t, ready)
			}
			for i := range lifecycleWorkers / 2 {
				cancels[i]()
			}
			for _, result := range results[:lifecycleWorkers/2] {
				if err := awaitError(t, result); !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation: %v", err)
				}
			}
			c.Close()
			for _, result := range results[lifecycleWorkers/2:] {
				if err := awaitError(t, result); !errors.Is(err, context.Canceled) {
					t.Fatalf("close lost stream cancellation: %v", err)
				}
			}
			workers.Wait()
			for range lifecycleWorkers {
				awaitSignal(t, closed)
			}
			assertNoStreamSessions(t, c)
		})
	}
}
