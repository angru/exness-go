package exness

import (
	"encoding/json"
	"errors"
	"strconv"
	"unicode/utf8"
)

// TradeSide is the requested direction for a new trading operation.
type TradeSide string

const (
	TradeBuy  TradeSide = "buy"
	TradeSell TradeSide = "sell"
)

// OpenPositionRequest encodes an opening-position request without network access.
// Nil optional fields are omitted. Instrument-specific rules remain server decisions.
// MarshalJSON prepares validated bytes; Client.OpenPosition submits them once.
type OpenPositionRequest struct {
	Instrument      string    `json:"instrument"`
	Side            TradeSide `json:"side"`
	Volume          Decimal   `json:"volume"`
	Price           *Decimal  `json:"price,omitempty"`
	Deviation       *Decimal  `json:"deviation,omitempty"`
	StopLossPrice   *Decimal  `json:"stop_loss_price,omitempty"`
	TakeProfitPrice *Decimal  `json:"take_profit_price,omitempty"`
	Comment         *string   `json:"comment,omitempty"`
}

func positiveRequestDecimal(value Decimal) bool {
	rat, err := value.Rat()
	return err == nil && rat.Sign() > 0
}

// MarshalJSON validates the request and preserves exact decimal strings.
func (r OpenPositionRequest) MarshalJSON() ([]byte, error) {
	if !instrumentPattern.MatchString(r.Instrument) || (r.Side != TradeBuy && r.Side != TradeSell) {
		return nil, errors.New("invalid opening-position instrument or side")
	}
	if !positiveRequestDecimal(r.Volume) {
		return nil, errors.New("opening-position volume must be positive")
	}
	for _, price := range []*Decimal{r.Price, r.StopLossPrice, r.TakeProfitPrice} {
		if price != nil && !positiveRequestDecimal(*price) {
			return nil, errors.New("opening-position prices must be positive when supplied")
		}
	}
	// Deviation has DecimalString shape; execution-mode restrictions are server-side.
	if r.Deviation != nil && r.Deviation.text == "" {
		return nil, errors.New("opening-position deviation is unset")
	}
	if r.Comment != nil && !utf8.ValidString(*r.Comment) {
		return nil, errors.New("opening-position comment must be valid UTF-8")
	}
	type wire OpenPositionRequest
	return json.Marshal(wire(r))
}

// AckResponse means admission only, never successful execution.
// The contract requires only Status; an absent OperationID must not be fabricated.
type AckResponse struct {
	Status          string  `json:"status"`
	OperationID     *string `json:"operation_id,omitempty"`
	ClientRequestID *string `json:"client_request_id,omitempty"`
}

func (a *AckResponse) UnmarshalJSON(data []byte) error {
	type wire AckResponse
	var decoded wire
	if err := json.Unmarshal(data, &decoded); err != nil {
		return &ProtocolError{"Invalid acknowledgement response"}
	}
	if decoded.Status != "accepted" {
		return &ProtocolError{"Invalid acknowledgement response"}
	}
	if decoded.OperationID != nil {
		if _, err := strconv.ParseUint(*decoded.OperationID, 10, 64); err != nil || !operationIDPattern.MatchString(*decoded.OperationID) {
			return &ProtocolError{"Invalid acknowledgement response"}
		}
	}
	*a = AckResponse(decoded)
	return nil
}
