package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"

	exness "github.com/angru/exness-go"
)

func run(ctx context.Context) error {
	credentials, err := exness.NewCredentials(os.Getenv("EXN_API_KEY"), os.Getenv("EXN_PRIVATE_KEY"))
	if err != nil {
		return err
	}
	client, err := exness.NewClient(exness.Config{
		Credentials: credentials, AccountID: os.Getenv("EXN_ACCOUNT_ID"),
		DiscoveryURL: os.Getenv("EXN_DISCOVERY_BASE_URL"),
	})
	if err != nil {
		return err
	}
	defer client.Close()
	instrument := os.Getenv("EXN_INSTRUMENT")
	if instrument == "" {
		instrument = "EURUSD"
	}
	encoder := json.NewEncoder(os.Stdout)
	for event, err := range client.StreamTicks(ctx, []string{instrument}, exness.TickStreamOptions{MaxReconnects: 3}) {
		if err != nil {
			return err
		}
		if event.Gap != nil {
			fmt.Fprintf(os.Stderr, "possible tick loss; reconnect %d in %s\n", event.Gap.ReconnectAttempt, event.Gap.Delay)
			continue
		}
		if err := encoder.Encode(event.Tick); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
