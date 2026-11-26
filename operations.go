package exness

import (
	"context"
	"errors"
	"strconv"
	"time"
)

// OperationReference identifies an operation within one account. Empty fields are absent.
// A key-only reference can match events but cannot poll status.
type OperationReference struct {
	OperationID     string
	ClientRequestID string
}

// String and GoString omit identity values from routine formatting.
func (r OperationReference) String() string   { return "OperationReference{redacted}" }
func (r OperationReference) GoString() string { return r.String() }

func (r OperationReference) validate() error {
	if r.OperationID == "" && r.ClientRequestID == "" {
		return errors.New("operation identity is required")
	}
	if r.OperationID != "" && !validTradingID(r.OperationID) {
		return errors.New("invalid operation ID")
	}
	if r.ClientRequestID != "" && !idempotencyPattern.MatchString(r.ClientRequestID) {
		return errors.New("invalid client request ID")
	}
	return nil
}

// OperationReferenceFromAck fills a missing echo with the persisted caller key.
// Pass an empty clientRequestID when no caller key is available.
func OperationReferenceFromAck(ack AckResponse, clientRequestID string) (OperationReference, error) {
	if ack.Status != "accepted" {
		return OperationReference{}, &ProtocolError{"Invalid acknowledgement status"}
	}
	r := OperationReference{ClientRequestID: clientRequestID}
	if ack.OperationID != nil {
		if !validTradingID(*ack.OperationID) {
			return OperationReference{}, &ProtocolError{"Invalid acknowledgement operation ID"}
		}
		r.OperationID = *ack.OperationID
	}
	if ack.ClientRequestID != nil {
		if !idempotencyPattern.MatchString(*ack.ClientRequestID) {
			return OperationReference{}, errors.New("invalid client request ID")
		}
		if clientRequestID != "" && *ack.ClientRequestID != clientRequestID {
			return OperationReference{}, &ProtocolError{"Acknowledgement client request ID mismatch"}
		}
		r.ClientRequestID = *ack.ClientRequestID
	}
	return r, r.validate()
}

func (r OperationReference) matches(operationID, clientRequestID *string) (bool, error) {
	if err := r.validate(); err != nil {
		return false, err
	}
	if operationID != nil && !validTradingID(*operationID) {
		return false, &ProtocolError{"Invalid operation correlation ID"}
	}
	matched, mismatched := false, false
	if operationID != nil && r.OperationID != "" {
		left, _ := strconv.ParseUint(r.OperationID, 10, 64)
		right, _ := strconv.ParseUint(*operationID, 10, 64)
		matched, mismatched = left == right, left != right
	}
	if clientRequestID != nil && r.ClientRequestID != "" {
		matched = matched || r.ClientRequestID == *clientRequestID
		mismatched = mismatched || r.ClientRequestID != *clientRequestID
	}
	if matched && mismatched {
		return false, &ProtocolError{"Conflicting operation correlation identifiers"}
	}
	return matched, nil
}

// MatchesStatus matches identity, rejecting contradictory matching evidence.
func (r OperationReference) MatchesStatus(status OperationStatus) (bool, error) {
	return r.matches(&status.OperationID, status.ClientRequestID)
}

// MatchesEvent matches identity only; an event without status does not prove completion.
func (r OperationReference) MatchesEvent(event TransactionEvent) (bool, error) {
	return r.matches(event.OperationID, event.ClientRequestID)
}

// OperationWaitOptions controls explicit read-only polling. Zero values select defaults.
type OperationWaitOptions struct {
	Timeout      time.Duration // Default 30 seconds; includes discovery, reads and delays.
	PollInterval time.Duration // Default 1 second; delay after each pending result.
}

// OperationWaitTimeoutError preserves identity and the last correlated status.
// It does not imply that the operation failed; Unwrap preserves DeadlineExceeded.
type OperationWaitTimeoutError struct {
	Reference  OperationReference
	LastStatus *OperationStatus
	Attempts   int // Status reads started, including discovery for the first read.
}

func (e *OperationWaitTimeoutError) Error() string {
	return "Operation wait timed out; reconcile before retrying"
}
func (e *OperationWaitTimeoutError) Unwrap() error { return context.DeadlineExceeded }

// WaitOperation reads until terminal or deadline. Rejected/failed outcomes are data.
// Read errors stop immediately. It never submits or retries a trading write.
func (c *Client) WaitOperation(ctx context.Context, reference OperationReference, options OperationWaitOptions) (*OperationStatus, error) {
	if err := reference.validate(); err != nil {
		return nil, err
	}
	if reference.OperationID == "" {
		return nil, errors.New("status polling requires an operation ID")
	}
	if ctx == nil {
		return nil, errors.New("context is required")
	}
	if options.Timeout < 0 || options.PollInterval < 0 {
		return nil, errors.New("wait durations must not be negative")
	}
	if options.Timeout == 0 {
		options.Timeout = 30 * time.Second
	}
	if options.PollInterval == 0 {
		options.PollInterval = time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()
	var last *OperationStatus
	attempts := 0
	interrupted := func() error {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return &OperationWaitTimeoutError{Reference: reference, LastStatus: last, Attempts: attempts}
		}
		return &TransportError{ctx.Err()}
	}
	for {
		if ctx.Err() != nil {
			return nil, interrupted()
		}
		attempts++
		status, err := c.GetOperationStatus(ctx, reference.OperationID)
		if ctx.Err() != nil {
			return nil, interrupted()
		}
		if err != nil {
			return nil, err
		}
		matched, err := reference.MatchesStatus(*status)
		if err != nil {
			return nil, err
		}
		if !matched {
			return nil, &ProtocolError{"Operation status identity mismatch"}
		}
		last = status
		if status.IsTerminal() {
			return status, nil
		}
		timer := time.NewTimer(options.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, interrupted()
		case <-timer.C:
		}
	}
}
