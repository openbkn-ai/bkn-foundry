// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

// Package boot assembles bkn-eval: it builds the driven adapters and hands them
// to the driver adapter. Adding a server later means adding a `serve` command
// here that wires the same ports into an HTTP driver adapter.
package boot

import (
	"io"

	"github.com/openbkn-ai/bkn-foundry/bkn-eval/src/drivenadapter/fileaccess/datasetfile"
	"github.com/openbkn-ai/bkn-foundry/bkn-eval/src/driveradapter/cli"
)

// Run executes one command-line invocation and returns the exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	return cli.Run(args, stdout, stderr, cli.Deps{
		Datasets: datasetfile.Store{},
	})
}
