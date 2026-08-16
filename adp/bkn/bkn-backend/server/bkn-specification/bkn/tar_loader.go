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
	"path/filepath"
	"strings"
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
	mfs := NewMemoryFileSystem()
	tr := tar.NewReader(reader)

	var rootDir string

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, "", fmt.Errorf("failed to read tar header: %w", err)
		}

		// Skip directories.
		if header.Typeflag == tar.TypeDir {
			continue
		}

		base := filepath.Base(header.Name)
		// Skip macOS AppleDouble extended-attribute files (._*) to avoid parsing empty ObjectTypes.
		if strings.HasPrefix(base, "._") {
			if _, err := io.CopyN(io.Discard, tr, header.Size); err != nil {
				return nil, "", fmt.Errorf("failed to skip %s body: %w", header.Name, err)
			}
			continue
		}

		// Process only supported file types (.bkn and .md) and the CHECKSUM file.
		ext := strings.ToLower(filepath.Ext(header.Name))
		if !SupportedExtensions[ext] && base != ChecksumFileName {
			if _, err := io.CopyN(io.Discard, tr, header.Size); err != nil {
				return nil, "", fmt.Errorf("failed to skip %s body: %w", header.Name, err)
			}
			continue
		}

		// Read file contents.
		content := make([]byte, header.Size)
		if _, err := io.ReadFull(tr, content); err != nil {
			return nil, "", fmt.Errorf("failed to read file %s: %w", header.Name, err)
		}

		// Normalize paths by removing the leading "./" and using / as the separator.
		path := strings.TrimPrefix(filepath.ToSlash(header.Name), "./")
		mfs.AddFile(path, content)

		// Check whether this is a root-file candidate and record its directory.
		if strings.EqualFold(base, RootFileName) {
			rootDir = filepath.Dir(path)
			if rootDir == "" {
				rootDir = "."
			}
		}
	}

	if rootDir == "" {
		return nil, "", fmt.Errorf("no root network file found in tar")
	}

	return mfs, rootDir, nil
}
