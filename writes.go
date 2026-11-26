package exness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
)

// UnknownOutcomeError means a submitted write may have reached the server.
// Identity fields are omitted from Error; Path excludes query values.
// Unwrap preserves APIError/ProtocolError/TransportError and context cancellation.
type UnknownOutcomeError struct {
	Method         string
	Path           string
	AccountID      string
	IdempotencyKey string
	cause          error
}

func (e *UnknownOutcomeError) Error() string {
	return "Trading request outcome is unknown; reconcile before retrying"
}
func (e *UnknownOutcomeError) Unwrap() error { return e.cause }

func (c *Client) submit(ctx context.Context, method, resource string, body []byte, query url.Values, key string) (*AckResponse, error) {
	if !idempotencyPattern.MatchString(key) {
		return nil, errors.New("invalid idempotency key: expected 5..128 URL-safe ASCII characters")
	}
	host, err := c.Discover(ctx, false)
	if err != nil {
		return nil, err
	}
	path := "/v1/trading/accounts/" + c.accountID + "/" + resource
	u, err := url.Parse(host + path)
	if err != nil {
		return nil, errors.New("invalid request URL")
	}
	u.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("invalid request context or URL")
	}
	// Never offer a replayable body to the default HTTP transport.
	request.GetBody = nil
	request.Header, err = c.credentials.signRequest(method, request.URL.RequestURI(), body, key, c.clock())
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	if len(body) != 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return nil, errors.New("Exness Client is closed")
	}
	if err := ctx.Err(); err != nil {
		return nil, &TransportError{cause: err}
	}
	unknown := func(cause error) (*AckResponse, error) {
		return nil, &UnknownOutcomeError{Method: method, Path: path, AccountID: c.accountID, IdempotencyKey: key, cause: cause}
	}
	response, err := c.http.Do(request)
	if err != nil {
		return unknown(&TransportError{cause: err})
	}
	defer response.Body.Close()
	const maxBody = 16 << 20
	received, err := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	if err != nil {
		return unknown(&TransportError{cause: err})
	}
	if len(received) > maxBody {
		return unknown(&ProtocolError{"API response exceeds 16 MiB"})
	}
	if response.StatusCode != http.StatusAccepted {
		apiErr := decodeAPIError(response, received)
		if response.StatusCode >= 400 && response.StatusCode < 500 && response.StatusCode != 408 {
			return nil, apiErr
		}
		return unknown(apiErr)
	}
	var ack AckResponse
	if err := json.Unmarshal(received, &ack); err != nil {
		return unknown(&ProtocolError{"Invalid acknowledgement response"})
	}
	if ack.ClientRequestID != nil && *ack.ClientRequestID != key {
		return unknown(&ProtocolError{"Acknowledgement returned a different client request id"})
	}
	return &ack, nil
}

// OpenPosition submits once. An accepted ACK is not execution success.
func (c *Client) OpenPosition(ctx context.Context, request OpenPositionRequest, key string) (*AckResponse, error) {
	body, err := request.MarshalJSON()
	if err != nil {
		return nil, err
	}
	return c.submit(ctx, "POST", "positions", body, nil, key)
}

// PlacePendingOrder submits one limit, stop or SLTP order without retries.
func (c *Client) PlacePendingOrder(ctx context.Context, request PendingOrderRequest, key string) (*AckResponse, error) {
	if request == nil {
		return nil, errors.New("pending-order request is required")
	}
	body, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	if bytes.Equal(body, []byte("null")) {
		return nil, errors.New("pending-order request is required")
	}
	return c.submit(ctx, "POST", "orders", body, nil, key)
}

// ModifyOrder preserves omitted SL/TP and clears explicitly removed levels.
func (c *Client) ModifyOrder(ctx context.Context, orderID string, request ModifyOrderRequest, key string) (*AckResponse, error) {
	if !validTradingID(orderID) {
		return nil, errors.New("order ID must be a uint64 decimal string")
	}
	body, err := request.MarshalJSON()
	if err != nil {
		return nil, err
	}
	return c.submit(ctx, "PATCH", "orders/"+orderID, body, nil, key)
}

// CancelPendingOrder requests cancellation; its ACK is not a final result.
func (c *Client) CancelPendingOrder(ctx context.Context, orderID, key string) (*AckResponse, error) {
	if !validTradingID(orderID) {
		return nil, errors.New("order ID must be a uint64 decimal string")
	}
	return c.submit(ctx, "DELETE", "orders/"+orderID, nil, nil, key)
}

// ClosePosition requests full, partial or close-by closure once using signed query options.
func (c *Client) ClosePosition(ctx context.Context, positionID string, request ClosePositionRequest, key string) (*AckResponse, error) {
	if !validTradingID(positionID) {
		return nil, errors.New("position ID must be a uint64 decimal string")
	}
	query, err := request.Query()
	if err != nil {
		return nil, err
	}
	return c.submit(ctx, "DELETE", "positions/"+positionID, nil, query, key)
}
