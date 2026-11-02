package main

import (
	"log/slog"
	"os"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/app"
)

// main is the agent-operator-integration entry point. Startup lives in package
// app so another entry point can run the same code.
func main() {
	a, err := app.Boot(app.Options{})
	if err != nil {
		slog.Error("boot agent-operator-integration", "err", err)
		os.Exit(1)
	}
	if err := a.Run(); err != nil {
		slog.Error("run agent-operator-integration", "err", err)
		os.Exit(1)
	}
}
