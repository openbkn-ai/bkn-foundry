// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// validate-dir loads a BKN directory and prints ValidateNetwork results. For ad-hoc checks.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"bkn-backend/bkn-specification/bkn"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: %s <path-to-bkn-directory>\n", os.Args[0])
		os.Exit(2)
	}
	dir := os.Args[1]
	net, err := bkn.LoadNetwork(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load: %v\n", err)
		os.Exit(1)
	}
	res := bkn.ValidateNetwork(net)
	out := map[string]any{
		"ok":     res.OK(),
		"errors": res.Errors,
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
	if !res.OK() {
		os.Exit(1)
	}
}
