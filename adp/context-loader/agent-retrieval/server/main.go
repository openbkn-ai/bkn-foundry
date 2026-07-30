// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package main

import (
	"log/slog"
	"os"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/app"
)

// main is the community entry point. Everything it does lives in package app,
// so the enterprise entry point runs the same startup and only inserts its
// extension assembly between Boot and Run.
func main() {
	a, err := app.Boot(app.Options{})
	if err != nil {
		slog.Error("boot agent-retrieval", "err", err)
		os.Exit(1)
	}
	if err := a.Run(); err != nil {
		slog.Error("run agent-retrieval", "err", err)
		os.Exit(1)
	}
}
