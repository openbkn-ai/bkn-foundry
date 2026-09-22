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
	"path"
	"strings"
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
	mfs := NewMemoryFileSystem()
	tr := tar.NewReader(reader)

	var (
		rootDir    string
		entryCount int
		totalSize  int64
	)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, "", fmt.Errorf("failed to read tar header: %w", err)
		}
		entryCount++
		if entryCount > maxTarEntries {
			return nil, "", fmt.Errorf("tar contains more than %d entries", maxTarEntries)
		}
		// Global PAX headers describe archive metadata rather than filesystem entries.
		if header.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		if header.Typeflag == tar.TypeDir && (header.Name == "." || header.Name == "./") {
			continue
		}

		safePath, err := normalizeTarEntryPath(header.Name)
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

		base := path.Base(safePath)
		// Skip macOS AppleDouble extended-attribute files (._*) to avoid parsing empty ObjectTypes.
		if strings.HasPrefix(base, "._") {
			continue
		}

		// Process only supported file types (.bkn and .md) and the CHECKSUM file.
		ext := strings.ToLower(path.Ext(safePath))
		if !SupportedExtensions[ext] && base != ChecksumFileName {
			continue
		}
		if header.Size < 0 || header.Size > maxTarFileSize {
			return nil, "", fmt.Errorf("tar entry %q exceeds the %d-byte file limit", header.Name, maxTarFileSize)
		}
		if header.Size > maxTarTotalSize-totalSize {
			return nil, "", fmt.Errorf("tar contents exceed the %d-byte total limit", maxTarTotalSize)
		}
		totalSize += header.Size

		// Read file contents.
		content := make([]byte, int(header.Size))
		if _, err := io.ReadFull(tr, content); err != nil {
			return nil, "", fmt.Errorf("failed to read file %s: %w", header.Name, err)
		}

		mfs.AddFile(safePath, content)

		// Check whether this is a root-file candidate and record its directory.
		if strings.EqualFold(base, RootFileName) {
			rootDir = path.Dir(safePath)
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

func normalizeTarEntryPath(name string) (string, error) {
	if name == "" || strings.ContainsRune(name, '\x00') {
		return "", fmt.Errorf("invalid empty tar entry path")
	}
	if strings.ContainsRune(name, '\\') {
		return "", fmt.Errorf("tar entry %q uses a non-portable path separator", name)
	}
	if path.IsAbs(name) || (len(name) >= 2 && name[1] == ':') {
		return "", fmt.Errorf("tar entry %q uses an absolute path", name)
	}

	trimmed := name
	for strings.HasPrefix(trimmed, "./") {
		trimmed = strings.TrimPrefix(trimmed, "./")
	}
	cleaned := path.Clean(trimmed)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("tar entry %q escapes the archive root", name)
	}
	for _, segment := range strings.Split(trimmed, "/") {
		if segment == ".." {
			return "", fmt.Errorf("tar entry %q contains parent traversal", name)
		}
	}

	return cleaned, nil
}
