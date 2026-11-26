package exness

import (
	"encoding/json"
	"testing"
	"time"
)

func BenchmarkParseSnapshot(b *testing.B) {
	data := readFixture(b, "snapshot")
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		var snapshot TradingSnapshot
		if err := json.Unmarshal(data, &snapshot); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParseTick(b *testing.B) {
	data := readFixture(b, "tick")
	names := map[string]bool{"EURUSD": true}
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		if _, err := decodeTick(data, names, "benchmark"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSignGET(b *testing.B) {
	var vector struct {
		APIKey     string `json:"api_key"`
		PrivateKey string `json:"private_key_hex"`
		Timestamp  int64  `json:"timestamp_ms"`
		Path       string `json:"path"`
		Signature  string `json:"exn_sign"`
	}
	if err := json.Unmarshal(readFixture(b, "signing-vector"), &vector); err != nil {
		b.Fatal(err)
	}
	credentials, err := NewCredentials(vector.APIKey, vector.PrivateKey)
	if err != nil {
		b.Fatal(err)
	}
	now := time.UnixMilli(vector.Timestamp)
	if credentials.signGET(vector.Path, now).Get("EXN-SIGN") != vector.Signature {
		b.Fatal("signing preflight failed")
	}
	b.ReportAllocs()
	for b.Loop() {
		credentials.signGET(vector.Path, now)
	}
}

func BenchmarkSignPOST(b *testing.B) {
	var corpus struct {
		APIKey     string `json:"api_key"`
		PrivateKey string `json:"private_key_hex"`
		Vectors    []struct {
			Method    string `json:"method"`
			Path      string `json:"path"`
			Body      string `json:"body_utf8"`
			Key       string `json:"idempotency_key"`
			Timestamp int64  `json:"timestamp_ms"`
			Signature string `json:"exn_sign"`
		} `json:"vectors"`
	}
	if err := json.Unmarshal(readFixture(b, "mutation-signing-vectors"), &corpus); err != nil {
		b.Fatal(err)
	}
	credentials, err := NewCredentials(corpus.APIKey, corpus.PrivateKey)
	if err != nil {
		b.Fatal(err)
	}
	vector := corpus.Vectors[0]
	body, now := []byte(vector.Body), time.UnixMilli(vector.Timestamp)
	headers, err := credentials.signRequest(vector.Method, vector.Path, body, vector.Key, now)
	if err != nil || headers.Get("EXN-SIGN") != vector.Signature {
		b.Fatal("signing preflight failed")
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := credentials.signRequest(vector.Method, vector.Path, body, vector.Key, now); err != nil {
			b.Fatal(err)
		}
	}
}
