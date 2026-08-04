// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Command vega-backend is the community build of vega-backend.
//
// Startup lives in server/app so that the enterprise build — which is the same
// service plus the paid capabilities in the openbkn-ee repository — can insert
// its assembly step between Boot and Run without forking this file. Everything
// this command does, that one does too; it simply has nothing to assemble.
package main

import (
	"github.com/openbkn-ai/bkn-comm-go/logger"
	_ "go.uber.org/automaxprocs"
	_ "unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/app"
)

func main() {
	a, err := app.Boot(app.Options{})
	if err != nil {
		logger.Fatalf("boot: %v", err)
	}

	if err := a.Run(); err != nil {
		logger.Fatalf("run: %v", err)
	}
}
