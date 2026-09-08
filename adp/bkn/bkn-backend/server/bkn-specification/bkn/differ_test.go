// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const networkFileWithCapabilities = `---
type: knowledge_network
id: supplychain
name: 供应链主网
tags: [供应链, 采购]
capabilities:
  skills:
    - id: skill-a
      name: 库存盘点
  functions:
    - box_id: box-1
      tool_id: tool-1
      box_name: 采购工具箱
      tool_name: 下单
---

# 供应链主网

网络说明。
`

const objectTypeFile = `---
type: object_type
id: bom
name: 产品BOM
tags: [供应链, BOM, 已审核]
---

## ObjectType: 产品BOM

定义产品的物料清单结构。
`

// newNetworkFS lays out a minimal network directory in memory.
func newNetworkFS(networkFile, bomFile string) *MemoryFileSystem {
	mfs := NewMemoryFileSystem()
	mfs.AddFile("/net/network.bkn", []byte(networkFile))
	mfs.AddFile("/net/object_types/bom.bkn", []byte(bomFile))
	return mfs
}

func TestDiffNetworks_ClassifiesEveryAction(t *testing.T) {
	oldSums := map[string]string{
		"object_type:bom":           "sha256:aaaa",
		"object_type:material":      "sha256:bbbb",
		"relation_type:po2supplier": "sha256:cccc",
	}
	newSums := map[string]string{
		"object_type:bom":      "sha256:dddd", // changed
		"object_type:material": "sha256:bbbb", // untouched
		"object_type:batch":    "sha256:eeee", // added
	}

	result := DiffNetworks(oldSums, newSums)
	require.NotNil(t, result)
	assert.True(t, result.HasChanges())

	require.Len(t, result.Updates(), 1)
	assert.Equal(t, "object_type", result.Updates()[0].Type)
	assert.Equal(t, "bom", result.Updates()[0].ID)
	assert.Equal(t, "sha256:aaaa", result.Updates()[0].OldChecksum)
	assert.Equal(t, "sha256:dddd", result.Updates()[0].NewChecksum)

	require.Len(t, result.Creates(), 1)
	assert.Equal(t, "object_type", result.Creates()[0].Type)
	assert.Equal(t, "batch", result.Creates()[0].ID)
	assert.Empty(t, result.Creates()[0].OldChecksum)

	require.Len(t, result.Deletes(), 1)
	assert.Equal(t, "relation_type", result.Deletes()[0].Type)
	assert.Equal(t, "po2supplier", result.Deletes()[0].ID)
	assert.Empty(t, result.Deletes()[0].NewChecksum)

	require.Len(t, result.Skips(), 1)
	assert.Equal(t, "material", result.Skips()[0].ID)
}

func TestDiffNetworks_IdenticalNetworksReportNoChanges(t *testing.T) {
	sums := map[string]string{"object_type:bom": "sha256:aaaa"}
	assert.False(t, DiffNetworks(sums, sums).HasChanges())
}

// A definition's name and tags live in its frontmatter. They were once outside the checksum, so
// renaming or retagging a definition produced an identical hash and a diff reported "unchanged".
func TestComputeNetworkChecksums_DetectsFrontmatterOnlyChange(t *testing.T) {
	renamed := strings.Replace(objectTypeFile, "name: 产品BOM", "name: 产品物料清单", 1)
	renamed = strings.Replace(renamed, "已审核", "已废止", 1)
	require.NotEqual(t, objectTypeFile, renamed)

	base, err := ComputeNetworkChecksums(newNetworkFS(networkFileWithCapabilities, objectTypeFile), "/net")
	require.NoError(t, err)
	changed, err := ComputeNetworkChecksums(newNetworkFS(networkFileWithCapabilities, renamed), "/net")
	require.NoError(t, err)

	require.Contains(t, base, "object_type:bom")
	assert.NotEqual(t, base["object_type:bom"], changed["object_type:bom"],
		"a frontmatter-only edit must change the definition's checksum")
	assert.True(t, DiffNetworks(base, changed).HasChanges())
}

// The root file carries the capability dependency block. It is a definition like any other and
// must take part in the diff.
func TestComputeNetworkChecksums_CoversTheNetworkFile(t *testing.T) {
	base, err := ComputeNetworkChecksums(newNetworkFS(networkFileWithCapabilities, objectTypeFile), "/net")
	require.NoError(t, err)
	require.Contains(t, base, "network", "the root file must contribute a checksum entry")

	rebound := strings.Replace(networkFileWithCapabilities, "name: 库存盘点", "name: 库存盘点V2", 1)
	require.NotEqual(t, networkFileWithCapabilities, rebound)

	changed, err := ComputeNetworkChecksums(newNetworkFS(rebound, objectTypeFile), "/net")
	require.NoError(t, err)
	assert.NotEqual(t, base["network"], changed["network"],
		"rebinding a capability must change the network checksum")
}

// Frontmatter is decoded into a map, and Go randomizes map iteration order. Without a canonical
// rendering the same bytes would hash differently between runs and every diff would be noise.
func TestComputeNetworkChecksums_StableAcrossRuns(t *testing.T) {
	first, err := ComputeNetworkChecksums(newNetworkFS(networkFileWithCapabilities, objectTypeFile), "/net")
	require.NoError(t, err)

	for i := 0; i < 20; i++ {
		again, err := ComputeNetworkChecksums(newNetworkFS(networkFileWithCapabilities, objectTypeFile), "/net")
		require.NoError(t, err)
		assert.Equal(t, first, again, "checksums must not depend on map iteration order")
	}
}

func TestGenerateChecksumFileWithFS_DeclaresCurrentFormat(t *testing.T) {
	mfs := newNetworkFS(networkFileWithCapabilities, objectTypeFile)
	content, err := GenerateChecksumFileWithFS(mfs, "/net")
	require.NoError(t, err)

	assert.Contains(t, content, "# format: 2")
	assert.Equal(t, checksumFormatCurrent, declaredChecksumFormat(content))

	ok, msgs := VerifyChecksumFileWithFS(mfs, "/net")
	assert.True(t, ok, "a freshly generated CHECKSUM must verify: %v", msgs)
}

// CHECKSUM files written before the format header exist in packages already shipped. They declare
// body-only hashes and must keep verifying, or every import of an existing package would warn.
func TestVerifyChecksumFileWithFS_LegacyFileStillVerifies(t *testing.T) {
	mfs := newNetworkFS(networkFileWithCapabilities, objectTypeFile)

	legacy := []string{"# BKN Directory Checksum", "# generated: 2026-08-04T17:17:22+08:00"}
	legacy = append(legacy, computeBknChecksumWithFS(mfs, "/net/object_types/bom.bkn", checksumFormatBodyOnly)...)
	legacy = append(legacy, computeBknChecksumWithFS(mfs, "/net/network.bkn", checksumFormatBodyOnly)...)
	require.NoError(t, mfs.WriteFile("/net/"+ChecksumFileName, []byte(strings.Join(legacy, "\n")+"\n"), 0644))

	assert.Equal(t, checksumFormatBodyOnly, declaredChecksumFormat(strings.Join(legacy, "\n")))

	ok, msgs := VerifyChecksumFileWithFS(mfs, "/net")
	assert.True(t, ok, "a legacy CHECKSUM must verify under format 1: %v", msgs)
}

// A legacy file is verified under format 1, so an edit the old checksum could not see stays
// invisible there. The guarantee is only that verification does not turn into false alarms.
func TestVerifyChecksumFileWithFS_RejectsBodyEditUnderLegacyFormat(t *testing.T) {
	mfs := newNetworkFS(networkFileWithCapabilities, objectTypeFile)

	legacy := []string{"# BKN Directory Checksum"}
	legacy = append(legacy, computeBknChecksumWithFS(mfs, "/net/object_types/bom.bkn", checksumFormatBodyOnly)...)
	require.NoError(t, mfs.WriteFile("/net/"+ChecksumFileName, []byte(strings.Join(legacy, "\n")+"\n"), 0644))

	edited := strings.Replace(objectTypeFile, "定义产品的物料清单结构。", "定义产品的物料清单结构（已修订）。", 1)
	mfs.AddFile("/net/object_types/bom.bkn", []byte(edited))

	ok, msgs := VerifyChecksumFileWithFS(mfs, "/net")
	assert.False(t, ok)
	assert.NotEmpty(t, msgs)
}
