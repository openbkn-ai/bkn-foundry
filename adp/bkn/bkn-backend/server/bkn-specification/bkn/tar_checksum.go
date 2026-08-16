// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"fmt"
	"io"
	"path/filepath"
)

// ComputeChecksumFromTar calculates checksums for all definitions in a tar stream.
// It returns map["type:id"] = "sha256:hash".
func ComputeChecksumFromTar(r io.Reader) (map[string]string, error) {
	mfs, rootFile, err := ExtractTarToMemory(r)
	if err != nil {
		return nil, fmt.Errorf("failed to extract tar: %w", err)
	}
	rootDir := filepath.Dir(rootFile)
	return ComputeNetworkChecksums(mfs, rootDir)
}

// GenerateChecksumFromTar generates CHECKSUM file content from a tar stream.
// It returns the CHECKSUM file content as a string.
func GenerateChecksumFromTar(r io.Reader) (string, error) {
	mfs, rootFile, err := ExtractTarToMemory(r)
	if err != nil {
		return "", fmt.Errorf("failed to extract tar: %w", err)
	}
	rootDir := filepath.Dir(rootFile)
	return GenerateChecksumFileWithFS(mfs, rootDir)
}

// VerifyChecksumFromTar verifies that the CHECKSUM file in a tar stream matches its actual contents.
// The tar archive must contain a CHECKSUM file.
func VerifyChecksumFromTar(r io.Reader) (bool, []string) {
	mfs, rootFile, err := ExtractTarToMemory(r)
	if err != nil {
		return false, []string{fmt.Sprintf("failed to extract tar: %v", err)}
	}
	rootDir := filepath.Dir(rootFile)
	return VerifyChecksumFileWithFS(mfs, rootDir)
}

// DiffNetworksFromTar compares definition differences between two tar archives.
// It returns a DiffResult containing create, update, skip, and delete entries.
func DiffNetworksFromTar(oldTar, newTar io.Reader) (*DiffResult, error) {
	oldChecksums, err := ComputeChecksumFromTar(oldTar)
	if err != nil {
		return nil, fmt.Errorf("failed to compute old checksums: %w", err)
	}
	newChecksums, err := ComputeChecksumFromTar(newTar)
	if err != nil {
		return nil, fmt.Errorf("failed to compute new checksums: %w", err)
	}
	return DiffNetworks(oldChecksums, newChecksums), nil
}
