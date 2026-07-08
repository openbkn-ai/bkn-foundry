// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"archive/tar"
	"bytes"
	"io/fs"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// === Tar Entry Tests ===

func TestWriteTarEntry_Success(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	defer func() { _ = tw.Close() }()

	content := []byte("test content")
	err := writeTarEntry(tw, "test.bkn", content, time.Now())
	require.NoError(t, err)
	_ = tw.Close()

	// Verify tar can be read
	tr := tar.NewReader(&buf)
	hdr, err := tr.Next()
	require.NoError(t, err)

	assert.Equal(t, "test.bkn", hdr.Name)
	assert.Equal(t, int64(len(content)), hdr.Size)
	assert.Equal(t, int64(0644), hdr.Mode)
}

// === Memory FileSystem Tests ===

func TestMemoryFileSystem_BasicOperations(t *testing.T) {
	mfs := NewMemoryFileSystem()

	// Add and read file
	mfs.AddFile("test.bkn", []byte("content"))
	data, err := mfs.ReadFile("test.bkn")
	require.NoError(t, err)
	assert.Equal(t, "content", string(data))

	// Stat file
	info, err := mfs.Stat("test.bkn")
	require.NoError(t, err)
	assert.False(t, info.IsDir())
	assert.Equal(t, "test.bkn", info.Name())

	// Read nonexistent file
	_, err = mfs.ReadFile("nonexistent.bkn")
	assert.Error(t, err)

	// Stat nonexistent file
	_, err = mfs.Stat("nonexistent.bkn")
	assert.Error(t, err)
}

func TestMemoryFileSystem_DirectoryOperations(t *testing.T) {
	mfs := NewMemoryFileSystem()

	// Add files in subdirectories
	mfs.AddFile("object_types/pod.bkn", []byte("pod content"))
	mfs.AddFile("object_types/node.bkn", []byte("node content"))
	mfs.AddFile("network.bkn", []byte("network content"))

	// Check directory detection
	assert.True(t, mfs.IsDir("object_types"))
	assert.False(t, mfs.IsDir("network.bkn"))

	// Read directory
	entries, err := mfs.ReadDir("object_types")
	require.NoError(t, err)
	assert.Len(t, entries, 2)

	// Walk directory
	var foundFiles []string
	err = mfs.Walk(".", func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			foundFiles = append(foundFiles, path)
		}
		return nil
	})
	require.NoError(t, err)
	assert.Len(t, foundFiles, 3)
}

// === Extract Tar to Memory Tests ===

func TestExtractTarToMemory_SingleNetworkFile(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	content := []byte("---\ntype: network\nid: test-network\nname: Test Network\n---\n")
	_ = tw.WriteHeader(&tar.Header{Name: "network.bkn", Size: int64(len(content)), Mode: 0644})
	_, _ = tw.Write(content)
	_ = tw.Close()

	mfs, rootDir, err := ExtractTarToMemory(&buf)
	require.NoError(t, err)
	assert.Equal(t, ".", rootDir)

	data, err := mfs.ReadFile("network.bkn")
	require.NoError(t, err)
	assert.Equal(t, string(content), string(data))
}

func TestExtractTarToMemory_WithSubdirectories(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	files := map[string]string{
		"network.bkn":            "---\ntype: network\nid: test\n---\n",
		"object_types/pod.bkn":   "---\ntype: object_type\nid: pod\n---\n",
		"object_types/node.bkn":  "---\ntype: object_type\nid: node\n---\n",
		"relation_types/rel.bkn": "---\ntype: relation_type\nid: rel\n---\n",
		"action_types/act.bkn":   "---\ntype: action_type\nid: act\n---\n",
		"SKILL.md":               "# Test Network\n",
		"CHECKSUM":               "# Checksum\nnetwork  sha256:abc123\n",
	}

	for name, content := range files {
		data := []byte(content)
		_ = tw.WriteHeader(&tar.Header{Name: name, Size: int64(len(data)), Mode: 0644})
		_, _ = tw.Write(data)
	}
	_ = tw.Close()

	mfs, rootDir, err := ExtractTarToMemory(&buf)
	require.NoError(t, err)
	assert.Equal(t, ".", rootDir)

	// Verify all files exist
	for name := range files {
		_, err := mfs.ReadFile(name)
		assert.NoError(t, err, "file %s should exist", name)
	}
}

func TestExtractTarToMemory_EmptyTar(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.Close()

	_, _, err := ExtractTarToMemory(&buf)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no root network file found")
}

func TestExtractTarToMemory_NoNetworkFile(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	content := []byte("---\ntype: object_type\nid: pod\n---\n")
	_ = tw.WriteHeader(&tar.Header{Name: "object_types/pod.bkn", Size: int64(len(content)), Mode: 0644})
	_, _ = tw.Write(content)
	_ = tw.Close()

	_, _, err := ExtractTarToMemory(&buf)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no root network file found")
}

func TestExtractTarToMemory_NestedRoot(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	content := []byte("---\ntype: network\nid: test\n---\n")
	_ = tw.WriteHeader(&tar.Header{Name: "myapp/network.bkn", Size: int64(len(content)), Mode: 0644})
	_, _ = tw.Write(content)
	_ = tw.Close()

	mfs, rootDir, err := ExtractTarToMemory(&buf)
	require.NoError(t, err)
	assert.Equal(t, "myapp", rootDir)

	data, err := mfs.ReadFile("myapp/network.bkn")
	require.NoError(t, err)
	assert.Equal(t, string(content), string(data))
}

func TestExtractTarToMemory_SkipsAppleDouble(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	networkContent := []byte("---\ntype: network\nid: test\n---\n")
	podContent := []byte("---\ntype: object_type\nid: pod\nname: Pod\n---\n")
	appleDoubleContent := []byte("invalid apple double")

	_ = tw.WriteHeader(&tar.Header{Name: "network.bkn", Size: int64(len(networkContent)), Mode: 0644})
	_, _ = tw.Write(networkContent)
	_ = tw.WriteHeader(&tar.Header{Name: "object_types/pod.bkn", Size: int64(len(podContent)), Mode: 0644})
	_, _ = tw.Write(podContent)
	_ = tw.WriteHeader(&tar.Header{Name: "object_types/._pod.bkn", Size: int64(len(appleDoubleContent)), Mode: 0644})
	_, _ = tw.Write(appleDoubleContent)
	_ = tw.Close()

	rawBytes := buf.Bytes()

	mfs, rootDir, err := ExtractTarToMemory(bytes.NewReader(rawBytes))
	require.NoError(t, err)
	assert.Equal(t, ".", rootDir)

	_, err = mfs.ReadFile("object_types/pod.bkn")
	require.NoError(t, err)

	_, err = mfs.ReadFile("object_types/._pod.bkn")
	assert.Error(t, err, "._pod.bkn should be skipped")

	loaded, err := LoadNetworkFromTar(bytes.NewReader(rawBytes))
	require.NoError(t, err)
	assert.Len(t, loaded.ObjectTypes, 1)
	assert.Equal(t, "pod", loaded.ObjectTypes[0].ID)
}

// === Network Serialization Tests ===

func TestWriteNetworkToTar_MinimalNetwork(t *testing.T) {
	net := &BknNetwork{
		BknNetworkFrontmatter: BknNetworkFrontmatter{
			Type:    "network",
			ID:      "minimal-net",
			Name:    "Minimal Network",
			Version: "1.0.0",
		},
	}

	var buf bytes.Buffer
	err := WriteNetworkToTar(net, &buf)
	require.NoError(t, err)

	// Load back and verify
	loaded, err := LoadNetworkFromTar(&buf)
	require.NoError(t, err)

	assert.Equal(t, "minimal-net", loaded.ID)
	assert.Equal(t, "Minimal Network", loaded.Name)
	assert.Equal(t, "1.0.0", loaded.Version)
}

func TestWriteNetworkToTar_FullNetwork(t *testing.T) {
	net := &BknNetwork{
		BknNetworkFrontmatter: BknNetworkFrontmatter{
			Type:    "network",
			ID:      "full-net",
			Name:    "Full Network",
			Tags:    []string{"test", "full"},
			Version: "2.0",
			Branch:  "main",
		},
		Description: "A complete network",
		ObjectTypes: []*BknObjectType{
			{
				BknObjectTypeFrontmatter: BknObjectTypeFrontmatter{
					Type: "object_type",
					ID:   "pod",
					Name: "Pod",
					Tags: []string{"k8s"},
				},
				Description: "Kubernetes Pod",
				DataSource: &ResourceInfo{
					Type: "data_view",
					ID:   "dv1",
					Name: "Pod View",
				},
				DataProperties: []*DataProperty{
					{Name: "name", DisplayName: "Name", Type: "string"},
				},
				PrimaryKeys: []string{"id"},
				DisplayKey:  "name",
			},
		},
		RelationTypes: []*BknRelationType{
			{
				BknRelationTypeFrontmatter: BknRelationTypeFrontmatter{
					Type: "relation_type",
					ID:   "belongs_to",
					Name: "Belongs To",
				},
				Endpoint: Endpoint{
					Source: "pod",
					Target: "node",
					Type:   "direct",
				},
			},
		},
		ActionTypes: []*BknActionType{
			{
				BknActionTypeFrontmatter: BknActionTypeFrontmatter{
					Type:       "action_type",
					ID:         "restart",
					Name:       "Restart Pod",
					ActionType: "modify",
				},
				BoundObject: "pod",
				Parameters: []Parameter{
					{Name: "graceful", Type: "boolean", Source: "const", Value: true},
				},
			},
		},
	}

	var buf bytes.Buffer
	err := WriteNetworkToTar(net, &buf)
	require.NoError(t, err)

	// Load back and verify
	loaded, err := LoadNetworkFromTar(&buf)
	require.NoError(t, err)

	// Verify network
	assert.Equal(t, "full-net", loaded.ID)
	assert.Equal(t, "Full Network", loaded.Name)
	assert.ElementsMatch(t, []string{"test", "full"}, loaded.Tags)

	// Verify object type
	require.Len(t, loaded.ObjectTypes, 1)
	assert.Equal(t, "pod", loaded.ObjectTypes[0].ID)
	assert.Equal(t, "Pod", loaded.ObjectTypes[0].Name)
	require.NotNil(t, loaded.ObjectTypes[0].DataSource)
	assert.Equal(t, "data_view", loaded.ObjectTypes[0].DataSource.Type)
	assert.ElementsMatch(t, []string{"id"}, loaded.ObjectTypes[0].PrimaryKeys)
	assert.Equal(t, "name", loaded.ObjectTypes[0].DisplayKey)

	// Verify relation type
	require.Len(t, loaded.RelationTypes, 1)
	assert.Equal(t, "belongs_to", loaded.RelationTypes[0].ID)
	assert.Equal(t, "pod", loaded.RelationTypes[0].Endpoint.Source)
	assert.Equal(t, "node", loaded.RelationTypes[0].Endpoint.Target)
	assert.Equal(t, "direct", loaded.RelationTypes[0].Endpoint.Type)

	// Verify action type
	require.Len(t, loaded.ActionTypes, 1)
	assert.Equal(t, "restart", loaded.ActionTypes[0].ID)
	assert.Equal(t, "modify", loaded.ActionTypes[0].ActionType)
	assert.Equal(t, "pod", loaded.ActionTypes[0].BoundObject)
}

// === Round Trip Tests ===

func TestRoundTrip_NetworkWithAllTypes(t *testing.T) {
	original := &BknNetwork{
		BknNetworkFrontmatter: BknNetworkFrontmatter{
			Type:    "network",
			ID:      "roundtrip-net",
			Name:    "Roundtrip Network",
			Tags:    []string{"test"},
			Version: "1.0",
		},
		Description: "Testing round trip",
		ObjectTypes: []*BknObjectType{
			{
				BknObjectTypeFrontmatter: BknObjectTypeFrontmatter{
					Type: "object_type",
					ID:   "obj1",
					Name: "Object 1",
				},
				DataProperties: []*DataProperty{
					{Name: "prop1", DisplayName: "Property 1", Type: "string"},
					{Name: "prop2", DisplayName: "Property 2", Type: "number"},
				},
				PrimaryKeys: []string{"id"},
				DisplayKey:  "prop1",
			},
		},
		RelationTypes: []*BknRelationType{
			{
				BknRelationTypeFrontmatter: BknRelationTypeFrontmatter{
					Type: "relation_type",
					ID:   "rel1",
					Name: "Relation 1",
				},
				Endpoint: Endpoint{
					Source: "obj1",
					Target: "obj2",
					Type:   "direct",
				},
			},
		},
		ActionTypes: []*BknActionType{
			{
				BknActionTypeFrontmatter: BknActionTypeFrontmatter{
					Type:       "action_type",
					ID:         "act1",
					Name:       "Action 1",
					ActionType: "create",
				},
				BoundObject: "obj1",
			},
		},
		Metrics: []*BknMetric{
			{
				BknMetricFrontmatter: BknMetricFrontmatter{
					Type: "metric",
					ID:   "met1",
					Name: "Metric One",
					Tags: []string{"t"},
				},
				Description:                  "roundtrip metric",
				ScopeType:                    "object_type",
				ScopeRef:                     "obj1",
				HasScopeSection:              true,
				HasCalculationFormulaSection: true,
				Formula: &MetricFormula{
					Kind: "atomic",
					Atomic: &MetricAtomic{
						Aggregation: &MetricAggregation{Property: "prop1", Aggr: "count"},
					},
				},
			},
		},
	}

	// Write to tar
	var buf bytes.Buffer
	err := WriteNetworkToTar(original, &buf)
	require.NoError(t, err)

	// Load back
	loaded, err := LoadNetworkFromTar(&buf)
	require.NoError(t, err)

	// Verify all fields
	assert.Equal(t, original.ID, loaded.ID)
	assert.Equal(t, original.Name, loaded.Name)
	assert.Equal(t, original.Description, loaded.Description)
	assert.ElementsMatch(t, original.Tags, loaded.Tags)

	require.Len(t, loaded.ObjectTypes, 1)
	assert.Equal(t, original.ObjectTypes[0].ID, loaded.ObjectTypes[0].ID)
	assert.Equal(t, original.ObjectTypes[0].Name, loaded.ObjectTypes[0].Name)

	require.Len(t, loaded.RelationTypes, 1)
	assert.Equal(t, original.RelationTypes[0].ID, loaded.RelationTypes[0].ID)

	require.Len(t, loaded.ActionTypes, 1)
	assert.Equal(t, original.ActionTypes[0].ID, loaded.ActionTypes[0].ID)

	require.Len(t, loaded.Metrics, 1)
	assert.Equal(t, original.Metrics[0].ID, loaded.Metrics[0].ID)
	require.NotNil(t, loaded.Metrics[0].Formula)
	assert.Equal(t, "atomic", loaded.Metrics[0].Formula.Kind)
}

// === Checksum Tests ===

func TestComputeChecksumFromTar_ValidTar(t *testing.T) {
	// Create a tar with network and CHECKSUM
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	networkContent := []byte("---\ntype: network\nid: test\n---\n")
	_ = tw.WriteHeader(&tar.Header{Name: "network.bkn", Size: int64(len(networkContent)), Mode: 0644})
	_, _ = tw.Write(networkContent)

	objContent := []byte("---\ntype: object_type\nid: pod\n---\n")
	_ = tw.WriteHeader(&tar.Header{Name: "object_types/pod.bkn", Size: int64(len(objContent)), Mode: 0644})
	_, _ = tw.Write(objContent)
	_ = tw.Close()

	checksumMap, err := ComputeChecksumFromTar(&buf)
	require.NoError(t, err)
	assert.NotEmpty(t, checksumMap)
	assert.Contains(t, checksumMap, "network")
	assert.Contains(t, checksumMap, "object_type:pod")
}

func TestGenerateChecksumFromTar_ValidTar(t *testing.T) {
	// Create a tar with network
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	networkContent := []byte("---\ntype: network\nid: test\n---\n")
	_ = tw.WriteHeader(&tar.Header{Name: "network.bkn", Size: int64(len(networkContent)), Mode: 0644})
	_, _ = tw.Write(networkContent)

	objContent := []byte("---\ntype: object_type\nid: pod\n---\n")
	_ = tw.WriteHeader(&tar.Header{Name: "object_types/pod.bkn", Size: int64(len(objContent)), Mode: 0644})
	_, _ = tw.Write(objContent)
	_ = tw.Close()

	checksum, err := GenerateChecksumFromTar(&buf)
	require.NoError(t, err)
	assert.NotEmpty(t, checksum)
	assert.Contains(t, checksum, "network")
	assert.Contains(t, checksum, "object_type:pod")
}

func TestComputeChecksumFromTar_NoNetworkFile(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	content := []byte("---\ntype: object_type\nid: pod\n---\n")
	_ = tw.WriteHeader(&tar.Header{Name: "object_types/pod.bkn", Size: int64(len(content)), Mode: 0644})
	_, _ = tw.Write(content)
	_ = tw.Close()

	_, err := ComputeChecksumFromTar(&buf)
	assert.Error(t, err)
}

func TestVerifyChecksumFromTar_Valid(t *testing.T) {
	// First create a valid tar and compute its checksum
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	networkContent := []byte("---\ntype: network\nid: test\n---\n")
	_ = tw.WriteHeader(&tar.Header{Name: "network.bkn", Size: int64(len(networkContent)), Mode: 0644})
	_, _ = tw.Write(networkContent)
	_ = tw.Close()

	checksum, err := GenerateChecksumFromTar(&buf)
	require.NoError(t, err)

	// Now create a new tar with the checksum file
	var buf2 bytes.Buffer
	tw2 := tar.NewWriter(&buf2)

	_ = tw2.WriteHeader(&tar.Header{Name: "network.bkn", Size: int64(len(networkContent)), Mode: 0644})
	_, _ = tw2.Write(networkContent)

	checksumContent := []byte(checksum)
	_ = tw2.WriteHeader(&tar.Header{Name: "CHECKSUM", Size: int64(len(checksumContent)), Mode: 0644})
	_, _ = tw2.Write(checksumContent)
	_ = tw2.Close()

	// Verify
	ok, errs := VerifyChecksumFromTar(&buf2)
	assert.True(t, ok, "verification should pass, errors: %v", errs)
	assert.Empty(t, errs)
}

func TestVerifyChecksumFromTar_InvalidChecksum(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	networkContent := []byte("---\ntype: network\nid: test\n---\n")
	_ = tw.WriteHeader(&tar.Header{Name: "network.bkn", Size: int64(len(networkContent)), Mode: 0644})
	_, _ = tw.Write(networkContent)

	// Add invalid checksum
	invalidChecksum := []byte("# Checksum\nnetwork  sha256:invalid123\n")
	_ = tw.WriteHeader(&tar.Header{Name: "CHECKSUM", Size: int64(len(invalidChecksum)), Mode: 0644})
	_, _ = tw.Write(invalidChecksum)
	_ = tw.Close()

	ok, errs := VerifyChecksumFromTar(&buf)
	assert.False(t, ok)
	assert.NotEmpty(t, errs)
}

func TestVerifyChecksumFromTar_MissingChecksumFile(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	content := []byte("---\ntype: network\nid: test\n---\n")
	_ = tw.WriteHeader(&tar.Header{Name: "network.bkn", Size: int64(len(content)), Mode: 0644})
	_, _ = tw.Write(content)
	_ = tw.Close()

	ok, errs := VerifyChecksumFromTar(&buf)
	assert.False(t, ok)
	assert.NotEmpty(t, errs)
}

// === SKILL.md Generation Tests ===

func TestGenerateSkillMd_Content(t *testing.T) {
	net := &BknNetwork{
		BknNetworkFrontmatter: BknNetworkFrontmatter{
			Type:    "network",
			ID:      "test-net",
			Name:    "Test Network",
			Version: "1.2.3",
			Tags:    []string{"test", "demo"},
		},
		Description: "A test network for validation",
		ObjectTypes: []*BknObjectType{
			{BknObjectTypeFrontmatter: BknObjectTypeFrontmatter{ID: "pod", Name: "Pod"}},
			{BknObjectTypeFrontmatter: BknObjectTypeFrontmatter{ID: "node", Name: "Node"}},
		},
		RelationTypes: []*BknRelationType{
			{BknRelationTypeFrontmatter: BknRelationTypeFrontmatter{ID: "runs_on", Name: "Runs On"}},
		},
		ActionTypes: []*BknActionType{
			{BknActionTypeFrontmatter: BknActionTypeFrontmatter{ID: "restart", Name: "Restart"}},
		},
	}

	skill := generateSkillMd(net)

	// Verify content
	assert.Contains(t, skill, "# Test Network")
	assert.Contains(t, skill, "test-net")
	assert.Contains(t, skill, "1.2.3")
	assert.Contains(t, skill, "Pod")
	assert.Contains(t, skill, "Node")
	assert.Contains(t, skill, "Runs On")
	assert.Contains(t, skill, "Restart")
}

func TestGenerateSkillMd_EmptyNetwork(t *testing.T) {
	net := &BknNetwork{
		BknNetworkFrontmatter: BknNetworkFrontmatter{
			Type: "network",
			ID:   "empty",
			Name: "Empty Network",
		},
	}

	skill := generateSkillMd(net)

	assert.Contains(t, skill, "# Empty Network")
	assert.Contains(t, skill, "empty")
}
