// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
)

const (
	// maxGzipExpandedSize caps the decompressed stream of a tar.gz. The tar reader still applies
	// its per-entry and total limits to the files it keeps, but it reads through skipped entries
	// too, so without this cap a small archive could expand into an unbounded amount of work.
	maxGzipExpandedSize = 4 * maxTarTotalSize
	// maxZipArchiveSize caps the zip file itself, which has to be held in memory to read its
	// central directory.
	maxZipArchiveSize = 2 * maxTarTotalSize
)

var (
	gzipMagic = []byte{0x1f, 0x8b}
	// A zip starts with a local file header, or with the end-of-central-directory record when it
	// holds no files.
	zipMagic      = []byte("PK\x03\x04")
	zipEmptyMagic = []byte("PK\x05\x06")
)

// LoadNetworkFromArchive loads a BKN network from an uploaded archive: a plain tar, a gzip
// compressed tar, or a zip. The format is read from the first bytes rather than from a file name,
// so a mislabeled upload still loads. Anything that is neither gzip nor zip goes to the tar reader
// exactly as LoadNetworkFromTar always has.
func LoadNetworkFromArchive(r io.Reader) (*BknNetwork, error) {
	br := bufio.NewReader(r)
	magic, _ := br.Peek(len(zipMagic))

	switch {
	case bytes.HasPrefix(magic, gzipMagic):
		gz, err := gzip.NewReader(br)
		if err != nil {
			return nil, fmt.Errorf("failed to read gzip archive: %w", err)
		}
		defer func() { _ = gz.Close() }()
		mfs, rootDir, err := ExtractTarToMemory(&expansionLimitReader{r: gz, remaining: maxGzipExpandedSize})
		if err != nil {
			return nil, fmt.Errorf("failed to extract tar.gz: %w", err)
		}
		return LoadNetworkWithFS(mfs, rootDir)

	case bytes.HasPrefix(magic, zipMagic) || bytes.HasPrefix(magic, zipEmptyMagic):
		data, err := io.ReadAll(io.LimitReader(br, maxZipArchiveSize+1))
		if err != nil {
			return nil, fmt.Errorf("failed to read zip archive: %w", err)
		}
		if int64(len(data)) > maxZipArchiveSize {
			return nil, fmt.Errorf("zip archive exceeds the %d-byte limit", maxZipArchiveSize)
		}
		mfs, rootDir, err := ExtractZipToMemory(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, fmt.Errorf("failed to extract zip: %w", err)
		}
		return LoadNetworkWithFS(mfs, rootDir)
	}

	return LoadNetworkFromTar(br)
}

// errGzipExpansion is returned once a tar.gz has decompressed past maxGzipExpandedSize.
var errGzipExpansion = errors.New("tar.gz expands beyond the size limit")

type expansionLimitReader struct {
	r         io.Reader
	remaining int64
}

func (l *expansionLimitReader) Read(p []byte) (int, error) {
	if l.remaining <= 0 {
		return 0, errGzipExpansion
	}
	if int64(len(p)) > l.remaining {
		p = p[:l.remaining]
	}
	n, err := l.r.Read(p)
	l.remaining -= int64(n)
	return n, err
}

// ExtractZipToMemory extracts a zip archive into the in-memory file system under the same rules as
// ExtractTarToMemory: the same path checks, file filter and size limits, and the same root
// discovery. macOS "Compress" adds a __MACOSX/ folder of resource forks; it is skipped.
func ExtractZipToMemory(r io.ReaderAt, size int64) (*MemoryFileSystem, string, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read zip directory: %w", err)
	}

	c := newArchiveCollector("zip")
	for _, f := range zr.File {
		if err := c.countEntry(); err != nil {
			return nil, "", err
		}
		if f.Name == "." || f.Name == "./" || strings.HasPrefix(f.Name, "__MACOSX/") {
			continue
		}

		safePath, err := c.normalize(f.Name)
		if err != nil {
			return nil, "", err
		}

		mode := f.Mode()
		if mode.IsDir() {
			continue
		}
		if !mode.IsRegular() {
			return nil, "", fmt.Errorf("unsupported zip entry type for %q", f.Name)
		}

		keep, err := c.filter(safePath)
		if err != nil {
			return nil, "", err
		}
		if !keep {
			continue
		}

		declared := f.UncompressedSize64
		if declared > uint64(maxTarFileSize) {
			return nil, "", fmt.Errorf("zip entry %q exceeds the %d-byte file limit", f.Name, maxTarFileSize)
		}
		if err := c.reserve(f.Name, safePath, int64(declared)); err != nil {
			return nil, "", err
		}

		// The size in the header is what the limits were checked against, so the entry may not
		// decompress to more than that.
		rc, err := f.Open()
		if err != nil {
			return nil, "", fmt.Errorf("failed to open file %s: %w", f.Name, err)
		}
		content, err := io.ReadAll(io.LimitReader(rc, int64(declared)+1))
		_ = rc.Close()
		if err != nil {
			return nil, "", fmt.Errorf("failed to read file %s: %w", f.Name, err)
		}
		if uint64(len(content)) != declared {
			return nil, "", fmt.Errorf("zip entry %q does not match the size in its header", f.Name)
		}

		c.add(safePath, content)
	}

	return c.finish()
}

// archiveCollector holds the rules every archive format is extracted under. The tar and zip
// readers differ only in how they walk entries and which entry types they know; what is kept,
// how large it may be and where the root is are decided here, once.
//
// kind names the format in error messages, so the tar reader's messages read as they always have.
type archiveCollector struct {
	kind              string
	mfs               *MemoryFileSystem
	rootDir           string
	entryCount        int
	totalSize         int64
	objectMetricPaths map[string]struct{}
}

func newArchiveCollector(kind string) *archiveCollector {
	return &archiveCollector{
		kind:              kind,
		mfs:               NewMemoryFileSystem(),
		objectMetricPaths: make(map[string]struct{}),
	}
}

func (c *archiveCollector) countEntry() error {
	c.entryCount++
	if c.entryCount > maxTarEntries {
		return fmt.Errorf("%s contains more than %d entries", c.kind, maxTarEntries)
	}
	return nil
}

func (c *archiveCollector) normalize(name string) (string, error) {
	return normalizeArchiveEntryPath(c.kind, name)
}

// filter reports whether a regular file is part of the network, and refuses the legacy metrics
// layout outright.
func (c *archiveCollector) filter(safePath string) (bool, error) {
	base := path.Base(safePath)
	if strings.Contains("/"+safePath, "/metrics/") {
		return false, fmt.Errorf("legacy metrics/*.bkn is unsupported; migrate metrics to object metrics V1 before import")
	}
	// Skip macOS AppleDouble extended-attribute files (._*) to avoid parsing empty ObjectTypes.
	if strings.HasPrefix(base, "._") {
		return false, nil
	}

	// Process only supported file types (.bkn and .md) and the CHECKSUM file.
	ext := strings.ToLower(path.Ext(safePath))
	if !SupportedExtensions[ext] && base != ChecksumFileName &&
		(ext != ".json" || !strings.Contains("/"+safePath, "/object_metrics/")) {
		return false, nil
	}
	return true, nil
}

// reserve checks a kept file against the size limits before it is read.
func (c *archiveCollector) reserve(name, safePath string, size int64) error {
	if size < 0 || size > maxTarFileSize {
		return fmt.Errorf("%s entry %q exceeds the %d-byte file limit", c.kind, name, maxTarFileSize)
	}
	if size > maxTarTotalSize-c.totalSize {
		return fmt.Errorf("%s contents exceed the %d-byte total limit", c.kind, maxTarTotalSize)
	}
	c.totalSize += size
	ext := strings.ToLower(path.Ext(safePath))
	if ext == ".json" && strings.Contains("/"+safePath, "/object_metrics/") {
		if _, exists := c.objectMetricPaths[safePath]; exists {
			return fmt.Errorf("duplicate object metric archive file %q", safePath)
		}
		c.objectMetricPaths[safePath] = struct{}{}
	}
	return nil
}

// add stores a file and records the directory of network.bkn as the root.
func (c *archiveCollector) add(safePath string, content []byte) {
	c.mfs.AddFile(safePath, content)

	// Check whether this is a root-file candidate and record its directory.
	if strings.EqualFold(path.Base(safePath), RootFileName) {
		c.rootDir = path.Dir(safePath)
		if c.rootDir == "" {
			c.rootDir = "."
		}
	}
}

func (c *archiveCollector) finish() (*MemoryFileSystem, string, error) {
	if c.rootDir == "" {
		return nil, "", fmt.Errorf("no root network file found in %s", c.kind)
	}
	return c.mfs, c.rootDir, nil
}

func normalizeArchiveEntryPath(kind, name string) (string, error) {
	if name == "" || strings.ContainsRune(name, '\x00') {
		return "", fmt.Errorf("invalid empty %s entry path", kind)
	}
	if strings.ContainsRune(name, '\\') {
		return "", fmt.Errorf("%s entry %q uses a non-portable path separator", kind, name)
	}
	if path.IsAbs(name) || (len(name) >= 2 && name[1] == ':') {
		return "", fmt.Errorf("%s entry %q uses an absolute path", kind, name)
	}

	trimmed := name
	for strings.HasPrefix(trimmed, "./") {
		trimmed = strings.TrimPrefix(trimmed, "./")
	}
	cleaned := path.Clean(trimmed)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("%s entry %q escapes the archive root", kind, name)
	}
	for _, segment := range strings.Split(trimmed, "/") {
		if segment == ".." {
			return "", fmt.Errorf("%s entry %q contains parent traversal", kind, name)
		}
	}

	return cleaned, nil
}
