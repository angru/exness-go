package exness_test

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"iter"
	"testing"

	exness "github.com/angru/exness-go"
)

// Exercise every public stream through an external package, including cancellation
// inspection and the neutral option type accepted by all legacy signatures.
func TestPublicStreamsWithCommonOptions(t *testing.T) {
	key := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	credentials, err := exness.NewCredentials("synthetic", hex.EncodeToString(key))
	if err != nil {
		t.Fatal(err)
	}
	client, err := exness.NewClient(exness.Config{Credentials: credentials, AccountID: "123456", DiscoveryURL: "https://gateway.example"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	options := exness.StreamOptions{MaxReconnects: 2}
	assertCanceled(t, client.StreamTicks(ctx, []string{"EURUSD"}, options))
	assertCanceled(t, client.StreamTrading(ctx, options))
	assertCanceled(t, client.StreamAccountState(ctx, options))
	assertCanceled(t, client.StreamInstruments(ctx, []string{"all"}, options))
	assertCanceled(t, client.StreamHMR(ctx, []string{"EURUSD"}, options))
}

func assertCanceled[T any](t *testing.T, stream iter.Seq2[T, error]) {
	t.Helper()
	failures := 0
	for _, err := range stream {
		var transport *exness.TransportError
		if !errors.As(err, &transport) || !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation not inspectable: %v", err)
		}
		failures++
	}
	if failures != 1 {
		t.Fatal("expected one terminal cancellation")
	}
}
