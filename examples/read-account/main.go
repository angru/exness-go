package main

import (
	"context"
	"encoding/json"
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
	account, err := client.GetAccount(ctx)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(account)
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
