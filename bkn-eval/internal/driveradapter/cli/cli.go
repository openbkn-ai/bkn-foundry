// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

// Package cli is the command-line driver adapter. It parses arguments and
// prints results; the work itself goes through ports into the domain, so a
// future HTTP driver adapter can offer the same commands.
package cli

import (
	"fmt"
	"io"

	"github.com/openbkn-ai/bkn-foundry/bkn-eval/internal/port"
)

const usage = `usage: bkn-eval <command> [arguments]

commands:
  validate <dataset.json>...   check datasets against the dataset contract

planned (not implemented yet):
  fixture push                 import a fixture network through bkn-backend
  run                          run a dataset against an MCP entry point
  grade                        score run results against the dataset facts
  report                       compare arms and report non-inferiority
`

// Deps are the driven adapters the commands use.
type Deps struct {
	Datasets port.DatasetSource
}

// Run executes one command and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer, deps Deps) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "validate":
		return validate(args[1:], stdout, stderr, deps)
	case "fixture", "run", "grade", "report":
		fmt.Fprintf(stderr, "bkn-eval: %s is not implemented yet\n", args[0])
		return 2
	case "-h", "--help", "help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "bkn-eval: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

func validate(refs []string, stdout, stderr io.Writer, deps Deps) int {
	if len(refs) == 0 {
		fmt.Fprintln(stderr, "bkn-eval validate: at least one dataset file is required")
		return 2
	}
	failed := false
	for _, ref := range refs {
		ds, err := deps.Datasets.Load(ref)
		if err != nil {
			fmt.Fprintln(stderr, err)
			failed = true
			continue
		}
		fmt.Fprintf(stdout, "ok  %s  %s@%s  %d cases\n", ref, ds.DatasetID, ds.Version, len(ds.Cases))
	}
	if failed {
		return 1
	}
	return 0
}
