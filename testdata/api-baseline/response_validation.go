package exness

import (
	"encoding/json"
	"slices"
)

// Optional does not mean nullable. Keep this policy limited to audited fields;
// unknown fields and explicitly nullable contract fields remain supported.
func decodeResponseObject[T any](data []byte, result *T, required, nonnull []string) error {
	fields, ok := requiredObject(data, required...)
	if !ok {
		return &ProtocolError{"Missing required response fields"}
	}
	for _, key := range nonnull {
		if raw, present := fields[key]; present && string(raw) == "null" {
			return &ProtocolError{"Unexpected null response field"}
		}
	}
	if json.Unmarshal(data, result) != nil {
		return &ProtocolError{"Invalid response field type"}
	}
	return nil
}

func optionalID(value *string) bool { return value == nil || validTradingID(*value) }
func optionalDirection(value *string) bool {
	return value == nil || *value == "buy" || *value == "sell"
}
func optionalInstrument(value *string) bool {
	return value == nil || instrumentPattern.MatchString(*value)
}

func validDeal(deal Deal) bool {
	return validTradingID(deal.DealID) && optionalID(deal.OrderID) && optionalID(deal.PositionID) &&
		optionalDirection(deal.Direction) && optionalInstrument(deal.Instrument) && !deal.CreateTime.IsZero() &&
		slices.Contains([]DealType{DealOpen, DealClose, DealBalance, DealNullCompensation, DealSwapFee, DealAdminFee, DealDividend, DealExnessDollar}, deal.Type)
}

func (o *Order) UnmarshalJSON(data []byte) error {
	type wire Order
	var decoded wire
	if err := decodeResponseObject(data, &decoded, []string{"order_id", "order_type", "state", "create_time"}, []string{"position_id", "direction"}); err != nil {
		return err
	}
	if !validOrder(Order(decoded)) {
		return &ProtocolError{"Invalid order response"}
	}
	*o = Order(decoded)
	return nil
}

func (p *Position) UnmarshalJSON(data []byte) error {
	type wire Position
	var decoded wire
	if err := decodeResponseObject(data, &decoded, []string{"position_id", "direction", "instrument", "state", "create_time", "volume"}, nil); err != nil {
		return err
	}
	if !validPosition(Position(decoded)) {
		return &ProtocolError{"Invalid position response"}
	}
	*p = Position(decoded)
	return nil
}

func (d *Deal) UnmarshalJSON(data []byte) error {
	type wire Deal
	var decoded wire
	if err := decodeResponseObject(data, &decoded, []string{"deal_id", "type", "create_time"}, []string{"order_id", "position_id", "direction"}); err != nil {
		return err
	}
	if !validDeal(Deal(decoded)) {
		return &ProtocolError{"Invalid deal response"}
	}
	*d = Deal(decoded)
	return nil
}

func (s *TradingSnapshot) UnmarshalJSON(data []byte) error {
	type wire TradingSnapshot
	var decoded wire
	if err := decodeResponseObject(data, &decoded, []string{"account_state"}, []string{"orders", "positions"}); err != nil {
		return err
	}
	if !validState(decoded.AccountState) {
		return &ProtocolError{"Invalid snapshot monetary state"}
	}
	*s = TradingSnapshot(decoded)
	return nil
}

func (s *OperationStatus) UnmarshalJSON(data []byte) error {
	type wire OperationStatus
	var decoded wire
	if err := decodeResponseObject(data, &decoded, []string{"operation_id", "status", "created_at", "updated_at"}, []string{"client_request_id", "error_code", "error_message"}); err != nil {
		return err
	}
	if !validTradingID(decoded.OperationID) || decoded.CreatedAt.IsZero() || decoded.UpdatedAt.IsZero() || !slices.Contains([]OperationState{OperationPending, OperationConfirmed, OperationRejected, OperationFailed}, decoded.Status) {
		return &ProtocolError{"Invalid operation status response"}
	}
	*s = OperationStatus(decoded)
	return nil
}

func (e *TransactionEvent) UnmarshalJSON(data []byte) error {
	type wire TransactionEvent
	var decoded wire
	if err := decodeResponseObject(data, &decoded, []string{"event_type", "payload"}, []string{"operation_id", "status", "error_code", "error_message", "event_time"}); err != nil {
		return err
	}
	// client_request_id is explicitly nullable. All other audited envelope fields
	// may be omitted, but cannot be supplied as null.
	if decoded.EventType != "transaction_event" || !optionalID(decoded.OperationID) || (decoded.Status != nil && !slices.Contains([]string{"success", "rejected", "failed"}, *decoded.Status)) || (decoded.ErrorCode != nil && (int64(*decoded.ErrorCode) < -2147483648 || int64(*decoded.ErrorCode) > 2147483647)) {
		return &ProtocolError{"Invalid transaction event response"}
	}
	*e = TransactionEvent(decoded)
	return nil
}
