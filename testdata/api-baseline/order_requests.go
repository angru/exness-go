package exness

import (
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

// PendingOrderRequest is a limit, stop or SLTP request.
type PendingOrderRequest interface {
	json.Marshaler
	pendingOrder()
}

// LimitOrderRequest places a pending limit order with optional attached SL/TP.
type LimitOrderRequest struct {
	Instrument      string    `json:"instrument"`
	Side            TradeSide `json:"side"`
	Volume          Decimal   `json:"volume"`
	Price           Decimal   `json:"price"`
	StopLossPrice   *Decimal  `json:"stop_loss_price,omitempty"`
	TakeProfitPrice *Decimal  `json:"take_profit_price,omitempty"`
	Comment         *string   `json:"comment,omitempty"`
}

type StopOrderRequest LimitOrderRequest

func (LimitOrderRequest) pendingOrder() {}
func (StopOrderRequest) pendingOrder()  {}

func marshalPendingOrder(r LimitOrderRequest, kind string) ([]byte, error) {
	check := OpenPositionRequest{
		Instrument: r.Instrument, Side: r.Side, Volume: r.Volume, Price: &r.Price,
		StopLossPrice: r.StopLossPrice, TakeProfitPrice: r.TakeProfitPrice, Comment: r.Comment,
	}
	if _, err := check.MarshalJSON(); err != nil {
		return nil, err
	}
	type fields LimitOrderRequest
	return json.Marshal(struct {
		fields
		Type string `json:"type"`
	}{fields(r), kind})
}
func (r LimitOrderRequest) MarshalJSON() ([]byte, error) { return marshalPendingOrder(r, "limit") }
func (r StopOrderRequest) MarshalJSON() ([]byte, error) {
	return marshalPendingOrder(LimitOrderRequest(r), "stop")
}

// SltpOrderRequest attaches at least one positive SL/TP level to a position.
type SltpOrderRequest struct {
	PositionID      string   `json:"position_id"`
	StopLossPrice   *Decimal `json:"stop_loss_price,omitempty"`
	TakeProfitPrice *Decimal `json:"take_profit_price,omitempty"`
	Comment         *string  `json:"comment,omitempty"`
}

func (SltpOrderRequest) pendingOrder() {}

func validTradingID(value string) bool {
	_, err := strconv.ParseUint(value, 10, 64)
	return err == nil && operationIDPattern.MatchString(value)
}

func (r SltpOrderRequest) MarshalJSON() ([]byte, error) {
	if !validTradingID(r.PositionID) || (r.StopLossPrice == nil && r.TakeProfitPrice == nil) {
		return nil, errors.New("SLTP requires a uint64 position ID and at least one price")
	}
	for _, price := range []*Decimal{r.StopLossPrice, r.TakeProfitPrice} {
		if price != nil && !positiveRequestDecimal(*price) {
			return nil, errors.New("SLTP prices must be positive")
		}
	}
	if r.Comment != nil && !utf8.ValidString(*r.Comment) {
		return nil, errors.New("comment must be valid UTF-8")
	}
	type fields SltpOrderRequest
	return json.Marshal(struct {
		fields
		Type string `json:"type"`
	}{fields(r), "sltp"})
}

// PriceChange distinguishes omission (zero value), replacement and explicit removal.
// Use SetPrice or ClearPrice when building a ModifyOrderRequest.
type PriceChange struct {
	set   bool
	value *Decimal
}

// SetPrice replaces an SL/TP level. Positivity is validated before submission.
func SetPrice(value Decimal) PriceChange { return PriceChange{set: true, value: &value} }

// ClearPrice removes an SL/TP level. It performs no network operation.
func ClearPrice() PriceChange { return PriceChange{set: true} }

type ModifyOrderRequest struct {
	Price           *Decimal
	StopLossPrice   PriceChange
	TakeProfitPrice PriceChange
	Comment         *string
}

func (r ModifyOrderRequest) MarshalJSON() ([]byte, error) {
	if r.Price == nil && !r.StopLossPrice.set && !r.TakeProfitPrice.set {
		return nil, errors.New("modification requires a price or SL/TP change")
	}
	fields := make(map[string]any)
	if r.Price != nil {
		if !positiveRequestDecimal(*r.Price) {
			return nil, errors.New("price must be positive")
		}
		fields["price"] = r.Price
	}
	for name, change := range map[string]PriceChange{"stop_loss_price": r.StopLossPrice, "take_profit_price": r.TakeProfitPrice} {
		if change.set {
			if change.value != nil && !positiveRequestDecimal(*change.value) {
				return nil, errors.New("SL/TP price must be positive")
			}
			fields[name] = change.value
		}
	}
	if r.Comment != nil {
		if !utf8.ValidString(*r.Comment) {
			return nil, errors.New("comment must be valid UTF-8")
		}
		fields["comment"] = *r.Comment
	}
	return json.Marshal(fields)
}

// ClosePositionRequest with no fields requests full closure. Options go in the query.
type ClosePositionRequest struct {
	Volume            *Decimal
	Price             *Decimal
	Deviation         *Decimal
	CloseByPositionID *string
}

func (r ClosePositionRequest) Query() (url.Values, error) {
	query := make(url.Values)
	if r.CloseByPositionID != nil {
		if !validTradingID(*r.CloseByPositionID) {
			return nil, errors.New("close-by position ID must be a uint64 decimal string")
		}
		query.Set("close_by_position_id", *r.CloseByPositionID)
	}
	if r.Volume != nil {
		rat, err := r.Volume.Rat()
		if err != nil || strings.HasPrefix(r.Volume.text, "-") || (rat.Sign() == 0 && r.CloseByPositionID == nil) {
			return nil, errors.New("close volume must be positive, or zero with a close-by position")
		}
		query.Set("volume", r.Volume.text)
	}
	if r.Price != nil {
		if !positiveRequestDecimal(*r.Price) {
			return nil, errors.New("close price must be positive")
		}
		query.Set("price", r.Price.text)
	}
	if r.Deviation != nil {
		if r.Deviation.text == "" {
			return nil, errors.New("close deviation is unset")
		}
		query.Set("deviation", r.Deviation.text)
	}
	return query, nil
}
