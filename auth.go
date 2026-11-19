package exness

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Credentials holds signing material. Its formatting methods redact both fields.
type Credentials struct {
	apiKey     string
	privateKey ed25519.PrivateKey
}

func (Credentials) String() string   { return "Credentials(<redacted>)" }
func (Credentials) GoString() string { return "Credentials(<redacted>)" }

// NewCredentials accepts an Exness 128-character hex key or unencrypted PKCS#8 PEM.
func NewCredentials(apiKey, privateKey string) (*Credentials, error) {
	if apiKey == "" {
		return nil, errors.New("API key must not be empty")
	}
	for _, char := range apiKey {
		if char < 33 || char > 126 {
			return nil, errors.New("API key must be printable ASCII without whitespace")
		}
	}
	value := strings.TrimSpace(privateKey)
	if len(value) == 128 {
		raw, err := hex.DecodeString(value)
		if err == nil {
			key := ed25519.NewKeyFromSeed(raw[:32])
			if subtle.ConstantTimeCompare(key[32:], raw[32:]) == 1 {
				return &Credentials{apiKey: apiKey, privateKey: key}, nil
			}
		}
	}
	block, rest := pem.Decode([]byte(value))
	if block != nil && block.Type == "PRIVATE KEY" && len(bytes.TrimSpace(rest)) == 0 {
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err == nil {
			if key, ok := parsed.(ed25519.PrivateKey); ok {
				return &Credentials{apiKey: apiKey, privateKey: key}, nil
			}
		}
	}
	return nil, errors.New("expected valid 128-character Ed25519 hex or unencrypted PKCS#8 PEM")
}

type signedPayload struct {
	APIKey         string `json:"api_key"`
	IdempotencyKey string `json:"idempotency_key"`
	Timestamp      int64  `json:"timestamp"`
	SignVersion    int    `json:"sign_version"`
	Method         string `json:"method"`
	Path           string `json:"path"`
	BodyHash       string `json:"body_hash"`
}

func (c *Credentials) signGET(path string, now time.Time) http.Header {
	hash := sha256.Sum256(nil)
	payload := signedPayload{c.apiKey, "", now.UnixMilli(), 1, "GET", path, base64.RawURLEncoding.EncodeToString(hash[:])}
	// These fields are all serializable primitives; encoding cannot fail.
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(payload)
	data := bytes.TrimSuffix(buf.Bytes(), []byte{'\n'})
	headers := make(http.Header)
	headers.Set("EXN-API-KEY", c.apiKey)
	headers.Set("EXN-IDEMPOTENCY-KEY", "")
	headers.Set("EXN-TIMESTAMP", strconv.FormatInt(now.UnixMilli(), 10))
	headers.Set("EXN-SIGN-VERSION", "1")
	headers.Set("EXN-DATA", base64.RawURLEncoding.EncodeToString(data))
	headers.Set("EXN-SIGN", base64.RawURLEncoding.EncodeToString(ed25519.Sign(c.privateKey, data)))
	return headers
}
