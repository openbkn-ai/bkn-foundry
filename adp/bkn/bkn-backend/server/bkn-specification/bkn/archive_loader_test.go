// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"hash/crc32"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const archiveTestExample = "../examples/k8s-network"

func packExample(t *testing.T, gzip bool) []byte {
	t.Helper()
	out := filepath.Join(t.TempDir(), "network.tar")
	require.NoError(t, PackDirToTar(archiveTestExample, out, gzip))
	data, err := os.ReadFile(out)
	require.NoError(t, err)
	return data
}

// zipExample zips the example the way macOS "Compress" does: under one top-level folder, with a
// __MACOSX/ folder of AppleDouble files beside it.
func zipExample(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	err := filepath.WalkDir(archiveTestExample, func(p string, d fs.DirEntry, err error) error {
		require.NoError(t, err)
		rel, _ := filepath.Rel(archiveTestExample, p)
		name := "k8s-network/" + filepath.ToSlash(rel)
		if d.IsDir() {
			if rel != "." {
				_, err = zw.Create(name + "/")
			}
			return err
		}
		data, err := os.ReadFile(p)
		require.NoError(t, err)
		w, err := zw.Create(name)
		require.NoError(t, err)
		_, err = w.Write(data)
		return err
	})
	require.NoError(t, err)
	w, err := zw.Create("__MACOSX/k8s-network/._network.bkn")
	require.NoError(t, err)
	_, _ = w.Write([]byte("resource fork"))
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

// canonical renders a loaded network independently of the order definitions were read in.
func canonical(t *testing.T, net *BknNetwork) string {
	t.Helper()
	sort.Slice(net.ObjectTypes, func(i, j int) bool { return net.ObjectTypes[i].ID < net.ObjectTypes[j].ID })
	sort.Slice(net.RelationTypes, func(i, j int) bool { return net.RelationTypes[i].ID < net.RelationTypes[j].ID })
	sort.Slice(net.ActionTypes, func(i, j int) bool { return net.ActionTypes[i].ID < net.ActionTypes[j].ID })
	sort.Slice(net.ConceptGroups, func(i, j int) bool { return net.ConceptGroups[i].ID < net.ConceptGroups[j].ID })
	out, err := json.Marshal(net)
	require.NoError(t, err)
	return string(out)
}

// Every format loads the same network as the plain tar it was made from.
func TestLoadNetworkFromArchive_FormatsLoadTheSameNetwork(t *testing.T) {
	fromTar, err := LoadNetworkFromTar(bytes.NewReader(packExample(t, false)))
	require.NoError(t, err)
	want := canonical(t, fromTar)

	for name, data := range map[string][]byte{
		"tar":    packExample(t, false),
		"tar.gz": packExample(t, true),
		"zip":    zipExample(t),
	} {
		got, err := LoadNetworkFromArchive(bytes.NewReader(data))
		require.NoError(t, err, name)
		assert.Equal(t, want, canonical(t, got), name)
	}
}

// A plain tar goes down the same path as before, errors included.
func TestLoadNetworkFromArchive_TarErrorsUnchanged(t *testing.T) {
	_, tarErr := LoadNetworkFromTar(strings.NewReader("not an archive"))
	_, archiveErr := LoadNetworkFromArchive(strings.NewReader("not an archive"))
	require.Error(t, tarErr)
	assert.Equal(t, tarErr.Error(), archiveErr.Error())
}

func zipWith(t *testing.T, build func(zw *zip.Writer)) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("net/network.bkn")
	require.NoError(t, err)
	_, _ = w.Write([]byte("---\ntype: knowledge_network\nid: kn\nname: kn\n---\n"))
	build(zw)
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func TestExtractZipToMemory_RefusesUnsafeEntries(t *testing.T) {
	for _, tc := range []struct {
		name  string
		want  string
		build func(zw *zip.Writer)
	}{
		{"parent traversal", "escapes the archive root", func(zw *zip.Writer) {
			_, _ = zw.Create("../evil.bkn")
		}},
		{"absolute path", "uses an absolute path", func(zw *zip.Writer) {
			_, _ = zw.Create("/etc/evil.bkn")
		}},
		{"symlink", "unsupported zip entry type", func(zw *zip.Writer) {
			h := &zip.FileHeader{Name: "net/object_types/link.bkn"}
			h.SetMode(fs.ModeSymlink | 0o777)
			w, _ := zw.CreateHeader(h)
			_, _ = w.Write([]byte("/etc/passwd"))
		}},
		{"legacy metrics", "legacy metrics", func(zw *zip.Writer) {
			_, _ = zw.Create("net/metrics/m.bkn")
		}},
	} {
		_, err := LoadNetworkFromArchive(bytes.NewReader(zipWith(t, tc.build)))
		assert.ErrorContains(t, err, tc.want, tc.name)
	}
}

// The limits are checked against the size in the entry header, so an entry holding more than its
// header claims must be refused rather than read past the limit. archive/zip already rejects it;
// ExtractZipToMemory checks the length again so the limit does not depend on that.
func TestExtractZipToMemory_RefusesEntryLargerThanItsHeader(t *testing.T) {
	data := zipWith(t, func(zw *zip.Writer) {
		content := []byte("12345")
		w, err := zw.CreateRaw(&zip.FileHeader{
			Name:               "net/object_types/pod.bkn",
			Method:             zip.Store,
			CRC32:              crc32.ChecksumIEEE(content),
			CompressedSize64:   uint64(len(content)),
			UncompressedSize64: 3,
		})
		require.NoError(t, err)
		_, _ = w.Write(content)
	})
	_, err := LoadNetworkFromArchive(bytes.NewReader(data))
	assert.ErrorContains(t, err, "failed to extract zip")
}

func TestExtractZipToMemory_RequiresRootFile(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("net/object_types/pod.bkn")
	_, _ = w.Write([]byte("---\ntype: object_type\nid: pod\n---\n"))
	require.NoError(t, zw.Close())
	_, err := LoadNetworkFromArchive(bytes.NewReader(buf.Bytes()))
	assert.ErrorContains(t, err, "no root network file found in zip")
}

func TestExpansionLimitReader(t *testing.T) {
	r := &expansionLimitReader{r: strings.NewReader("abcdef"), remaining: 4}
	got, err := io.ReadAll(r)
	assert.Equal(t, "abcd", string(got))
	assert.True(t, errors.Is(err, errGzipExpansion))
}
