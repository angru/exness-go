package exness

import (
	"encoding/json"
	"math/big"
	"testing"
)

func TestExactDecimalAndInvalidWireValues(t *testing.T) {
	value, err := NewDecimal("12345678901234567890.123456789")
	if err != nil {
		t.Fatal(err)
	}
	rat, err := value.Rat()
	if err != nil {
		t.Fatal(err)
	}
	want, _ := new(big.Rat).SetString("12345678901234567890123456789/1000000000")
	if rat.Cmp(want) != 0 {
		t.Fatal("decimal precision lost")
	}
	data, err := json.Marshal(value)
	if err != nil || string(data) != `"12345678901234567890.123456789"` {
		t.Fatalf("marshal: %s %v", data, err)
	}
	for _, data := range []string{`1.2`, `100`, `"NaN"`, `"Infinity"`, `"1e3"`, `"1,000"`, `""`, `null`} {
		var d Decimal
		if json.Unmarshal([]byte(data), &d) == nil {
			t.Fatalf("accepted invalid decimal %s", data)
		}
	}
	if _, err := (Decimal{}).Rat(); err == nil {
		t.Fatal("unset decimal accepted")
	}
	if _, err := json.Marshal(Decimal{}); err == nil {
		t.Fatal("unset decimal serialized")
	}
}

func TestBadPaginationAndEarlyStop(t *testing.T) {
	cursor := "repeated"
	more, done := true, false
	for _, page := range []*HistoryPage[int]{
		{HasMore: &more}, {HasMore: &done, NextCursor: &cursor},
		{HasMore: &more, NextCursor: &cursor},
	} {
		calls := 0
		get := func() (*HistoryPage[int], error) { calls++; return page, nil }
		found := false
		for _, err := range iterate(get, func(string) (*HistoryPage[int], error) { return get() }) {
			if err != nil {
				found = true
			}
		}
		if !found || calls > 2 {
			t.Fatal("bad pagination was not stopped")
		}
	}
	for range iterate(
		func() (*HistoryPage[int], error) { return &HistoryPage[int]{Items: []int{1}, NextCursor: &cursor}, nil },
		func(string) (*HistoryPage[int], error) { t.Fatal("iterator fetched after early stop"); return nil, nil },
	) {
		break
	}
}
