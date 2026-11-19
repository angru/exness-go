package exness

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"iter"
	"net"
	"net/http"
	"slices"
	"time"

	"github.com/coder/websocket"
)

// TickStreamOptions controls one iteration's lifetime reconnect budget. Zero values
// select defaults, except MaxReconnects, whose zero disables reconnection.
type TickStreamOptions struct {
	MaxReconnects     int
	ReconnectDelay    time.Duration // Default 1 second.
	MaxReconnectDelay time.Duration // Default 30 seconds.
	OpenTimeout       time.Duration // Default 15 seconds, handshake only.
	MaxMessageBytes   int64         // Default 64 KiB; maximum 16 MiB.
}

func (o TickStreamOptions) normalized() (TickStreamOptions, error) {
	if o.MaxReconnects < 0 || o.MaxReconnects > 1000 {
		return o, errors.New("MaxReconnects must be between 0 and 1000")
	}
	if o.ReconnectDelay == 0 {
		o.ReconnectDelay = time.Second
	}
	if o.MaxReconnectDelay == 0 {
		o.MaxReconnectDelay = 30 * time.Second
	}
	if o.OpenTimeout == 0 {
		o.OpenTimeout = 15 * time.Second
	}
	if o.MaxMessageBytes == 0 {
		o.MaxMessageBytes = 65536
	}
	if o.ReconnectDelay < 0 || o.OpenTimeout < 0 || o.MaxReconnectDelay < o.ReconnectDelay {
		return o, errors.New("stream delays/timeouts must be positive and maximum delay must cover initial delay")
	}
	if o.MaxMessageBytes < 1 || o.MaxMessageBytes > 16<<20 {
		return o, errors.New("MaxMessageBytes must be between 1 and 16 MiB")
	}
	return o, nil
}

// TickStreamGap signals possible loss; the server cannot replay or count missed ticks.
type TickStreamGap struct {
	ReconnectAttempt int
	Delay            time.Duration
	CloseCode        *int // Nil if a connection couldn't be established.
}

// TickStreamEvent contains exactly one Tick or Gap when yielded without an error.
type TickStreamEvent struct {
	Tick *Tick
	Gap  *TickStreamGap
}

// StreamClosed reports a terminal remote close code or local message-size limit.
// Arbitrary server close reason text is deliberately omitted.
type StreamClosed struct{ Code int }

func (e *StreamClosed) Error() string {
	return fmt.Sprintf("Exness WebSocket closed with code %d", e.Code)
}

type tickSession struct{ cancel context.CancelFunc }

func (c *Client) beginTicks(ctx context.Context) (context.Context, func(), error) {
	if ctx == nil {
		return nil, nil, errors.New("invalid request context")
	}
	ctx, cancel := context.WithCancel(ctx)
	session := &tickSession{cancel: cancel}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		cancel()
		return nil, nil, errors.New("Exness Client is closed")
	}
	if c.tickSessions == nil {
		c.tickSessions = make(map[*tickSession]struct{})
	}
	c.tickSessions[session] = struct{}{}
	c.mu.Unlock()
	return ctx, func() {
		cancel()
		c.mu.Lock()
		delete(c.tickSessions, session)
		c.mu.Unlock()
	}, nil
}

// StreamTicks lazily yields live ticks and explicit gap events. Break or cancel ctx
// to release the connection. No background tick queue, sorting or deduplication is
// performed. The instrument list is copied; each iteration gets a fresh budget.
func (c *Client) StreamTicks(ctx context.Context, instruments []string, options TickStreamOptions) iter.Seq2[TickStreamEvent, error] {
	instruments = slices.Clone(instruments)
	return func(yield func(TickStreamEvent, error) bool) {
		fail := func(err error) { yield(TickStreamEvent{}, err) }
		options, err := options.normalized()
		if err != nil {
			fail(err)
			return
		}
		if _, _, err := tickSubscription(instruments, "validation"); err != nil {
			fail(err)
			return
		}
		ctx, finish, err := c.beginTicks(ctx)
		if err != nil {
			fail(err)
			return
		}
		defer finish()
		delay := options.ReconnectDelay
		attempts := 0
		path := "/v1/server-events/accounts/" + c.accountID + "/ws/ticks"
		for {
			if err := ctx.Err(); err != nil {
				fail(&TransportError{cause: err})
				return
			}
			host, err := c.Discover(ctx, false)
			if err != nil {
				fail(err)
				return
			}
			var nonce [16]byte
			if _, err := rand.Read(nonce[:]); err != nil {
				fail(&TransportError{cause: err})
				return
			}
			requestID := hex.EncodeToString(nonce[:])
			data, names, err := tickSubscription(instruments, requestID)
			if err != nil {
				fail(err)
				return
			}
			if int64(len(data)) > options.MaxMessageBytes {
				fail(errors.New("subscription exceeds MaxMessageBytes"))
				return
			}
			connected, stopped, err := c.tickConnection(ctx, host+path, path, data, names, requestID, options, yield)
			if stopped {
				return
			}
			if ctx.Err() != nil {
				fail(&TransportError{cause: ctx.Err()})
				return
			}
			code := websocket.CloseStatus(err)
			if code == websocket.StatusNormalClosure {
				return
			}
			var api *APIError
			var protocol *ProtocolError
			var subscription *WebSocketAPIError
			var closed *StreamClosed
			if errors.As(err, &api) || errors.As(err, &protocol) || errors.As(err, &subscription) || errors.As(err, &closed) {
				fail(err)
				return
			}
			if code != -1 && code != 1001 && code != 1006 && code != 1011 && code != 1012 {
				fail(&StreamClosed{Code: int(code)})
				return
			}
			if permanentTLS(err) {
				fail(&TransportError{cause: err})
				return
			}
			if code == -1 && !transientTickError(err) {
				fail(&ProtocolError{"Invalid WebSocket handshake or frame"})
				return
			}
			if attempts >= options.MaxReconnects {
				fail(&TransportError{cause: fmt.Errorf("tick reconnect budget exhausted: %w", err)})
				return
			}
			attempts++
			var closeCode *int
			if connected {
				value := int(code)
				if value == -1 {
					value = 1006
				}
				closeCode = &value
			}
			if !yield(TickStreamEvent{Gap: &TickStreamGap{attempts, delay, closeCode}}, nil) {
				return
			}
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				fail(&TransportError{cause: ctx.Err()})
				return
			case <-timer.C:
			}
			// Avoid overflowing time.Duration on caller-supplied large delays.
			if delay > options.MaxReconnectDelay/2 {
				delay = options.MaxReconnectDelay
			} else {
				delay *= 2
			}
		}
	}
}

func permanentTLS(err error) bool {
	var verification *tls.CertificateVerificationError
	var unknown x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var certificate x509.CertificateInvalidError
	var record tls.RecordHeaderError
	var alert tls.AlertError
	return errors.As(err, &verification) || errors.As(err, &unknown) || errors.As(err, &hostname) ||
		errors.As(err, &certificate) || errors.As(err, &record) || errors.As(err, &alert)
}

func transientTickError(err error) bool {
	var network net.Error
	return errors.As(err, &network) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed)
}

func (c *Client) tickConnection(ctx context.Context, url, path string, subscription []byte, names map[string]bool, requestID string, options TickStreamOptions, yield func(TickStreamEvent, error) bool) (connected, stopped bool, err error) {
	// coder/websocket converts this Timeout to a handshake-only context, clearing
	// http.Client.Timeout before the upgrade. It is not a tick inactivity timeout.
	httpClient := &http.Client{Transport: c.http.Transport, Timeout: options.OpenTimeout, CheckRedirect: c.http.CheckRedirect}
	conn, response, err := websocket.Dial(ctx, url, &websocket.DialOptions{
		HTTPClient: httpClient, HTTPHeader: c.credentials.signGET(path, c.clock()),
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		if response != nil {
			if response.StatusCode != http.StatusSwitchingProtocols {
				return false, false, &APIError{StatusCode: response.StatusCode, RetryAfter: response.Header.Get("Retry-After")}
			}
			return false, false, &ProtocolError{"Invalid WebSocket handshake"}
		}
		return false, false, err
	}
	defer conn.CloseNow()
	stopCancellation := context.AfterFunc(ctx, func() { _ = conn.CloseNow() })
	defer stopCancellation()
	stopHeartbeat := tickHeartbeat(ctx, conn, 20*time.Second, 20*time.Second)
	defer stopHeartbeat()
	// Read one extra byte ourselves so local size-limit failures have a typed code,
	// independent of the transport library's limit error representation.
	conn.SetReadLimit(options.MaxMessageBytes + 1)
	if err := conn.Write(ctx, websocket.MessageText, subscription); err != nil {
		return true, false, err
	}
	for {
		if err := ctx.Err(); err != nil {
			return true, false, err
		}
		_, reader, err := conn.Reader(ctx)
		if err != nil {
			return true, false, err
		}
		data, err := io.ReadAll(io.LimitReader(reader, options.MaxMessageBytes+1))
		if int64(len(data)) > options.MaxMessageBytes {
			return true, false, &StreamClosed{Code: 1009}
		}
		if err != nil {
			return true, false, err
		}
		tick, err := decodeTick(data, names, requestID)
		if err != nil {
			return true, false, err
		}
		if !yield(TickStreamEvent{Tick: tick}, nil) {
			return true, true, nil
		}
	}
}

// No tick reader goroutine is created. Ping/pong progress requires the consumer to
// keep reading; a stalled consumer can lose its connection after the heartbeat budget.
func tickHeartbeat(ctx context.Context, conn *websocket.Conn, interval, timeout time.Duration) func() {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		timer := time.NewTicker(interval)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				pingCtx, pingCancel := context.WithTimeout(ctx, timeout)
				err := conn.Ping(pingCtx)
				pingCancel()
				if err != nil {
					_ = conn.CloseNow()
					return
				}
			}
		}
	}()
	return func() { cancel(); <-done }
}
