// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"archive/tar"
	"fmt"
	"io"
)

const (
	maxTarEntries   = 10000
	maxTarFileSize  = int64(16 << 20)
	maxTarTotalSize = int64(64 << 20)
)

// LoadNetworkFromTar loads a BKN network directly from a tar archive.
// It processes the archive entirely in memory without writing to the local file system.
func LoadNetworkFromTar(tarReader io.Reader) (*BknNetwork, error) {
	// 1. Extract the tar archive into the in-memory file system.
	mfs, rootDir, err := ExtractTarToMemory(tarReader)
	if err != nil {
		return nil, fmt.Errorf("failed to extract tar: %w", err)
	}

	// 2. Load the network through the in-memory file system using the directory path.
	return LoadNetworkWithFS(mfs, rootDir)
}

// ExtractTarToMemory extracts a tar archive into the in-memory file system.
// It returns the in-memory file system and root directory path.
func ExtractTarToMemory(reader io.Reader) (*MemoryFileSystem, string, error) {
	tr := tar.NewReader(reader)
	c := newArchiveCollector("tar")

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, "", fmt.Errorf("failed to read tar header: %w", err)
		}
		if err := c.countEntry(); err != nil {
			return nil, "", err
		}
		// Global PAX headers describe archive metadata rather than filesystem entries.
		if header.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		if header.Typeflag == tar.TypeDir && (header.Name == "." || header.Name == "./") {
			continue
		}

		safePath, err := c.normalize(header.Name)
		if err != nil {
			return nil, "", err
		}

		// Skip directories.
		if header.Typeflag == tar.TypeDir {
			continue
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return nil, "", fmt.Errorf("unsupported tar entry type for %q", header.Name)
		}

		keep, err := c.filter(safePath)
		if err != nil {
			return nil, "", err
		}
		if !keep {
			continue
		}
		if err := c.reserve(header.Name, safePath, header.Size); err != nil {
			return nil, "", err
		}

		// Read file contents.
		content := make([]byte, int(header.Size))
		if _, err := io.ReadFull(tr, content); err != nil {
			return nil, "", fmt.Errorf("failed to read file %s: %w", header.Name, err)
		}

		c.add(safePath, content)
	}

	return c.finish()
}

func normalizeTarEntryPath(name string) (string, error) {
	return normalizeArchiveEntryPath("tar", name)
}
