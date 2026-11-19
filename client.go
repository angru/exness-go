// Package exness provides an unofficial read-only Exness Trading API client.
package exness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// APIError reports an unsuccessful REST response or WebSocket handshake. ErrorMessage is untrusted server text.
type APIError struct {
	StatusCode   int
	Code         *int
	ErrorMessage *string
	RetryAfter   string
}

func (e *APIError) Error() string {
	if e.Code != nil {
		return fmt.Sprintf("Exness API returned HTTP %d (code=%d)", e.StatusCode, *e.Code)
	}
	return fmt.Sprintf("Exness API returned HTTP %d", e.StatusCode)
}

type ProtocolError struct{ Message string }

func (e *ProtocolError) Error() string { return e.Message }

// TransportError keeps a sanitized message, while supporting errors.Is/As for its cause.
type TransportError struct{ cause error }

func (e *TransportError) Error() string { return "Exness request failed at the transport layer" }
func (e *TransportError) Unwrap() error { return e.cause }

type Config struct {
	Credentials  *Credentials
	AccountID    string
	DiscoveryURL string
	Timeout      time.Duration     // REST timeout; zero selects 15 seconds. WebSockets use StreamOptions.OpenTimeout.
	Transport    http.RoundTripper // Optional; Close closes its idle connections.
}

// Client is safe for concurrent use. Close prevents new operations and cancels
// active streams; in-flight REST calls may finish.
type Client struct {
	credentials    Credentials
	accountID      string
	discoveryURL   string
	http           *http.Client
	mu             sync.Mutex
	discoveryGate  chan struct{}
	accessPoint    string
	closed         bool
	clock          func() time.Time
	streamSessions map[*streamSession]struct{}
}

var accountPattern = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)

func validAccount(value string) bool {
	_, err := strconv.ParseUint(value, 10, 64)
	return err == nil && accountPattern.MatchString(value)
}

func baseURL(value string) (string, error) {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery ||
		strings.Contains(value, "#") || u.Opaque != "" || u.RawPath != "" {
		return "", errors.New("API host must be an HTTPS origin without credentials, path or query")
	}
	return strings.TrimSuffix(u.String(), "/"), nil
}

func NewClient(config Config) (*Client, error) {
	if config.Credentials == nil || len(config.Credentials.privateKey) != 64 || config.Credentials.apiKey == "" {
		return nil, errors.New("valid Credentials are required")
	}
	if !validAccount(config.AccountID) {
		return nil, errors.New("AccountID must be a positive uint64 decimal string")
	}
	host, err := baseURL(config.DiscoveryURL)
	if err != nil {
		return nil, err
	}
	if config.Timeout < 0 {
		return nil, errors.New("Timeout must not be negative")
	}
	if config.Timeout == 0 {
		config.Timeout = 15 * time.Second
	}
	transport := config.Transport
	if transport == nil {
		// Use a dedicated pool without environment proxy settings.
		transport = &http.Transport{
			ForceAttemptHTTP2: true, MaxIdleConns: 100, IdleConnTimeout: 90 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second, ExpectContinueTimeout: time.Second,
		}
	}
	return &Client{
		credentials: *config.Credentials, accountID: config.AccountID, discoveryURL: host,
		clock: time.Now, discoveryGate: make(chan struct{}, 1),
		http: &http.Client{
			Timeout: config.Timeout, Transport: transport,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

func (c *Client) Close() {
	c.mu.Lock()
	c.closed = true
	for session := range c.streamSessions {
		session.cancel()
	}
	c.mu.Unlock()
	c.http.CloseIdleConnections()
}

func (c *Client) get(ctx context.Context, host, path string, query url.Values, result any) error {
	u, err := url.Parse(host + path)
	if err != nil {
		return errors.New("invalid request URL")
	}
	u.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return errors.New("invalid request context or URL")
	}
	request.Header = c.credentials.signGET(request.URL.RequestURI(), c.clock())
	request.Header.Set("Accept", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return &TransportError{cause: err}
	}
	defer response.Body.Close()
	const maxBody = 16 << 20
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	if err != nil {
		return &TransportError{cause: err}
	}
	if len(body) > maxBody {
		return &ProtocolError{"API response exceeds 16 MiB"}
	}
	if response.StatusCode != http.StatusOK {
		var public struct {
			Code    *int    `json:"code"`
			Message *string `json:"error_message"`
		}
		if json.Unmarshal(body, &public) != nil || public.Code == nil || public.Message == nil {
			public.Code, public.Message = nil, nil
		}
		return &APIError{response.StatusCode, public.Code, public.Message, response.Header.Get("Retry-After")}
	}
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" || trimmed[0] != '{' {
		return &ProtocolError{"Expected a JSON object response"}
	}
	if err := json.Unmarshal(body, result); err != nil {
		return &ProtocolError{"Invalid API response"}
	}
	return nil
}

// Discover resolves the account host once. Set refresh after an account migration.
func (c *Client) Discover(ctx context.Context, refresh bool) (string, error) {
	if ctx == nil {
		return "", errors.New("invalid request context")
	}
	if err := ctx.Err(); err != nil {
		return "", &TransportError{cause: err}
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return "", errors.New("Exness Client is closed")
	}
	if c.accessPoint != "" && !refresh {
		host := c.accessPoint
		c.mu.Unlock()
		return host, nil
	}
	c.mu.Unlock()
	// Serialize discovery without holding the state mutex across network I/O.
	// A caller waiting for another discovery must still honor its own context.
	select {
	case c.discoveryGate <- struct{}{}:
		defer func() { <-c.discoveryGate }()
	case <-ctx.Done():
		return "", &TransportError{cause: ctx.Err()}
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return "", errors.New("Exness Client is closed")
	}
	if c.accessPoint != "" && !refresh {
		host := c.accessPoint
		c.mu.Unlock()
		return host, nil
	}
	c.mu.Unlock()
	var result struct {
		AccessPoint string  `json:"access_point"`
		AccountID   *string `json:"account_id"`
	}
	if err := c.get(ctx, c.discoveryURL, "/v1/trading/access-point", url.Values{"account_id": {c.accountID}}, &result); err != nil {
		return "", err
	}
	if result.AccountID != nil && *result.AccountID != c.accountID {
		return "", &ProtocolError{"Discovery returned a different account"}
	}
	host, err := baseURL(result.AccessPoint)
	if err != nil {
		return "", &ProtocolError{"Discovery returned an invalid HTTPS origin"}
	}
	c.mu.Lock()
	if !c.closed {
		c.accessPoint = host
	}
	c.mu.Unlock()
	return host, nil
}

func (c *Client) accountGET(ctx context.Context, service, resource string, query url.Values, result any) error {
	host, err := c.Discover(ctx, false)
	if err != nil {
		return err
	}
	return c.get(ctx, host, "/v1/"+service+"/accounts/"+c.accountID+"/"+resource, query, result)
}

func (c *Client) GetAccount(ctx context.Context) (*AccountDetails, error) {
	var result AccountDetails
	if err := c.accountGET(ctx, "configuration", "account", nil, &result); err != nil {
		return nil, err
	}
	if result.AccountID != c.accountID || result.CreateDate.IsZero() {
		return nil, &ProtocolError{"Invalid account identity or creation date"}
	}
	return &result, nil
}

func (c *Client) GetInstruments(ctx context.Context) (*Instruments, error) {
	var result Instruments
	if err := c.accountGET(ctx, "configuration", "instruments", nil, &result); err != nil {
		return nil, err
	}
	for _, instrument := range result.Instruments {
		if !instrumentPattern.MatchString(instrument) {
			return nil, &ProtocolError{"Invalid instrument name in response"}
		}
	}
	return &result, nil
}

func (c *Client) GetSnapshot(ctx context.Context) (*TradingSnapshot, error) {
	var result TradingSnapshot
	if err := c.accountGET(ctx, "trading", "snapshot", nil, &result); err != nil {
		return nil, err
	}
	if result.AccountState.Balance.text == "" || result.AccountState.Equity.text == "" || result.AccountState.UsedMargin.text == "" {
		return nil, &ProtocolError{"Missing account monetary state"}
	}
	for _, order := range result.Orders {
		if !validOrder(order) {
			return nil, &ProtocolError{"Invalid order in snapshot"}
		}
	}
	for _, position := range result.Positions {
		if !validAccount(position.PositionID) || position.CreateTime.IsZero() || position.Direction == "" || position.State == "" || position.Instrument == "" || position.Volume.text == "" {
			return nil, &ProtocolError{"Invalid position in snapshot"}
		}
	}
	return &result, nil
}

func (c *Client) GetLimits(ctx context.Context) (*Limits, error) {
	var result Limits
	if err := c.accountGET(ctx, "configuration", "limits", nil, &result); err != nil {
		return nil, err
	}
	if result.Limits.Rest.GlobalAccountRate.Limit <= 0 || result.Limits.Rest.GlobalAccountRate.WindowSeconds <= 0 || result.Limits.Rest.Methods == nil || result.Limits.WebSocket.Endpoints == nil || result.Limits.WebSocket.GlobalAccountLimits.MaxActiveConnections <= 0 {
		return nil, &ProtocolError{"Invalid API limits response"}
	}
	return &result, nil
}

func validOrder(order Order) bool {
	return validAccount(order.OrderID) && order.OrderType != "" && order.State != "" && !order.CreateTime.IsZero()
}
