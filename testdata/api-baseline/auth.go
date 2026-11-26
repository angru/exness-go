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
	"regexp"
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
	headers, _ := c.signRequest(http.MethodGet, path, nil, "", now)
	return headers
}

var idempotencyPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{5,128}$`)

// signRequest signs the final method, path/query and body bytes without serializing them.
func (c *Credentials) signRequest(method, path string, body []byte, key string, now time.Time) (http.Header, error) {
	switch method {
	case http.MethodGet:
		if len(body) != 0 || key != "" {
			return nil, errors.New("signed GET requires an empty body and idempotency key")
		}
	case http.MethodPost, http.MethodPatch, http.MethodDelete:
		if !idempotencyPattern.MatchString(key) {
			return nil, errors.New("invalid idempotency key: expected 5..128 URL-safe ASCII characters")
		}
	default:
		return nil, errors.New("unsupported signed HTTP method")
	}
	hash := sha256.Sum256(body)
	payload := signedPayload{c.apiKey, key, now.UnixMilli(), 1, method, path, base64.RawURLEncoding.EncodeToString(hash[:])}
	// These fields are all serializable primitives; encoding cannot fail.
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(payload)
	data := bytes.TrimSuffix(buf.Bytes(), []byte{'\n'})
	headers := make(http.Header)
	headers.Set("EXN-API-KEY", c.apiKey)
	headers.Set("EXN-IDEMPOTENCY-KEY", key)
	headers.Set("EXN-TIMESTAMP", strconv.FormatInt(now.UnixMilli(), 10))
	headers.Set("EXN-SIGN-VERSION", "1")
	headers.Set("EXN-DATA", base64.RawURLEncoding.EncodeToString(data))
	headers.Set("EXN-SIGN", base64.RawURLEncoding.EncodeToString(ed25519.Sign(c.privateKey, data)))
	return headers, nil
}
