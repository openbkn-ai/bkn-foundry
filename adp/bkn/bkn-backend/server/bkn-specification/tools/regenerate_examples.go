// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

//go:build ignore

// Regenerate example BKN directories using the current SDK serializer.
//
// Usage:
//
//	go run regenerate_examples.go <examples-dir>
//
// For every immediate subdirectory of <examples-dir> that contains a
// network.bkn, the network is loaded, re-serialized via WriteNetworkToTar,
// and the resulting tar is extracted on top of the original directory.
// CHECKSUM and SKILL.md are regenerated as part of the same pass.
//
// Run this after changing serializer output (table formatting, field
// ordering, etc.) to keep examples/ aligned with the SDK.
package main

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"bkn-backend/bkn-specification/bkn"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: go run regenerate_examples.go <examples-dir>")
		os.Exit(1)
	}

	examplesDir := os.Args[1]
	entries, err := os.ReadDir(examplesDir)
	if err != nil {
		fmt.Printf("Error reading dir: %v\n", err)
		os.Exit(1)
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(examplesDir, e.Name())
		if _, err := os.Stat(filepath.Join(dir, "network.bkn")); err != nil {
			continue
		}

		fmt.Printf("Regenerating %s...\n", e.Name())
		if err := regenerate(dir); err != nil {
			fmt.Printf("  Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("  Done\n")
	}
}

func regenerate(dir string) error {
	doc, err := bkn.LoadNetwork(dir)
	if err != nil {
		return fmt.Errorf("load: %w", err)
	}

	var buf bytes.Buffer
	if err := bkn.WriteNetworkToTar(doc, &buf); err != nil {
		return fmt.Errorf("write tar: %w", err)
	}

	tr := tar.NewReader(&buf)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar: %w", err)
		}
		if hdr.Typeflag == tar.TypeDir {
			continue
		}
		dest := filepath.Join(dir, filepath.FromSlash(hdr.Name))
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return err
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dest, data, 0644); err != nil {
			return err
		}
	}
	return nil
}
