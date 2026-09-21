// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

// Command bkn-eval is the command-line entry of bkn-eval. It runs locally and
// in CI; it is not a service. It only wires driven adapters into the
// command-line driver adapter. A future server is a second command in this
// module that wires the same ports into an HTTP driver adapter.
package main

import (
	"os"

	"github.com/openbkn-ai/bkn-foundry/bkn-eval/internal/drivenadapter/filestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-eval/internal/driveradapter/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr, cli.Deps{
		Datasets: filestore.Datasets{},
	}))
}
