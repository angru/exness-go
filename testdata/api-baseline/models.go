package exness

import (
	"encoding/json"
	"errors"
	"math/big"
	"regexp"
	"time"
)

var decimalPattern = regexp.MustCompile(`^[+-]?[0-9]+(?:\.[0-9]+)?$`)

// Decimal preserves the API's exact decimal string. Use Rat for exact arithmetic.
// The zero value is unset, not a numeric zero. Construct with NewDecimal.
type Decimal struct{ text string }

func NewDecimal(value string) (Decimal, error) {
	if !decimalPattern.MatchString(value) {
		return Decimal{}, errors.New("expected plain finite decimal string")
	}
	return Decimal{text: value}, nil
}

func (d Decimal) String() string { return d.text }
func (d Decimal) Rat() (*big.Rat, error) {
	value, ok := new(big.Rat).SetString(d.text)
	if !ok {
		return nil, errors.New("decimal is unset")
	}
	return value, nil
}
func (d Decimal) MarshalJSON() ([]byte, error) {
	if d.text == "" {
		return nil, errors.New("decimal is unset")
	}
	return json.Marshal(d.text)
}
func (d *Decimal) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return errors.New("expected JSON decimal string")
	}
	value, err := NewDecimal(text)
	if err != nil {
		return err
	}
	*d = value
	return nil
}

type AccountSettings struct {
	Currency        string   `json:"currency"`
	Leverage        string   `json:"leverage"`
	AccountStatus   string   `json:"account_status"`
	TradeMode       string   `json:"trade_mode"`
	ExecutionMode   string   `json:"execution_mode"`
	MarginCallLevel *Decimal `json:"margin_call_level,omitempty"`
	StopoutLevel    *Decimal `json:"stopout_level,omitempty"`
}

type AccountDetails struct {
	AccountID   string           `json:"account_id"`
	CreateDate  time.Time        `json:"create_date"`
	AccountName *string          `json:"account_name,omitempty"`
	Settings    *AccountSettings `json:"settings,omitempty"`
}

type AccountState struct {
	Balance    Decimal `json:"balance"`
	Equity     Decimal `json:"equity"`
	UsedMargin Decimal `json:"used_margin"`
}

type Instruments struct {
	Instruments []string `json:"instruments"`
}

type Order struct {
	OrderID         string     `json:"order_id"`
	OrderType       string     `json:"order_type"`
	State           string     `json:"state"`
	CreateTime      time.Time  `json:"create_time"`
	PositionID      *string    `json:"position_id,omitempty"`
	Direction       *string    `json:"direction,omitempty"`
	Instrument      *string    `json:"instrument,omitempty"`
	Price           *Decimal   `json:"price,omitempty"`
	StopLossPrice   *Decimal   `json:"stop_loss_price,omitempty"`
	TakeProfitPrice *Decimal   `json:"take_profit_price,omitempty"`
	UpdateTime      *time.Time `json:"update_time,omitempty"`
	Volume          *Decimal   `json:"volume,omitempty"`
	Reason          *string    `json:"reason,omitempty"`
}

type Position struct {
	PositionID    string     `json:"position_id"`
	Direction     string     `json:"direction"`
	Instrument    string     `json:"instrument"`
	State         string     `json:"state"`
	CreateTime    time.Time  `json:"create_time"`
	Volume        Decimal    `json:"volume"`
	InitialVolume *Decimal   `json:"initial_volume,omitempty"`
	OpenPrice     *Decimal   `json:"open_price,omitempty"`
	MarginRate    *Decimal   `json:"margin_rate,omitempty"`
	Commission    *Decimal   `json:"commission,omitempty"`
	Swap          *Decimal   `json:"swap,omitempty"`
	RolloverTime  *time.Time `json:"rollover_time,omitempty"`
}

type TradingSnapshot struct {
	AccountState AccountState `json:"account_state"`
	Orders       []Order      `json:"orders"`
	Positions    []Position   `json:"positions"`
}

type ProfitAmount struct {
	Amount   *Decimal `json:"amount,omitempty"`
	Currency *string  `json:"currency,omitempty"`
}

type Deal struct {
	DealID     string        `json:"deal_id"`
	Type       DealType      `json:"type"`
	CreateTime time.Time     `json:"create_time"`
	OrderID    *string       `json:"order_id,omitempty"`
	PositionID *string       `json:"position_id,omitempty"`
	Direction  *string       `json:"direction,omitempty"`
	Instrument *string       `json:"instrument,omitempty"`
	Volume     *Decimal      `json:"volume,omitempty"`
	Price      *Decimal      `json:"price,omitempty"`
	Profit     *ProfitAmount `json:"profit,omitempty"`
	Commission *Decimal      `json:"commission,omitempty"`
}

type OrderHistoryItem struct {
	EventTime time.Time `json:"event_time"`
	Order     Order     `json:"order"`
}

type DealHistoryItem struct {
	EventTime time.Time `json:"event_time"`
	Deal      Deal      `json:"deal"`
}

type HistoryRange struct {
	From *time.Time `json:"from,omitempty"`
	To   *time.Time `json:"to,omitempty"`
}

type HistoryPage[T any] struct {
	Items      []T           `json:"items"`
	NextCursor *string       `json:"next_cursor,omitempty"`
	HasMore    *bool         `json:"has_more,omitempty"`
	Range      *HistoryRange `json:"range,omitempty"`
}

type RateLimit struct {
	Limit         int `json:"limit"`
	WindowSeconds int `json:"window_seconds"`
}
type RequestLimits struct {
	MaxCandlesPerRequest            *int    `json:"max_candles_per_request,omitempty"`
	MaxCandlesPerRequestDescription *string `json:"max_candles_per_request_description,omitempty"`
	HistoryRangeDays                *int    `json:"history_range_days,omitempty"`
	HistoryAgeDays                  *int    `json:"history_age_days,omitempty"`
}
type RestMethodLimits struct {
	APIOperationID string         `json:"api_operation_id"`
	HTTPMethod     string         `json:"http_method"`
	Path           string         `json:"path"`
	RateLimit      RateLimit      `json:"rate_limit"`
	Limits         *RequestLimits `json:"limits,omitempty"`
}
type RestLimits struct {
	GlobalAccountRate RateLimit          `json:"global_account_rate"`
	Methods           []RestMethodLimits `json:"methods"`
}
type TokenBucketLimit struct {
	Limit         int     `json:"limit"`
	WindowSeconds float64 `json:"window_seconds"`
	Burst         int     `json:"burst"`
}
type StrikeLimits struct {
	RefillAmount int `json:"refill_amount"`
	Threshold    int `json:"threshold"`
	PeriodMS     int `json:"period_ms"`
}
type WebSocketAccountLimits struct {
	MaxActiveConnections   int              `json:"max_active_connections"`
	MaxInboundMessageBytes int              `json:"max_inbound_message_bytes"`
	ConnectionRate         TokenBucketLimit `json:"connection_rate"`
	InboundMessageRate     TokenBucketLimit `json:"inbound_message_rate"`
	Strikes                StrikeLimits     `json:"strikes"`
}
type WebSocketEndpointLimits struct {
	APIOperationID            string                      `json:"api_operation_id"`
	HTTPMethod                string                      `json:"http_method"`
	Path                      string                      `json:"path"`
	ConnectionRate            TokenBucketLimit            `json:"connection_rate"`
	InboundMessageRate        TokenBucketLimit            `json:"inbound_message_rate"`
	SubscriptionOperationRate map[string]TokenBucketLimit `json:"subscription_operation_rate"`
}
type WebSocketLimits struct {
	GlobalAccountLimits WebSocketAccountLimits    `json:"global_account_limits"`
	Endpoints           []WebSocketEndpointLimits `json:"endpoints"`
}
type APILimits struct {
	Rest      RestLimits      `json:"rest"`
	WebSocket WebSocketLimits `json:"websocket"`
}
type Limits struct {
	Limits APILimits `json:"limits"`
}
