package main

import (
	"context"
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
	for event, err := range client.StreamTrading(ctx, exness.TradingStreamOptions{MaxReconnects: 3}) {
		if err != nil {
			return err
		}
		switch {
		case event.Gap != nil:
			fmt.Println("Connection lost: invalidate cached state and await a fresh snapshot.")
		case event.Snapshot != nil:
			fmt.Printf("New baseline: %d orders, %d positions. Replace any previous cache.\n", len(event.Snapshot.Orders), len(event.Snapshot.Positions))
		case event.Transaction != nil:
			transaction := event.Transaction
			status := "unspecified"
			if transaction.Status != nil {
				status = *transaction.Status
			}
			fmt.Printf("Transaction status=%s; changed orders=%d, positions=%d, deals=%d\n", status, len(transaction.Payload.Orders), len(transaction.Payload.Positions), len(transaction.Payload.Deals))
		}
	}
	fmt.Println("Stream ended: cached state is no longer current.")
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
