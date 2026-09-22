// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package main

import (
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/app"
)

func main() {
	application, err := app.Boot(app.Options{})
	if err != nil {
		logger.Fatalf("Failed to initialize VEGA Manager: %v", err)
	}
	if err := application.Run(); err != nil {
		logger.Fatalf("VEGA Manager stopped unexpectedly: %v", err)
	}
}
