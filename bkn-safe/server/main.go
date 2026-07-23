// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Command bkn-safe is the ISF replacement auth service: authentication
// (hydra login/consent/device provider), authorization (Casbin), and user
// management (directory + LDAP). hydra issues the tokens.
//
// This is the community entry point. It registers no extensions, so the paid
// enterprise code is not merely switched off here — it is not in the binary.
// The enterprise entry point lives in the private bkn-foundry-ee repository
// and differs only by its Setup calls between Boot and Run.
package main

import (
	"flag"
	"log/slog"
	"os"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/app"
)

func main() {
	configPath := flag.String("config", "", "YAML config file (overrides defaults; env SAFE_CONFIG if unset)")
	flag.Parse()

	a, err := app.Boot(app.Options{ConfigPath: *configPath})
	if err != nil {
		fatal("boot", err)
	}
	if err := a.Run(); err != nil {
		fatal("http serve", err)
	}
}

func fatal(msg string, err error) {
	slog.Error(msg, "err", err)
	os.Exit(1)
}
