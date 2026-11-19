package exness

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

const testSeed = "9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60"
const testPublic = "d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a"

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func testCredentials(t *testing.T) *Credentials {
	t.Helper()
	c, err := NewCredentials("exnsk-synthetic-test", testSeed+testPublic)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSharedOpenSSLSigningVector(t *testing.T) {
	var vector struct {
		APIKey     string `json:"api_key"`
		PrivateKey string `json:"private_key_hex"`
		Timestamp  int64  `json:"timestamp_ms"`
		Path       string `json:"path"`
		Payload    string `json:"payload_utf8"`
		Data       string `json:"exn_data"`
		Signature  string `json:"exn_sign"`
	}
	if err := json.Unmarshal(readFixture(t, "signing-vector"), &vector); err != nil {
		t.Fatal(err)
	}
	c, err := NewCredentials(vector.APIKey, vector.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	h := c.signGET(vector.Path, time.UnixMilli(vector.Timestamp))
	if h.Get("EXN-DATA") != vector.Data || h.Get("EXN-SIGN") != vector.Signature {
		t.Fatal("Go signature differs from the shared Python/OpenSSL vector")
	}
	data, _ := base64.RawURLEncoding.DecodeString(h.Get("EXN-DATA"))
	if string(data) != vector.Payload {
		t.Fatal("payload bytes differ")
	}
	if _, ok := h["Exn-Idempotency-Key"]; !ok {
		t.Fatal("empty idempotency header is missing")
	}
	if h.Get("EXN-IDEMPOTENCY-KEY") != "" {
		t.Fatal("GET key must be empty")
	}
	if c.signGET(vector.Path, time.UnixMilli(vector.Timestamp+1)).Get("EXN-SIGN") == vector.Signature {
		t.Fatal("timestamp did not refresh")
	}
}

func TestPEMAndHexMatchAndCredentialsAreRedacted(t *testing.T) {
	seed, _ := hex.DecodeString(testSeed)
	key := ed25519.NewKeyFromSeed(seed)
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	encoded := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	c, err := NewCredentials("exnsk-synthetic-test", string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	when := time.UnixMilli(1773656070123)
	if c.signGET("/test", when).Get("EXN-SIGN") != testCredentials(t).signGET("/test", when).Get("EXN-SIGN") {
		t.Fatal("PEM mismatch")
	}
	for _, format := range []string{"%v", "%+v", "%#v", "%s"} {
		text := fmt.Sprintf(format, c)
		if strings.Contains(text, testSeed) || strings.Contains(text, "exnsk-synthetic-test") {
			t.Fatal("credentials leaked")
		}
	}
}

func TestBadCredentials(t *testing.T) {
	for _, key := range []string{"", "bad", testSeed, testSeed + strings.Repeat("00", 32), strings.Repeat("g", 128), "-----BEGIN PRIVATE KEY-----\nbad\n-----END PRIVATE KEY-----"} {
		if _, err := NewCredentials("test", key); err == nil {
			t.Fatalf("accepted invalid key length %d", len(key))
		}
	}
	for _, apiKey := range []string{"", "with space", "key\r\nHost: invalid", "ключ"} {
		if _, err := NewCredentials(apiKey, testSeed+testPublic); err == nil {
			t.Fatal("accepted bad API key")
		}
	}
}
