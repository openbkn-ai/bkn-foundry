// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package capability

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

const (
	// capabilityKeySeparator joins the three-part identity into the single keyword the whitelist
	// filters on. It is a byte that cannot appear in any of the three parts.
	capabilityKeySeparator = "\x1f"
	// defaultFulltextAnalyzer is the analyzer a new dataset is built with.
	//
	// The platform ships OpenSearch with the IK Chinese analyzer baked into its image
	// (deploy/scripts/lib/common.sh pins that rebuild), so this is part of the baseline rather
	// than an optional plugin. It matters: "standard" splits Chinese one character at a time, so
	// a description containing 汇总 scores against a query about 汇率 on the shared 汇, while a
	// query two characters longer has each character's contribution diluted until the right
	// document stops matching. Both were observed on the test server before this was changed.
	defaultFulltextAnalyzer = "ik_max_word"
)

// capabilityKey is the readable composite identity stored on every document.
//
// The whitelist filters on this one keyword rather than on the three parts separately: a network
// with 1000 mounts would otherwise need 1000 three-clause AND groups, and OpenSearch rejects a
// boolean query past indices.query.bool.max_clause_count (1024 by default). One terms filter over
// composite keys is a single clause whatever the size of the mounted set.
func capabilityKey(ref interfaces.CapabilityRef) string {
	return strings.Join([]string{
		strings.TrimSpace(ref.CapabilityType),
		strings.TrimSpace(ref.OwnerID),
		strings.TrimSpace(ref.CapabilityID),
	}, capabilityKeySeparator)
}

// capabilityDocID is the document id for one capability.
//
// It is a hash rather than the readable key because the id travels in a URL path segment. An MCP
// tool name comes from a remote server and is not ours to constrain; a "/" in it would be
// percent-encoded by the client and then decoded back into a path separator by the router, and
// the write would land somewhere else or 404. The readable identity is kept in capability_key,
// which is queryable.
func capabilityDocID(ref interfaces.CapabilityRef) string {
	sum := sha256.Sum256([]byte(capabilityKey(ref)))
	return hex.EncodeToString(sum[:])
}

// keywordProperty is an exactly-filtered string field.
func keywordProperty(name, description string) interfaces.VegaProperty {
	return interfaces.VegaProperty{
		Name:         name,
		Type:         "string",
		DisplayName:  name,
		OriginalName: name,
		Description:  description,
		Features: []interfaces.VegaPropertyFeature{{
			Name:        "keyword_" + name,
			DisplayName: "keyword_" + name,
			FeatureType: "keyword",
			Description: description,
			IsDefault:   true,
			IsNative:    false,
			Config:      map[string]any{"ignore_above": 1024},
		}},
	}
}

// searchableProperty is a field carrying both an exact keyword and a full-text feature.
func searchableProperty(name, description, analyzer string) interfaces.VegaProperty {
	return interfaces.VegaProperty{
		Name:         name,
		Type:         "text",
		DisplayName:  name,
		OriginalName: name,
		Description:  description,
		Features: []interfaces.VegaPropertyFeature{
			{
				Name:        "keyword_" + name,
				DisplayName: "keyword_" + name,
				FeatureType: "keyword",
				Description: description,
				IsDefault:   true,
				IsNative:    false,
				Config:      map[string]any{"ignore_above": 1024},
			},
			{
				Name:        "fulltext_" + name,
				DisplayName: "fulltext_" + name,
				FeatureType: "fulltext",
				Description: description,
				IsDefault:   true,
				IsNative:    false,
				Config:      map[string]any{"analyzer": analyzer},
			},
		},
	}
}

// datetimeProperty is an epoch-millisecond timestamp.
func datetimeProperty(name, description string) interfaces.VegaProperty {
	return interfaces.VegaProperty{
		Name:         name,
		Type:         "datetime",
		DisplayName:  name,
		OriginalName: name,
		Description:  description,
	}
}

// buildCapabilityIndexSchema builds the capability index schema.
//
// One schema holds all three kinds. Fields that do not apply to a kind are written blank rather
// than split into per-kind schemas or folded into a free-form object: vega copies feature config
// into the OpenSearch mapping verbatim, so an open-ended bag of keys is how index construction
// starts failing.
//
// The embedding model name does not belong here. It is resolved at build time and kept in memory;
// putting it in a vector feature's config would copy it into the knn_vector mapping, which
// OpenSearch rejects.
func buildCapabilityIndexSchema(dimension int, analyzer string) []interfaces.VegaProperty {
	if strings.TrimSpace(analyzer) == "" {
		analyzer = defaultFulltextAnalyzer
	}
	return []interfaces.VegaProperty{
		keywordProperty("capability_type", "能力类型：skill / function / mcp_tool"),
		keywordProperty("owner_id", "能力归属：函数工具的工具箱、MCP 工具的服务端，Skill 为空"),
		keywordProperty("capability_id", "能力在归属内的标识"),
		keywordProperty("metadata_type", "函数工具的工具箱类型：openapi / function；Skill 与 MCP 工具为空"),
		keywordProperty("capability_key", "三段式身份的复合键，白名单过滤的落点"),
		searchableProperty("name", "能力名称", analyzer),
		searchableProperty("description", "能力描述", analyzer),
		keywordProperty("version", "版本，仅 Skill 适用"),
		keywordProperty("category", "分类，仅 Skill 适用"),
		keywordProperty("create_user", "创建人"),
		datetimeProperty("create_time", "创建时间"),
		keywordProperty("update_user", "更新人"),
		datetimeProperty("update_time", "更新时间"),
		{
			Name:         "_vector",
			Type:         "vector",
			DisplayName:  "_vector",
			OriginalName: "_vector",
			Description:  "能力名称与描述的向量",
			Features: []interfaces.VegaPropertyFeature{{
				Name:        "vector_capability",
				DisplayName: "vector_capability",
				FeatureType: "vector",
				Description: "能力语义检索向量",
				IsDefault:   true,
				IsNative:    false,
				Config: map[string]any{
					"dimension": dimension,
					"method": map[string]any{
						"name":       "hnsw",
						"space_type": "cosinesimil",
						"engine":     "lucene",
						"parameters": map[string]any{
							"ef_construction": 256,
							"m":               48,
						},
					},
				},
			}},
		},
	}
}

// schemaAnalyzer reads the full-text analyzer an existing dataset was built with.
//
// The analyzer is decided once, when the dataset is created, and is never a reason to rebuild:
// changing it would reindex everything, and a deployment whose capability probe momentarily fails
// would flap between analyzers, rebuilding the index each way.
func schemaAnalyzer(schema []interfaces.VegaProperty) string {
	for _, property := range schema {
		if property.Name != "name" {
			continue
		}
		for _, feature := range property.Features {
			if feature.FeatureType != "fulltext" {
				continue
			}
			if analyzer, ok := feature.Config["analyzer"].(string); ok && strings.TrimSpace(analyzer) != "" {
				return analyzer
			}
		}
	}
	return ""
}
