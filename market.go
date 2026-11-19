package exness

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"time"
)

type CandleTimeframe string

const (
	TimeframeS1  CandleTimeframe = "S1"
	TimeframeS5  CandleTimeframe = "S5"
	TimeframeS15 CandleTimeframe = "S15"
	TimeframeM1  CandleTimeframe = "M1"
	TimeframeM3  CandleTimeframe = "M3"
	TimeframeM5  CandleTimeframe = "M5"
	TimeframeM10 CandleTimeframe = "M10"
	TimeframeM15 CandleTimeframe = "M15"
	TimeframeM30 CandleTimeframe = "M30"
	TimeframeH1  CandleTimeframe = "H1"
	TimeframeH2  CandleTimeframe = "H2"
	TimeframeH4  CandleTimeframe = "H4"
	TimeframeD1  CandleTimeframe = "D1"
	TimeframeW1  CandleTimeframe = "W1"
	TimeframeMN  CandleTimeframe = "MN"
)

type CandlePriceType string

const (
	PriceBid  CandlePriceType = "bid"
	PriceAsk  CandlePriceType = "ask"
	PriceBoth CandlePriceType = "both"
)

// CandleSide uses JSON doubles per the market-data contract, not trading Decimal strings.
type CandleSide struct {
	Open  *float64 `json:"open,omitempty"`
	High  *float64 `json:"high,omitempty"`
	Low   *float64 `json:"low,omitempty"`
	Close *float64 `json:"close,omitempty"`
}

// Candle preserves flat bid/ask or nested both-side data without guessing a discriminator.
type Candle struct {
	CandleSide
	Time   *time.Time  `json:"time,omitempty"`
	Volume *float64    `json:"volume,omitempty"`
	Bid    *CandleSide `json:"bid,omitempty"`
	Ask    *CandleSide `json:"ask,omitempty"`
}

type CandleHistory struct {
	Candles []Candle `json:"candles"`
}

type CandleQuery struct {
	Instrument string
	Timeframe  CandleTimeframe
	From       time.Time
	To         *time.Time
	Count      *int            // Must be nonzero; negative counts are valid. Exactly one of To or Count.
	PriceType  CandlePriceType // Empty selects bid.
}

// GetCandleHistory performs one request. Dynamic candle caps are enforced by the server.
func (c *Client) GetCandleHistory(ctx context.Context, query CandleQuery) (*CandleHistory, error) {
	if !instrumentPattern.MatchString(query.Instrument) {
		return nil, errors.New("invalid Instrument")
	}
	if !slices.Contains([]CandleTimeframe{TimeframeS1, TimeframeS5, TimeframeS15, TimeframeM1, TimeframeM3, TimeframeM5, TimeframeM10, TimeframeM15, TimeframeM30, TimeframeH1, TimeframeH2, TimeframeH4, TimeframeD1, TimeframeW1, TimeframeMN}, query.Timeframe) {
		return nil, errors.New("invalid candle Timeframe")
	}
	if query.PriceType == "" {
		query.PriceType = PriceBid
	}
	if !slices.Contains([]CandlePriceType{PriceBid, PriceAsk, PriceBoth}, query.PriceType) {
		return nil, errors.New("invalid candle PriceType")
	}
	if query.From.IsZero() {
		return nil, errors.New("From is required")
	}
	if (query.To == nil) == (query.Count == nil) {
		return nil, errors.New("provide exactly one of To or Count")
	}
	params := url.Values{
		"instrument": {query.Instrument}, "timeframe": {string(query.Timeframe)},
		"from": {query.From.UTC().Format(time.RFC3339Nano)}, "price_type": {string(query.PriceType)},
	}
	if query.To != nil {
		if !query.To.After(query.From) {
			return nil, errors.New("To must be later than From")
		}
		params.Set("to", query.To.UTC().Format(time.RFC3339Nano))
	}
	if query.Count != nil {
		if *query.Count == 0 {
			return nil, errors.New("Count must be nonzero")
		}
		params.Set("count", strconv.Itoa(*query.Count))
	}
	var result CandleHistory
	if err := c.accountGET(ctx, "market-data", "candles", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

type SwapMultiplier struct {
	Day        *string `json:"day,omitempty"`
	Multiplier *int32  `json:"multiplier,omitempty"`
}

type Money struct {
	Amount   *Decimal `json:"amount,omitempty"`
	Currency *string  `json:"currency,omitempty"`
}

type CommissionSettings struct {
	CommissionType  *string `json:"commission_type,omitempty"`
	CommissionValue *Money  `json:"commission_value,omitempty"`
}

type InstrumentSession struct {
	TradeMode     *string `json:"trade_mode,omitempty"`
	WeekStartTime *string `json:"week_start_time,omitempty"`
	WeekEndTime   *string `json:"week_end_time,omitempty"`
}

type InstrumentConditions struct {
	Instrument         string              `json:"instrument"`
	Description        *string             `json:"description,omitempty"`
	InternationalName  *string             `json:"international_name,omitempty"`
	Category           *string             `json:"category,omitempty"`
	BaseCurrency       *string             `json:"base_currency,omitempty"`
	QuoteCurrency      *string             `json:"quote_currency,omitempty"`
	MarginCurrency     *string             `json:"margin_currency,omitempty"`
	PointDigits        *int32              `json:"point_digits,omitempty"`
	CalcMode           *string             `json:"calc_mode,omitempty"`
	ExecMode           *string             `json:"exec_mode,omitempty"`
	MarginPercent      *Decimal            `json:"margin_percent,omitempty"`
	ContractSize       *Decimal            `json:"contract_size,omitempty"`
	VolumeMin          *Decimal            `json:"volume_min,omitempty"`
	VolumeMax          *Decimal            `json:"volume_max,omitempty"`
	VolumeStep         *Decimal            `json:"volume_step,omitempty"`
	SwapLong           *Decimal            `json:"swap_long,omitempty"`
	SwapShort          *Decimal            `json:"swap_short,omitempty"`
	SwapMultipliers    []SwapMultiplier    `json:"swap_multipliers,omitempty"`
	TradeMode          *string             `json:"trade_mode,omitempty"`
	OrderTypes         []string            `json:"order_types,omitempty"`
	FillingModes       []string            `json:"filling_modes,omitempty"`
	TradeSessions      []InstrumentSession `json:"trade_sessions,omitempty"`
	CommissionSettings *CommissionSettings `json:"commission_settings,omitempty"`
}

func (c *Client) GetInstrumentConditions(ctx context.Context, instrument string) (*InstrumentConditions, error) {
	if !instrumentPattern.MatchString(instrument) || instrument == "." || instrument == ".." {
		return nil, errors.New("invalid instrument path segment")
	}
	var result InstrumentConditions
	if err := c.accountGET(ctx, "configuration", "instruments/"+instrument+"/conditions", nil, &result); err != nil {
		return nil, err
	}
	if result.Instrument != instrument {
		return nil, &ProtocolError{"Conditions response returned a different instrument"}
	}
	return &result, nil
}

type OperationState string

const (
	OperationPending   OperationState = "pending"
	OperationConfirmed OperationState = "confirmed"
	OperationRejected  OperationState = "rejected"
	OperationFailed    OperationState = "failed"
)

type OperationStatus struct {
	OperationID     string         `json:"operation_id"`
	Status          OperationState `json:"status"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	ClientRequestID *string        `json:"client_request_id,omitempty"`
	ErrorCode       *int32         `json:"error_code,omitempty"`
	ErrorMessage    *string        `json:"error_message,omitempty"`
}

// IsTerminal is true for confirmed, rejected or failed; it does not mean success.
func (s OperationStatus) IsTerminal() bool {
	return s.Status == OperationConfirmed || s.Status == OperationRejected || s.Status == OperationFailed
}

var operationIDPattern = regexp.MustCompile(`^[0-9]{1,20}$`)

// GetOperationStatus reads an existing operation once. Rejected/failed are returned as data.
func (c *Client) GetOperationStatus(ctx context.Context, operationID string) (*OperationStatus, error) {
	id, err := strconv.ParseUint(operationID, 10, 64)
	if err != nil || !operationIDPattern.MatchString(operationID) {
		return nil, errors.New("operation ID must be a uint64 decimal string")
	}
	var result OperationStatus
	if err := c.accountGET(ctx, "trading", "operations/"+operationID, nil, &result); err != nil {
		return nil, err
	}
	resultID, err := strconv.ParseUint(result.OperationID, 10, 64)
	if err != nil || !operationIDPattern.MatchString(result.OperationID) || resultID != id {
		return nil, &ProtocolError{"Status response returned an invalid or different operation"}
	}
	if result.CreatedAt.IsZero() || result.UpdatedAt.IsZero() || !slices.Contains([]OperationState{OperationPending, OperationConfirmed, OperationRejected, OperationFailed}, result.Status) {
		return nil, &ProtocolError{"Invalid operation status response"}
	}
	return &result, nil
}
