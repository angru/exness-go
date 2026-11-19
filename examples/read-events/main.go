package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"

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
	kind := os.Getenv("EXN_STREAM")
	if kind == "" {
		kind = "account_state"
	}
	rawNames := os.Getenv("EXN_INSTRUMENTS")
	if rawNames == "" {
		rawNames = "EURUSD"
	}
	names := strings.Split(rawNames, ",")
	options := exness.ServerStreamOptions{MaxReconnects: 3}
	gap := func() { fmt.Println("Gap: invalidate cached state; see README.md for recovery rules.") }
	switch kind {
	case "account_state":
		for event, err := range client.StreamAccountState(ctx, options) {
			if err != nil {
				return err
			}
			if event.Gap != nil {
				gap()
				continue
			}
			fmt.Printf("Account observation %s: equity=%s\n", event.AccountState.EventTime, event.AccountState.Payload.AccountState.Equity)
		}
	case "instruments":
		for event, err := range client.StreamInstruments(ctx, names, options) {
			if err != nil {
				return err
			}
			if event.Gap != nil {
				gap()
				continue
			}
			fmt.Printf("Instrument %s: %s (full conditions; del removes cached instrument).\n", event.Instrument.Payload.Instrument, event.Instrument.Action)
		}
	case "hmr":
		for event, err := range client.StreamHMR(ctx, names, options) {
			if err != nil {
				return err
			}
			if event.Gap != nil {
				gap()
				continue
			}
			if event.Snapshot != nil {
				fmt.Printf("Replace HMR baseline with %d periods.\n", len(event.Snapshot.Periods))
			} else {
				fmt.Printf("HMR: upsert %d periods, remove IDs %v from latest baseline.\n", len(event.Update.UpsertedPeriods), event.Update.RemovedPeriodIDs)
			}
		}
	default:
		return errors.New("EXN_STREAM must be account_state, instruments or hmr")
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
