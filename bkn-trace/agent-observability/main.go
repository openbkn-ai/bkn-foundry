package main

import (
	"context"
	"errors"
	"log"
	"os/signal"
	"syscall"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/boot"
)

// @title Agent Observability API
// @version 1.0
// @description APIs for querying agent traces from OpenSearch.
// @BasePath /api/agent-observability/v1
func main() {
	app, err := boot.NewApp()
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Printf("agent-observability listening on :8080")
	if err := run(ctx, app); err != nil {
		log.Fatal(err)
	}
}

type application interface {
	Start() error
	Shutdown(context.Context) error
}

func run(ctx context.Context, app application) error {
	startResult := make(chan error, 1)
	go func() {
		startResult <- app.Start()
	}()

	select {
	case err := <-startResult:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	shutdownErr := app.Shutdown(shutdownCtx)
	startErr := <-startResult
	return errors.Join(shutdownErr, startErr)
}
