package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"time"

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
	conditions, err := client.GetInstrumentConditions(ctx, instrument)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(conditions); err != nil {
		return err
	}
	count := 3
	candles, err := client.GetCandleHistory(ctx, exness.CandleQuery{
		Instrument: instrument, Timeframe: exness.TimeframeM1, From: time.Now().UTC().Add(-time.Hour),
		Count: &count, PriceType: exness.PriceBoth,
	})
	if err != nil {
		return err
	}
	if err := encoder.Encode(candles); err != nil {
		return err
	}
	if id := os.Getenv("EXN_OPERATION_ID"); id != "" {
		operation, err := client.GetOperationStatus(ctx, id)
		if err != nil {
			return err
		}
		fmt.Println(operation.OperationID, operation.Status, operation.IsTerminal())
	}
	return nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
