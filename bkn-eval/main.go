// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

// Command bkn-eval is the command-line entry of bkn-eval. It runs locally and
// in CI; it is not a service. All assembly happens in src/boot.
package main

import (
	"os"

	"github.com/openbkn-ai/bkn-foundry/bkn-eval/src/boot"
)

func main() {
	os.Exit(boot.Run(os.Args[1:], os.Stdout, os.Stderr))
}
