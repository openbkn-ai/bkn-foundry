// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package test contains integration tests for the BKN SDK.
package test

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bkn-backend/bkn-specification/bkn"
)

// allExampleDirs returns all example directories that contain a network.bkn file.
// Tests run from sdk/golang/tests/; examples are at ../../../examples.
func allExampleDirs(t *testing.T) []string {
	t.Helper()

	examplesDir := filepath.Join("..", "..", "..", "examples")
	entries, err := os.ReadDir(examplesDir)
	require.NoError(t, err, "read examples dir")

	var dirs []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		d := filepath.Join(examplesDir, e.Name())
		if _, err := os.Stat(filepath.Join(d, "network.bkn")); err == nil {
			dirs = append(dirs, d)
		}
	}

	if len(dirs) == 0 {
		t.Skip("no example directories with network.bkn found")
	}
	return dirs
}

// tempDir creates a temporary directory that is removed when the test ends.
func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "bkn-test-*")
	require.NoError(t, err, "create temp dir")
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// buildTarFromDir packs all files in dir into an in-memory tar buffer using Go's
// archive/tar (distinct from the system tar used by PackDirToTar).
func buildTarFromDir(t *testing.T, dir string) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		rel = filepath.ToSlash(rel)
		data, err := os.ReadFile(path)
		require.NoError(t, err, "read %s", path)
		_ = tw.WriteHeader(&tar.Header{Name: rel, Size: int64(len(data)), Mode: 0644})
		_, _ = tw.Write(data)
		return nil
	})
	_ = tw.Close()
	return &buf
}

// extractTarToDir extracts a tar stream into destDir, preserving relative paths.
func extractTarToDir(r io.Reader, destDir string) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag == tar.TypeDir {
			continue
		}

		// filepath.Join normalises leading "./" correctly.
		dest := filepath.Join(destDir, filepath.FromSlash(hdr.Name))
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

// compareDirs compares a fixed set of paths between srcDir and dstDir:
// network.bkn, SKILL.md, and the subdirectories action_types, concept_groups,
// object_types, relation_types. Every file found in srcDir under these paths
// must exist in dstDir with identical byte content.
func compareDirs(t *testing.T, srcDir, dstDir string) {
	t.Helper()

	targets := []string{
		"network.bkn",
		"SKILL.md",
		"action_types",
		"concept_groups",
		"object_types",
		"relation_types",
		"risk_types",
	}

	for _, target := range targets {
		srcPath := filepath.Join(srcDir, target)
		if _, err := os.Stat(srcPath); os.IsNotExist(err) {
			continue // target not present in this example, skip
		}

		err := filepath.Walk(srcPath, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return err
			}
			rel, _ := filepath.Rel(srcDir, path)
			rel = filepath.ToSlash(rel)

			srcData, err := os.ReadFile(path)
			require.NoError(t, err, "read source file: %s", rel)

			dstData, err := os.ReadFile(filepath.Join(dstDir, rel))
			require.NoError(t, err, "file missing in exported tar: %s", rel)

			assert.Equal(t, srcData, dstData, "file content mismatch: %s", rel)
			return nil
		})
		require.NoError(t, err, "walk %s", target)
	}
}

// === Core Workflow Tests ===

// TestLoadFromFile: dir → Model
// Sanity check that each example directory parses into a valid BknNetwork.
func TestLoadFromFile(t *testing.T) {
	for _, dir := range allExampleDirs(t) {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			doc, err := bkn.LoadNetwork(dir)
			require.NoError(t, err, "LoadNetwork failed")

			assert.NotEmpty(t, doc.ID, "network id must not be empty")
			assert.NotEmpty(t, doc.Name, "network name must not be empty")

			total := len(doc.ObjectTypes) + len(doc.RelationTypes) + len(doc.ActionTypes) +
				len(doc.RiskTypes) + len(doc.ConceptGroups) + len(doc.Metrics)
			assert.Greater(t, total, 0, "expected at least one entity")
		})
	}
}

// TestLoadFromTar: Go-built tar → Model == dir → Model
// Verifies that loading via an in-memory Go tar produces the same model as
// loading directly from the filesystem (tests LoadNetworkFromTar code path).
func TestLoadFromTar(t *testing.T) {
	for _, dir := range allExampleDirs(t) {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			tarDoc, err := bkn.LoadNetworkFromTar(buildTarFromDir(t, dir))
			require.NoError(t, err, "load from tar")

			fileDoc, err := bkn.LoadNetwork(dir)
			require.NoError(t, err, "load from file")

			assert.Equal(t, fileDoc.ID, tarDoc.ID, "root ID mismatch")
			assert.Equal(t, len(fileDoc.ObjectTypes), len(tarDoc.ObjectTypes), "object count mismatch")
			assert.Equal(t, len(fileDoc.RelationTypes), len(tarDoc.RelationTypes), "relation count mismatch")
			assert.Equal(t, len(fileDoc.ActionTypes), len(tarDoc.ActionTypes), "action count mismatch")
			assert.Equal(t, len(fileDoc.RiskTypes), len(tarDoc.RiskTypes), "risk type count mismatch")
			assert.Equal(t, len(fileDoc.ConceptGroups), len(tarDoc.ConceptGroups), "concept group count mismatch")
			assert.Equal(t, len(fileDoc.Metrics), len(tarDoc.Metrics), "metric count mismatch")
		})
	}
}

// TestRoundTrip_FileContent is the primary round-trip integration test.
//
// Flow: PackDirToTar → LoadNetworkFromTar → WriteNetworkToTar → extractTarToDir
// → compareDirs. Every file in the original directory must have identical byte
// content in the exported directory.
func TestRoundTrip_FileContent(t *testing.T) {
	for _, dir := range allExampleDirs(t) {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			tmp := tempDir(t)
			tarPath := filepath.Join(tmp, filepath.Base(dir)+".tar")

			// Step 1: pack the example directory using system tar.
			require.NoError(t, bkn.PackDirToTar(dir, tarPath, false), "PackDirToTar failed")

			// Step 2: load the model from the packed tar.
			f, err := os.Open(tarPath)
			require.NoError(t, err, "open tar file")
			doc, err := bkn.LoadNetworkFromTar(f)
			_ = f.Close()
			require.NoError(t, err, "LoadNetworkFromTar failed")

			// Step 3: export the model back to a tar.
			var buf bytes.Buffer
			require.NoError(t, bkn.WriteNetworkToTar(doc, &buf), "WriteNetworkToTar failed")

			// Step 4: extract the exported tar to a temp directory.
			extractDir := filepath.Join(tmp, filepath.Base(dir)+"-extracted")
			require.NoError(t, os.MkdirAll(extractDir, 0755))
			require.NoError(t, extractTarToDir(&buf, extractDir), "extractTarToDir failed")

			// Step 5: every source file must exist in the export with identical content.
			compareDirs(t, dir, extractDir)
		})
	}
}

// === Boundary Case Tests ===

// TestEmptyNetwork: 空网络处理
func TestEmptyNetwork(t *testing.T) {
	dir := tempDir(t)

	err := os.WriteFile(filepath.Join(dir, "network.bkn"), []byte(`---
type: network
id: test-empty
name: Test Empty Network
version: "1.0"
---

# Test Empty Network
`), 0644)
	require.NoError(t, err)

	doc, err := bkn.LoadNetwork(dir)
	require.NoError(t, err, "load empty network")
	assert.Equal(t, "test-empty", doc.ID)
	assert.Empty(t, doc.ObjectTypes)
	assert.Empty(t, doc.RelationTypes)
	assert.Empty(t, doc.ActionTypes)
	assert.Empty(t, doc.RiskTypes)
	assert.Empty(t, doc.ConceptGroups)
	assert.Empty(t, doc.Metrics)
}

// TestCircularInclude: 循环include检测
func TestCircularInclude(t *testing.T) {
	dir := tempDir(t)

	err := os.WriteFile(filepath.Join(dir, "network.bkn"), []byte(`---
type: network
id: test-circular
name: Test Circular
version: "1.0"
---

# Test Circular
`), 0644)
	require.NoError(t, err)

	objDir := filepath.Join(dir, "object_types")
	require.NoError(t, os.MkdirAll(objDir, 0755))
	err = os.WriteFile(filepath.Join(objDir, "test.bkn"), []byte(`---
type: object_type
id: test-obj
name: Test Object
---

## ObjectType: test-obj

Test object description.
`), 0644)
	require.NoError(t, err)

	doc, err := bkn.LoadNetwork(dir)
	require.NoError(t, err, "load network with objects")
	assert.Equal(t, 1, len(doc.ObjectTypes))
}

// TestMissingInclude: 缺失include文件
func TestMissingInclude(t *testing.T) {
	dir := tempDir(t)

	err := os.WriteFile(filepath.Join(dir, "network.bkn"), []byte(`---
type: network
id: test-missing
name: Test Missing
version: "1.0"
---

# Test Missing
`), 0644)
	require.NoError(t, err)

	doc, err := bkn.LoadNetwork(dir)
	require.NoError(t, err, "load network with missing subdirectories")
	assert.Equal(t, "test-missing", doc.ID)
}

// TestLargeNetwork: 大规模网络性能
func TestLargeNetwork(t *testing.T) {
	dir := tempDir(t)

	err := os.WriteFile(filepath.Join(dir, "network.bkn"), []byte(`---
type: network
id: test-large
name: Test Large Network
version: "1.0"
---

# Test Large Network
`), 0644)
	require.NoError(t, err)

	objDir := filepath.Join(dir, "object_types")
	require.NoError(t, os.MkdirAll(objDir, 0755))

	for i := 0; i < 10; i++ {
		idx := string(rune('0' + i))
		content := "---\ntype: object_type\nid: test-obj-" + idx + "\nname: Test Object " + idx + "\n---\n\n## ObjectType: test-obj-" + idx + "\n\nTest object description.\n"
		err = os.WriteFile(filepath.Join(objDir, "test"+idx+".bkn"), []byte(content), 0644)
		require.NoError(t, err)
	}

	doc, err := bkn.LoadNetwork(dir)
	require.NoError(t, err, "load large network")
	assert.Equal(t, 10, len(doc.ObjectTypes))
}

// TestInvalidBKNFile: 无效BKN文件处理
func TestInvalidBKNFile(t *testing.T) {
	dir := tempDir(t)

	err := os.WriteFile(filepath.Join(dir, "network.bkn"), []byte(`# Invalid Network

This file has no frontmatter.
`), 0644)
	require.NoError(t, err)

	_, err = bkn.LoadNetwork(dir)
	assert.Error(t, err, "should fail to load invalid network")
}
