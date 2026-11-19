package exness

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The oracle accumulates base-ten digits, independently of the production parser.
func decimalOracle(s string) (*big.Rat, bool) {
	negative := strings.HasPrefix(s, "-")
	if strings.HasPrefix(s, "+") || negative {
		s = s[1:]
	}
	parts := strings.Split(s, ".")
	if len(parts) > 2 {
		return nil, false
	}
	numerator, denominator := new(big.Int), big.NewInt(1)
	for i, part := range parts {
		if part == "" {
			return nil, false
		}
		for _, digit := range part {
			if digit < '0' || digit > '9' {
				return nil, false
			}
			numerator.Mul(numerator, big.NewInt(10))
			numerator.Add(numerator, big.NewInt(int64(digit-'0')))
			if i == 1 {
				denominator.Mul(denominator, big.NewInt(10))
			}
		}
	}
	if negative {
		numerator.Neg(numerator)
	}
	return new(big.Rat).SetFrac(numerator, denominator), true
}

func FuzzDecimal(f *testing.F) {
	for _, s := range []string{"0", "010", "08", "+0009", "-010", "-0.00", "12345678901234567890.123456789", "1e4", "NaN", "", "١"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > 256 {
			t.Skip()
		}
		want, valid := decimalOracle(s)
		d, err := NewDecimal(s)
		if (err == nil) != valid {
			t.Fatal("decimal grammar mismatch")
		}
		if !valid {
			return
		}
		rat, err := d.Rat()
		if err != nil || rat.Cmp(want) != 0 {
			t.Fatalf("base-ten arithmetic mismatch for %q: %v", s, err)
		}
		data, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		var decoded Decimal
		if err := json.Unmarshal(data, &decoded); err != nil || decoded.String() != s {
			t.Fatal("decimal JSON loses precision or spelling")
		}
		// JSON numbers must not silently enter the exact-string model.
		if json.Unmarshal([]byte(s), &decoded) == nil {
			t.Fatal("accepted an unquoted decimal")
		}
	})
}

func FuzzCredentials(f *testing.F) {
	f.Add("exnsk-synthetic-test", testSeed+testPublic)
	f.Add("bad\nheader", "not a key")
	f.Add("", "")
	f.Fuzz(func(t *testing.T, apiKey, key string) {
		if len(apiKey)+len(key) > 4096 {
			t.Skip()
		}
		c, err := NewCredentials(apiKey, key)
		if err != nil {
			return
		}
		if fmt.Sprintf("%v", c) != "Credentials(<redacted>)" || fmt.Sprintf("%#v", c) != "Credentials(<redacted>)" {
			t.Fatal("credential formatting is not redacted")
		}
		h := c.signGET("/test?cursor=a%2Fb", time.UnixMilli(123))
		data, err := base64.RawURLEncoding.DecodeString(h.Get("EXN-DATA"))
		if err != nil {
			t.Fatal(err)
		}
		sig, err := base64.RawURLEncoding.DecodeString(h.Get("EXN-SIGN"))
		if err != nil || !ed25519.Verify(c.privateKey.Public().(ed25519.PublicKey), data, sig) {
			t.Fatal("accepted key cannot sign")
		}
	})
}

func FuzzOrigins(f *testing.F) {
	for _, s := range []string{"https://example.test/", "http://example.test", "https://a:b@example.test", "https://example.test/%2f", "https://example.test?", "https://[::1]:443", ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > 2048 {
			t.Skip()
		}
		origin, err := baseURL(s)
		if err != nil {
			return
		}
		u, err := url.Parse(origin + "/v1/test")
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Path != "/v1/test" || u.RawQuery != "" || u.Fragment != "" {
			t.Fatal("origin changes the request route")
		}
	})
}

func FuzzCursorEncoding(f *testing.F) {
	for _, s := range []string{"a+/=&?#%", "Курсор 🌍", "\x00\xff", ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, cursor string) {
		if len(cursor) > 2048 {
			t.Skip()
		}
		params, err := historyParams(HistoryQuery{Cursor: &cursor, Limit: 17}, "", "")
		if cursor == "" {
			if err == nil {
				t.Fatal("empty cursor accepted")
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		u := &url.URL{Scheme: "https", Host: "example.test", Path: "/v1/history", RawQuery: params.Encode()}
		if u.Query().Get("cursor") != cursor || len(u.Query()) != 2 {
			t.Fatal("cursor changed or filters leaked")
		}
		c := testCredentials(t)
		h := c.signGET(u.RequestURI(), time.UnixMilli(123))
		data, _ := base64.RawURLEncoding.DecodeString(h.Get("EXN-DATA"))
		var payload signedPayload
		if json.Unmarshal(data, &payload) != nil || payload.Path != u.RequestURI() {
			t.Fatal("signed path differs from encoded cursor")
		}
	})
}

func FuzzPagination(f *testing.F) {
	f.Add(uint8(3), false, false)
	f.Add(uint8(2), true, false)
	f.Add(uint8(0), false, true)
	f.Fuzz(func(t *testing.T, size uint8, cycle, inconsistent bool) {
		count := int(size%32) + 1
		calls := 0
		page := func() (*HistoryPage[int], error) {
			index := calls
			calls++
			if calls > count+1 {
				t.Fatal("unbounded pagination")
			}
			more := index < count-1 || cycle
			cursor := ""
			if more {
				cursor = strconv.Itoa(index + 1)
			}
			if index >= count {
				cursor = "1"
			}
			if inconsistent && index == 0 {
				more = !more
			}
			return &HistoryPage[int]{Items: []int{index}, NextCursor: &cursor, HasMore: &more}, nil
		}
		rows, failures := 0, 0
		for item, err := range iterate(page, func(string) (*HistoryPage[int], error) { return page() }) {
			if err != nil {
				var protocol *ProtocolError
				if !errors.As(err, &protocol) {
					t.Fatal(err)
				}
				failures++
				continue
			}
			if item != rows {
				t.Fatal("items reordered or duplicated")
			}
			rows++
		}
		if inconsistent {
			if rows != 0 || failures != 1 || calls != 1 {
				t.Fatal("inconsistent first page escaped validation")
			}
			return
		}
		wantFailures := 0
		if cycle {
			wantFailures = 1
		}
		if rows != count || failures != wantFailures || calls != count+wantFailures {
			t.Fatal("wrong termination or repeated-page handling")
		}
	})
}
