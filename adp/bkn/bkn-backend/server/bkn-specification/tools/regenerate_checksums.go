// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

//go:build ignore

// Rewrite the CHECKSUM file of every example network under <examples-dir>.
//
// Unlike regenerate_examples.go this touches nothing but CHECKSUM: definition files keep their
// current bytes. Run it after changing what a checksum covers.
//
// Usage:
//
//	go run regenerate_checksums.go <examples-dir>
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"bkn-backend/bkn-specification/bkn"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: go run regenerate_checksums.go <examples-dir>")
		os.Exit(1)
	}
	root := os.Args[1]
	entries, err := os.ReadDir(root)
	if err != nil {
		fmt.Printf("read %s: %v\n", root, err)
		os.Exit(1)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		if _, err := os.Stat(filepath.Join(dir, "network.bkn")); err != nil {
			continue
		}
		if _, err := bkn.GenerateChecksumFile(dir); err != nil {
			fmt.Printf("%s: %v\n", entry.Name(), err)
			os.Exit(1)
		}
		fmt.Printf("regenerated %s/CHECKSUM\n", entry.Name())
	}
}
