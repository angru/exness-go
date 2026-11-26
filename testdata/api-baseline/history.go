package exness

import (
	"context"
	"errors"
	"iter"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"time"
)

type ClosedOrderState string

const (
	OrderFilled    ClosedOrderState = "filled"
	OrderCancelled ClosedOrderState = "cancelled"
	OrderRejected  ClosedOrderState = "rejected"
)

type DealType string

const (
	DealOpen             DealType = "open"
	DealClose            DealType = "close"
	DealBalance          DealType = "balance"
	DealNullCompensation DealType = "null_compensation"
	DealSwapFee          DealType = "swap_fee"
	DealAdminFee         DealType = "admin_fee"
	DealDividend         DealType = "dividend"
	DealExnessDollar     DealType = "exness_dollar"
)

type HistoryQuery struct {
	From       *time.Time
	To         *time.Time
	Cursor     *string
	Limit      int // Zero selects 100. Otherwise 1..200.
	Instrument string
}
type OrderHistoryQuery struct {
	HistoryQuery
	OrderState ClosedOrderState
}
type DealHistoryQuery struct {
	HistoryQuery
	DealType DealType
}

var instrumentPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,11}$`)

func historyParams(query HistoryQuery, filter, value string) (url.Values, error) {
	if query.Limit == 0 {
		query.Limit = 100
	}
	if query.Limit < 1 || query.Limit > 200 {
		return nil, errors.New("history Limit must be between 1 and 200")
	}
	params := url.Values{"limit": {strconv.Itoa(query.Limit)}}
	if query.Cursor != nil {
		if *query.Cursor == "" || query.From != nil || query.To != nil || query.Instrument != "" || value != "" {
			return nil, errors.New("nonempty Cursor can only be combined with Limit")
		}
		params.Set("cursor", *query.Cursor)
		return params, nil
	}
	if query.From == nil || query.From.IsZero() {
		return nil, errors.New("From is required without a Cursor")
	}
	params.Set("from", query.From.UTC().Format(time.RFC3339Nano))
	if query.To != nil {
		if !query.To.After(*query.From) {
			return nil, errors.New("To must be later than From")
		}
		params.Set("to", query.To.UTC().Format(time.RFC3339Nano))
	}
	if query.Instrument != "" {
		if !instrumentPattern.MatchString(query.Instrument) {
			return nil, errors.New("invalid Instrument")
		}
		params.Set("instrument", query.Instrument)
	}
	if value != "" {
		params.Set(filter, value)
	}
	return params, nil
}

func (c *Client) GetOrdersHistory(ctx context.Context, query OrderHistoryQuery) (*HistoryPage[OrderHistoryItem], error) {
	if query.OrderState != "" && !slices.Contains([]ClosedOrderState{OrderFilled, OrderCancelled, OrderRejected}, query.OrderState) {
		return nil, errors.New("invalid closed order state")
	}
	params, err := historyParams(query.HistoryQuery, "order_state", string(query.OrderState))
	if err != nil {
		return nil, err
	}
	var result HistoryPage[OrderHistoryItem]
	if err := c.accountGET(ctx, "history", "orders", params, &result); err != nil {
		return nil, err
	}
	if result.Items == nil {
		return nil, &ProtocolError{"Missing history items"}
	}
	for _, item := range result.Items {
		if item.EventTime.IsZero() || !validOrder(item.Order) {
			return nil, &ProtocolError{"Invalid order history item"}
		}
	}
	return &result, nil
}

func (c *Client) GetDealsHistory(ctx context.Context, query DealHistoryQuery) (*HistoryPage[DealHistoryItem], error) {
	if query.DealType != "" && !slices.Contains([]DealType{DealOpen, DealClose, DealBalance, DealNullCompensation, DealSwapFee, DealAdminFee, DealDividend, DealExnessDollar}, query.DealType) {
		return nil, errors.New("invalid deal type")
	}
	params, err := historyParams(query.HistoryQuery, "deal_type", string(query.DealType))
	if err != nil {
		return nil, err
	}
	var result HistoryPage[DealHistoryItem]
	if err := c.accountGET(ctx, "history", "deals", params, &result); err != nil {
		return nil, err
	}
	if result.Items == nil {
		return nil, &ProtocolError{"Missing history items"}
	}
	for _, item := range result.Items {
		if item.EventTime.IsZero() || !validDeal(item.Deal) {
			return nil, &ProtocolError{"Invalid deal history item"}
		}
	}
	return &result, nil
}

func iterate[T any](first func() (*HistoryPage[T], error), following func(string) (*HistoryPage[T], error)) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		page, err := first()
		seen := make(map[string]bool)
		var zero T
		for {
			if err != nil {
				yield(zero, err)
				return
			}
			cursor := ""
			if page.NextCursor != nil {
				cursor = *page.NextCursor
			}
			if (page.HasMore != nil && *page.HasMore != (cursor != "")) || (cursor != "" && seen[cursor]) {
				yield(zero, &ProtocolError{"Inconsistent or repeated history continuation cursor"})
				return
			}
			for _, item := range page.Items {
				if !yield(item, nil) {
					return
				}
			}
			if cursor == "" {
				return
			}
			seen[cursor] = true
			page, err = following(cursor)
		}
	}
}

// IterOrdersHistory lazily yields rows and at most one error, then stops.
func (c *Client) IterOrdersHistory(ctx context.Context, query OrderHistoryQuery) iter.Seq2[OrderHistoryItem, error] {
	return iterate(
		func() (*HistoryPage[OrderHistoryItem], error) { return c.GetOrdersHistory(ctx, query) },
		func(cursor string) (*HistoryPage[OrderHistoryItem], error) {
			return c.GetOrdersHistory(ctx, OrderHistoryQuery{HistoryQuery: HistoryQuery{Cursor: &cursor, Limit: query.Limit}})
		},
	)
}

// IterDealsHistory lazily yields rows and at most one error, then stops.
func (c *Client) IterDealsHistory(ctx context.Context, query DealHistoryQuery) iter.Seq2[DealHistoryItem, error] {
	return iterate(
		func() (*HistoryPage[DealHistoryItem], error) { return c.GetDealsHistory(ctx, query) },
		func(cursor string) (*HistoryPage[DealHistoryItem], error) {
			return c.GetDealsHistory(ctx, DealHistoryQuery{HistoryQuery: HistoryQuery{Cursor: &cursor, Limit: query.Limit}})
		},
	)
}
